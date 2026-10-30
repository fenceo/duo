package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const workspaceExecutionCheckpointInterval = 15 * time.Second

// These are public transcript observations, not structured task-state patches.
// In particular, a tool-start event cannot establish a completed plan step.
func (s *Store) workspaceExecutionObservations(ctx context.Context, task Task, run Run, through int64, includeUser bool) (string, error) {
	events := []Event{}
	kinds := []string{"progress", "assistant"}
	if includeUser {
		kinds = append(kinds, "user", "error")
	}
	for _, kind := range kinds {
		limit := 2
		if kind == "assistant" {
			limit = 1 // Cumulative assistant snapshots must never be concatenated.
		}
		rows, err := s.QueryContext(ctx, `SELECT seq,kind,substr(text,1,16001),created FROM events
WHERE task_id=? AND run_id=? AND kind=? AND seq<=? ORDER BY seq DESC LIMIT ?`, task.ID, run.ID, kind, through, limit)
		if err != nil {
			return "", err
		}
		for rows.Next() {
			var event Event
			if err = rows.Scan(&event.Seq, &event.Kind, &event.Text, &event.Created); err != nil {
				rows.Close()
				return "", err
			}
			// Read only a prefix, never an unlabelled credential tail. Sanitize
			// before the final display bound and hide incomplete memory suffixes.
			event.Text, _ = visibleMemoryStream(event.Text, run.ID)
			event.Text = workspaceWorklogText(event.Text, task.Workspace, 650)
			if strings.TrimSpace(event.Text) != "" {
				events = append(events, event)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Seq < events[j].Seq })
	var body strings.Builder
	labels := map[string]string{"user": "用户补充", "progress": "公开进度", "assistant": "最近助手文本", "error": "错误报告"}
	for _, event := range events {
		fmt.Fprintf(&body, "#### %s · %s · 事件 %d\n\n%s", workspaceExecutionTime(event.Created), labels[event.Kind], event.Seq, workspaceWorklogQuote(event.Text))
	}
	return body.String(), nil
}

func workspaceExecutionTime(at int64) string {
	return time.UnixMilli(at).UTC().Format("2006-01-02 15:04:05.000 UTC")
}

// This runs under the same workspaceMemorySlot as final export and Markdown
// editing. Recheck the live run after acquiring it: an old writer must not
// resurrect a snapshot after finish, even when it waited on another task.
func (a *App) syncWorkspaceExecutionCheckpoint(ctx context.Context, taskID, runID string, previousSeq int64) (seq int64, written bool, report WorkspaceMemoryReport, err error) {
	task, err := a.store.task(taskID)
	report = workspaceMemoryReport(task)
	if err != nil || task.Deleted || task.Archived {
		return previousSeq, false, report, err
	}
	c, err := automaticKnowledgeConfig(a.store)
	if err != nil || !c.Capture || !a.store.workspaceMemoryEnabled() {
		return previousSeq, false, report, err
	}
	report.Enabled = true
	select {
	case workspaceMemorySlot <- struct{}{}:
		defer func() { <-workspaceMemorySlot }()
	case <-ctx.Done():
		return previousSeq, false, report, ctx.Err()
	}
	var paused int
	var nextAt int64
	err = a.store.QueryRowContext(ctx, "SELECT paused,next_at FROM workspace_memory_sync_queue WHERE task_id=?", taskID).Scan(&paused, &nextAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return previousSeq, false, report, err
	}
	if paused != 0 || nextAt > now() {
		return previousSeq, false, report, nil
	}
	var run Run
	run.ID, run.TaskID = runID, taskID
	err = a.store.QueryRowContext(ctx, `SELECT substr(input,1,16001),kind,status,created FROM runs WHERE task_id=? AND id=?`, taskID, runID).Scan(&run.Input, &run.Kind, &run.Status, &run.Created)
	if err != nil || run.Status != "running" || run.Kind != "chat" {
		return previousSeq, false, report, err
	}
	if err = a.store.applyRunExecution(runID, &task); err != nil {
		return previousSeq, false, report, err
	}
	var at int64
	err = a.store.QueryRowContext(ctx, "SELECT coalesce(max(seq),0),coalesce(max(created),?) FROM events WHERE task_id=? AND run_id=?", run.Created, taskID, runID).Scan(&seq, &at)
	if err != nil || seq == previousSeq {
		return seq, false, report, err
	}
	observations, err := a.store.workspaceExecutionObservations(ctx, task, run, seq, true)
	if err != nil {
		return previousSeq, false, report, err
	}
	clean := func(value string, limit int) string { return workspaceWorklogText(value, task.Workspace, limit) }
	var body strings.Builder
	fmt.Fprintf(&body, "<!-- duo:execution-source:%s/%s -->\n\n", task.ID, run.ID)
	fmt.Fprintf(&body, "## 执行快照（未结束）\n\n截至 %s，轮次 `%s` 处于执行中；事件上界 %d。此处是已保存事件的有界观察，可能已过时，不代表任务完成或操作验证。目标、计划和证据仍以独立任务状态区为准。\n\n引擎：%s · 模型：%s\n\n### 本轮需求（原始输入，非自动目标）\n\n%s", workspaceExecutionTime(at), run.ID, seq, clean(task.Engine, 100), clean(task.Model, 180), workspaceWorklogQuote(clean(run.Input, 1000)))
	if observations == "" {
		body.WriteString("### 当前活动\n\n尚未取得可记录的公开进度；等待引擎响应。\n\n")
	} else {
		body.WriteString("### 近期公开记录（来源报告，未独立验证）\n\n" + observations)
	}
	var toolSeq, toolAt int64
	err = a.store.QueryRowContext(ctx, "SELECT seq,created FROM events WHERE task_id=? AND run_id=? AND kind IN ('tool','command') AND seq<=? ORDER BY seq DESC LIMIT 1", taskID, runID, seq).Scan(&toolSeq, &toolAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return previousSeq, false, report, err
	}
	if err == nil {
		fmt.Fprintf(&body, "最近工具活动：%s · 事件 %d。快照不复制工具参数或原始输出，事件出现不代表操作成功；详情见 Duo 本轮执行记录。\n\n", workspaceExecutionTime(toolAt), toolSeq)
	}
	body.WriteString("本区按变化限频更新；停止、失败或完成后由正式时间记录接替。服务重启后的状态以 Duo 执行记录为准。\n")
	content := body.String()
	// An omitted path is resolved by the file protocol to this task's existing
	// canonical directory, including after a title change. It never authorizes
	// an arbitrary target and avoids a second full discovery call.
	report, err = workspaceMemoryIORequest(ctx, task, workspaceMemoryPayload{Action: "checkpoint", Files: []workspaceMemoryFile{{Content: workspaceWorklogHeader(task, ""), CheckpointOnly: true, ExecutionContent: &content}}})
	report.Enabled = true
	return seq, err == nil && len(report.Warnings) == 0 && len(report.Conflicts) == 0, report, err
}

// One run-scoped writer, independent of stream frequency. Callbacks only persist
// events; no remote filesystem operation blocks CLI output consumption. A joined
// stop cancels in-flight I/O before finish can replace the live section.
func (a *App) startWorkspaceExecutionCheckpoint(task Task, run Run) func() {
	noop := func() {}
	c, err := automaticKnowledgeConfig(a.store)
	if run.Kind != "chat" || err != nil || !c.Capture || !a.store.workspaceMemoryEnabled() || task.Deleted || task.Archived {
		return noop
	}
	if _, err = a.store.Exec(`INSERT OR IGNORE INTO workspace_memory_sync_queue(task_id,next_at) VALUES(?,0)`, task.ID); err != nil {
		_ = a.store.event(task.ID, run.ID, "progress", "执行快照的待同步进度未能保存；本轮继续，原始执行记录仍保留。")
		return noop
	}
	base := a.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(workspaceExecutionCheckpointInterval)
		defer ticker.Stop()
		lastSeq := int64(-1)
		for {
			writeCtx, writeCancel := context.WithTimeout(ctx, 8*time.Second)
			seq, written, report, writeErr := a.syncWorkspaceExecutionCheckpoint(writeCtx, task.ID, run.ID, lastSeq)
			writeCancel()
			if ctx.Err() != nil {
				return // Cancellation leaves the persistent receipt for finish/restart.
			}
			if written {
				lastSeq = seq
			} else if writeErr != nil || len(report.Conflicts) > 0 || len(report.Warnings) > 0 {
				// A region warning also pauses automatic replacement; a manual
				// sync can inspect/retry without changing the model's result.
				if writeErr == nil && len(report.Warnings) > 0 {
					report.Conflicts = append(report.Conflicts, report.Warnings...)
				}
				a.trackWorkspaceMemorySync(task.ID, report, writeErr)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(cancel); <-done }
}
