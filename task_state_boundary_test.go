package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestLiveTaskStatePromptEchoCannotCreateMilestone(t *testing.T) {
	const run = "prompt-echo-fixture"
	const public = "实际公开结论：合成检查已通过，仍需用户验收。"
	prompt := liveTaskStatePrompt(run)
	if _, patch, _ := splitLiveTaskState(prompt, run); patch != nil {
		t.Fatal("prompt's format example was treated as an actual milestone", patch)
	}
	// A runner may echo its full input. The format placeholder must remain
	// non-state, while later actual prose and the final memory still parse.
	memory := taskMemory{Summary: "保留正常最终记忆", TaskState: &taskStatePatch{Goal: "真实终态目标"}, KnowledgeCandidates: []knowledgeCandidate{}}
	raw, _ := json.Marshal(memory)
	suffix := memoryMarker(run) + "\n" + string(raw) + "\n<!-- /duo-memory:" + run + " -->"
	for _, text := range []string{prompt, "reply: " + prompt + memoryPrompt(run) + public + "\n" + suffix} {
		visible, updates, hidden := extractLiveTaskStates(text, run)
		if !hidden || len(updates) != 0 || strings.Contains(visible, liveTaskStateMarker(run)) {
			t.Fatal("echoed prompt created state or exposed its internal marker", updates, visible)
		}
		if strings.Contains(text, public) {
			body, parsed := splitTaskMemory(visible, run)
			if parsed == nil || parsed.Summary != memory.Summary || parsed.TaskState == nil || parsed.TaskState.Goal != memory.TaskState.Goal || !strings.Contains(body, public) {
				t.Fatal("removing echoed format consumed actual prose or final memory", body, parsed)
			}
		}
	}
	// The fix must not suppress a genuine milestone following the echoed
	// instructions. Its persisted receipt must pass the same replay checks.
	actual := taskStatePatch{Progress: []string{public}, Plan: []taskStatePlanStep{{Text: "合成检查", Status: "done", Evidence: public}}}
	text := "reply: " + prompt + memoryPrompt(run) + liveStateMessage(run, public, actual) + "\n" + suffix
	visible, updates, _ := extractLiveTaskStates(text, run)
	if len(updates) != 1 || len(updates[0].State.Plan) != 1 || updates[0].State.Plan[0].Evidence != public || !strings.Contains(updates[0].Report, public) {
		t.Fatal("genuine milestone after echoed prompt was lost", updates)
	}
	receipt, _ := json.Marshal(updates[0])
	if value := decodeLiveTaskState(string(receipt), ""); value == nil || len(value.State.Plan) != 1 || value.State.Plan[0].Evidence != public {
		t.Fatal("genuine public milestone could not be replayed", value)
	}
	body, parsed := splitTaskMemory(visible, run)
	if parsed == nil || parsed.Summary != memory.Summary || !strings.Contains(body, public) {
		t.Fatal("genuine milestone consumed the actual body or independent final memory", body, parsed)
	}
}

func TestLiveTaskStateHiddenMemoryCannotBecomePublicEvidence(t *testing.T) {
	const run = "public-boundary-fixture"
	const hiddenQuote = "只有隐藏元数据声称已完成验证"
	block := func(id string) string {
		return memoryMarker(id) + "\n{\"summary\":\"" + hiddenQuote + "\"}\n<!-- /duo-memory:" + id + " -->"
	}
	claim := taskStatePatch{Plan: []taskStatePlanStep{{Text: "不可伪造的完成", Status: "done", Evidence: hiddenQuote}}}
	for name, report := range map[string]string{
		"current envelope":    "公开仍在等待。\n" + block(run),
		"old run envelope":    "公开仍在等待。\n" + block("previous-run"),
		"multiple envelopes":  "公开仍在等待。\n" + block("previous-run") + "\n中间公开说明。\n" + block(run),
		"unfinished envelope": "公开仍在等待。\n" + memoryMarker("previous-run") + "\n{\"summary\":\"" + hiddenQuote + "\"",
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(liveTaskState{State: claim, Report: report})
			if value := decodeLiveTaskState(string(raw), ""); value != nil {
				t.Fatal("imported receipt accepted hidden metadata as public evidence", value)
			}
			if _, updates, _ := extractLiveTaskStates(liveStateMessage(run, report, claim), run); len(updates) != 0 {
				t.Fatal("live parser and imported receipt have different evidence boundaries", updates)
			}
		})
	}
	// Complete envelopes do not discard later public prose or its valid quote.
	const publicQuote = "公开的后续合成检查已经通过。"
	report := "首段公开说明。\n" + block("previous-run") + "\n" + publicQuote + "\n" + block(run)
	valid := taskStatePatch{Plan: []taskStatePlanStep{{Text: "保留公开检查", Status: "done", Evidence: publicQuote}}}
	raw, _ := json.Marshal(liveTaskState{State: valid, Report: report})
	value := decodeLiveTaskState(string(raw), "")
	if value == nil || !strings.Contains(value.Report, publicQuote) || strings.Contains(value.Report, hiddenQuote) || strings.Contains(value.Report, "duo-memory") {
		t.Fatal("valid public quote was lost or hidden memory persisted", value)
	}
	_, updates, _ := extractLiveTaskStates(liveStateMessage(run, report, valid), run)
	if len(updates) != 1 || updates[0].Report != value.Report {
		t.Fatal("live and replayed public reports diverged", updates, value)
	}
	// Simulate a legacy/imported database receipt, bypassing the live parser.
	a, task := workspaceMemoryFixture(t)
	a.updating.Store(true)
	source := workspaceMemoryRun(t, a, task, "等待公开验证", "", "running")
	if err := a.store.captureLiveTaskState(task, source.ID, &taskStatePatch{Goal: "保留原任务目标"}, "公开目标已约定。"); err != nil {
		t.Fatal(err)
	}
	forged, _ := json.Marshal(liveTaskState{State: claim, Report: "公开仍在等待。\n" + block("previous-run")})
	if err := a.store.event(task.ID, source.ID, "task_state", string(forged)); err != nil {
		t.Fatal(err)
	}
	state, _, err := a.store.workspaceMemoryCurrentState(context.Background(), task)
	if err != nil || state.Goal != "保留原任务目标" || len(state.Plan) != 0 {
		t.Fatal("state rebuild folded an invalid imported completion claim", state, err)
	}
	stored, err := a.store.liveTaskStateForRun(context.Background(), task, source.ID)
	if err != nil || len(stored) != 1 || stored[0].State.Goal != "保留原任务目标" {
		t.Fatal("checkpoint replay accepted an invalid imported report", stored, err)
	}
}

func TestKnowledgeExtractionCannotChangeWorkspaceTaskState(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	a.updating.Store(true)
	ctx := context.Background()
	chat := workspaceMemoryRun(t, a, task, "继续调试", "公开任务目标与计划。", "done")
	initial := taskMemory{TaskState: &taskStatePatch{Goal: "原调试目标", Plan: []taskStatePlanStep{{Text: "等待设备验收", Status: "pending"}}}}
	raw, _ := json.Marshal(initial)
	if _, err := a.store.Exec("INSERT INTO task_memories(run_id,task_id,payload,updated) VALUES(?,?,?,?)", chat.ID, task.ID, string(raw), now()); err != nil {
		t.Fatal(err)
	}
	// Old releases may already have saved task_state on independent knowledge
	// jobs. Rebuilding must ignore it without dropping their ordinary history.
	legacy := workspaceMemoryRun(t, a, task, "旧知识整理", "已完成知识摘录。", "done")
	legacy.Kind = "knowledge"
	if _, err := a.store.Exec("UPDATE runs SET kind='knowledge' WHERE id=?", legacy.ID); err != nil {
		t.Fatal(err)
	}
	legacyMemory := taskMemory{Summary: "旧知识结果", TaskState: &taskStatePatch{Goal: "错误覆盖成知识整理目标", Plan: []taskStatePlanStep{{Text: "整理知识", Status: "done", Evidence: legacy.Result}}}}
	raw, _ = json.Marshal(legacyMemory)
	if _, err := a.store.Exec("INSERT INTO task_memories(run_id,task_id,payload,updated) VALUES(?,?,?,?)", legacy.ID, task.ID, string(raw), now()); err != nil {
		t.Fatal(err)
	}
	state, sources, err := a.store.workspaceMemoryCurrentState(ctx, task)
	if err != nil || state.Goal != initial.TaskState.Goal || len(state.Plan) != 1 || state.Plan[0].Status != "pending" || len(sources) != 1 || sources[0].ID != chat.ID {
		t.Fatal("legacy knowledge extraction replaced the chat task state", state, sources, err)
	}
	if workspaceTaskStatePatchForRun(task, legacy, &legacyMemory) != nil {
		t.Fatal("knowledge history was presented as a valid current-task delta")
	}
	// New captures retain useful candidates and summaries but never persist
	// the summarizer's optional state as the source task's current state.
	fresh := workspaceMemoryRun(t, a, task, "新知识整理", "合成验证记录通过，未调用真实模型或设备。", "done")
	if _, err := a.store.Exec("UPDATE runs SET kind='knowledge' WHERE id=?", fresh.ID); err != nil {
		t.Fatal(err)
	}
	memory := organizationMemory("候选正文仍应可正常保存和提取。")
	memory.Summary, memory.TaskState = "保存知识摘要", legacyMemory.TaskState
	tx, err := a.store.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = captureExtractedMemory(tx, task.ID, fresh.ID, &memory); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err = a.store.QueryRow("SELECT payload FROM task_memories WHERE run_id=?", fresh.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var captured taskMemory
	if json.Unmarshal([]byte(payload), &captured) != nil || captured.TaskState != nil || captured.Summary != memory.Summary || len(captured.KnowledgeCandidates) != 1 || memory.TaskState == nil {
		t.Fatal("knowledge capture lost useful material or retained source-task state", captured)
	}
	report, err := a.store.extractTaskKnowledge(ctx, task.ID, fresh.ID, nil)
	if err != nil || report.Created != 1 {
		t.Fatal("isolating task state broke ordinary knowledge harvesting", report, err)
	}
	state, _, err = a.store.workspaceMemoryCurrentState(ctx, task)
	if err != nil || state.Goal != initial.TaskState.Goal || len(state.Plan) != 1 || state.Plan[0].Status != "pending" {
		t.Fatal("new knowledge job changed the workspace task state", state, err)
	}
}
