package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func enableNotebook(t *testing.T, a *App) {
	t.Helper()
	if _, err := a.store.Exec(`UPDATE settings SET value='{"capture":true,"recall":true,"organize":true}' WHERE key='automatic_knowledge'`); err != nil {
		t.Fatal(err)
	}
}
func notebookSample() taskMemory {
	return taskMemory{Summary: "解决拖动性能问题", Prerequisites: []string{"在 C:\\fixture\\project 中运行"}, Decisions: []string{"尺寸更新每帧合并"}, OpenQuestions: []string{"尚未验证真实硬件"}, Tags: []string{"性能", "typescript"}, Insights: []memoryInsight{{Title: "拖动时减少布局更新", Question: "为什么拖动卡顿？", Conclusion: "合并每帧尺寸写入", Evidence: "模拟 100 次移动，仅合并为一次更新。", Applicability: "仅界面拖动", Tags: []string{"性能"}}}}
}
func memoryReply(run string, m taskMemory) string {
	raw, _ := json.Marshal(m)
	return "模拟 100 次移动，仅合并为一次更新。\n\n" + memoryMarker(run) + "\n" + string(raw) + "\n<!-- /duo-memory:" + run + " -->"
}
func finishNotebook(t *testing.T, a *App, task Task, m taskMemory) Run {
	t.Helper()
	r := Run{ID: uid(), TaskID: task.ID, Input: "检查 C:\\fixture\\project 的拖动性能", Kind: "chat", Created: now()}
	if _, err := a.store.Exec(`INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES(?,?,?,'chat','web','running',?)`, r.ID, task.ID, r.Input, r.Created); err != nil {
		t.Fatal(err)
	}
	a.finish(task.ID, r, "", memoryReply(r.ID, m), nil)
	return r
}

type notebookRunner struct {
	calls int
	input string
}

func (f *notebookRunner) Run(ctx context.Context, c Config, task Task, input string, emit func(string, string)) (string, string, error) {
	f.calls++
	f.input = input
	parts := regexp.MustCompile(`<!-- duo-memory:([a-f0-9]{24}) -->`).FindStringSubmatch(input)
	if len(parts) != 2 {
		return "", "", errors.New("missing memory contract")
	}
	reply := memoryReply(parts[1], notebookSample())
	at := strings.Index(reply, memoryMarker(parts[1]))
	emit("assistant", strings.TrimSpace(reply[:at]))
	emit("assistant", reply[:at+18])
	emit("assistant", reply[:len(reply)-30])
	emit("assistant", reply)
	return "fixture-session", reply, nil
}

func TestNotebookSameTurnCaptureReadAndRecall(t *testing.T) {
	f := &notebookRunner{}
	a := fixture(t, f)
	enableNotebook(t, a)
	task := taskFor(t, a)
	r, err := a.submit(task.ID, "排查拖动", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.workers[task.ID] == nil })
	if f.calls != 1 || !strings.Contains(f.input, "排查拖动") {
		t.Fatal("unexpected extra model turn")
	}
	var result string
	a.store.QueryRow("SELECT result FROM runs WHERE id=?", r.ID).Scan(&result)
	if strings.Contains(result, "duo-memory") || !strings.Contains(result, "100 次移动") {
		t.Fatal(result)
	}
	var leaked int
	a.store.QueryRow("SELECT count(*) FROM events WHERE run_id=? AND kind='assistant' AND text LIKE '%duo-memory%'", r.ID).Scan(&leaked)
	if leaked != 0 {
		t.Fatal("private metadata shown as assistant reply")
	}
	a.store.QueryRow("SELECT count(*) FROM events WHERE run_id=? AND kind='assistant'", r.ID).Scan(&leaked)
	if leaked != 1 {
		t.Fatal("metadata chunks created duplicate visible replies", leaked)
	}
	docs, err := a.store.notebookDocuments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, d := range docs {
		kinds[d.Kind]++
		if strings.Contains(d.Content, `C:\fixture`) || strings.Contains(d.Content, "aha2") {
			t.Fatal("machine path or foreign integration leaked", d)
		}
		if len(d.Tags) == 0 {
			t.Fatal("missing inferred tags")
		}
	}
	if kinds["task"] != 3 || kinds["insight"] != 1 || kinds["topic"] == 0 {
		t.Fatal(kinds)
	}
	list, err := a.store.searchLibrary(context.Background(), "", "", "", false, LibrarySearchOptions{Layer: "knowledge", Tag: "性能"})
	if err != nil || len(list.Documents) != 1 {
		t.Fatal(list, err)
	}
	doc, err := a.store.libraryDocument(context.Background(), list.Documents[0].ID)
	if err != nil || !strings.Contains(doc.Content, "依据") {
		t.Fatal(doc, err)
	}
	_, ref, _, err := a.store.libraryReference(context.Background(), doc.ID, doc.Hash)
	if err != nil || !strings.Contains(ref, "knowledge/") || strings.Contains(ref, `C:\fixture`) {
		t.Fatal(ref, err)
	}
	recall, ids, err := a.store.automaticKnowledgeContext(context.Background(), task)
	if err != nil || len(ids) != 1 || !strings.Contains(recall, "尚未验证真实硬件") {
		t.Fatal(recall, ids, err)
	}
	other := taskFor(t, a)
	if text, _, err := a.store.automaticKnowledgeContext(context.Background(), other); err != nil || text != "" {
		t.Fatal("cross-task recall", text, err)
	}
	archive, _, err := a.store.workspaceArchiveSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.Runs) != 1 || archive.Runs[0].Memory == nil {
		t.Fatal("workspace export lost memory")
	}
	if err = validateWorkspaceArchive(archive); err != nil {
		t.Fatal(err)
	}
}

func TestNotebookPayloadScopeEvidenceAndOptOut(t *testing.T) {
	id := uid()
	m := notebookSample()
	raw := memoryReply(id, m)
	if clean, memory := splitTaskMemory(raw, id); memory == nil || strings.Contains(clean, "duo-memory") {
		t.Fatal("valid metadata rejected")
	}
	for _, value := range []string{strings.Replace(raw, id, uid(), -1), raw + "visible text", strings.Replace(raw, `"summary"`, `"unsupported"`, 1)} {
		if clean, memory := splitTaskMemory(value, id); memory != nil || clean != value {
			t.Fatal("invalid output consumed")
		}
	}
	m.Insights[0].Evidence = "编造的实验证据"
	if _, parsed := splitTaskMemory(memoryReply(id, m), id); parsed == nil || len(parsed.Insights) != 0 {
		t.Fatal("unsupported evidence promoted")
	}
	a := fixture(t, &fakeRunner{})
	enableNotebook(t, a)
	task := taskFor(t, a)
	run := finishNotebook(t, a, task, notebookSample())
	a.finish(task.ID, run, "", memoryReply(run.ID, taskMemory{Summary: "overwrite"}), nil)
	var payload string
	a.store.QueryRow("SELECT payload FROM task_memories WHERE run_id=?", run.ID).Scan(&payload)
	if strings.Contains(payload, "overwrite") {
		t.Fatal("duplicate completion overwrote memory")
	}
	a.store.Exec(`UPDATE settings SET value='{"capture":false,"recall":false,"organize":true}' WHERE key='automatic_knowledge'`)
	next := finishNotebook(t, a, task, notebookSample())
	var count int
	a.store.QueryRow("SELECT count(*) FROM task_memories WHERE run_id=?", next.ID).Scan(&count)
	if count != 0 {
		t.Fatal("capture opt-out ignored")
	}
	for _, id := range []string{"document:../private.md", "document:/etc/passwd.md", "document:C:/file.md", "document:tasks/../../file.md"} {
		if _, err := a.store.libraryDocument(context.Background(), id); err == nil {
			t.Fatal("unsafe reference", id)
		}
	}
}

func TestNotebookPortablePathsAndTags(t *testing.T) {
	input := "项目 C:\\work\\repo\\src\\main.go，用户 /home/alice/private/file.txt，网络 \\\\server\\share\\file，链接 https://example.com/docs。"
	got := portableKnowledge(input, `C:\work\repo`)
	if !strings.Contains(got, `<workspace>\src\main.go`) || strings.Contains(got, "alice") || strings.Contains(got, "server") || !strings.Contains(got, "https://example.com/docs") {
		t.Fatal(got)
	}
	tags := notebookTags([]string{"性能", "性能", "../../private", "#GitHub", "C:/secret"}, "typescript 知识库")
	if strings.Join(tags, ",") != "性能,github,typescript,知识库" {
		t.Fatal(tags)
	}
}

func TestNotebookVaultLayoutConflictAndIdleIndex(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	enableNotebook(t, a)
	task := taskFor(t, a)
	run := finishNotebook(t, a, task, notebookSample())
	dir := t.TempDir()
	c := VaultConfig{Enabled: true, Directory: dir, IncludeRuns: true, IncludeAutomatic: true, TaskFolders: true}
	raw, _ := json.Marshal(c)
	a.store.Exec("UPDATE settings SET value=? WHERE key='knowledge_vault'", string(raw))
	report := syncTestVault(t, a)
	if len(report.Conflicts) > 0 {
		t.Fatal(report)
	}
	root := filepath.Join(dir, "Duo")
	for _, name := range []string{"summary.md", "context.md", "conversations.md", "conversations/" + run.ID + ".md"} {
		b := readTestFile(t, filepath.Join(root, "tasks", task.ID, filepath.FromSlash(name)))
		if strings.Contains(b, `C:\fixture`) {
			t.Fatal("absolute path exported")
		}
	}
	summary := filepath.Join(root, "tasks", task.ID, "summary.md")
	original := readTestFile(t, summary)
	var before, after int64
	a.store.QueryRow("SELECT total_changes()").Scan(&before)
	syncTestVault(t, a)
	a.store.QueryRow("SELECT total_changes()").Scan(&after)
	if after != before {
		t.Fatalf("idle scan rewrote %d rows", after-before)
	}
	writeTestFile(t, summary, original+"\n保留人工修改\n")
	finishNotebook(t, a, task, taskMemory{Summary: "后续新的结果", Tags: []string{"性能"}})
	report = syncTestVault(t, a)
	if len(report.Conflicts) == 0 || !strings.Contains(readTestFile(t, summary), "保留人工修改") {
		t.Fatal("external edit overwritten", report)
	}
	if _, err := os.Stat(filepath.Join(root, "knowledge", run.ID+"-1.md")); err != nil {
		t.Fatal(err)
	}
}

func TestNotebookDefaultDirectoryFollowsDataRoot(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	a.store.Exec("DELETE FROM settings WHERE key='knowledge_vault'")
	config := a.store.vaultConfig()
	if !config.Enabled || !config.TaskFolders || !config.IncludeRuns || !config.IncludeAutomatic || config.Directory != filepath.Join(a.store.directory, "knowledge") {
		t.Fatal(config)
	}
	syncTestVault(t, a)
	if _, err := os.Stat(filepath.Join(config.Directory, "Duo")); err != nil {
		t.Fatal(err)
	}
}

func TestNotebookVaultMigratesLegacyPathsAndKeepsTags(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	k := libraryKnowledge(t, a, task, "查看 https://example.com/docs 后确认", "observed")
	dir := t.TempDir()
	setTestVault(t, a, dir, true)
	legacy := a.store.vaultConfig()
	legacy.IncludeAutomatic = true
	legacyJSON, _ := json.Marshal(legacy)
	a.store.set("knowledge_vault", string(legacyJSON))
	oldRun := finishAutomaticFixture(t, a, task, "旧版自动记录", "旧版回复", "chat", nil)
	automatic, _ := a.store.knowledgeForRun(task.ID, oldRun.ID)
	syncTestVault(t, a)
	old := filepath.Join(dir, "Duo", "knowledge", k.ID+".md")
	raw := readTestFile(t, old)
	writeTestFile(t, old, strings.Replace(raw, "---\n", "---\ntags: user-topic\n", 1))
	syncTestVault(t, a)
	config := a.store.vaultConfig()
	config.TaskFolders = true
	config.IncludeAutomatic = true
	data, _ := json.Marshal(config)
	a.store.set("knowledge_vault", string(data))
	report := syncTestVault(t, a)
	next := filepath.Join(dir, "Duo", "tasks", task.ID, "notes", k.ID+".md")
	content := readTestFile(t, next)
	if !strings.Contains(content, "user-topic") || !strings.Contains(content, "https://example.com/docs") || len(report.Conflicts) > 0 {
		t.Fatal(content, report)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("unchanged tracked file was not moved", err)
	}
	automaticPath := filepath.Join(dir, "Duo", "tasks", task.ID, "notes", automatic.ID+".md")
	if !strings.Contains(readTestFile(t, automaticPath), "旧版回复") {
		t.Fatal("tracked automatic file was lost during layout migration")
	}
	nextRun := finishAutomaticFixture(t, a, task, "新版自动记录", "新版回复", "chat", nil)
	nextAutomatic, _ := a.store.knowledgeForRun(task.ID, nextRun.ID)
	syncTestVault(t, a)
	if _, err := os.Stat(filepath.Join(dir, "Duo", "tasks", task.ID, "notes", nextAutomatic.ID+".md")); !os.IsNotExist(err) {
		t.Fatal("new transcript duplicated as an automatic note", err)
	}
	f, err := parseVaultFile("notes/test.md", "---\ntags: git, 性能\n---\n\n笔记")
	if err != nil || len(f.Document.Tags) != 2 {
		t.Fatal(f, err)
	}
}
