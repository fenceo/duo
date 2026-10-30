package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func legacyGreetingWorklogFixture(task Task) string {
	return "---\ntype: task_worklog\ntask_id: " + task.ID + "\ntitle: \"fixture\"\ntags: []\n---\n\n# fixture\n\n任务 ID：`" + task.ID + "`\n\n" +
		"## 任务目标\n\n你好\n\n## 当前计划\n\n尚未手工填写。可在这里维护下一步计划。\n\n## 备注\n\n可直接编辑目标、计划、备注和已有记录；后续只追加新轮次。请保留来源标记以避免重复回填。\n\n## 进展记录\n\n以下按轮次时间持续追加。历史回复和自动整理不等于独立验证；失败记录保留失败状态。\n"
}

func TestWorkspaceWorklogKeepsRequestResultsAndTailWithoutStructuredState(t *testing.T) {
	task := Task{ID: "fixture-task", Title: "验证任务", Workspace: `C:\fixture\workspace`}
	run := Run{ID: "fixture-run", Input: "只检查连接，不烧录设备。\npassword=fixture-request-secret", Result: "检查开始。\n" + strings.Repeat("中间结果", 2400) + "\n结论：设备仍未连接，真实硬件尚未验证。", Status: "failed", Created: 1000, Finished: 9000, Error: "合成连接失败"}
	content := workspaceWorklogEntryContent(task, run, nil, "runs/fixture-run.md", "## 过程摘录\n\n读取合成配置，未接触真实设备。")
	for _, want := range []string{"本轮需求（原始用户输入，非自动目标）", "只检查连接，不烧录设备。", "助手结论与执行结果", "检查开始。", "结论：设备仍未连接，真实硬件尚未验证。", "中间内容省略", "结束时间", "轮次 `fixture-run`", "runs/fixture-run.md", "执行过程摘录", "读取合成配置", "合成连接失败", "尚未整理为任务状态"} {
		if !strings.Contains(content, want) {
			t.Fatal("missing actual task material", want)
		}
	}
	if strings.Contains(content, "fixture-request-secret") || len([]rune(content)) > 10000 {
		t.Fatal("excerpt leaked a credential or exceeded its bounded size")
	}
	state := workspaceTaskStateContent(task, taskStatePatch{}, run, "runs/fixture-run.md")
	if !strings.Contains(state, "待明确任务目标") || strings.Contains(state, run.Input) || strings.Contains(state, "只检查连接") {
		t.Fatal("raw request became an invented goal", state)
	}
	structured := workspaceWorklogEntryContent(task, Run{ID: run.ID, Input: "本轮用户的详细要求", Result: "助手报告：已完成合成检查。", Status: "done"}, &taskMemory{Summary: "简短摘要"}, "runs/fixture-run.md")
	if !strings.Contains(structured, "本轮用户的详细要求") || !strings.Contains(structured, "助手报告：已完成合成检查。") || !strings.Contains(structured, "简短摘要") {
		t.Fatal("a short summary hid the underlying requirement or result", structured)
	}
}

func TestWorkspaceWorklogLegacyDetailBackfillPreservesHumanTextAndIsIdempotent(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	run := workspaceMemoryRun(t, a, task, "核对合成连接配置，不做真实烧录", "已核对合成配置；真实连接仍待验证。", "done")
	base := workspaceMemoryTaskBase(task)
	main := filepath.Join(task.Workspace, filepath.FromSlash(base), workspaceWorklogFilename)
	if err := os.MkdirAll(filepath.Dir(main), 0700); err != nil {
		t.Fatal(err)
	}
	history := "\n\n" + workspaceWorklogMarker(task.ID+"/"+run.ID) + "\n### 旧进展\n尚未整理，原来只有来源链接。\n\n人工追加：保留两格空白  \n"
	old := strings.ReplaceAll(legacyGreetingWorklogFixture(task)+history, "\n", "\r\n")
	if err := os.WriteFile(main, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	after := readWorkspaceWorklog(t, task, report.MainDocument)
	if strings.Contains(after, "## 任务目标\r\n\r\n你好") || !strings.Contains(after, "待明确任务目标") || !strings.Contains(after, strings.ReplaceAll(history, "\n", "\r\n")) || !strings.Contains(after, "核对合成连接配置，不做真实烧录") || !strings.Contains(after, "已核对合成配置；真实连接仍待验证。") || !strings.Contains(strings.Join(report.Warnings, " "), "旧版问候目标") {
		t.Fatal("legacy migration lost human/history text or failed to fill source content", after, report)
	}
	if strings.Count(after, workspaceWorklogMarker(task.ID+"/"+run.ID)) != 1 || strings.Count(after, workspaceWorklogDetailMarker(task.ID+"/"+run.ID)) != 1 {
		t.Fatal("canonical source or detail marker duplicated", after)
	}
	if _, err := a.syncWorkspaceMemory(context.Background(), task.ID); err != nil || readWorkspaceWorklog(t, task, report.MainDocument) != after {
		t.Fatal("legacy source backfill was not idempotent", err)
	}
}

func TestWorkspaceWorklogLegacyGreetingCorrectionRejectsManualTemplateEdits(t *testing.T) {
	original := legacyGreetingWorklogFixture(Task{ID: "fixture-task"}) + "\n人工记录保留。\n"
	managed := workspaceTaskStateBlock("## 当前任务状态\n\n### 任务目标\n\n人工当前目标\n") + "\n\n"
	hybrid := strings.Replace(original, "## 任务目标\n\n你好", managed+"## 任务目标\n\n你好", 1)
	if got, changed := upgradeWorkspaceLegacyWorklogHeader(hybrid); !changed || !strings.Contains(got, managed) || strings.Contains(got, "## 任务目标\n\n你好") || !strings.HasSuffix(got, "\n人工记录保留。\n") {
		t.Fatal("known legacy section was not corrected independently of current manual state", got)
	}
	for _, edited := range []string{
		strings.Replace(original, "尚未手工填写。", "手工计划：先检查接线。", 1),
		strings.Replace(original, "## 备注\n\n", "## 备注\n\n人工重要约定。\n", 1),
		strings.Replace(original, "\n\n你好\n\n", "\n\n人工维护的目标\n\n", 1),
		strings.Replace(original, "type: task_worklog", "type: personal_note", 1),
	} {
		got, changed := upgradeWorkspaceLegacyWorklogHeader(edited)
		if changed || got != edited {
			t.Fatal("an edited or unrelated document was changed", got)
		}
	}
}

func TestWorkspaceWorklogProcessIncludesRecentResultsAndEventSources(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	run := workspaceMemoryRun(t, a, task, "检查合成过程输出", "助手报告：仍有一项待验证。", "failed")
	for i := 0; i < 45; i++ {
		text := fmt.Sprintf("event-%02d", i)
		if i == 44 {
			text += "\n开始合成检查\n" + strings.Repeat("过程", 1000) + "\n最后结果：仍需真实设备验证。"
		}
		if err := a.store.event(task.ID, run.ID, "tool", text); err != nil {
			t.Fatal(err)
		}
	}
	process, err := a.store.workspaceMemoryProcess(context.Background(), run, task.Workspace)
	if err != nil || strings.Contains(process, "event-00") || !strings.Contains(process, "event-05") || !strings.Contains(process, "event-44") || !strings.Contains(process, "最后结果：仍需真实设备验证。") || !strings.Contains(process, "最近 40 条") || !strings.Contains(process, "事件 ") || strings.Index(process, "event-05") > strings.Index(process, "event-44") {
		t.Fatal("process omitted latest result, chronology or source identity", process, err)
	}
	report, err := a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	content := readWorkspaceWorklog(t, task, report.MainDocument)
	if !strings.Contains(content, "最后结果：仍需真实设备验证。") || !strings.Contains(content, "助手报告：仍有一项待验证。") || !strings.Contains(content, "执行过程摘录") {
		t.Fatal("primary Markdown omitted actual execution material", content)
	}
}
