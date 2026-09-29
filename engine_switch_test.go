package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func switchFixture(t *testing.T, runner Runner) (*App, Task) {
	t.Helper()
	a := fixture(t, runner)
	c := a.config.get()
	c.Environments = []Environment{{ID: "switch", Name: "Synthetic", Type: "windows", Codex: "not-executed", Workspaces: []string{t.TempDir()}}}
	c.DefaultEnvironment = "switch"
	if err := a.config.save(c); err != nil {
		t.Fatal(err)
	}
	task, err := a.createWithExecution("接续测试", c.Environments[0].Workspaces[0], "model-a", "codex", "", "switch")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.Exec("UPDATE tasks SET session='native-original' WHERE id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	task, err = a.store.task(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return a, task
}
func switchRequest(t *testing.T, a *App, task Task) handoffRequest {
	t.Helper()
	p, err := a.continuationPreview(task.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	current, err := a.store.task(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	revision := "legacy"
	if current.Binding != nil {
		revision = current.Binding.Revision
	}
	return handoffRequest{ExpectedBindingRevision: revision, Confirm: true, EnvironmentID: task.Environment.ID, Workspace: task.Workspace, Engine: task.Engine, Model: task.Model, ModeID: "work", ProfileID: "__current__", ContextMode: "full", Fingerprint: p.Fingerprint}
}
func TestEngineSwitchSameTaskModelAndAccount(t *testing.T) {
	a, task := switchFixture(t, &fakeRunner{})
	v := switchRequest(t, a, task)
	v.Model = "model-b"
	result, err := a.switchTaskEngine(task.ID, v)
	if err != nil {
		t.Fatal(err)
	}
	if result.NewSession || result.Task.Session != task.Session || result.Task.Model != "model-b" || result.Task.ID != task.ID {
		t.Fatalf("model edit lost session: %+v", result)
	}
	profile := EngineCredentialProfile{ID: "other", Name: "Other", EnvironmentID: task.Environment.ID, Engine: "codex", Kind: "codex_home", Reference: t.TempDir(), Updated: 123}
	if err = a.store.saveEngineProfiles([]EngineCredentialProfile{profile}); err != nil {
		t.Fatal(err)
	}
	v = switchRequest(t, a, result.Task)
	v.ProfileID = profile.ID
	v.ExpectedProfile = &profile
	result, err = a.switchTaskEngine(task.ID, v)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NewSession || result.Task.Session != "" || result.Task.Binding.Profile.ID != profile.ID {
		t.Fatal("account switch reused native identity")
	}
	again, err := a.switchTaskEngine(task.ID, v)
	if err != nil || again.Task.Binding.Revision != result.Task.Binding.Revision {
		t.Fatal("lost-response retry not idempotent", err)
	}
	var tasks, switches int
	a.store.QueryRow("SELECT count(*) FROM tasks").Scan(&tasks)
	a.store.QueryRow("SELECT count(*) FROM task_engine_switches").Scan(&switches)
	if tasks != 1 || switches != 2 {
		t.Fatal("duplicate task/switch", tasks, switches)
	}
	if got := a.store.activeEngineProfile(task.Environment.ID, task.Engine); got != "" {
		t.Fatal("task switch changed global default")
	}
}
func TestEngineSwitchRejectsStaleBusyProfileAndRollsBack(t *testing.T) {
	a, task := switchFixture(t, &fakeRunner{})
	v := switchRequest(t, a, task)
	v.Engine = "claude"
	v.ProfileID = ""
	if _, err := a.store.Exec("INSERT INTO knowledge_entries(id,task_id,title,content,status,source,revision,created,updated) VALUES('new',?,'新增','changed','observed','manual',1,1,1)", task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.switchTaskEngine(task.ID, v); !errors.Is(err, errHandoffChanged) {
		t.Fatal("stale preview accepted", err)
	}
	v = switchRequest(t, a, task)
	v.Engine = "claude"
	v.ProfileID = ""
	a.mu.Lock()
	a.workers[task.ID] = &worker{}
	a.mu.Unlock()
	if _, err := a.switchTaskEngine(task.ID, v); err == nil {
		t.Fatal("busy switch accepted")
	}
	a.mu.Lock()
	delete(a.workers, task.ID)
	a.mu.Unlock()
	if _, err := a.store.Exec("CREATE TRIGGER deny_switch BEFORE UPDATE ON task_execution BEGIN SELECT RAISE(FAIL,'synthetic rejection'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.switchTaskEngine(task.ID, v); err == nil {
		t.Fatal("failed transaction accepted")
	}
	current, _ := a.store.task(task.ID)
	var count int
	a.store.QueryRow("SELECT count(*) FROM task_handoff_context").Scan(&count)
	if current.Session != task.Session || current.Engine != task.Engine || count != 0 {
		t.Fatal("partial switch survived rollback")
	}
	a.store.Exec("DROP TRIGGER deny_switch")
	p := EngineCredentialProfile{ID: "p", EnvironmentID: task.Environment.ID, Engine: "claude", Kind: "claude_home", Reference: t.TempDir(), Updated: 1}
	a.store.saveEngineProfiles([]EngineCredentialProfile{p})
	v.ProfileID = p.ID
	v.ExpectedProfile = &p
	changed := p
	changed.Reference = t.TempDir()
	a.store.saveEngineProfiles([]EngineCredentialProfile{changed})
	if _, err := a.switchTaskEngine(task.ID, v); !errors.Is(err, errHandoffChanged) {
		t.Fatal("changed account accepted", err)
	}
}

type switchedCall struct {
	task    Task
	profile map[string]string
	input   string
	archive string
	path    string
}
type switchRunner struct {
	mu    sync.Mutex
	calls []switchedCall
	gate  chan struct{}
}

func (r *switchRunner) Run(ctx context.Context, c Config, task Task, input string, emit func(string, string)) (string, string, error) {
	call := switchedCall{task: task, profile: c.EngineEnv, input: input}
	for _, file := range task.Files {
		if file.Name == "Duo-历史对话.md" {
			data, err := os.ReadFile(file.Path)
			if err != nil {
				return "", "", err
			}
			call.archive = string(data)
			call.path = file.Path
		}
	}
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
	if r.gate != nil {
		select {
		case <-r.gate:
		case <-ctx.Done():
			return "", "", ctx.Err()
		}
	}
	return "target-native", "synthetic result", nil
}
func TestEngineSwitchTransfersFullHistoryAndRetainsDraftIndependentRuns(t *testing.T) {
	runner := &switchRunner{}
	a, task := switchFixture(t, runner)
	for i := 0; i < 27; i++ {
		status := "done"
		if i == 12 {
			status = "failed"
		}
		run := fmt.Sprintf("run%02d", i)
		_, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,error,created,finished) VALUES(?, ?,?,'chat','web',?,?,?, ?,?)", run, task.ID, fmt.Sprintf("request-%02d api_key=sensitive", i), status, fmt.Sprintf("answer-%02d", i), "", i+1, i+2)
		if err != nil {
			t.Fatal(err)
		}
		a.store.event(task.ID, run, "user", fmt.Sprintf("steer-%02d", i))
		a.store.event(task.ID, run, "tool", "synthetic tool output")
	}
	p, archive, err := a.buildContinuation(task, "full")
	if err != nil {
		t.Fatal(err)
	}
	if p.Runs != 27 || !p.Truncated || !strings.Contains(archive, "request-00") || !strings.Contains(archive, "steer-26") || strings.Contains(archive, "sensitive") {
		t.Fatal("full archive dropped rounds or leaked credentials")
	}
	v := switchRequest(t, a, task)
	v.Engine = "claude"
	v.ProfileID = ""
	result, err := a.switchTaskEngine(task.ID, v)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NewSession || result.Task.ID != task.ID {
		t.Fatal("engine switch failed")
	}
	if len(runner.calls) != 0 {
		t.Fatal("switch called model before a user message")
	}
	run, err := a.submit(task.ID, "continue after switch", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		var status string
		a.store.QueryRow("SELECT status FROM runs WHERE id=?", run.ID).Scan(&status)
		return status == "done"
	})
	runner.mu.Lock()
	call := runner.calls[0]
	runner.mu.Unlock()
	if call.task.Session != "" || call.task.Engine != "claude" || !strings.Contains(call.archive, "answer-00") || !strings.Contains(call.archive, "steer-26") || !strings.Contains(call.input, "本轮用户要求：\ncontinue after switch") {
		t.Fatalf("wrong engine context %+v", call)
	}
	if _, err = os.Stat(call.path); !os.IsNotExist(err) {
		t.Fatal("temporary handoff file not cleaned up", err)
	}
	var stored string
	a.store.QueryRow("SELECT input FROM runs WHERE id=?", run.ID).Scan(&stored)
	if stored != "continue after switch" {
		t.Fatal("context duplicated into visible user messages")
	}
	runs, err := a.store.runs(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range runs {
		want := "codex"
		if r.ID == run.ID {
			want = "claude"
		}
		if r.Engine != want {
			t.Fatal("historical speaker lost its engine", r.ID, r.Engine, want)
		}
	}
	waitUntil(t, func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.workers[task.ID] == nil })
	fresh, err := a.resetSession(task.ID)
	if err != nil || fresh.Binding.HistoryID != "" {
		t.Fatal("explicit blank session retained handoff context", err)
	}
}
func TestEngineBindingQueuesKeepOriginalProfileAndModel(t *testing.T) {
	runner := &switchRunner{gate: make(chan struct{})}
	a, task := switchFixture(t, runner)
	p := EngineCredentialProfile{ID: "profile-a", Name: "A", EnvironmentID: task.Environment.ID, Engine: "codex", Kind: "codex_home", Reference: filepath.Join(t.TempDir(), "account-a")}
	a.store.saveEngineProfiles([]EngineCredentialProfile{p})
	a.store.activateEngineProfile(task.Environment.ID, "codex", p.ID)
	// Existing task is explicitly bound to native defaults; new tasks capture A.
	if env := a.activeEngineEnvironment(task); env != nil {
		t.Fatal("global profile redirected an existing task")
	}
	created, err := a.createWithExecution("queued", task.Workspace, "queued-model", "codex", "", task.Environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.submit(created.ID, "first", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { runner.mu.Lock(); defer runner.mu.Unlock(); return len(runner.calls) == 1 })
	second, err := a.submit(created.ID, "second", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	a.store.removeEngineProfile(p.ID)
	// Even direct persistence changes cannot redirect accepted queued models.
	a.store.Exec("UPDATE tasks SET model='later-model' WHERE id=?", created.ID)
	close(runner.gate)
	waitUntil(t, func() bool {
		var n int
		a.store.QueryRow("SELECT count(*) FROM runs WHERE id IN (?,?) AND status='done'", first.ID, second.ID).Scan(&n)
		return n == 2
	})
	runner.mu.Lock()
	defer runner.mu.Unlock()
	for _, call := range runner.calls {
		if call.profile["CODEX_HOME"] != p.Reference || call.task.Model != "queued-model" {
			t.Fatal("queued run drifted", call)
		}
	}
	if runner.calls[1].task.Session != "target-native" {
		t.Fatal("queue snapshot overrode newly created native session")
	}
}
func TestEngineBindingLegacyRequiresExplicitHandoff(t *testing.T) {
	a, task := switchFixture(t, &fakeRunner{})
	a.store.Exec("DELETE FROM task_engine_bindings WHERE task_id=?", task.ID)
	if _, err := a.submit(task.ID, "continue", "chat", "web"); err == nil {
		t.Fatal("guessed old session owner")
	}
	v := switchRequest(t, a, task)
	v.ProfileID = ""
	result, err := a.switchTaskEngine(task.ID, v)
	if err != nil || !result.NewSession {
		t.Fatal("legacy handoff unavailable", err)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "native-original") {
		t.Fatal("old native identity leaked into target")
	}
}
