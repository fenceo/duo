package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Nil slices are omitted patches; an explicitly empty slice clears that field.
// This is task state, not a reusable knowledge claim or an independent verifier.
type taskStatePatch struct {
	Goal        string              `json:"goal,omitempty"`
	GoalReason  string              `json:"goal_reason,omitempty"`
	Acceptance  []string            `json:"acceptance"`
	Plan        []taskStatePlanStep `json:"plan"`
	Progress    []string            `json:"progress"`
	Decisions   []string            `json:"decisions"`
	Evidence    []taskStateEvidence `json:"evidence"`
	Blockers    []string            `json:"blockers"`
	NextActions []string            `json:"next_actions"`
}

type taskStatePlanStep struct {
	Text     string `json:"text"`
	Status   string `json:"status"`
	Evidence string `json:"evidence,omitempty"`
}

type taskStateEvidence struct {
	Text   string `json:"text"`
	Quote  string `json:"quote"`
	Status string `json:"status"`
}

func validTaskStateText(value string, limit int) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00') && utf8.RuneCountInString(value) <= limit
}

func validTaskStatePatch(state *taskStatePatch, reply string) bool {
	if state == nil {
		return true
	}
	if !validTaskStateText(state.Goal, 1500) || !validTaskStateText(state.GoalReason, 1000) || len(state.Plan) > 8 || len(state.Evidence) > 8 {
		return false
	}
	if state.GoalReason != "" && strings.TrimSpace(state.Goal) == "" {
		return false
	}
	switch strings.ToLower(strings.Trim(state.Goal, " !?。！？，,.\t\n\r")) {
	case "你好", "您好", "hello", "hi", "好的", "继续", "你好继续", "ok":
		return false
	}
	for _, list := range [][]string{state.Acceptance, state.Progress, state.Decisions, state.Blockers, state.NextActions} {
		if len(list) > 8 {
			return false
		}
		for _, text := range list {
			if strings.TrimSpace(text) == "" || !validTaskStateText(text, 1000) {
				return false
			}
		}
	}
	for _, step := range state.Plan {
		if strings.TrimSpace(step.Text) == "" || !validTaskStateText(step.Text, 1000) || !validTaskStateText(step.Evidence, 1500) {
			return false
		}
		switch step.Status {
		case "pending", "in_progress", "blocked":
		case "done":
			if strings.TrimSpace(step.Evidence) == "" {
				return false
			}
		default:
			return false
		}
		if step.Evidence != "" && !strings.Contains(reply, step.Evidence) {
			return false
		}
	}
	for _, evidence := range state.Evidence {
		if strings.TrimSpace(evidence.Text) == "" || strings.TrimSpace(evidence.Quote) == "" || !validTaskStateText(evidence.Text, 1000) || !validTaskStateText(evidence.Quote, 1500) || !strings.Contains(reply, evidence.Quote) {
			return false
		}
		if evidence.Status != "passed" && evidence.Status != "failed" && evidence.Status != "unverified" {
			return false
		}
	}
	return true
}

func portableTaskStatePatch(state *taskStatePatch, workspace string) *taskStatePatch {
	if state == nil {
		return nil
	}
	copy := *state
	clean := func(text string) string { return workspaceMemoryText(text, workspace) }
	copy.Goal, copy.GoalReason = clean(copy.Goal), clean(copy.GoalReason)
	list := func(values []string) []string {
		if values == nil {
			return nil
		}
		out := make([]string, len(values))
		for i, value := range values {
			out[i] = clean(value)
		}
		return out
	}
	copy.Acceptance, copy.Progress, copy.Decisions = list(copy.Acceptance), list(copy.Progress), list(copy.Decisions)
	copy.Blockers, copy.NextActions = list(copy.Blockers), list(copy.NextActions)
	if copy.Plan != nil {
		copy.Plan = append([]taskStatePlanStep{}, copy.Plan...)
		for i := range copy.Plan {
			copy.Plan[i].Text, copy.Plan[i].Evidence = clean(copy.Plan[i].Text), clean(copy.Plan[i].Evidence)
		}
	}
	if copy.Evidence != nil {
		copy.Evidence = append([]taskStateEvidence{}, copy.Evidence...)
		for i := range copy.Evidence {
			copy.Evidence[i].Text, copy.Evidence[i].Quote = clean(copy.Evidence[i].Text), clean(copy.Evidence[i].Quote)
		}
	}
	return &copy
}

func mergeTaskState(current taskStatePatch, patch *taskStatePatch) taskStatePatch {
	if patch == nil {
		return current
	}
	if strings.TrimSpace(patch.Goal) != "" {
		current.Goal = patch.Goal
		current.GoalReason = patch.GoalReason
	}
	if patch.Acceptance != nil {
		current.Acceptance = patch.Acceptance
	}
	if patch.Plan != nil {
		current.Plan = patch.Plan
	}
	if patch.Progress != nil {
		current.Progress = patch.Progress
	}
	if patch.Decisions != nil {
		current.Decisions = patch.Decisions
	}
	if patch.Evidence != nil {
		current.Evidence = patch.Evidence
	}
	if patch.Blockers != nil {
		current.Blockers = patch.Blockers
	}
	if patch.NextActions != nil {
		current.NextActions = patch.NextActions
	}
	return current
}

// A persisted patch is still a model report. Reject malformed or unsupported
// patches when rebuilding state as well as when initially parsing the reply.
// A missing patch never becomes an inferred goal, plan or completed action.
func workspaceTaskStatePatchForRun(task Task, run Run, memory *taskMemory) *taskStatePatch {
	if memory == nil || memory.TaskState == nil {
		return nil
	}
	patch := portableTaskStatePatch(memory.TaskState, task.Workspace)
	if !validTaskStatePatch(patch, workspaceMemoryText(run.Result, task.Workspace)) {
		return nil
	}
	return patch
}

func workspaceTaskStateCoverage(task Task, latest Run, memory *taskMemory) string {
	if latest.ID == "" {
		return "尚无已结束轮次；目标和计划等待有依据的更新。"
	}
	if workspaceTaskStatePatchForRun(task, latest, memory) != nil {
		return "最近一轮提供了格式与引用检查通过的任务状态增量；这仍是来源报告，不代表独立验证。"
	}
	if memory != nil && memory.TaskState != nil || strings.Contains(latest.Result, memoryMarker(latest.ID)) {
		return "最近一轮的附加状态未通过格式或引用检查，未用于覆盖已有目标、计划或验证结论；原始回复和执行状态仍按时间保存。"
	}
	return "最近一轮未提供有效 task_state；已有目标、计划和验证结论保留为先前记录，不能据此判断本轮已经更新或完成。原始需求、回复及执行状态仍按时间保存。"
}

func workspaceTaskStateContent(task Task, state taskStatePatch, latest Run, source string, latestMemory ...*taskMemory) string {
	var body strings.Builder
	clean := func(value string) string { return workspaceWorklogText(value, task.Workspace, 1800) }
	body.WriteString("## 当前任务状态\n\n验证结果保留来源和适用条件，来源报告不等于独立复核。人工补充可写在下方备注中。\n\n")
	if latest.ID != "" {
		fmt.Fprintf(&body, "最近执行：%s · %s · [来源](%s)\n\n", memoryDate(latest.Created), clean(latest.Status), source)
		if latest.Error != "" {
			body.WriteString("最近执行错误（未解决）：" + clean(latest.Error) + "\n\n")
		}
	}
	var memory *taskMemory
	if len(latestMemory) != 0 {
		memory = latestMemory[0]
	}
	body.WriteString("状态维护：" + workspaceTaskStateCoverage(task, latest, memory) + "\n\n")
	goal := clean(state.Goal)
	if strings.TrimSpace(goal) == "" {
		goal = "待明确任务目标。本轮需求与助手结果已按时间保留在下方；尚无有效目标增量，不从问候、任务名称或单条问题推断。"
	}
	body.WriteString("### 任务目标\n\n" + goal + "\n\n")
	if state.GoalReason != "" {
		body.WriteString("目标变更原因：" + clean(state.GoalReason) + "\n\n")
	}
	section := func(title string, values []string, fallback string) {
		body.WriteString("### " + title + "\n\n")
		if len(values) == 0 {
			body.WriteString(fallback + "\n\n")
			return
		}
		for _, value := range values {
			body.WriteString("- " + clean(value) + "\n")
		}
		body.WriteByte('\n')
	}
	section("验收标准", state.Acceptance, "尚未记录验收标准。")
	body.WriteString("### 当前计划\n\n")
	if len(state.Plan) == 0 {
		body.WriteString("待整理计划。\n\n")
	}
	labels := map[string]string{"pending": "待执行", "in_progress": "进行中", "done": "已完成（来源报告）", "blocked": "受阻"}
	for _, step := range state.Plan {
		fmt.Fprintf(&body, "- [%s] %s\n", labels[step.Status], clean(step.Text))
		if step.Evidence != "" {
			body.WriteString("  - 来源依据：" + clean(step.Evidence) + "\n")
		}
	}
	section("当前进展", state.Progress, "待整理关键进展；原始过程见下方来源记录。")
	section("决策与理由", state.Decisions, "尚未记录决策。")
	body.WriteString("### 验证与证据\n\n")
	if len(state.Evidence) == 0 {
		body.WriteString("尚未记录验证结果。\n\n")
	}
	for _, item := range state.Evidence {
		label := map[string]string{"passed": "来源报告通过", "failed": "来源报告失败", "unverified": "未验证"}[item.Status]
		fmt.Fprintf(&body, "- **%s**：%s\n  - 回复依据：“%s”\n", label, clean(item.Text), clean(item.Quote))
	}
	section("阻塞", state.Blockers, "尚无已记录阻塞，不代表任务已经完成。")
	section("下一步", state.NextActions, "尚未整理下一步。")
	return body.String()
}
