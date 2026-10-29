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
	for _, reason := range []string{"native", "archived", "deleted", "recall-off"} {
		t.Run(reason, func(t *testing.T) {
			candidate := task
			switch reason {
			case "native":
				candidate.Session = "existing-native-session"
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

func TestWorkspaceWorklogAppReadsHumanNotesOnlyOnFreshSession(t *testing.T) {
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
	if len(f.inputs) != 2 || !strings.Contains(f.inputs[0], "editorial-context-app-handoff") || !strings.Contains(f.inputs[0], "Duo 本任务 Markdown 记录") || strings.Contains(f.inputs[1], "Duo 本任务 Markdown 记录") {
		t.Fatal("Markdown edits were omitted or replayed into a continuing native session", f.inputs)
	}
	var stored string
	if err := a.store.QueryRow("SELECT input FROM runs WHERE task_id=? AND input='start-fresh-with-edited-file'", task.ID).Scan(&stored); err != nil || stored != "start-fresh-with-edited-file" {
		t.Fatal("recall changed the stored user request", stored, err)
	}
}
