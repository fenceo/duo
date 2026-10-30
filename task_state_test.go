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

func TestTaskStatePatchRequiresQuotedCompletionEvidenceAndPreservesClears(t *testing.T) {
	const quote = "合成回环收到 OK。"
	valid := &taskStatePatch{Goal: "验证串口回环", Plan: []taskStatePlanStep{{Text: "验证回环", Status: "done", Evidence: quote}}, Evidence: []taskStateEvidence{{Text: "回环通过", Quote: quote, Status: "passed"}}, Blockers: []string{}}
	if !validTaskStatePatch(valid, quote) {
		t.Fatal("valid quoted completion rejected")
	}
	raw, _ := json.Marshal(valid)
	var decoded taskStatePatch
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Blockers == nil || decoded.Progress != nil {
		t.Fatal("explicit clearing or omitted field lost in persistence", string(raw), err)
	}
	previous := taskStatePatch{Goal: "旧目标", Progress: []string{"保持进展"}, Blockers: []string{"旧阻塞"}}
	merged := mergeTaskState(previous, &decoded)
	if merged.Goal != valid.Goal || len(merged.Blockers) != 0 || len(merged.Progress) != 1 {
		t.Fatal("patch erased an omitted field or retained an explicit clear", merged)
	}
	for _, invalid := range []*taskStatePatch{
		{Goal: "你好"},
		{GoalReason: "没有目标的变更理由"},
		{Plan: []taskStatePlanStep{{Text: "未验证步骤", Status: "done"}}},
		{Plan: []taskStatePlanStep{{Text: "编造完成", Status: "done", Evidence: "并不存在的结果"}}},
		{Plan: []taskStatePlanStep{{Text: "步骤", Status: "finished"}}},
		{Evidence: []taskStateEvidence{{Text: "结果", Quote: "不存在", Status: "passed"}}},
		{Evidence: []taskStateEvidence{{Text: "结果", Quote: quote, Status: "verified"}}},
		{Progress: []string{"\x00"}},
		{Blockers: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}},
	} {
		if validTaskStatePatch(invalid, quote) {
			t.Fatal("invalid task state accepted", invalid)
		}
	}
	memory := taskMemory{Summary: "记录合成验证", TaskState: valid}
	encoded, _ := json.Marshal(memory)
	result := quote + "\n\n" + memoryMarker("fixture") + "\n" + string(encoded) + "\n<!-- /duo-memory:fixture -->"
	if clean, memory := splitTaskMemory(result, "fixture"); clean != quote || memory == nil || memory.TaskState == nil {
		t.Fatal("task state suffix not consumed", clean, memory)
	}
	unknown := strings.Replace(result, `"goal":`, `"arbitrary_command":`, 1)
	if clean, memory := splitTaskMemory(unknown, "fixture"); clean != unknown || memory != nil {
		t.Fatal("unknown nested field accepted")
	}
	portable := portableTaskStatePatch(&taskStatePatch{Goal: `检查 C:\fixture\repo\main.go`, Blockers: []string{"串口密码：synthetic-secret"}}, `C:\fixture\repo`)
	if !strings.Contains(portable.Goal, "<workspace>") || strings.Contains(portable.Blockers[0], "synthetic-secret") {
		t.Fatal("state persisted private path or credential", portable)
	}
}

func TestWorkspaceTaskStateCaptureIndependentOfKnowledgeOrganizationAndPortableHandoff(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	if err := a.store.set("automatic_knowledge", `{"capture":true,"recall":true,"organize":false}`); err != nil {
		t.Fatal(err)
	}
	const quote = "模拟 100 次移动，仅合并为一次更新。"
	memory := taskMemory{Summary: "回环状态交接", TaskState: &taskStatePatch{Goal: "验证375平台回环并交接", Plan: []taskStatePlanStep{{Text: "完成合成检查", Status: "done", Evidence: quote}}, Evidence: []taskStateEvidence{{Text: "合成检查通过", Quote: quote, Status: "passed"}}, NextActions: []string{"真实设备待验证"}}}
	run := finishNotebook(t, a, task, memory)
	var payload string
	if err := a.store.QueryRow("SELECT payload FROM task_memories WHERE run_id=?", run.ID).Scan(&payload); err != nil || !strings.Contains(payload, "task_state") {
		t.Fatal("first-layer state was gated by long-term organization", payload, err)
	}
	if _, err := a.syncWorkspaceMemory(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	fresh := task
	fresh.Engine = "deepseek-harness"
	fresh.Session = ""
	if text, _, err := a.workspaceWorklogContext(context.Background(), fresh); err != nil || !strings.Contains(text, "验证375平台回环并交接") || !strings.Contains(text, "真实设备待验证") {
		t.Fatal("fresh engine did not read current task state", text, err)
	}
	other := workspaceMemoryLocalTask(t, a, taskFor(t, a), task.Workspace)
	text, ids, err := a.workspaceMemoryContext(context.Background(), other)
	if err != nil || len(ids) != 1 || !strings.Contains(text, "验证375平台回环并交接") || !strings.Contains(text, workspaceWorklogFilename) {
		t.Fatal("same-workspace task state was absent or duplicated raw sources", text, ids, err)
	}
	fork := workspaceMemoryLocalTask(t, a, taskFor(t, a), task.Workspace)
	copied := workspaceMemoryRun(t, a, fork, run.Input, quote, "done")
	if _, err := a.store.Exec("INSERT INTO run_fork_sources VALUES(?,?,?)", copied.ID, task.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(memory)
	if _, err := a.store.Exec("INSERT INTO task_memories(run_id,task_id,payload,updated) VALUES(?,?,?,?)", copied.ID, fork.ID, string(raw), now()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.syncWorkspaceMemory(context.Background(), fork.ID); err != nil {
		t.Fatal(err)
	}
	if _, ids, err := a.workspaceMemoryContext(context.Background(), other); err != nil || len(ids) != 1 {
		t.Fatal("fork repeated the same task-state provenance", ids, err)
	}
	if err := a.trashTask(task.ID, false); err != nil {
		t.Fatal(err)
	}
	if text, ids, err := a.workspaceMemoryContext(context.Background(), other); err != nil || text != "" || len(ids) != 0 {
		t.Fatal("deleted origin returned through copied state", text, ids, err)
	}
}

func TestWorkspaceTaskStateFinishPreservesFailedAndStoppedProgress(t *testing.T) {
	for _, test := range []struct {
		name, status string
		failure      error
	}{
		{"failed", "failed", errors.New("synthetic transport failure")},
		{"stopped", "interrupted", context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, task := workspaceMemoryFixture(t)
			if err := a.store.set("automatic_knowledge", `{"capture":true,"recall":true,"organize":false}`); err != nil {
				t.Fatal(err)
			}
			finishNotebook(t, a, task, taskMemory{Summary: "明确任务目标", TaskState: &taskStatePatch{Goal: "检查375平台连接与配置"}})
			run := Run{ID: uid(), TaskID: task.ID, Input: "读取配置后检查连接", Kind: "chat", Source: "web", Created: now()}
			if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES(?,?,?,'chat','web','running',?)", run.ID, task.ID, run.Input, run.Created); err != nil {
				t.Fatal(err)
			}
			const completed = "合成配置读取完成。"
			const failed = "合成连接失败，尚未完成设备验证。"
			memory := taskMemory{Summary: "配置已读取，连接仍失败", TaskState: &taskStatePatch{
				Plan:     []taskStatePlanStep{{Text: "读取配置", Status: "done", Evidence: completed}, {Text: "验证设备连接", Status: "blocked"}},
				Progress: []string{"已完成配置读取，设备验证未完成"},
				Evidence: []taskStateEvidence{{Text: "配置读取通过", Quote: completed, Status: "passed"}, {Text: "连接检查失败", Quote: failed, Status: "failed"}},
				Blockers: []string{"连接失败，等待排查"}, NextActions: []string{"检查回环连接"},
			}}
			raw, _ := json.Marshal(memory)
			result := completed + "\n" + failed + "\n\n" + memoryMarker(run.ID) + "\n" + string(raw) + "\n<!-- /duo-memory:" + run.ID + " -->"
			a.finish(task.ID, run, "", result, test.failure)
			var status, storedResult, payload string
			if err := a.store.QueryRow("SELECT status,result FROM runs WHERE id=?", run.ID).Scan(&status, &storedResult); err != nil || status != test.status || strings.Contains(storedResult, "duo-memory:") {
				t.Fatal("finish did not preserve failed/stopped status or strip valid metadata", status, err)
			}
			if err := a.store.QueryRow("SELECT payload FROM task_memories WHERE run_id=?", run.ID).Scan(&payload); err != nil || !strings.Contains(payload, "task_state") || !strings.Contains(payload, completed) {
				t.Fatal("valid partial progress was discarded on failure/stop", err)
			}
			var knowledgeCount int
			if err := a.store.QueryRow("SELECT count(*) FROM knowledge_entries WHERE run_id=?", run.ID).Scan(&knowledgeCount); err != nil || knowledgeCount != 0 {
				t.Fatal("failed/stopped run entered done-only automatic knowledge capture", knowledgeCount, err)
			}
			report, err := workspaceMemoryIO(context.Background(), task, "read", nil)
			if err != nil {
				t.Fatal(err)
			}
			content := readWorkspaceWorklog(t, task, report.MainDocument)
			top := content[:strings.Index(content, workspaceTaskStateEnd)]
			for _, want := range []string{"检查375平台连接与配置", test.status, "配置读取通过", "来源报告失败", "[受阻]", "连接失败，等待排查", "检查回环连接"} {
				if !strings.Contains(top, want) {
					t.Fatal("finish hook failed to update task state while retaining prior goal", want, top)
				}
			}
			if strings.Count(content, workspaceWorklogMarker(task.ID+"/"+run.ID)) != 1 {
				t.Fatal("failed/stopped milestone was missing or duplicated")
			}
		})
	}
}

func TestWorkspaceTaskStateUpdatesIndependentlyOfMilestonesAndProtectsHumanText(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	add := func(id string, stamp int64, status string, patch *taskStatePatch) {
		t.Helper()
		if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,result,kind,source,status,created,finished) VALUES(?,?,?,'合成检查失败。','chat','web',?,?,?)", id, task.ID, "你好", status, stamp, stamp+1); err != nil {
			t.Fatal(err)
		}
		if patch != nil {
			raw, _ := json.Marshal(taskMemory{Summary: "状态更新-" + id, TaskState: patch})
			if _, err := a.store.Exec("INSERT INTO task_memories(run_id,task_id,payload,updated) VALUES(?,?,?,?)", id, task.ID, string(raw), stamp+1); err != nil {
				t.Fatal(err)
			}
		}
	}
	add("greeting", 10, "done", nil)
	report, err := a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(task.Workspace, filepath.FromSlash(report.MainDocument))
	initial := readWorkspaceWorklog(t, task, report.MainDocument)
	if strings.Contains(initial, "\n你好\n") || !strings.Contains(initial, "待明确任务目标") {
		t.Fatal("greeting became task goal", initial)
	}
	human := "\n## 人工备注\n保留电源接线说明。\n"
	if err := os.WriteFile(file, []byte(initial+human), 0600); err != nil {
		t.Fatal(err)
	}
	add("state-one", 20, "done", &taskStatePatch{Goal: "验证375平台串口回环", Acceptance: []string{"返回OK"}, Plan: []taskStatePlanStep{{Text: "执行合成检查", Status: "pending"}}, Progress: []string{"已定位回环端口"}, Decisions: []string{"先用合成回环排除软件路径"}})
	if _, err := a.syncWorkspaceMemory(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	after := readWorkspaceWorklog(t, task, report.MainDocument)
	if !strings.Contains(after, human) || !strings.Contains(after, "### 任务目标\n\n验证375平台串口回环") || strings.Count(after, workspaceWorklogMarker(task.ID+"/greeting")) != 1 {
		t.Fatal("state or preserved source missing", after)
	}
	add("state-two", 30, "failed", &taskStatePatch{Progress: []string{"检查返回失败"}, Evidence: []taskStateEvidence{{Text: "合成检查失败", Quote: "合成检查失败。", Status: "failed"}}, Blockers: []string{"等待有效回环设备"}, NextActions: []string{"重新检查连线"}})
	if _, err := a.store.Exec("UPDATE runs SET error='合成超时' WHERE id='state-two'"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.syncWorkspaceMemory(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	after = readWorkspaceWorklog(t, task, report.MainDocument)
	top := after[:strings.Index(after, workspaceTaskStateEnd)]
	for _, want := range []string{"验证375平台串口回环", "返回OK", "[待执行]", "来源报告失败", "等待有效回环设备", "failed", "合成超时"} {
		if !strings.Contains(top, want) {
			t.Fatal("failed round lost existing state or error", want, top)
		}
	}
	add("state-clear", 40, "done", &taskStatePatch{Blockers: []string{}, NextActions: []string{"继续真实设备验证（尚未执行）"}})
	if _, err := a.syncWorkspaceMemory(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	after = readWorkspaceWorklog(t, task, report.MainDocument)
	top = after[:strings.Index(after, workspaceTaskStateEnd)]
	if strings.Contains(top, "等待有效回环设备") || !strings.Contains(top, "来源报告失败") {
		t.Fatal("explicit clear or omitted evidence merge failed", top)
	}
	// Editing managed text pauses automatic replacement while milestones keep
	// their original chronological source markers and manual notes remain intact.
	edited := strings.Replace(after, "### 任务目标\n\n验证375平台串口回环", "### 任务目标\n\n人工调整：检查电源", 1)
	if err := os.WriteFile(file, []byte(edited), 0600); err != nil {
		t.Fatal(err)
	}
	add("state-after-edit", 50, "done", &taskStatePatch{Goal: "不能覆盖人工目标", Progress: []string{"新增进展保留在里程碑"}})
	report, err = a.syncWorkspaceMemory(context.Background(), task.ID)
	final := readWorkspaceWorklog(t, task, report.MainDocument)
	if err != nil || !strings.Contains(strings.Join(report.Warnings, " "), "人工修改") || !strings.Contains(final, "人工调整：检查电源") || !strings.Contains(final, human) || !strings.Contains(final, workspaceWorklogMarker(task.ID+"/state-after-edit")) {
		t.Fatal("human state edit lost or later progress omitted", report, err, final)
	}
	if _, err := a.syncWorkspaceMemory(context.Background(), task.ID); err != nil || final != readWorkspaceWorklog(t, task, report.MainDocument) {
		t.Fatal("state synchronization was not idempotent", err)
	}
}

func TestWorkspaceTaskStateIncompleteHistoryPreservesManagedStateAndAppendsSources(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	add := func(summary string, patch *taskStatePatch) Run {
		t.Helper()
		run := workspaceMemoryRun(t, a, task, "检查合成任务", summary, "done")
		raw, _ := json.Marshal(taskMemory{Summary: summary, TaskState: patch})
		if _, err := a.store.Exec("INSERT INTO task_memories(run_id,task_id,payload,updated) VALUES(?,?,?,?)", run.ID, task.ID, string(raw), run.Finished); err != nil {
			t.Fatal(err)
		}
		return run
	}
	add("已明确合成目标与计划", &taskStatePatch{Goal: "保留已有375平台验证目标", Plan: []taskStatePlanStep{{Text: "连接合成回环后检查数据", Status: "pending"}}})
	report, err := a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	initial := readWorkspaceWorklog(t, task, report.MainDocument)
	region := func(content string) string {
		t.Helper()
		start, end := strings.Index(content, workspaceTaskStateStart), strings.Index(content, workspaceTaskStateEnd)
		if start < 0 || end < start {
			t.Fatal("managed state region missing", content)
		}
		return content[start : end+len(workspaceTaskStateEnd)]
	}
	originalRegion := region(initial)
	partial := &taskStatePatch{Progress: []string{"新进展已记录，等待回环设备"}}
	newRun := add("新进展已记录，等待回环设备", partial)
	files, _, err := a.store.workspaceMemoryFiles(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	var limited workspaceMemoryFile
	for _, file := range files {
		if file.AppendWorklog {
			limited = file
		}
	}
	if limited.Path == "" {
		t.Fatal("generated append worklog missing")
	}
	// Exercise the real writer with an incomplete reconstructed patch window,
	// without creating 2001 large turns. Older omitted goal/plan cannot be
	// rebuilt from this partial patch and must never replace the current state.
	limited.StateIncomplete = true
	limited.StateContent = workspaceTaskStateContent(task, *partial, newRun, "")
	if strings.Contains(limited.StateContent, "保留已有375平台验证目标") {
		t.Fatal("test did not create an incomplete state candidate")
	}
	report, err = workspaceMemoryIO(context.Background(), task, "sync", []workspaceMemoryFile{limited})
	if err != nil || !strings.Contains(strings.Join(report.Warnings, " "), "历史不完整") {
		t.Fatal("incomplete history did not warn", report, err)
	}
	after := readWorkspaceWorklog(t, task, limited.Path)
	if region(after) != originalRegion || !strings.Contains(after, "连接合成回环后检查数据") || strings.Count(after, workspaceWorklogMarker(task.ID+"/"+newRun.ID)) != 1 || !strings.Contains(after, "新进展已记录，等待回环设备") {
		t.Fatal("incomplete history replaced existing state or failed to append new progress", after)
	}
	if _, err := workspaceMemoryIO(context.Background(), task, "sync", []workspaceMemoryFile{limited}); err != nil || after != readWorkspaceWorklog(t, task, limited.Path) {
		t.Fatal("limited synchronization was not idempotent", err)
	}
}

func TestWorkspaceTaskStateHashHonorsCRLFAndRejectsMalformedBlocks(t *testing.T) {
	body := "## 当前任务状态\n\n### 任务目标\n\n合成目标\n"
	original := "# 人工标题\n\n" + workspaceTaskStateBlock(body) + "\n\n人工尾部  \n"
	crlf := strings.ReplaceAll(original, "\n", "\r\n")
	updated, conflict := updateWorkspaceWorklogState(crlf, strings.Replace(body, "合成目标", "下一目标", 1))
	if conflict || !strings.HasPrefix(updated, "# 人工标题\r\n\r\n") || !strings.HasSuffix(updated, "人工尾部  \r\n") || strings.Contains(strings.ReplaceAll(updated, "\r\n", ""), "\n") {
		t.Fatal("CRLF byte preservation failed", updated, conflict)
	}
	for _, bad := range []string{strings.Replace(original, "合成目标", "人工目标", 1), original + workspaceTaskStateEnd, strings.Replace(original, " -->", " malformed -->", 1)} {
		got, conflict := updateWorkspaceWorklogState(bad, body)
		if !conflict || got != bad {
			t.Fatal("invalid or edited region overwritten", got, conflict)
		}
	}
}
