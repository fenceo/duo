package main

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func workspaceMemoryLegacyFixture(t *testing.T, a *App, task Task) string {
	t.Helper()
	files, _, err := a.store.workspaceMemoryFiles(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	current := workspaceMemoryTaskBase(task)
	legacy := path.Join(".duo", "tasks", task.ID)
	for i := range files {
		files[i].Path = strings.ReplaceAll(files[i].Path, current, legacy)
		content := strings.Split(files[i].Content, "\n<!-- duo-workspace-generated:")[0]
		files[i].Content = workspaceMemoryManaged(strings.ReplaceAll(content, current, legacy))
	}
	if _, err = workspaceMemoryIO(context.Background(), task, "sync", files); err != nil {
		t.Fatal(err)
	}
	return legacy
}

func TestWorkspaceMemoryReadableDirectoriesStableRenameAndLegacyMigration(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	task.Title = "375 调试 / NFS:烧录"
	if _, err := a.store.Exec("UPDATE tasks SET title=? WHERE id=?", task.Title, task.ID); err != nil {
		t.Fatal(err)
	}
	run := workspaceMemoryRun(t, a, task, "legacy requirement", "one canonical conversation", "done")
	legacy := workspaceMemoryLegacyFixture(t, a, task)
	report, err := a.syncWorkspaceMemory(context.Background(), task.ID)
	base := workspaceMemoryTaskBase(task)
	if err != nil || len(report.Tasks) != 1 || report.TaskDirectory != base || !strings.Contains(base, "375-调试-NFS-烧录-") || strings.Contains(base, task.ID) {
		t.Fatal(report, base, err)
	}
	if len(report.Documents) != 3 {
		t.Fatal("task documents not discoverable", report.Documents)
	}
	canonical := path.Join(base, "runs", run.ID+".md")
	data, err := os.ReadFile(filepath.Join(task.Workspace, filepath.FromSlash(canonical)))
	if err != nil || !strings.Contains(string(data), "one canonical conversation") {
		t.Fatal(string(data), err)
	}
	redirect, err := os.ReadFile(filepath.Join(task.Workspace, filepath.FromSlash(path.Join(legacy, "runs", run.ID+".md"))))
	if err != nil || strings.Contains(string(redirect), "one canonical conversation") || !strings.Contains(string(redirect), "duo-workspace-redirect:"+base) {
		t.Fatal("old path should contain a link, not duplicate transcript", string(redirect), err)
	}
	if _, err := a.store.Exec("UPDATE tasks SET title='renamed task' WHERE id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	report, err = a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil || report.TaskDirectory != base || len(report.Tasks) != 1 {
		t.Fatal("rename changed stable directory", report, err)
	}
	if data, err := workspaceMemoryIORequest(context.Background(), task, workspaceMemoryPayload{Action: "document", Path: canonical}); err != nil || data.Path != canonical || !strings.Contains(data.Content, "one canonical conversation") {
		t.Fatal("canonical document not readable", data, err)
	}
	other := workspaceMemoryLocalTask(t, a, taskFor(t, a), task.Workspace)
	if text, ids, err := a.workspaceMemoryContext(context.Background(), other); err != nil || len(ids) != 1 || !strings.Contains(text, base+"/handoff.md") {
		t.Fatal("migrated source was lost or duplicated", text, ids, err)
	}
}

func TestWorkspaceMemoryLegacyHumanEditsAndExtraFilesBlockMigration(t *testing.T) {
	for _, extra := range []bool{false, true} {
		t.Run(map[bool]string{false: "edited record", true: "extra file"}[extra], func(t *testing.T) {
			a, task := workspaceMemoryFixture(t)
			run := workspaceMemoryRun(t, a, task, "legacy", "generated", "done")
			legacy := workspaceMemoryLegacyFixture(t, a, task)
			file := path.Join(legacy, "runs", run.ID+".md")
			if extra {
				file = path.Join(legacy, "my-notes.md")
			}
			if err := os.WriteFile(filepath.Join(task.Workspace, filepath.FromSlash(file)), []byte("important human text\n"), 0600); err != nil {
				t.Fatal(err)
			}
			report, err := a.syncWorkspaceMemory(context.Background(), task.ID)
			if err != nil || report.TaskDirectory != legacy || !strings.Contains(strings.Join(report.Warnings, " "), "人工修改") {
				t.Fatal("edited legacy archive was migrated", report, err)
			}
			data, err := os.ReadFile(filepath.Join(task.Workspace, filepath.FromSlash(file)))
			if err != nil || string(data) != "important human text\n" {
				t.Fatal("human text not retained", string(data), err)
			}
			if _, err := os.Stat(filepath.Join(task.Workspace, filepath.FromSlash(workspaceMemoryTaskBase(task)))); !os.IsNotExist(err) {
				t.Fatal("archive duplicated into titled directory", err)
			}
		})
	}
}

func TestWorkspaceMemoryDocumentReaderConfinesAndBoundsFiles(t *testing.T) {
	_, task := workspaceMemoryFixture(t)
	for _, unsafe := range []string{"AGENTS.md", ".duo/tasks/../secret.md", ".duo/tasks/a/runs/../../README.md", ".duo/tasks/a/runs/secret.txt", ".duo/tasks/a/.git/config", ".duo/tasks/a/runs/CON.md:stream"} {
		if _, err := workspaceMemoryIORequest(context.Background(), task, workspaceMemoryPayload{Action: "document", Path: unsafe}); err == nil {
			t.Fatal("unsafe document accepted", unsafe)
		}
	}
	file := filepath.Join(task.Workspace, ".duo", "tasks", "fixture", "runs", "oversized.md")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(strings.Repeat("x", workspaceMemoryFileLimit+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := workspaceMemoryIORequest(context.Background(), task, workspaceMemoryPayload{Action: "document", Path: ".duo/tasks/fixture/runs/oversized.md"}); err == nil {
		t.Fatal("oversize record accepted")
	}
}

func TestWorkspaceMemoryForkPrefersMigratedCanonicalDirectoryOverStaleAlias(t *testing.T) {
	a, original := workspaceMemoryFixture(t)
	run := workspaceMemoryRun(t, a, original, "source requirement", "canonical source answer", "done")
	legacy := workspaceMemoryLegacyFixture(t, a, original)
	fork := workspaceMemoryLocalTask(t, a, taskFor(t, a), original.Workspace)
	copied := workspaceMemoryRun(t, a, fork, run.Input, run.Result, run.Status)
	if _, err := a.store.Exec("INSERT INTO run_fork_sources VALUES(?,?,?)", copied.ID, original.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.syncWorkspaceMemory(context.Background(), fork.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.syncWorkspaceMemory(context.Background(), original.ID); err != nil {
		t.Fatal(err)
	}
	canonical := workspaceMemoryTaskBase(original)
	// The preserved user handoff has no machine metadata; the fork still has
	// the old source path. A removed redirect must never recreate the content.
	if err := os.WriteFile(filepath.Join(original.Workspace, filepath.FromSlash(path.Join(canonical, "handoff.md"))), []byte("human maintained handoff\n"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(original.Workspace, filepath.FromSlash(path.Join(legacy, "runs", run.ID+".md")))
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	report, err := a.syncWorkspaceMemory(context.Background(), fork.ID)
	if err != nil {
		t.Fatal(report, err)
	}
	if _, err := os.Stat(alias); !os.IsNotExist(err) {
		t.Fatal("stale alias recreated a duplicate canonical source", err)
	}
	for _, task := range report.Tasks {
		if task.ID == fork.ID && (len(task.Sources) != 1 || task.Sources[0].Path != path.Join(canonical, "runs", run.ID+".md")) {
			t.Fatal("fork did not repair the old source path", task)
		}
	}
}
