package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWorkspaceWorklogRecallReadsBoundedEditedMarkdownAndHonorsScope(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	workspaceMemoryRun(t, a, task, "record before manual editing", "original response", "done")
	report, err := a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil || report.MainDocument == "" {
		t.Fatal(report, err)
	}
	file := filepath.Join(task.Workspace, filepath.FromSlash(report.MainDocument))
	edited := "# 人工维护的任务记录\n\n## 当前计划\neditorial-plan-use-fixture-nfs\npassword=manual-recall-secret\n\n" + strings.Repeat("中", 9500) + "\nlatest-editorial-verification-still-pending\n"
	if err := os.WriteFile(file, []byte(edited), 0600); err != nil {
		t.Fatal(err)
	}
	text, id, err := a.workspaceWorklogContext(context.Background(), task)
	if err != nil || !strings.HasPrefix(id, "worklog:"+task.ID+":") || !strings.Contains(text, "editorial-plan-use-fixture-nfs") || !strings.Contains(text, "latest-editorial-verification-still-pending") || strings.Contains(text, "manual-recall-secret") || !strings.Contains(text, `"truncated":true`) || utf8.RuneCountInString(text) > 8200 {
		t.Fatal("edited Markdown recall was absent, unsafe, or unbounded", id, text, err)
	}
	other := workspaceMemoryLocalTask(t, a, taskFor(t, a), task.Workspace)
	if text, _, err := a.workspaceWorklogContext(context.Background(), other); err != nil || text != "" {
		t.Fatal("another task received the edited worklog", text, err)
	}
	for _, reason := range []string{"archived", "deleted", "recall-off"} {
		t.Run(reason, func(t *testing.T) {
			candidate := task
			switch reason {
			case "archived":
				candidate.Archived = true
			case "deleted":
				candidate.Deleted = true
			case "recall-off":
				if err := a.store.set("automatic_knowledge", `{"capture":true,"recall":false}`); err != nil {
					t.Fatal(err)
				}
			}
			if text, _, err := a.workspaceWorklogContext(context.Background(), candidate); err != nil || text != "" {
				t.Fatal("recall scope/opt-out ignored", text, err)
			}
		})
	}
}

func TestWorkspaceWorklogAppReadsHumanNotesAndContinuingChanges(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	workspaceMemoryRun(t, a, task, "earlier request", "earlier answer", "done")
	report, err := a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(task.Workspace, filepath.FromSlash(report.MainDocument))
	if err := os.WriteFile(file, []byte("# Task log\n\n## 人工备注\neditorial-context-app-handoff\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"start-fresh-with-edited-file", "continue-the-existing-session"} {
		if _, err := a.submit(task.ID, input, "chat", "web"); err != nil {
			t.Fatal(err)
		}
		waitUntil(t, func() bool {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.workers[task.ID] == nil
		})
	}
	f := a.runner.(*fakeRunner)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.inputs) != 2 || !strings.Contains(f.inputs[0], "editorial-context-app-handoff") || !strings.Contains(f.inputs[0], "Duo 本任务 Markdown 记录") || !strings.Contains(f.inputs[1], `"mode":"changed"`) {
		t.Fatal("Markdown edits or incremental continuing-session context were omitted", f.inputs)
	}
	var stored string
	if err := a.store.QueryRow("SELECT input FROM runs WHERE task_id=? AND input='start-fresh-with-edited-file'", task.ID).Scan(&stored); err != nil || stored != "start-fresh-with-edited-file" {
		t.Fatal("recall changed the stored user request", stored, err)
	}
}

func TestWorkspaceWorklogContinuingReceiptsSkipUnchangedAndRetryUnacknowledged(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	workspaceMemoryRun(t, a, task, "synthetic prior question", "synthetic prior answer", "done")
	report, err := a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(task.Workspace, filepath.FromSlash(report.MainDocument))
	before := "# Editable task record\nold-context-never-replay\n\n## Current plan\nread-config-before-flashing\nobsolete-manual-action\n\n## Context\nunchanged-context-one\nunchanged-context-two\nunchanged-tail\n"
	if err := os.WriteFile(file, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := a.prepareWorkspaceWorklogContext(context.Background(), task)
	if err != nil || first == nil || !strings.Contains(first.Text, `"mode":"snapshot"`) {
		t.Fatal("fresh snapshot unavailable", first, err)
	}
	if err := a.acknowledgeWorkspaceWorklogContext(context.Background(), task, first, "native-receipt-fixture"); err != nil {
		t.Fatal(err)
	}
	task.Session = "native-receipt-fixture"
	if unchanged, err := a.prepareWorkspaceWorklogContext(context.Background(), task); err != nil || unchanged != nil {
		t.Fatal("unchanged document was replayed", unchanged, err)
	}
	after := strings.Replace(before, "read-config-before-flashing\nobsolete-manual-action", "manual-change-check-nfs-only\npassword=receipt-private-fixture", 1)
	if err := os.WriteFile(file, []byte(after), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := a.prepareWorkspaceWorklogContext(context.Background(), task)
	if err != nil || changed == nil || !strings.Contains(changed.Text, `"mode":"changed"`) || !strings.Contains(changed.Text, "manual-change-check-nfs-only") || strings.Contains(changed.Text, "old-context-never-replay") || strings.Contains(changed.Text, "obsolete-manual-action") || strings.Contains(changed.Text, "receipt-private-fixture") || !strings.Contains(changed.Text, `"replaced_or_removed_lines":2`) {
		t.Fatal("incremental context absent, repeated unrelated history, or leaked secret", changed, err)
	}
	// Preparing a request that fails before success must not consume the change.
	retry, err := a.prepareWorkspaceWorklogContext(context.Background(), task)
	if err != nil || retry == nil || retry.ID != changed.ID {
		t.Fatal("unacknowledged request lost its retry context", retry, err)
	}
	concurrent := after + "\nconcurrent-editor-note-not-yet-sent\n"
	if err := os.WriteFile(file, []byte(concurrent), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.acknowledgeWorkspaceWorklogContext(context.Background(), task, changed, ""); err != nil {
		t.Fatal(err)
	}
	newer, err := a.prepareWorkspaceWorklogContext(context.Background(), task)
	if err != nil || newer == nil || !strings.Contains(newer.Text, "concurrent-editor-note-not-yet-sent") {
		t.Fatal("acknowledgement consumed an unseen concurrent edit", newer, err)
	}
	if err := a.acknowledgeWorkspaceWorklogContext(context.Background(), task, newer, ""); err != nil {
		t.Fatal(err)
	}
	// Line-ending-only changes carry no new task information.
	if err := os.WriteFile(file, []byte(strings.ReplaceAll(concurrent, "\n", "\r\n")), 0600); err != nil {
		t.Fatal(err)
	}
	if unchanged, err := a.prepareWorkspaceWorklogContext(context.Background(), task); err != nil || unchanged != nil {
		t.Fatal("CRLF normalization created repeated recall", unchanged, err)
	}
	var count int
	var stored string
	if err := a.store.QueryRow("SELECT count(*),max(content) FROM workspace_worklog_context_receipts WHERE task_id=?", task.ID).Scan(&count, &stored); err != nil || count != 1 || strings.Contains(stored, "receipt-private-fixture") {
		t.Fatal("receipt was unbounded per-session history or retained a secret", count, err)
	}
	for _, changedTask := range []Task{
		func() Task { v := task; v.Engine = "deepseek-harness"; return v }(),
		func() Task { v := task; v.Session = "different-session"; return v }(),
		func() Task { v := task; v.Binding = &TaskEngineBinding{Revision: "new-binding"}; return v }(),
	} {
		newScope, err := a.prepareWorkspaceWorklogContext(context.Background(), changedTask)
		if err != nil || newScope == nil || !strings.Contains(newScope.Text, `"mode":"snapshot"`) {
			t.Fatal("receipt crossed engine, session, or binding scope", newScope, err)
		}
	}
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	cleared, err := a.prepareWorkspaceWorklogContext(context.Background(), task)
	if err != nil || cleared == nil || !strings.Contains(cleared.Text, `"mode":"changed"`) || !strings.Contains(cleared.Text, `"content":""`) || strings.Contains(cleared.Text, "concurrent-editor-note-not-yet-sent") {
		t.Fatal("clearing a previously acknowledged document retained obsolete recall", cleared, err)
	}
}

func TestWorkspaceWorklogChangedRegionBoundsLongEditsAndDescribesDeletion(t *testing.T) {
	previous := "# Task\nprior-value\nretained-tail\n"
	current := "# Task\n" + strings.Repeat("large middle fixture line\n", 2000) + "latest-edit-requires-real-validation\nretained-tail\n"
	region, first, last, removed := workspaceWorklogChangedRegion(previous, current)
	excerpt, cut := workspaceWorklogExcerpt(region, "", 7200)
	if !cut || utf8.RuneCountInString(excerpt) > 7350 || !strings.Contains(excerpt, "latest-edit-requires-real-validation") || first != 1 || last < 2000 || removed != 1 {
		t.Fatal("large change lost boundary, conclusion, or bounds", first, last, removed, excerpt)
	}
	region, _, _, removed = workspaceWorklogChangedRegion("heading\nremoved instruction\ntail\n", "heading\ntail\n")
	if strings.Contains(region, "removed instruction") || removed != 1 || !strings.Contains(region, "tail") {
		t.Fatal("deletion replayed an obsolete instruction", region, removed)
	}
}

func TestWorkspaceWorklogHandoffKeepsNewestRequestAndFinalConclusion(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	workspaceMemoryRun(t, a, task, "earlier request", "earlier answer", "done")
	report, err := a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	content := "# 当前人工任务说明\n\n保留当前已确认的接线方案。\n" + strings.Repeat("旧轮次只作为历史参考。\n", 1200) + "\n" +
		workspaceWorklogMarker(task.ID+"/recent-fixture") + "\n" + workspaceWorklogDetailMarker(task.ID+"/recent-fixture") + "\n\n### 最近一轮\n\n#### 本轮需求\n\n最新明确要求：先核对NFS配置，不执行烧录。\n\n" +
		strings.Repeat("助手中间的执行说明。", 1400) + "\n最终结论：仍需真实设备验证，不得宣称通过。\n"
	if err := os.WriteFile(filepath.Join(task.Workspace, filepath.FromSlash(report.MainDocument)), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	text, _, err := a.workspaceWorklogContext(context.Background(), task)
	if err != nil || !strings.Contains(text, "保留当前已确认的接线方案") || !strings.Contains(text, "最新明确要求：先核对NFS配置，不执行烧录。") || !strings.Contains(text, "最终结论：仍需真实设备验证，不得宣称通过。") || !strings.Contains(text, `"truncated":true`) || utf8.RuneCountInString(text) > 8500 {
		t.Fatal("handoff lost the newest requirement or final conclusion", text, err)
	}
}
