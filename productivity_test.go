package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveRestoreAndFeishuShareList(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	one := taskFor(t, a)
	two := taskFor(t, a)
	request := toolsClient(t, a)
	request("/api/tasks/"+one.ID+"/preferences", "PATCH", map[string]bool{"pinned": true}, 200)
	tasks, _ := a.store.tasks()
	if tasks[0].ID != one.ID || !tasks[0].Pinned {
		t.Fatal("pin ordering", tasks)
	}
	request("/api/tasks/"+one.ID+"/note", "PUT", Note{Content: "归档知识"}, 200)
	request("/api/tasks/"+one.ID+"/preferences", "PATCH", map[string]bool{"archived": true}, 200)
	if _, err := a.submit(one.ID, "不应运行", "chat", "web"); err == nil {
		t.Fatal("archived task executed")
	}
	if err := a.bind("test-chat", one.ID); err == nil {
		t.Fatal("bound archived task")
	}
	card, err := a.feishu.taskListCard("test-chat", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(card)
	if !strings.Contains(string(raw), "共 1 项") {
		t.Fatal("archived task still listed", string(raw))
	}
	request("/api/tasks/"+one.ID+"/preferences", "PATCH", map[string]bool{"archived": false}, 200)
	n, _ := a.store.note(one.ID)
	restored, _ := a.store.task(one.ID)
	if n.Content != "归档知识" || restored.Archived || !restored.Pinned {
		t.Fatal("restore lost data")
	}
	a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES('active',?,'','chat','test','running',1)", two.ID)
	request("/api/tasks/"+two.ID+"/preferences", "PATCH", map[string]bool{"archived": true}, 409)
	request("/api/tasks/missing/preferences", "PATCH", map[string]bool{"pinned": true}, 404)
}
func TestSearchAllSourcesLiteralKeywordsAndArchived(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	request := toolsClient(t, a)
	q := "串口 100%_ ' <script>"
	a.store.Exec("UPDATE tasks SET title=? WHERE id=?", q, task.ID)
	request("/api/tasks/"+task.ID+"/note", "PUT", Note{Content: q + " 知识"}, 200)
	request("/api/tasks/"+task.ID+"/scratch", "POST", Scratch{Content: q + " 便签"}, 201)
	a.store.event(task.ID, "", "assistant", strings.Repeat("前缀", 200)+q+" 对话")
	a.store.event(task.ID, "", "tool", q+" 不搜索命令输出")
	yes := true
	a.setTaskPreferences(task.ID, TaskPreferences{Archived: &yes})
	var result SearchResult
	json.Unmarshal(request("/api/search?q="+url.QueryEscape(q), "GET", nil, 200), &result)
	kinds := map[string]bool{}
	for _, hit := range result.Hits {
		if hit.TaskID != task.ID || !hit.Archived || !strings.Contains(hit.Snippet, q) {
			t.Fatal(hit)
		}
		kinds[hit.Kind] = true
	}
	if len(result.Hits) != 4 || len(kinds) != 4 {
		t.Fatal(result)
	}
	result, _ = a.store.search(context.Background(), "%_ NO SUCH")
	if len(result.Hits) != 0 {
		t.Fatal("SQL wildcard expansion")
	}
	for i := 0; i < 70; i++ {
		a.store.event(task.ID, "", "assistant", "limit-marker")
	}
	result, _ = a.store.search(context.Background(), "limit-marker")
	if len(result.Hits) != 60 || !result.Truncated {
		t.Fatal("unbounded search")
	}
	request("/api/search?q="+strings.Repeat("a", 161), "GET", nil, 400)
}
func localFileTask(t *testing.T, a *App, dir string) Task {
	c := a.config.get()
	c.Environments = []Environment{{ID: "local", Name: "Local", Type: "windows", Codex: "codex.exe", Workspaces: []string{dir}}}
	c.DefaultEnvironment = "local"
	if err := a.config.save(c); err != nil {
		t.Fatal(err)
	}
	task, err := a.create("文件测试", dir, "", "local")
	if err != nil {
		t.Fatal(err)
	}
	return task
}
func TestWorkspaceReadDownloadAndPathBoundary(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	dir := t.TempDir()
	task := localFileTask(t, a, dir)
	request := toolsClient(t, a)
	os.WriteFile(filepath.Join(dir, "中文 空格.txt"), []byte("你好 <script>不可执行</script>"), 0600)
	os.WriteFile(filepath.Join(dir, "binary.bin"), []byte{0, 1, 2, 255}, 0600)
	base := "/api/tasks/" + task.ID + "/files"
	for _, path := range []string{"../secret", "C:/secret", ".git/config", ".GiT/config", ".git./config", "sub/../../secret", "file:stream", "/etc/passwd", "x\\y"} {
		request(base+"?action=read&path="+url.QueryEscape(path), "GET", nil, 400)
	}
	var result FileResult
	json.Unmarshal(request(base+"?action=read&path="+url.QueryEscape("中文 空格.txt"), "GET", nil, 200), &result)
	if result.Content != "你好 <script>不可执行</script>" || result.Binary {
		t.Fatal(result)
	}
	raw := request(base+"?action=download&path=binary.bin", "GET", nil, 200)
	if !bytes.Equal(raw, []byte{0, 1, 2, 255}) {
		t.Fatal("binary download corrupt")
	}
	json.Unmarshal(request(base+"?action=read&path=binary.bin", "GET", nil, 200), &result)
	if !result.Binary || result.Content != "" {
		t.Fatal("binary rendered")
	}
	os.WriteFile(filepath.Join(dir, "large.txt"), bytes.Repeat([]byte("a"), previewLimit+12), 0600)
	result, _ = readLocalWorkspace(context.Background(), dir, "read", "large.txt")
	if !result.Truncated || len(result.Content) != previewLimit {
		t.Fatal("preview unbounded")
	}
	f, _ := os.Create(filepath.Join(dir, "oversize.bin"))
	f.Truncate(downloadLimit + 1)
	f.Close()
	request(base+"?action=download&path=oversize.bin", "GET", nil, 400)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("hidden"), 0600)
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err == nil {
		request(base+"?action=read&path=link/secret", "GET", nil, 400)
	} else {
		t.Log("symlink creation unavailable; lexical traversal and OpenRoot checks still covered")
	}
	request(base+"?action=write&path=large.txt", "GET", nil, 400)
}
func TestWorkspaceGitNestedStagedDeletedAndFilterDisabled(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "nested")
	os.Mkdir(dir, 0700)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		hideCommand(cmd)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "test@example.invalid")
	git("config", "user.name", "Workspace Test")
	git("config", "core.autocrlf", "false")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("before\n"), 0600)
	os.WriteFile(filepath.Join(root, "outside.txt"), []byte("outside\n"), 0600)
	os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("delete me\n"), 0600)
	git("add", ".")
	git("commit", "-qm", "fixture")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("staged\n"), 0600)
	git("add", "nested/a.txt")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("working\n"), 0600)
	os.Remove(filepath.Join(dir, "gone.txt"))
	os.WriteFile(filepath.Join(dir, "新文件.txt"), []byte("new\n"), 0600)
	os.WriteFile(filepath.Join(root, "outside.txt"), []byte("outside changed\n"), 0600)
	// A repository's clean/process filter must not execute during file viewing.
	git("config", "filter.probe.clean", "touch FILTER_RAN")
	git("config", "filter.probe.required", "true")
	os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("*.txt filter=probe\n"), 0600)
	result, err := readLocalWorkspace(context.Background(), dir, "changes", "")
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, f := range result.Items {
		paths[f.Path] = true
		if strings.Contains(f.Path, "nested/") || strings.Contains(f.Path, "outside") {
			t.Fatal("incorrect workspace-relative git path", result)
		}
	}
	if !paths["a.txt"] || !paths["gone.txt"] || !paths["新文件.txt"] {
		t.Fatal(result)
	}
	result, err = readLocalWorkspace(context.Background(), dir, "diff", "a.txt")
	if err != nil || !strings.Contains(result.Content, "+working") || !strings.Contains(result.Content, "+staged") || !strings.Contains(result.Content, "-before") {
		t.Fatal(result, err)
	}
	result, err = readLocalWorkspace(context.Background(), dir, "diff", "gone.txt")
	if err != nil || !strings.Contains(result.Content, "-delete me") {
		t.Fatal(result, err)
	}
	if _, err = os.Stat(filepath.Join(root, "FILTER_RAN")); err == nil {
		t.Fatal("Git filter executed")
	}
	for _, relative := range []string{"a.txt", "新文件.txt"} {
		result, err = readLocalWorkspace(context.Background(), dir, "download", relative)
		data, e := base64.StdEncoding.DecodeString(result.Data)
		if err != nil || e != nil || len(data) == 0 {
			t.Fatal("download", result, err)
		}
	}
}
func TestWorkspaceOutputBoundAppliesToIOCopy(t *testing.T) {
	out := &cappedOutput{limit: 100}
	var reader io.Reader = struct{ io.Reader }{strings.NewReader(strings.Repeat("x", 1000))}
	n, err := io.Copy(out, reader)
	if err != nil || n != 1000 || out.Len() != 100 || !out.truncated {
		t.Fatal("output limit bypassed", n, out.Len(), err)
	}
}
