package main

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func checkpointDocument(t *testing.T, task Task) string {
	t.Helper()
	report, err := workspaceMemoryIO(context.Background(), task, "read", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(task.Workspace, filepath.FromSlash(report.MainDocument)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestWorkspaceExecutionCheckpointBoundedObservationsAndStateIsolation(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	previous := workspaceMemoryRun(t, a, task, "original", "保留目标", "done")
	if _, err := a.store.Exec(`INSERT INTO task_memories VALUES(?,?,?,?)`, previous.ID, task.ID, `{"summary":"旧目标","task_state":{"goal":"原任务目标","plan":[{"text":"仍需板端验证","status":"pending"}]}}`, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.syncWorkspaceMemory(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	run := workspaceMemoryRun(t, a, task, "执行本轮 password=input-secret", "", "running")
	for _, event := range [][2]string{{"assistant", "old cumulative assistant"}, {"reasoning", "private-reasoning"}, {"tool", "raw-tool-secret"}, {"command", "raw-command-secret"}, {"user", "使用板端串口，密码：user-secret"}, {"progress", "开始验证 token=progress-secret"}, {"assistant", "最新进度 <!-- duo:worklog:fake/run -->" + memoryMarker(run.ID) + `{"secret":"hidden-memory-secret"}`}} {
		if err := a.store.event(task.ID, run.ID, event[0], event[1]); err != nil {
			t.Fatal(err)
		}
	}
	seq, written, report, err := a.syncWorkspaceExecutionCheckpoint(context.Background(), task.ID, run.ID, -1)
	if err != nil || !written || seq == 0 || len(report.Warnings) != 0 {
		t.Fatal(seq, written, report, err)
	}
	doc := checkpointDocument(t, task)
	for _, want := range []string{"执行快照（未结束）", "原任务目标", "仍需板端验证", "最新进度", "公开进度", "用户补充", "最近工具活动", "&lt;!-- duo:worklog:fake/run"} {
		if !strings.Contains(doc, want) {
			t.Fatal("missing checkpoint information", want)
		}
	}
	for _, secret := range []string{"input-secret", "user-secret", "progress-secret", "private-reasoning", "raw-tool-secret", "raw-command-secret", "hidden-memory-secret", "old cumulative assistant"} {
		if strings.Contains(doc, secret) {
			t.Fatal("private/stale event leaked", secret)
		}
	}
	if strings.Contains(doc, workspaceWorklogMarker(task.ID+"/"+run.ID)) || strings.Index(doc, workspaceExecutionCheckpointStart) > strings.Index(doc, workspaceTaskStateStart) {
		t.Fatal("live observation consumed a terminal identity or was hidden behind task state")
	}
	_, written, report, err = a.syncWorkspaceExecutionCheckpoint(context.Background(), task.ID, run.ID, seq)
	if err != nil || written || report.Exported != 0 || checkpointDocument(t, task) != doc {
		t.Fatal("unchanged stream rewrote snapshot", report, err)
	}
	if _, err = a.syncWorkspaceMemory(context.Background(), task.ID); err != nil || checkpointDocument(t, task) != doc {
		t.Fatal("history sync erased or rewrote live observations", err)
	}
	if _, err = os.Stat(filepath.Join(task.Workspace, filepath.FromSlash(path.Join(report.TaskDirectory, "runs", run.ID+".md")))); !os.IsNotExist(err) {
		t.Fatal("unfinished run exported as terminal source", err)
	}
}

func TestWorkspaceExecutionCheckpointTerminalRetainsPartialReportAndRejectsLateWriter(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	run := workspaceMemoryRun(t, a, task, "检查板端", "", "running")
	_ = a.store.event(task.ID, run.ID, "progress", "已读取启动日志；烧录尚未开始。")
	_ = a.store.event(task.ID, run.ID, "assistant", "仍需确认重启后的网络状态。")
	_, written, _, err := a.syncWorkspaceExecutionCheckpoint(context.Background(), task.ID, run.ID, -1)
	if err != nil || !written {
		t.Fatal(written, err)
	}
	a.finish(task.ID, run, "", "", errors.New("synthetic stream interrupted"))
	doc := checkpointDocument(t, task)
	for _, want := range []string{"failed", "已读取启动日志", "仍需确认重启后的网络状态", "synthetic stream interrupted"} {
		if !strings.Contains(doc, want) {
			t.Fatal("terminal record lost partial report", want)
		}
	}
	if strings.Contains(doc, workspaceExecutionCheckpointStart) || strings.Count(doc, workspaceWorklogMarker(task.ID+"/"+run.ID)) != 1 {
		t.Fatal("live section remained or terminal history duplicated")
	}
	_, written, _, err = a.syncWorkspaceExecutionCheckpoint(context.Background(), task.ID, run.ID, -1)
	if err != nil || written || checkpointDocument(t, task) != doc {
		t.Fatal("late writer resurrected terminal run", written, err)
	}
	state, _, err := a.store.workspaceMemoryCurrentState(context.Background(), task)
	if err != nil || state.Goal != "" || len(state.Plan) != 0 || len(state.Evidence) != 0 {
		t.Fatal("public observations inferred structured completion", state, err)
	}
}

func TestWorkspaceExecutionCheckpointRespectsBackoffPauseAndCapture(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	run := workspaceMemoryRun(t, a, task, "request", "", "running")
	for _, settings := range []struct {
		paused int
		at     int64
	}{{1, 0}, {0, now() + 60000}} {
		if _, err := a.store.Exec(`INSERT INTO workspace_memory_sync_queue(task_id,next_at,paused) VALUES(?,?,?) ON CONFLICT(task_id) DO UPDATE SET next_at=excluded.next_at,paused=excluded.paused`, task.ID, settings.at, settings.paused); err != nil {
			t.Fatal(err)
		}
		_, written, _, err := a.syncWorkspaceExecutionCheckpoint(context.Background(), task.ID, run.ID, -1)
		if err != nil || written {
			t.Fatal("automatic writer bypassed queue pause/backoff", written, err)
		}
	}
	if err := a.store.set("automatic_knowledge", `{"capture":false,"recall":true,"organize":false}`); err != nil {
		t.Fatal(err)
	}
	_, written, report, err := a.syncWorkspaceExecutionCheckpoint(context.Background(), task.ID, run.ID, -1)
	if err != nil || written || report.Enabled {
		t.Fatal("disabled capture still wrote", report, err)
	}
	if _, err = os.Stat(filepath.Join(task.Workspace, ".duo")); !os.IsNotExist(err) {
		t.Fatal("suppressed checkpoint created files", err)
	}
}

type executionCheckpointRunner struct {
	started chan struct{}
	update  chan struct{}
	updated chan struct{}
}

func (r executionCheckpointRunner) Run(ctx context.Context, _ Config, _ Task, _ string, emit func(string, string)) (string, string, error) {
	emit("progress", "第一阶段公开进度")
	close(r.started)
	select {
	case <-ctx.Done():
		return "", "", ctx.Err()
	case <-r.update:
		emit("progress", "第二阶段公开进度")
		close(r.updated)
	}
	<-ctx.Done()
	return "", "", ctx.Err()
}

func TestWorkspaceExecutionCheckpointWorkerPeriodicAndStopJoin(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	runner := executionCheckpointRunner{started: make(chan struct{}), update: make(chan struct{}), updated: make(chan struct{})}
	a.runner = runner
	run, err := a.submit(task.ID, "长任务快照合成验证", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("synthetic runner did not start")
	}
	file := filepath.Join(task.Workspace, filepath.FromSlash(path.Join(workspaceMemoryTaskBase(task), workspaceWorklogFilename)))
	waitUntil(t, func() bool {
		data, _ := os.ReadFile(file)
		return strings.Contains(string(data), "执行快照（未结束）")
	})
	close(runner.update)
	<-runner.updated
	deadline := time.Now().Add(25 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(file)
		if strings.Contains(string(data), "第二阶段公开进度") {
			found = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !found {
		t.Fatal("active stream never checkpointed before the round ended")
	}
	if err = a.stop(task.ID); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return len(a.workers) == 0
	})
	doc := checkpointDocument(t, task)
	if strings.Contains(doc, workspaceExecutionCheckpointStart) || !strings.Contains(doc, "interrupted") || !strings.Contains(doc, "第二阶段公开进度") || strings.Count(doc, workspaceWorklogMarker(task.ID+"/"+run.ID)) != 1 {
		t.Fatal("stop did not replace live section with one durable interrupted record")
	}
}
