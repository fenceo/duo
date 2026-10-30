package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWorkspaceWorklogHandoffCompactsLiveActivityBeforeDurableState(t *testing.T) {
	live := "## 执行快照（未结束）\n\n截至 2026-10-30 08:00:00.000 UTC，轮次 `run-observation-fixture` 处于执行中。\n\n" + strings.Repeat("公开活动尚未验证。", 650)
	state := "## 当前任务状态\n\n### 任务目标\n\n保留已确认的DDR排查目标。\n\n### 当前计划\n\n先核对NFS，不执行烧录。\n\n"
	content := "# Task record\n\n" + workspaceExecutionCheckpointBlock(live) + "\n\n" + workspaceTaskStateBlock(state) + "\n\n" + strings.Repeat("较早历史只作参考。\n", 900) + "\n" + workspaceWorklogMarker("task-fixture/run-fixture") + "\n### 最新要求\n\n最新终态要求仍需真实设备验证。\n"
	excerpt, cut := workspaceWorklogHandoffExcerpt(content)
	for _, want := range []string{"2026-10-30 08:00:00.000 UTC", "run-observation-fixture", "来源：原任务 Markdown", "保留已确认的DDR排查目标", "先核对NFS，不执行烧录", "最新终态要求仍需真实设备验证"} {
		if !strings.Contains(excerpt, want) {
			t.Fatal("live activity displaced durable handoff information", want, excerpt)
		}
	}
	if !cut || utf8.RuneCountInString(excerpt) > 7350 || strings.Contains(excerpt, workspaceExecutionCheckpointStart) {
		t.Fatal("compacted handoff has incorrect truncation or a misleading ownership marker", cut, utf8.RuneCountInString(excerpt))
	}
}

func TestWorkspaceWorklogHandoffLiveCompactionPreservesDiskAndReceipt(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	workspaceMemoryRun(t, a, task, "prior request", "prior answer", "done")
	report, err := a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	live := "## 执行快照（未结束）\n\n截至 2026-10-30 08:30:00.000 UTC，轮次 `current-fixture` 处于执行中。\n\n" + strings.Repeat("公开进度未验证。", 700)
	content := "# Task record\n\n" + workspaceExecutionCheckpointBlock(live) + "\n\n" + workspaceTaskStateBlock("## 当前任务状态\n\n### 任务目标\n\n继续保留原目标。\n") + "\n\n" + strings.Repeat("人工备注内容不得修改。", 250)
	file := filepath.Join(task.Workspace, filepath.FromSlash(report.MainDocument))
	if err = os.WriteFile(file, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	recall, err := a.prepareWorkspaceWorklogContext(context.Background(), task)
	if err != nil || recall == nil || !strings.Contains(recall.Text, `"truncated":true`) || !strings.Contains(recall.Text, "继续保留原目标") || !strings.Contains(recall.Text, "来源：原任务 Markdown") {
		t.Fatal("fresh-session prompt did not report compacted live content", recall, err)
	}
	if recall.Content != content || recall.Hash != workspaceMemoryHash(content) {
		t.Fatal("prompt-only compaction changed the durable receipt snapshot")
	}
	if after, readErr := os.ReadFile(file); readErr != nil || string(after) != content {
		t.Fatal("prompt compaction modified the editable Markdown", readErr)
	}
}

func TestWorkspaceWorklogLiveCompactionMarksOmissionsEvenWhenResultFits(t *testing.T) {
	content := "# Task\n\n" + workspaceExecutionCheckpointBlock("## 执行快照（未结束）\n\n轮次 `fit-fixture`，截至 2026-10-30。\n\n"+strings.Repeat("中", 6900)) + "\n\n## 任务目标\n\n保留目标与全部人工尾注。\n" + strings.Repeat("尾", 400)
	excerpt, cut := workspaceWorklogHandoffExcerpt(content)
	if !cut || utf8.RuneCountInString(excerpt) >= 7200 || strings.Contains(excerpt, "[中间记录未自动附加") || !strings.HasSuffix(excerpt, strings.Repeat("尾", 400)) {
		t.Fatal("live-only truncation was hidden or unrelated content was clipped", cut, utf8.RuneCountInString(excerpt))
	}
}

func TestWorkspaceWorklogLiveCompactionLeavesManualOrShortBlocksOrdinary(t *testing.T) {
	block := workspaceExecutionCheckpointBlock("## 执行快照（未结束）\n\n" + strings.Repeat("活", 8000))
	for name, content := range map[string]string{
		"no-live":        "# Human notes\n" + strings.Repeat("人", 9000),
		"edited-hash":    strings.Replace(block, "执行快照", "人工修改", 1),
		"duplicate":      block + "\n" + block,
		"missing-end":    strings.Replace(block, workspaceExecutionCheckpointEnd, "", 1),
		"short-document": workspaceExecutionCheckpointBlock("short live report") + "\n人工备注。",
	} {
		t.Run(name, func(t *testing.T) {
			if compacted, cut := workspaceWorklogCompactExecutionExcerpt(content); cut || compacted != content {
				t.Fatal("invalid/manual/short live content received privileged compaction", cut)
			}
		})
	}
}
