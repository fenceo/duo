package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Public milestone messages may carry an optional state delta. This is never
// extracted from private reasoning, tool output, or an additional model call.
// Store the quote and delta together so rebuild/import can revalidate them.
type liveTaskState struct {
	State  taskStatePatch `json:"state"`
	Report string         `json:"report"`
}

func hasTaskStateDelta(p *taskStatePatch) bool {
	return p != nil && (p.Goal != "" || p.Acceptance != nil || p.Plan != nil || p.Progress != nil || p.Decisions != nil || p.Evidence != nil || p.Blockers != nil || p.NextActions != nil)
}

func liveTaskStateMarker(run string) string { return "<!-- duo-task-state:" + run + " -->" }

// Imported receipts and engine handoffs may carry an older run's memory
// envelope. None of its contents is public evidence, regardless of run ID.
// Keep later public prose after complete blocks; an unfinished block withholds
// the remaining suffix rather than exposing its structured contents.
func liveTaskStatePublicReport(text string) string {
	const prefix = "<!-- duo-memory:"
	var out strings.Builder
	for {
		start := strings.Index(text, prefix)
		if start < 0 {
			out.WriteString(text)
			return strings.TrimSpace(out.String())
		}
		out.WriteString(text[:start])
		rest := text[start+len(prefix):]
		headerEnd := strings.Index(rest, " -->")
		if headerEnd < 0 {
			return strings.TrimSpace(out.String())
		}
		end := "<!-- /duo-memory:" + rest[:headerEnd] + " -->"
		rest = rest[headerEnd+len(" -->"):]
		stop := strings.Index(rest, end)
		if stop < 0 {
			return strings.TrimSpace(out.String())
		}
		text = rest[stop+len(end):]
	}
}

func liveTaskStatePrompt(run string) string {
	return "执行中若正常公开进度已有目标、计划或里程碑变化，可在该条公开进度末尾附加可选任务状态 JSON，不额外调用工具、不输出私有推理、不另起模型请求。沿用 task_state 的字段、数组与逐字依据规则，JSON 直接为状态对象；完成/验证依据必须逐字出现在该条公开进度正文。未提供合法增量时保留旧状态，不从工具活动推断完成。用户要求严格格式或禁止附加时省略。下方仅为格式占位，必须用本轮实际变化替换，不得当作任务进展原样输出。标记仅用于本轮：\n" + liveTaskStateMarker(run) + "\n{...}\n<!-- /duo-task-state:" + run + " -->\n\n"
}

func splitLiveTaskState(text, run string) (string, *taskStatePatch, bool) {
	marker := liveTaskStateMarker(run)
	start := strings.LastIndex(text, marker)
	if start < 0 {
		// Do not expose partial structured JSON from cumulative streams.
		for length := min(len(marker)-1, len(text)); length >= len("<!-- duo-task-"); length-- {
			if strings.HasSuffix(text, marker[:length]) {
				return strings.TrimSpace(text[:len(text)-length]), nil, true
			}
		}
		return text, nil, false
	}
	visible := strings.TrimSpace(text[:start])
	end := "<!-- /duo-task-state:" + run + " -->"
	if !strings.HasSuffix(strings.TrimSpace(text), end) {
		return visible, nil, true
	}
	raw := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text[start+len(marker):]), end))
	if len(raw) > 24000 || len(visible) > 24000 || visible == "" {
		return visible, nil, true
	}
	var patch taskStatePatch
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	publicReport := liveTaskStatePublicReport(visible)
	if d.Decode(&patch) != nil || d.Decode(new(any)) != io.EOF || !hasTaskStateDelta(&patch) || !validTaskStatePatch(&patch, publicReport) {
		return visible, nil, true
	}
	return visible, &patch, true
}

// Harness/Claude may deliver cumulative assistant text. Remove each complete
// internal block, retain subsequent prose and the independent final memory
// suffix, and withhold only an unfinished block. Every delta keeps the public
// message immediately preceding it as its own revalidation source.
func extractLiveTaskStates(text, run string) (string, []liveTaskState, bool) {
	marker, end := liveTaskStateMarker(run), "<!-- /duo-task-state:"+run+" -->"
	var visible strings.Builder
	var updates []liveTaskState
	hidden := false
	for {
		start := strings.Index(text, marker)
		if start < 0 {
			v, _, partial := splitLiveTaskState(text, run)
			visible.WriteString(v)
			return strings.TrimSpace(visible.String()), updates, hidden || partial
		}
		stop := strings.Index(text[start+len(marker):], end)
		if stop < 0 {
			visible.WriteString(text[:start])
			return strings.TrimSpace(visible.String()), updates, true
		}
		stop += start + len(marker) + len(end)
		v, patch, _ := splitLiveTaskState(text[:stop], run)
		visible.WriteString(text[:start])
		if patch != nil {
			publicReport := liveTaskStatePublicReport(v)
			if strings.TrimSpace(publicReport) != "" {
				updates = append(updates, liveTaskState{State: *patch, Report: publicReport})
			}
		}
		hidden = true
		text = text[stop:]
	}
}

func decodeLiveTaskState(raw string, workspace string) *liveTaskState {
	if len(raw) > 64000 {
		return nil
	}
	var value liveTaskState
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&value) != nil || d.Decode(new(any)) != io.EOF {
		return nil
	}
	value.Report = liveTaskStatePublicReport(value.Report)
	if value.Report == "" || !hasTaskStateDelta(&value.State) || !validTaskStatePatch(&value.State, value.Report) {
		return nil
	}
	value.State = *portableTaskStatePatch(&value.State, workspace)
	value.Report = workspaceMemoryText(value.Report, workspace)
	if !validTaskStatePatch(&value.State, value.Report) {
		return nil
	}
	return &value
}

// A distinct event type keeps structured metadata out of the conversation UI;
// the ordinary public progress is saved separately. Existing event archives
// preserve these receipts, including their ordering and source quotes.
func (s *Store) captureLiveTaskState(task Task, run string, patch *taskStatePatch, report string) error {
	if !hasTaskStateDelta(patch) {
		return nil
	}
	c, err := automaticKnowledgeConfig(s)
	if err != nil || !c.Capture || !s.workspaceMemoryEnabled() {
		return err
	}
	value := liveTaskState{State: *portableTaskStatePatch(patch, task.Workspace), Report: workspaceMemoryText(liveTaskStatePublicReport(report), task.Workspace)}
	if !validTaskStatePatch(&value.State, value.Report) {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.Exec(`INSERT INTO events(task_id,run_id,kind,text,created)
 SELECT ?,?,'task_state',?,? WHERE EXISTS(SELECT 1 FROM runs WHERE id=? AND task_id=? AND kind='chat' AND status='running')
 AND NOT EXISTS(SELECT 1 FROM task_options WHERE task_id=? AND deleted=1)
 AND NOT EXISTS(SELECT 1 FROM events WHERE run_id=? AND task_id=? AND kind='task_state' AND text=?)
 AND (SELECT count(*) FROM events WHERE run_id=? AND task_id=? AND kind='task_state')<64`,
		task.ID, run, string(raw), now(), run, task.ID, task.ID, run, task.ID, string(raw), run, task.ID)
	return err
}

func (s *Store) liveTaskStateForRun(ctx context.Context, task Task, run string) ([]liveTaskState, error) {
	rows, err := s.QueryContext(ctx, "SELECT substr(text,1,64001) FROM events WHERE task_id=? AND run_id=? AND kind='task_state' ORDER BY seq LIMIT 64", task.ID, run)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []liveTaskState
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if value := decodeLiveTaskState(raw, task.Workspace); value != nil {
			out = append(out, *value)
		}
	}
	return out, rows.Err()
}

func liveTaskStateContent(updates []liveTaskState, workspace string) string {
	if len(updates) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## 执行中任务状态增量\n\n以下来自助手公开进度，完成与验证均为来源报告，未独立复核；轮次失败不撤销已报告子步骤，也不代表整体成功。\n\n")
	for i, update := range updates {
		fmt.Fprintf(&b, "### 里程碑 %d\n\n%s", i+1, workspaceWorklogQuote(workspaceWorklogText(update.Report, workspace, 2000)))
		p := update.State
		write := func(label, value string) {
			if value != "" {
				fmt.Fprintf(&b, "- %s：%s\n", label, workspaceWorklogText(value, workspace, 1800))
			}
		}
		write("目标", p.Goal)
		write("目标变更原因", p.GoalReason)
		for _, step := range p.Plan {
			write("计划（"+step.Status+"）", step.Text)
			write("完成依据（来源报告）", step.Evidence)
		}
		for _, list := range []struct {
			label  string
			values []string
		}{{"验收", p.Acceptance}, {"进展", p.Progress}, {"决策", p.Decisions}, {"阻塞", p.Blockers}, {"下一步", p.NextActions}} {
			for _, value := range list.values {
				write(list.label, value)
			}
		}
		for _, evidence := range p.Evidence {
			write("证据（"+evidence.Status+"，来源报告）", evidence.Text+"；依据：“"+evidence.Quote+"”")
		}
		b.WriteByte('\n')
	}
	body, cut := workspaceWorklogExcerpt(b.String(), workspace, 20000)
	if cut {
		body += "\n\n更多状态增量与逐字依据保留在 Duo 原始执行记录中。\n"
	}
	return body
}
