package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func libraryKnowledge(t *testing.T, a *App, task Task, content, status string) Knowledge {
	t.Helper()
	k := Knowledge{ID: uid(), TaskID: task.ID, Title: "串口排查", Content: content, Status: status, Source: "manual", Revision: 1, Created: now(), Updated: now()}
	if err := a.store.writeKnowledge(k, true); err != nil {
		t.Fatal(err)
	}
	return k
}
func setTestVault(t *testing.T, a *App, dir string, runs bool) {
	t.Helper()
	a.vaultMu.Lock()
	defer a.vaultMu.Unlock()
	b, _ := json.Marshal(VaultConfig{Enabled: true, Directory: dir, IncludeRuns: runs})
	if _, err := a.store.Exec("INSERT INTO settings VALUES('knowledge_vault',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(b)); err != nil {
		t.Fatal(err)
	}
}
func syncTestVault(t *testing.T, a *App) VaultReport {
	t.Helper()
	r, e := a.syncVault(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func readTestFile(t *testing.T, path string) string {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func writeTestFile(t *testing.T, path, text string) {
	t.Helper()
	if e := os.WriteFile(path, []byte(text), 0600); e != nil {
		t.Fatal(e)
	}
}

func TestLibrarySearchAndReferenceAcrossTasks(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	one, r := knowledgeFixture(t, a)
	_ = taskFor(t, a)
	k := libraryKnowledge(t, a, one, "串口乱码使用 115200，错误码 E_UNEXPECTED，100%_literal", "verified")
	libraryKnowledge(t, a, one, "串口旧办法", "stale")
	for _, q := range []string{"串口", "E_UNEXPECTED", "100%_literal", "串口 115200"} {
		result, err := a.store.searchLibrary(context.Background(), q, "", "", false)
		if err != nil || len(result.Documents) == 0 {
			t.Fatalf("query %s: %+v %v", q, result, err)
		}
		for _, d := range result.Documents {
			if d.Status == "stale" {
				t.Fatal("stale result included")
			}
		}
	}
	docs, err := a.store.searchLibrary(context.Background(), "E_UNEXPECTED", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	d := docs.Documents[0]
	_, text, cut, err := a.store.libraryReference(context.Background(), d.ID, d.Hash)
	if err != nil || cut || !strings.Contains(text, "knowledge:"+k.ID) || !strings.Contains(text, "只作为参考") {
		t.Fatal(text, err)
	}
	k.Content = "修订后的结论"
	if err = a.store.writeKnowledge(k, false); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = a.store.libraryReference(context.Background(), d.ID, d.Hash); err != errConflict {
		t.Fatal("outdated selection accepted", err)
	}
	results, err := a.store.searchLibrary(context.Background(), "115200", "", "run", false)
	if err != nil || len(results.Documents) != 1 || results.Documents[0].ID != "run:"+r.ID {
		t.Fatal(results, err)
	}
}

func TestVaultRoundTripAndConflictingEdits(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	k := libraryKnowledge(t, a, task, "原始结论", "observed")
	dir := t.TempDir()
	setTestVault(t, a, dir, true)
	syncTestVault(t, a)
	path := filepath.Join(dir, "Duo", "knowledge", k.ID+".md")
	raw := readTestFile(t, path)
	if !strings.Contains(raw, "duo_id:") || strings.Contains(raw, "jianzuo.db") {
		t.Fatal(raw)
	}
	writeTestFile(t, path, strings.Replace(raw, "原始结论", "Obsidian 修订", 1))
	syncTestVault(t, a)
	var content string
	var revision int64
	if err := a.store.QueryRow("SELECT content,revision FROM knowledge_entries WHERE id=?", k.ID).Scan(&content, &revision); err != nil || content != "Obsidian 修订" || revision != 2 {
		t.Fatal(content, revision, err)
	}
	// Unknown Obsidian properties survive a later local-only edit.
	raw = readTestFile(t, path)
	raw = strings.Replace(raw, "---\n", "---\ntags: [serial, test]\n", 1)
	writeTestFile(t, path, raw)
	syncTestVault(t, a)
	a.store.QueryRow("SELECT revision FROM knowledge_entries WHERE id=?", k.ID).Scan(&k.Revision)
	k.Content = "Duo 修订"
	if err := a.store.writeKnowledge(k, false); err != nil {
		t.Fatal(err)
	}
	syncTestVault(t, a)
	if got := readTestFile(t, path); !strings.Contains(got, "serial") || !strings.Contains(got, "Duo 修订") {
		t.Fatal(got)
	}
	// Both sides edit after a common baseline: neither is overwritten.
	raw = readTestFile(t, path)
	writeTestFile(t, path, strings.Replace(raw, "Duo 修订", "Vault 并行修订", 1))
	a.store.QueryRow("SELECT revision FROM knowledge_entries WHERE id=?", k.ID).Scan(&k.Revision)
	k.Content = "本地并行修订"
	if err := a.store.writeKnowledge(k, false); err != nil {
		t.Fatal(err)
	}
	report := syncTestVault(t, a)
	if len(report.Conflicts) == 0 {
		t.Fatal("concurrent change not reported")
	}
	if !strings.Contains(readTestFile(t, path), "Vault 并行修订") {
		t.Fatal("vault edit lost")
	}
	a.store.QueryRow("SELECT content FROM knowledge_entries WHERE id=?", k.ID).Scan(&content)
	if content != "本地并行修订" {
		t.Fatal(content)
	}
}

func TestVaultGitAcrossTwoComputers(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required for portable-vault integration")
	}
	base := t.TempDir()
	remote := filepath.Join(base, "remote.git")
	first := filepath.Join(base, "first")
	second := filepath.Join(base, "second")
	run := func(args ...string) {
		t.Helper()
		c := exec.Command(git, args...)
		c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(base, "empty-config"), "GIT_TERMINAL_PROMPT=0")
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("git %v: %s: %v", args, b, e)
		}
	}
	run("init", "--bare", remote)
	run("clone", remote, first)
	a := fixture(t, &fakeRunner{})
	task, r := knowledgeFixture(t, a)
	k := libraryKnowledge(t, a, task, "跨电脑原始结论", "verified")
	setTestVault(t, a, first, true)
	syncTestVault(t, a)
	run("-C", first, "add", "Duo")
	run("-C", first, "-c", "user.name=Duo Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "synthetic vault")
	run("-C", first, "push", "origin", "HEAD")
	run("clone", remote, second)
	b := fixture(t, &fakeRunner{})
	setTestVault(t, b, second, true)
	syncTestVault(t, b)
	result, e := b.store.searchLibrary(context.Background(), "跨电脑原始结论", "", "", false)
	if e != nil || len(result.Documents) != 1 {
		t.Fatal(result, e)
	}
	_, citation, _, e := b.store.libraryReference(context.Background(), result.Documents[0].ID, result.Documents[0].Hash)
	if e != nil || !strings.Contains(citation, task.Title) {
		t.Fatal(citation, e)
	}
	result, e = b.store.searchLibrary(context.Background(), "115200", "", "run", false)
	if e != nil || len(result.Documents) != 1 || result.Documents[0].RunID != r.ID {
		t.Fatal(result, e)
	}
	var tasks int
	b.store.QueryRow("SELECT count(*) FROM tasks").Scan(&tasks)
	if tasks != 0 {
		t.Fatal("import created executable tasks")
	}
	path := filepath.Join(second, "Duo", "knowledge", k.ID+".md")
	writeTestFile(t, path, strings.Replace(readTestFile(t, path), "跨电脑原始结论", "第二台电脑修改的结论", 1))
	run("-C", second, "add", "Duo")
	run("-C", second, "-c", "user.name=Duo Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "edit note")
	run("-C", second, "push", "origin", "HEAD")
	run("-C", first, "pull", "--ff-only")
	syncTestVault(t, a)
	var content string
	a.store.QueryRow("SELECT content FROM knowledge_entries WHERE id=?", k.ID).Scan(&content)
	if content != "第二台电脑修改的结论" {
		t.Fatal(content)
	}
}

func TestVaultDeletionsConflictsAndSecrets(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	k := libraryKnowledge(t, a, task, "api_key=sk-synthetic1234567890123456789\n串口资料", "observed")
	dir := t.TempDir()
	setTestVault(t, a, dir, false)
	syncTestVault(t, a)
	path := filepath.Join(dir, "Duo", "knowledge", k.ID+".md")
	raw := readTestFile(t, path)
	if strings.Contains(raw, "synthetic123") {
		t.Fatal("secret exported")
	}
	writeTestFile(t, path, raw+"\nObsidian 编辑\n")
	report := syncTestVault(t, a)
	if len(report.Conflicts) == 0 {
		t.Fatal("redacted original silently replaced")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	syncTestVault(t, a)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("deleted file recreated")
	}
	note := filepath.Join(dir, "Duo", "外部笔记.md")
	writeTestFile(t, note, "# 电机接线\n串口与电机测试")
	syncTestVault(t, a)
	result, err := a.store.searchLibrary(context.Background(), "电机", "", "note", false)
	if err != nil || len(result.Documents) != 1 {
		t.Fatal(result, err)
	}
	writeTestFile(t, note, "<<<<<<< HEAD\n冲突\n=======\n资料\n>>>>>>> other\n")
	syncTestVault(t, a)
	result, err = a.store.searchLibrary(context.Background(), "电机", "", "note", false)
	if err != nil || len(result.Documents) != 0 {
		t.Fatal("stale index retained", result, err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	writeTestFile(t, outside, "串口机密外部文件")
	link := filepath.Join(dir, "Duo", "linked.md")
	if err = os.Symlink(outside, link); err == nil {
		syncTestVault(t, a)
		result, err = a.store.searchLibrary(context.Background(), "机密外部文件", "", "", false)
		if err != nil || len(result.Documents) != 0 {
			t.Fatal("symlink indexed", result, err)
		}
	}
}

func TestLibraryHTTPAuthenticationAndHash(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	server := (&Server{app: a}).Handler()
	for _, path := range []string{"/api/library/search", "/api/library/reference?id=example", "/api/library/vault"} {
		w := httptest.NewRecorder()
		server.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 401 {
			t.Fatalf("unauthenticated library route %s: %d", path, w.Code)
		}
	}
	task := taskFor(t, a)
	libraryKnowledge(t, a, task, "跨任务检索", "verified")
	request := toolsClient(t, a)
	var result LibrarySearch
	if err := json.Unmarshal(request("/api/library/search?q=跨任务", "GET", nil, 200), &result); err != nil || len(result.Documents) != 1 {
		t.Fatal(result, err)
	}
	request("/api/library/reference?id="+result.Documents[0].ID+"&hash=outdated", "GET", nil, 409)
	request("/api/library/vault", "PUT", VaultConfig{Enabled: true, Directory: "relative"}, 400)
}

func TestKnowledgeConcurrentSaveAndEditedSource(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task, run := knowledgeFixture(t, a)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.store.knowledgeFromRun(task.ID, run.ID); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var count int
	a.store.QueryRow("SELECT count(*) FROM knowledge_entries WHERE task_id=? AND run_id=?", task.ID, run.ID).Scan(&count)
	if count != 1 {
		t.Fatalf("concurrent save created %d entries", count)
	}
	other, draft := knowledgeFixture(t, a)
	request := toolsClient(t, a)
	var saved Knowledge
	if err := json.Unmarshal(request("/api/tasks/"+other.ID+"/knowledge", "POST", Knowledge{Title: "编辑后的总结", Content: "修订结论", RunID: draft.ID, Status: "observed"}, 201), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.RunID != draft.ID || saved.Source != "run" {
		t.Fatal("edited draft lost provenance", saved)
	}
	request("/api/tasks/"+other.ID+"/knowledge", "POST", Knowledge{Title: "伪造来源", Content: "内容", RunID: run.ID}, 400)
}
