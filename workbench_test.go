package main

import (
	"bytes"
	"context"
	"encoding/json"
	"golang.org/x/crypto/bcrypt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type workbenchRunner struct {
	calls chan Task
	gate  chan struct{}
}

func (f *workbenchRunner) Run(ctx context.Context, c Config, t Task, input string, emit func(string, string)) (string, string, error) {
	f.calls <- t
	select {
	case <-f.gate:
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
	emitUsage("claude", json.RawMessage(`{"input_tokens":100,"output_tokens":200,"cache_read_input_tokens":2000,"server_tool_use":{"web_search_requests":0}}`), emit)
	emit("assistant", input)
	return "saved-session", input, nil
}
func TestWorkbenchModeQueueSnapshotAndMetrics(t *testing.T) {
	f := &workbenchRunner{calls: make(chan Task, 3), gate: make(chan struct{})}
	a := fixture(t, f)
	task := taskFor(t, a)
	cat := WorkCatalog{Modes: []WorkMode{{ID: "review", Name: "审查", Permission: "read", Prompt: "检查边界"}}}
	if err := validateCatalog(&cat); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cat)
	a.store.set("workbench_catalog", string(raw))
	first, err := a.submitWithOptions(task.ID, "第一条", "chat", "web", SubmitOptions{ModeID: "review"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-f.calls:
		if got.Mode.Permission != "read" {
			t.Fatal(got.Mode)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner not started")
	}
	second, err := a.submitWithOptions(task.ID, "第二条", "chat", "web", SubmitOptions{ModeID: "work"})
	if err != nil {
		t.Fatal(err)
	}
	cat.Modes[0].Prompt = "之后修改的要求"
	raw, _ = json.Marshal(cat)
	a.store.set("workbench_catalog", string(raw))
	if err = a.trashTask(task.ID, false); err == nil {
		t.Fatal("deleted active task")
	}
	close(f.gate)
	waitUntil(t, func() bool { runs, _ := a.store.runs(task.ID); return len(runs) == 2 && runs[1].Status == "done" })
	runs, _ := a.store.runs(task.ID)
	if runs[0].ID != first.ID || runs[1].ID != second.ID || runs[0].Mode.Prompt != "检查边界" || runs[1].Mode.ID != "work" {
		t.Fatal(runs)
	}
	if runs[0].Usage == nil || runs[0].Usage.Total != 2300 || runs[0].Started < runs[0].Created || !strings.Contains(runs[0].Result, "检查边界") || strings.Contains(runs[1].Result, "工作模式") {
		t.Fatal(runs)
	}
	for _, r := range runs {
		if r.Input != "第一条" && r.Input != "第二条" {
			t.Fatal("stored user input changed")
		}
	}
}
func TestUsageAndEngineModes(t *testing.T) {
	c := parseUsage("codex", json.RawMessage(`{"input_tokens":2100,"output_tokens":200,"cached_input_tokens":2000}`))
	if c == nil || c.Total != 2300 {
		t.Fatal(c)
	}
	for _, v := range []string{`null`, `{}`, `{"input_tokens":-1}`, `{"input_tokens":null}`, `{"output_tokens":"0"}`} {
		if parseUsage("claude", json.RawMessage(v)) != nil {
			t.Fatal(v)
		}
	}
	task := Task{Mode: &WorkMode{Permission: "read"}, Session: "native", Files: []RuntimeAttachment{{Attachment: Attachment{Mime: "image/png"}, Path: "/tmp/中文 image.png"}}}
	args := strings.Join(codexArgs(Config{}, task), "|")
	if !strings.Contains(args, `sandbox_mode="read-only"`) || !strings.Contains(args, "--image|/tmp/中文 image.png|native|-") {
		t.Fatal(args)
	}
	task.Engine = "claude"
	args = strings.Join(claudeArgs(task), "|")
	if !strings.Contains(args, "--permission-mode|plan") || !strings.Contains(args, "--input-format|stream-json") {
		t.Fatal(args)
	}
	var stream claudeStream
	usage := 0
	stream.consume(`{"type":"assistant","usage":{"input_tokens":999},"message":{"content":[]}}`, func(k, v string) {
		if k == "usage" {
			usage++
		}
	})
	stream.consume(`{"type":"result","usage":{"input_tokens":100,"output_tokens":200,"cache_creation_input_tokens":20,"cache_read_input_tokens":2000,"cache_creation":{"ephemeral_5m_input_tokens":20}}}`, func(k, v string) {
		if k == "usage" {
			var u RunUsage
			json.Unmarshal([]byte(v), &u)
			if u.Total != 2320 {
				t.Fatal(u)
			}
			usage++
		}
	})
	if usage != 1 {
		t.Fatal("counted non-result usage", usage)
	}
}

func TestRunsHydrateMetricsOptionsAndAttachments(t *testing.T) {
	s, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.Exec("INSERT INTO tasks(id,title,workspace,model,created,updated) VALUES('t','title','/tmp','',0,0)"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES('r','t','prompt','chat','web','done',1)"); err != nil {
		t.Fatal(err)
	}
	mode := WorkMode{ID: "review", Name: "审查", Permission: "read", Prompt: "检查边界"}
	attachments := []Attachment{{ID: "f1", Name: "画面.png", Mime: "image/png", Size: 42}}
	usage := RunUsage{Input: 100, Output: 200, Cached: 2000, Total: 2300}
	modeJSON, _ := json.Marshal(mode)
	attachmentJSON, _ := json.Marshal(attachments)
	usageJSON, _ := json.Marshal(usage)
	if _, err = s.Exec("INSERT INTO run_metrics(run_id,started,usage) VALUES('r',123,?)", string(usageJSON)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Exec("INSERT INTO run_options(run_id,mode,attachments) VALUES('r',?,?)", string(modeJSON), string(attachmentJSON)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES('legacy','t','old','chat','web','done',2)"); err != nil {
		t.Fatal(err)
	}

	runs, err := s.runs("t")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %#v", runs)
	}
	got := runs[0]
	if got.Started != 123 || got.Usage == nil || *got.Usage != usage || got.Mode == nil || *got.Mode != mode {
		t.Fatalf("hydrated run = %#v", got)
	}
	if len(got.Attachments) != 1 || got.Attachments[0] != attachments[0] {
		t.Fatalf("attachments = %#v", got.Attachments)
	}
	if legacy := runs[1]; legacy.Started != 0 || legacy.Usage != nil || legacy.Mode != nil || legacy.Attachments != nil {
		t.Fatalf("legacy defaults = %#v", legacy)
	}
}

func TestTrashPreservesHistoryScratchAndWorkspace(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	file := filepath.Join(t.TempDir(), "keep.txt")
	os.WriteFile(file, []byte("keep"), 0600)
	a.store.Exec("UPDATE tasks SET workspace=? WHERE id=?", filepath.Dir(file), task.ID)
	n := Scratch{ID: uid(), TaskID: task.ID, Content: "unique-note", Color: "pink", Revision: 1, Updated: now()}
	if e := a.store.writeScratch(n, true); e != nil {
		t.Fatal(e)
	}
	changed := n
	changed.Color = "green"
	if e := a.store.writeScratch(changed, false); e != nil {
		t.Fatal(e)
	}
	if e := a.store.writeScratch(n, false); e == nil {
		t.Fatal("stale note overwritten")
	}
	notes, e := a.store.scratchList("")
	if e != nil || len(notes) != 1 || notes[0].Color != "green" || notes[0].Revision != 2 {
		t.Fatal(notes, e)
	}
	a.store.Exec("INSERT INTO bindings VALUES('test-chat',?)", task.ID)
	if e = a.trashTask(task.ID, false); e != nil {
		t.Fatal(e)
	}
	list, _ := a.store.tasks()
	if len(list) != 0 {
		t.Fatal(list)
	}
	all, _ := a.store.tasks(true)
	if len(all) != 1 || !all[0].Deleted || !all[0].Archived {
		t.Fatal(all)
	}
	notes, _ = a.store.scratchList("")
	if len(notes) != 0 {
		t.Fatal("trash notes visible")
	}
	hits, _ := a.store.search(context.Background(), "unique-note")
	if len(hits.Hits) != 0 {
		t.Fatal(hits)
	}
	if a.bound("test-chat") != "" {
		t.Fatal("binding retained")
	}
	if _, e = a.submit(task.ID, "run", "chat", "feishu"); e == nil {
		t.Fatal("trash submitted")
	}
	if e = a.trashTask(task.ID, true); e != nil {
		t.Fatal(e)
	}
	notes, _ = a.store.scratchList("")
	if len(notes) != 1 || notes[0].Content != "unique-note" {
		t.Fatal(notes)
	}
	saved, _ := a.store.task(task.ID)
	if !saved.Archived || saved.Deleted {
		t.Fatal(saved)
	}
	if b, e := os.ReadFile(file); e != nil || string(b) != "keep" {
		t.Fatal("workspace touched", e)
	}
}
func TestWorkbenchAttachmentAPIAndStaging(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	other := taskFor(t, a)
	hash, _ := bcrypt.GenerateFromPassword([]byte("test-password"), bcrypt.MinCost)
	a.store.set("password_hash", string(hash))
	server := httptest.NewServer((&Server{app: a}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	req, _ := http.NewRequest("POST", server.URL+"/api/login", strings.NewReader(`{"password":"test-password"}`))
	req.Header.Set("Origin", server.URL)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var login map[string]string
	json.NewDecoder(resp.Body).Decode(&login)
	resp.Body.Close()
	upload := func(data []byte, csrf string) (int, []byte) {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		part, _ := writer.CreateFormFile("file", `..\picture.png`)
		part.Write(data)
		writer.Close()
		r, _ := http.NewRequest("POST", server.URL+"/api/tasks/"+task.ID+"/attachments", &buf)
		r.Header.Set("Content-Type", writer.FormDataContentType())
		r.Header.Set("Origin", server.URL)
		r.Header.Set("X-CSRF-Token", csrf)
		res, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, body
	}
	if code, _ := upload([]byte("bad"), ""); code != 403 {
		t.Fatal("CSRF", code)
	}
	code, body := upload([]byte("\x89PNG\r\n\x1a\nfixture"), login["csrf"])
	if code != 201 {
		t.Fatal(code, string(body))
	}
	var file Attachment
	json.Unmarshal(body, &file)
	if file.Name != "picture.png" || file.Mime != "image/png" {
		t.Fatal(file)
	}
	if _, e := a.store.messageAttachments(other.ID, []string{file.ID}); e == nil {
		t.Fatal("cross-task attachment")
	}
	if _, e := a.store.messageAttachments(task.ID, []string{file.ID, file.ID}); e == nil {
		t.Fatal("duplicate attachment")
	}
	if code, _ = upload(make([]byte, attachmentLimit+1), login["csrf"]); code != 400 {
		t.Fatal("oversize", code)
	}
	task.Environment = &Environment{Type: "windows"}
	files, cleanup, e := a.stageAttachments(context.Background(), task, []Attachment{file})
	if e != nil || len(files) != 1 {
		t.Fatal(files, e)
	}
	defer func() { cleanup() }()
	b, e := os.ReadFile(files[0].Path)
	if e != nil || !bytes.HasPrefix(b, []byte("\x89PNG")) {
		t.Fatal(e)
	}
	if !strings.HasPrefix(files[0].Path, filepath.Join(filepath.Dir(a.config.path), "attachments")) {
		t.Fatal(files)
	}
	stageRoot := filepath.Dir(files[0].Path)
	cleanup()
	cleanup = func() {}
	if _, e = os.Stat(stageRoot); !os.IsNotExist(e) {
		t.Fatal("attachment staging was not cleaned", e)
	}
	task.Engine = "claude"
	task.Files = files
	var frame struct {
		Type    string
		Message struct{ Content []map[string]any }
	}
	if e = json.Unmarshal([]byte(runnerInput(task, executionInput(task, "查看图片"))), &frame); e != nil || frame.Type != "user" || len(frame.Message.Content) != 2 || frame.Message.Content[1]["type"] != "image" {
		t.Fatal(frame, e)
	}
	r, _ := http.NewRequest("GET", server.URL+"/api/tasks/"+other.ID+"/attachments/"+file.ID, nil)
	res, e := client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatal("cross-task download")
	}
}
func TestWorkbenchDirectoryPicker(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "中文 ' $()"), 0700)
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("ignored"), 0600)
	got, e := browseDirectories(context.Background(), Environment{Type: "windows"}, dir)
	if e != nil || got.Path != dir || len(got.Items) != 1 || got.Items[0].Name != "中文 ' $()" {
		t.Fatal(got, e)
	}
	if _, e = browseDirectories(context.Background(), Environment{Type: "windows"}, "relative"); e == nil {
		t.Fatal("relative accepted")
	}
	if _, e = browseDirectories(context.Background(), Environment{Type: "wsl"}, "relative"); e == nil {
		t.Fatal("remote relative accepted")
	}
}
