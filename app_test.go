package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/larksuite/oapi-sdk-go/v3/scene/registration"
	"golang.org/x/crypto/bcrypt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeRunner struct {
	mu       sync.Mutex
	inputs   []string
	sessions []string
	gate     chan struct{}
	started  chan struct{}
}

func (f *fakeRunner) Run(ctx context.Context, c Config, t Task, input string, emit func(string, string)) (string, string, error) {
	f.mu.Lock()
	f.inputs = append(f.inputs, input)
	f.sessions = append(f.sessions, t.Session)
	f.mu.Unlock()
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
	}
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return "test-session", "", ctx.Err()
		}
	}
	emit("assistant", "reply: "+input)
	return "test-session", "reply: " + input, nil
}
func fixture(t *testing.T, r Runner) *App {
	t.Helper()
	dir := t.TempDir()
	s, e := openStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	c, e := loadConfig(dir)
	if e != nil {
		t.Fatal(e)
	}
	a := newApp(s, c, r)
	newFeishu(a)
	t.Cleanup(func() { a.close(); s.Close() })
	return a
}
func taskFor(t *testing.T, a *App) Task {
	t.Helper()
	c := a.config.get()
	task, e := a.create("测试任务", c.Workspaces[0], c.Model)
	if e != nil {
		t.Fatal(e)
	}
	return task
}
func waitUntil(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition timed out")
}

func TestQueuePreservesNativeSessionAndInput(t *testing.T) {
	f := &fakeRunner{}
	a := fixture(t, f)
	task := taskFor(t, a)
	if _, e := a.submit(task.ID, "精确的用户要求", "chat", "web"); e != nil {
		t.Fatal(e)
	}
	waitUntil(t, func() bool { runs, _ := a.store.runs(task.ID); return len(runs) == 1 && runs[0].Status == "done" })
	if _, e := a.submit(task.ID, "来自飞书的下一步", "chat", "feishu"); e != nil {
		t.Fatal(e)
	}
	waitUntil(t, func() bool { runs, _ := a.store.runs(task.ID); return len(runs) == 2 && runs[1].Status == "done" })
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.inputs) != 2 || f.inputs[0] != "精确的用户要求" || f.inputs[1] != "来自飞书的下一步" {
		t.Fatalf("unexpected orchestration: %v", f.inputs)
	}
	if f.sessions[0] != "" || f.sessions[1] != "test-session" {
		t.Fatalf("session not resumed: %v", f.sessions)
	}
}
func TestStopCancelsExecutionAndQueuedMessages(t *testing.T) {
	f := &fakeRunner{gate: make(chan struct{}), started: make(chan struct{}, 1)}
	a := fixture(t, f)
	task := taskFor(t, a)
	a.submit(task.ID, "first", "chat", "web")
	select {
	case <-f.started:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	a.submit(task.ID, "second", "chat", "feishu")
	if e := a.stop(task.ID); e != nil {
		t.Fatal(e)
	}
	waitUntil(t, func() bool {
		runs, _ := a.store.runs(task.ID)
		return len(runs) == 2 && runs[0].Status == "interrupted" && runs[1].Status == "interrupted"
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.inputs) != 1 {
		t.Fatal("queued operation ran after stop")
	}
}
func TestNoteCASAndRestartRecovery(t *testing.T) {
	dir := t.TempDir()
	s, e := openStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Exec("INSERT INTO tasks(id,title,workspace,model,status,created,updated) VALUES('t','title','/tmp','','running',0,0)")
	if e != nil {
		t.Fatal(e)
	}
	s.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES('r','t','work','chat','web','running',0)")
	n, e := s.saveNote("t", "# 解决办法\n中文知识", 0)
	if e != nil || n.Revision != 1 {
		t.Fatal(n, e)
	}
	if _, e = s.saveNote("t", "覆盖", 0); !errors.Is(e, errConflict) {
		t.Fatal("stale overwrite allowed", e)
	}
	if _, e = s.saveNote("missing", "orphan", 0); e == nil {
		t.Fatal("orphan note allowed")
	}
	s.Close()
	s, e = openStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	loaded, e := s.note("t")
	if e != nil || loaded != n {
		t.Fatal("knowledge lost", e)
	}
	var status string
	s.QueryRow("SELECT status FROM runs WHERE id='r'").Scan(&status)
	if status != "interrupted" {
		t.Fatal("crashed run silently replayed")
	}
}
func TestFeishuOwnerDedupAndUnifiedTask(t *testing.T) {
	f := &fakeRunner{}
	a := fixture(t, f)
	task := taskFor(t, a)
	c := a.config.get()
	c.Feishu = FeishuConfig{Enabled: true, AppID: "test", Secret: "test-secret", Owner: "owner"}
	if e := a.config.save(c); e != nil {
		t.Fatal(e)
	}
	a.bind("chat", task.ID)
	if e := a.feishu.receive("m0", "stranger", "chat", "不能执行"); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e := a.feishu.receive("m1", "owner", "chat", "检查文件"); e != nil {
			t.Fatal(e)
		}
	}
	waitUntil(t, func() bool { r, _ := a.store.runs(task.ID); return len(r) == 1 && r[0].Status == "done" })
	runs, _ := a.store.runs(task.ID)
	if runs[0].Source != "feishu" || runs[0].Input != "检查文件" {
		t.Fatal("separate task or changed input")
	}
	all, _ := a.store.tasks()
	if len(all) != 1 {
		t.Fatal("feishu created duplicate task")
	}
	var count int
	a.store.QueryRow("SELECT count(*) FROM feishu_run_cards").Scan(&count)
	if count != 1 {
		t.Fatal("one durable reply card expected", count)
	}
}
func TestFeishuPairingRequiresCode(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	c := a.config.get()
	c.Feishu = FeishuConfig{Enabled: true, AppID: "test", Secret: "secret"}
	a.config.save(c)
	a.feishu.receive("m0", "stranger", "chat", "hello")
	if a.config.get().Feishu.Owner != "" {
		t.Fatal("unpaired user claimed bot")
	}
	code := a.feishu.pairCode()
	a.feishu.receive("m1", "owner", "chat", "/配对 "+code)
	if a.config.get().Feishu.Owner != "owner" || a.store.setting("feishu_chat") != "chat" {
		t.Fatal("pairing failed")
	}
}
func TestHTTPLoginCSRFNotesAndSecretRedaction(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	password, _ := bcrypt.GenerateFromPassword([]byte("test-password"), bcrypt.MinCost)
	a.store.set("password_hash", string(password))
	c := a.config.get()
	c.Feishu.Secret = "do-not-expose"
	a.config.save(c)
	srv := httptest.NewServer((&Server{app: a}).Handler())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := ""
	request := func(path, method string, data any, origin string) (int, []byte) {
		t.Helper()
		b, _ := json.Marshal(data)
		r, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(b))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		resp, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, out
	}
	if code, _ := request("/api/tasks", "GET", nil, srv.URL); code != 401 {
		t.Fatal(code)
	}
	code, b := request("/api/login", "POST", map[string]string{"password": "test-password"}, srv.URL)
	if code != 200 {
		t.Fatal(code, string(b))
	}
	var login map[string]string
	json.Unmarshal(b, &login)
	path := "/api/tasks/" + task.ID + "/note"
	if code, _ = request(path, "PUT", Note{Content: "bad"}, srv.URL); code != 403 {
		t.Fatal("CSRF bypass", code)
	}
	csrf = login["csrf"]
	a.feishu.register = func(ctx context.Context, _ *registration.Options) (*registration.RegisterAppResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if code, _ = request("/api/feishu/setup", "POST", map[string]string{}, "http://evil.example"); code != 403 {
		t.Fatal("setup origin bypass", code)
	}
	code, b = request("/api/feishu/setup", "POST", map[string]string{}, srv.URL)
	if code != 409 {
		t.Fatal("configured application was not protected", code, string(b))
	}

	if code, _ = request(path, "PUT", Note{Content: "bad"}, "http://evil.example"); code != 403 {
		t.Fatal("origin bypass", code)
	}
	if code, b = request(path, "PUT", Note{Content: "# 中文知识"}, srv.URL); code != 200 {
		t.Fatal(code, string(b))
	}
	if code, _ = request(path, "PUT", Note{Content: "stale"}, srv.URL); code != 409 {
		t.Fatal("lost update", code)
	}
	if code, b = request(path+"?download=1", "GET", nil, srv.URL); code != 200 || string(b) != "# 中文知识" {
		t.Fatal("export mismatch")
	}
	_, b = request("/api/settings", "GET", nil, srv.URL)
	if strings.Contains(string(b), "do-not-expose") {
		t.Fatal("secret leaked")
	}
	_, b = request("/", "GET", nil, srv.URL)
	if !strings.Contains(string(b), "Duo") {
		t.Fatal("independent frontend missing")
	}
}
func TestCodexResumeTargetsExactSession(t *testing.T) {
	task := Task{Workspace: filepath.ToSlash("/home/dev/work/project with space"), Session: "exact-id", Model: "model"}
	args := codexArgs(Config{}, task)
	joined := strings.Join(args, "|")
	if !strings.Contains(joined, "exec|resume") || !strings.HasSuffix(joined, "exact-id|-") || strings.Contains(joined, "--last") {
		t.Fatal(args)
	}
}
