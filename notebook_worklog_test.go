package main

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func notebookTaskDocument(t *testing.T, a *App, task Task) LibraryDocument {
	t.Helper()
	d, err := a.store.libraryDocument(context.Background(), "notebook:tasks/"+task.ID+"/summary.md")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func useTaskNotebookVault(t *testing.T, a *App) string {
	t.Helper()
	dir := t.TempDir()
	raw, _ := json.Marshal(VaultConfig{Enabled: true, Directory: dir, IncludeAutomatic: true, IncludeRuns: true, TaskFolders: true})
	if err := a.store.set("knowledge_vault", string(raw)); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "Duo")
}

func TestTaskWorklogReadableDocumentSourcesAndPlan(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	enableNotebook(t, a)
	task := taskFor(t, a)
	run := finishNotebook(t, a, task, notebookSample())
	d := notebookTaskDocument(t, a, task)
	if !readableTaskNotebookPath(d.Path) || path.Dir(d.Path) != "tasks" || !strings.Contains(d.Path, task.Title) || !strings.Contains(d.Path, task.ID[:8]) {
		t.Fatal("unreadable primary task document", d.Path)
	}
	for _, want := range []string{"## 任务目标", "## 当前计划", "## 进展记录", "## 决策与证据", "## 验证与可复用结论", "## 未决问题", "- [ ] 尚未验证真实硬件", "尺寸更新每帧合并", "模拟 100 次移动，仅合并为一次更新。", run.ID + ".md"} {
		if !strings.Contains(d.Content, want) {
			t.Fatalf("task document missing %q", want)
		}
	}
	if strings.Contains(d.Content, `C:\fixture`) || strings.Contains(d.Content, memoryMarker(run.ID)) {
		t.Fatal("task document leaked machine path or protocol payload")
	}
	readable, err := a.store.libraryDocument(context.Background(), "document:"+d.Path)
	if err != nil || readable.Content != d.Content {
		t.Fatal("new readable links do not resolve before file synchronization", err)
	}
	page, err := a.store.searchLibrary(context.Background(), "", task.ID, "", false, LibrarySearchOptions{Layer: "tasks"})
	if err != nil || len(page.Documents) != 1 || page.Documents[0].Path != d.Path {
		t.Fatal("task reading list must have one primary document", page, err)
	}
	if !strings.Contains(d.Content, "不是新增的执行指令") {
		t.Fatal("historical open questions were promoted to instructions")
	}
}

func TestTaskWorklogFilenameStableAcrossRenameAndExternalEdits(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	enableNotebook(t, a)
	task := taskFor(t, a)
	finishNotebook(t, a, task, notebookSample())
	root := useTaskNotebookVault(t, a)
	syncTestVault(t, a)
	initial := notebookTaskDocument(t, a, task)
	file := filepath.Join(root, filepath.FromSlash(initial.Path))
	if _, err := a.store.Exec("UPDATE tasks SET title='新任务标题' WHERE id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	renamed := notebookTaskDocument(t, a, task)
	if renamed.Path != initial.Path || !strings.Contains(renamed.Content, "# 新任务标题") {
		t.Fatal("renaming a task changed its exported filename or retained old title", renamed.Path)
	}
	report := syncTestVault(t, a)
	if len(report.Conflicts) != 0 || !strings.Contains(readTestFile(t, file), "# 新任务标题") {
		t.Fatal(report)
	}
	writeTestFile(t, file, readTestFile(t, file)+"\n外部人工补充，不要覆盖\n")
	report = syncTestVault(t, a)
	if len(report.Conflicts) != 0 {
		t.Fatal("an idle sync should preserve and index external annotations", report)
	}
	finishNotebook(t, a, task, taskMemory{Summary: "新的实际进展"})
	report = syncTestVault(t, a)
	if len(report.Conflicts) == 0 || !strings.Contains(readTestFile(t, file), "外部人工补充，不要覆盖") {
		t.Fatal("external worklog edits were overwritten", report)
	}
}

func TestTaskWorklogMigratesOldSummaryAndKeepsOldLinks(t *testing.T) {
	for _, edited := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "externally-edited"}[edited], func(t *testing.T) {
			a := fixture(t, &fakeRunner{})
			enableNotebook(t, a)
			task := taskFor(t, a)
			finishNotebook(t, a, task, notebookSample())
			root := useTaskNotebookVault(t, a)
			d := notebookTaskDocument(t, a, task)
			legacy := "tasks/" + task.ID + "/summary.md"
			old := d
			old.Content = "# 旧任务摘要\n\n保留旧摘要记录。"
			old.Path = legacy
			raw, err := renderVaultFile(old, map[string]any{"custom_property": "用户属性"})
			if err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(legacy))), 0700); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(root, filepath.FromSlash(legacy)), raw)
			if _, err = a.store.Exec("INSERT INTO library_links VALUES(?,?,?,?)", d.ID, legacy, documentHash(old), hash(raw)); err != nil {
				t.Fatal(err)
			}
			if edited {
				// Preview the upgrade before the external editor changes the old
				// file: a database-only notebook cache must not hide the change.
				if _, err = a.store.notebookDocuments(context.Background()); err != nil {
					t.Fatal(err)
				}
				writeTestFile(t, filepath.Join(root, filepath.FromSlash(legacy)), raw+"\n旧文件的外部编辑\n")
			}
			report := syncTestVault(t, a)
			if edited {
				if len(report.Conflicts) == 0 || !strings.Contains(readTestFile(t, filepath.Join(root, filepath.FromSlash(legacy))), "旧文件的外部编辑") {
					t.Fatal("legacy external edit was lost", report)
				}
				actual := notebookTaskDocument(t, a, task)
				if actual.Path != legacy {
					t.Fatal("protected old summary stopped being the actual navigation target", actual.Path)
				}
				for _, rel := range []string{"README.md", "tasks/" + task.ID + "/context.md", "tasks/" + task.ID + "/conversations.md"} {
					if strings.Contains(readTestFile(t, filepath.Join(root, filepath.FromSlash(rel))), path.Base(d.Path)) {
						t.Fatal("navigation points to a destination that could not be migrated", rel)
					}
				}
				return
			}
			if len(report.Conflicts) > 0 {
				t.Fatal(report)
			}
			if content := readTestFile(t, filepath.Join(root, filepath.FromSlash(d.Path))); !strings.Contains(content, "用户属性") || !strings.Contains(content, "## 任务目标") {
				t.Fatal("old properties or readable body lost", content)
			}
			if content := readTestFile(t, filepath.Join(root, filepath.FromSlash(legacy))); !strings.Contains(content, "打开任务记录") || !strings.Contains(content, path.Base(d.Path)) {
				t.Fatal("old summary links no longer open the task", content)
			}
			if _, err = a.store.libraryDocument(context.Background(), "document:"+legacy); err != nil {
				t.Fatal("legacy library link cannot resolve", err)
			}
		})
	}
}

func TestTaskWorklogKeepsAcknowledgedLegacyAnnotation(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	enableNotebook(t, a)
	task := taskFor(t, a)
	finishNotebook(t, a, task, notebookSample())
	root := useTaskNotebookVault(t, a)
	d := notebookTaskDocument(t, a, task)
	legacy := "tasks/" + task.ID + "/summary.md"
	d.Path = legacy
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(legacy))), 0700); err != nil {
		t.Fatal(err)
	}
	annotated, err := renderVaultFile(LibraryDocument{ID: d.ID, Kind: d.Kind, TaskID: d.TaskID, TaskTitle: d.TaskTitle, Title: d.Title, Status: d.Status, Revision: d.Revision, Updated: d.Updated, Content: d.Content + "\n已被空闲同步确认的批注\n", Generated: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, filepath.FromSlash(legacy)), annotated)
	// Simulate the prior version's idle sync: file_hash acknowledges the edit,
	// while local_hash continues to describe the generated original.
	if _, err = a.store.Exec("INSERT INTO library_links VALUES(?,?,?,?)", d.ID, legacy, documentHash(d), hash(annotated)); err != nil {
		t.Fatal(err)
	}
	actual := notebookTaskDocument(t, a, task)
	if actual.Path != legacy {
		t.Fatal("acknowledged annotation was treated as safe to migrate", actual.Path)
	}
	syncTestVault(t, a)
	if !strings.Contains(readTestFile(t, filepath.Join(root, filepath.FromSlash(legacy))), "已被空闲同步确认的批注") {
		t.Fatal("acknowledged legacy annotation was overwritten")
	}
}

func TestTaskWorklogNamesAreSafeAndDisambiguated(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	ids := []string{"aaaaaaaa0000000000000001", "aaaaaaaa0000000000000002"}
	for i, id := range ids {
		title := []string{`../Foo\\:*?<>|`, `../foo\\:*?<>|`}[i]
		if _, err := a.store.Exec("INSERT INTO tasks(id,title,workspace,model,status,created,updated) VALUES(?,?,?,'','idle',1,1)", id, title, "fixture"); err != nil {
			t.Fatal(err)
		}
		if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created,finished) VALUES(?,?,?,'chat','web','done',1,2)", uid(), id, "相同标题但不同任务"); err != nil {
			t.Fatal(err)
		}
	}
	first := notebookTaskDocument(t, a, Task{ID: ids[0]})
	second := notebookTaskDocument(t, a, Task{ID: ids[1]})
	if strings.EqualFold(first.Path, second.Path) || !readableTaskNotebookPath(first.Path) || !readableTaskNotebookPath(second.Path) || strings.ContainsAny(path.Base(first.Path), `\\:*?<>|`) {
		t.Fatal(first.Path, second.Path)
	}
	if !strings.Contains(first.Content, "尚无已记录的下一步计划") || !strings.Contains(first.Content, "尚无带逐字依据的可复用结论") {
		t.Fatal("missing data was fabricated as a plan or verification", first.Content)
	}
}

func TestTaskWorklogNewCollisionKeepsTrackedFilename(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	root := useTaskNotebookVault(t, a)
	ids := []string{"bbbbbbbb0000000000000001", "bbbbbbbb0000000000000002"}
	create := func(id, title string) {
		t.Helper()
		if _, err := a.store.Exec("INSERT INTO tasks(id,title,workspace,model,status,created,updated) VALUES(?,?,?,'','idle',1,1)", id, title, "fixture"); err != nil {
			t.Fatal(err)
		}
		if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created,finished) VALUES(?,?,?,'chat','web','done',1,2)", uid(), id, "保留已有文件与外部批注"); err != nil {
			t.Fatal(err)
		}
	}
	create(ids[0], "Foo")
	syncTestVault(t, a)
	first := notebookTaskDocument(t, a, Task{ID: ids[0]})
	file := filepath.Join(root, filepath.FromSlash(first.Path))
	writeTestFile(t, file, readTestFile(t, file)+"\n外部批注\n")
	create(ids[1], "foo")
	second := notebookTaskDocument(t, a, Task{ID: ids[1]})
	if current := notebookTaskDocument(t, a, Task{ID: ids[0]}); current.Path != first.Path || strings.EqualFold(current.Path, second.Path) {
		t.Fatal("a new short-prefix collision moved an existing task", current.Path, second.Path)
	}
	report := syncTestVault(t, a)
	if len(report.Conflicts) != 0 || !strings.Contains(readTestFile(t, file), "外部批注") {
		t.Fatal("new task displaced existing annotations", report)
	}
}
