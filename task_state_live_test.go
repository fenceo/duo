package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func liveStateMessage(run, report string, patch taskStatePatch) string {
	raw, _ := json.Marshal(patch)
	return report + "\n" + liveTaskStateMarker(run) + "\n" + string(raw) + "\n<!-- /duo-task-state:" + run + " -->"
}

func TestLiveTaskStateProtocolRequiresPublicQuotedEvidence(t *testing.T) {
	const run, report = "fixture", "合成校验已通过，板端尚未验证。"
	patch := taskStatePatch{Goal: "验证任务交接", Plan: []taskStatePlanStep{{Text: "运行合成校验", Status: "done", Evidence: report}}, Blockers: []string{}}
	text := liveStateMessage(run, report, patch)
	visible, parsed, hidden := splitLiveTaskState(text, run)
	if visible != report || parsed == nil || !hidden || parsed.Blockers == nil {
		t.Fatal(visible, parsed, hidden)
	}
	if _, p, h := splitLiveTaskState(text, "different-run"); p != nil || h {
		t.Fatal("foreign marker consumed")
	}
	for _, invalid := range []string{
		strings.Replace(text, `"goal":`, `"command":`, 1),
		strings.Replace(text, `"evidence":"`+report+`"`, `"evidence":"不存在的验证"`, 1),
		strings.TrimSuffix(text, " -->"),
		report + "\n" + liveTaskStateMarker(run) + `{"plan":[{"text":"缺少依据","status":"done"}]}` + "\n<!-- /duo-task-state:" + run + " -->",
	} {
		v, p, h := splitLiveTaskState(invalid, run)
		if v != report || p != nil || !h {
			t.Fatal("invalid/partial metadata consumed or leaked", v, p, h)
		}
	}
	for n := len("<!-- duo-task-"); n < len(liveTaskStateMarker(run)); n++ {
		v, p, h := splitLiveTaskState(report+liveTaskStateMarker(run)[:n], run)
		if v != report || p != nil || !h {
			t.Fatal("partial marker leaked", n, v, p, h)
		}
	}
}

func TestLiveTaskStateCumulativeMessagesPreserveFinalMemoryAndPublicQuoteBoundary(t *testing.T) {
	const run = "combined-fixture"
	first := "已完成第一步合成检查。"
	second := "第二步仍在等待验收。"
	one := liveStateMessage(run, first, taskStatePatch{Plan: []taskStatePlanStep{{Text: "第一步", Status: "done", Evidence: first}}})
	two := liveStateMessage(run, second, taskStatePatch{NextActions: []string{"等待验收"}})
	memory := taskMemory{Summary: "正常最终回复", TaskState: &taskStatePatch{Goal: "最终目标"}, KnowledgeCandidates: []knowledgeCandidate{}}
	raw, _ := json.Marshal(memory)
	suffix := memoryMarker(run) + "\n" + string(raw) + "\n<!-- /duo-memory:" + run + " -->"
	for _, result := range []string{
		one + "\n" + two + "\n最终公开结论。\n" + suffix,
		"最终公开结论。\n" + suffix + "\n" + liveStateMessage(run, "", taskStatePatch{Goal: "不会借用隐藏依据"}),
	} {
		visible, updates, hidden := extractLiveTaskStates(result, run)
		clean, m := splitTaskMemory(visible, run)
		if !hidden || m == nil || m.TaskState == nil || m.TaskState.Goal != "最终目标" || !strings.Contains(clean, "最终公开结论") {
			t.Fatal("live protocol consumed final memory", visible, m)
		}
		if strings.Contains(result, one) && (len(updates) != 2 || updates[0].Report != first || updates[1].Report != second) {
			t.Fatal("cumulative milestone sources changed", updates)
		}
	}
	// A claim appearing only in the hidden final-memory JSON is not public
	// evidence for a completed milestone, irrespective of block order.
	secretQuote := "隐藏元数据独有的已完成说法"
	metadata, _ := json.Marshal(taskMemory{Summary: secretQuote})
	prefix := "仅公开等待结果。\n" + memoryMarker(run) + "\n" + string(metadata) + "\n<!-- /duo-memory:" + run + " -->"
	text := liveStateMessage(run, prefix, taskStatePatch{Plan: []taskStatePlanStep{{Text: "虚假完成", Status: "done", Evidence: secretQuote}}})
	visible, updates, _ := extractLiveTaskStates(text, run)
	if len(updates) != 0 || !strings.Contains(visible, string(metadata)) {
		t.Fatal("hidden quote accepted or final suffix lost", updates, visible)
	}
	partial := one + "\n" + second + liveTaskStateMarker(run) + `{"progress":["未完成`
	visible, updates, hidden := extractLiveTaskStates(partial, run)
	if !hidden || len(updates) != 1 || !strings.Contains(visible, second) || strings.Contains(visible, "未完成") {
		t.Fatal("cumulative partial suffix leaked or hid new prose", visible, updates)
	}
}

func TestLiveTaskStateCheckpointTerminalFallbackAndFinalOverride(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	run := workspaceMemoryRun(t, a, task, "维护任务目标", "", "running")
	const report = "合成检查已完成；真实设备仍未验证。"
	patch := &taskStatePatch{Goal: "保持跨引擎任务状态", Plan: []taskStatePlanStep{{Text: "合成检查", Status: "done", Evidence: report}, {Text: "真实设备", Status: "pending"}}, Progress: []string{"已完成合成检查"}, NextActions: []string{"等待设备验收"}}
	if err := a.store.captureLiveTaskState(task, run.ID, patch, report); err != nil {
		t.Fatal(err)
	}
	if err := a.store.captureLiveTaskState(task, run.ID, patch, report); err != nil {
		t.Fatal(err)
	}
	updates, err := a.store.liveTaskStateForRun(context.Background(), task, run.ID)
	if err != nil || len(updates) != 1 {
		t.Fatal("duplicate state update", updates, err)
	}
	_, written, _, err := a.syncWorkspaceExecutionCheckpoint(context.Background(), task.ID, run.ID, -1)
	if err != nil || !written {
		t.Fatal(written, err)
	}
	doc := checkpointDocument(t, task)
	for _, want := range []string{patch.Goal, "正在使用已保存的公开任务状态增量", report, "等待设备验收"} {
		if !strings.Contains(doc, want) {
			t.Fatal("live document missing", want)
		}
	}
	a.finish(task.ID, run, "", "", errors.New("合成中断"))
	state, sources, err := a.store.workspaceMemoryCurrentState(context.Background(), task)
	if err != nil || state.Goal != patch.Goal || len(state.Plan) != 2 || len(sources) != 1 {
		t.Fatal(state, sources, err)
	}
	doc = checkpointDocument(t, task)
	if strings.Contains(doc, workspaceExecutionCheckpointStart) || !strings.Contains(doc, "执行中任务状态增量") || !strings.Contains(doc, report) || !strings.Contains(doc, "failed") {
		t.Fatal("terminal fallback lost source or status")
	}
	// Terminal state takes precedence, while omitted fields retain the earlier
	// public milestone. An empty array deliberately clears its previous value.
	final := taskMemory{TaskState: &taskStatePatch{Goal: "终态目标", Plan: []taskStatePlanStep{}, NextActions: []string{"最终后续"}}}
	raw, _ := json.Marshal(final)
	if _, err = a.store.Exec("INSERT INTO task_memories VALUES(?,?,?,?)", run.ID, task.ID, string(raw), now()); err != nil {
		t.Fatal(err)
	}
	state, _, err = a.store.workspaceMemoryCurrentState(context.Background(), task)
	if err != nil || state.Goal != "终态目标" || len(state.Plan) != 0 || len(state.Progress) != 1 || state.NextActions[0] != "最终后续" {
		t.Fatal(state, err)
	}
	// Late updates cannot resurrect terminal state or another task's identity.
	if err = a.store.captureLiveTaskState(task, run.ID, &taskStatePatch{Goal: "迟到目标"}, report); err != nil {
		t.Fatal(err)
	}
	updates, err = a.store.liveTaskStateForRun(context.Background(), task, run.ID)
	if err != nil || len(updates) != 1 {
		t.Fatal("late update persisted", updates, err)
	}
}

func TestLiveTaskStateManualRegionAndCredentialProtection(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	run := workspaceMemoryRun(t, a, task, "request", "", "running")
	patch := &taskStatePatch{Goal: "初始目标", Blockers: []string{"串口密码：synthetic-live-secret"}}
	if err := a.store.captureLiveTaskState(task, run.ID, patch, "公开进展 token=synthetic-live-token"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := a.syncWorkspaceExecutionCheckpoint(context.Background(), task.ID, run.ID, -1); err != nil {
		t.Fatal(err)
	}
	doc := checkpointDocument(t, task)
	if strings.Contains(doc, "synthetic-live-secret") || strings.Contains(doc, "synthetic-live-token") {
		t.Fatal("secret leaked")
	}
	stateStart := strings.Index(doc, workspaceTaskStateStart)
	stateEnd := strings.Index(doc, workspaceTaskStateEnd)
	if stateStart < 0 || stateEnd < stateStart {
		t.Fatal("missing initial state region")
	}
	doc = doc[:stateStart] + strings.Replace(doc[stateStart:stateEnd], "初始目标", "人工维护的目标", 1) + doc[stateEnd:]
	file := filepath.Join(task.Workspace, filepath.FromSlash(workspaceMemoryTaskBase(task)+"/任务记录.md"))
	if err := os.WriteFile(file, []byte(doc), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.store.captureLiveTaskState(task, run.ID, &taskStatePatch{Goal: "新的自动目标"}, "新进展"); err != nil {
		t.Fatal(err)
	}
	_, _, result, err := a.syncWorkspaceExecutionCheckpoint(context.Background(), task.ID, run.ID, -1)
	if err != nil || len(result.Warnings) == 0 {
		t.Fatal("manual state conflict not surfaced", result, err)
	}
	doc = checkpointDocument(t, task)
	if !strings.Contains(doc, "人工维护的目标") {
		t.Fatal("manual goal overwritten")
	}
	stateStart = strings.Index(doc, workspaceTaskStateStart)
	stateEnd = strings.Index(doc, workspaceTaskStateEnd)
	if stateStart < 0 || stateEnd < stateStart || strings.Contains(doc[stateStart:stateEnd], "新的自动目标") {
		t.Fatal("state region overwritten")
	}
}

func TestLiveTaskStateForkAndWorkspaceArchiveKeepSourceReports(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	run := workspaceMemoryRun(t, a, task, "跨任务继续", "", "running")
	report := "合成来源已确认，仍需设备验收。"
	if err := a.store.captureLiveTaskState(task, run.ID, &taskStatePatch{Goal: "持久交接目标", Progress: []string{report}}, report); err != nil {
		t.Fatal(err)
	}
	a.finish(task.ID, run, "", "最终说明保留", nil)
	fork, err := a.forkTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, _, err := a.store.workspaceMemoryCurrentState(context.Background(), fork)
	if err != nil || state.Goal != "持久交接目标" {
		t.Fatal("fork lost live-only task state", state, err)
	}
	archive, _, err := a.store.workspaceArchiveSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := buildWorkspaceZip(archive, nil)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := readWorkspaceZip(bytes)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range decoded.Archive.Events {
		if e.Kind == "task_state" {
			value := decodeLiveTaskState(e.Text, "")
			if value == nil || value.State.Goal != "持久交接目标" || value.Report != report {
				t.Fatal("archive corrupted state receipt", e)
			}
			count++
		}
	}
	if count != 2 {
		t.Fatal("source/fork receipts missing from archive", count)
	}
}
