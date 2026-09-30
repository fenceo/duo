package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func saveTestBinding(t *testing.T, a *App, task Task) {
	t.Helper()
	tx, err := a.store.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = saveEngineBinding(tx, task.ID, task.Binding); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

type accountNativeCall struct {
	task Task
	auth string
	env  map[string]string
}
type accountNativeRunner struct {
	mu    sync.Mutex
	calls []accountNativeCall
	file  string
	gate  chan struct{}
}

func (r *accountNativeRunner) Run(ctx context.Context, c Config, t Task, input string, emit func(string, string)) (string, string, error) {
	raw, err := os.ReadFile(r.file)
	if err != nil {
		return "", "", err
	}
	r.mu.Lock()
	r.calls = append(r.calls, accountNativeCall{t, string(raw), c.EngineEnv})
	r.mu.Unlock()
	select {
	case <-r.gate:
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
	return "native-session", "synthetic reply", nil
}

func TestAccountSwitchDoesNotTouchTasksOrSessions(t *testing.T) {
	a, env := engineSetupFixture(t)
	target, source := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", target)
	oldAuth, newAuth := `{"OPENAI_API_KEY":"synthetic-old"}`, `{"OPENAI_API_KEY":"synthetic-new"}`
	for file, raw := range map[string]string{filepath.Join(target, "auth.json"): oldAuth, filepath.Join(source, "auth.json"): newAuth} {
		if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := EngineCredentialProfile{ID: "new-account", Name: "Synthetic", Engine: "codex", EnvironmentID: env.ID, Kind: "codex_home", Reference: source}
	if err := a.store.saveEngineProfiles([]EngineCredentialProfile{p}); err != nil {
		t.Fatal(err)
	}
	runner := &accountNativeRunner{file: filepath.Join(target, "auth.json"), gate: make(chan struct{})}
	a.runner = runner
	task, err := a.createWithExecution("existing", env.Workspaces[0], "fixed-model", "codex", "", env.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Even a legacy task-specific account reference must not select credentials.
	task.Binding.AccountMode = "pinned"
	task.Binding.Profile = &p
	saveTestBinding(t, a, task)
	if _, err = a.store.Exec("UPDATE tasks SET session='native-session' WHERE id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	first, err := a.submit(task.ID, "first", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { runner.mu.Lock(); defer runner.mu.Unlock(); return len(runner.calls) == 1 })
	second, err := a.submit(task.ID, "second", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	before, err := a.store.task(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var queuedBefore string
	if err = a.store.QueryRow("SELECT snapshot FROM run_execution WHERE run_id=?", second.ID).Scan(&queuedBefore); err != nil {
		t.Fatal(err)
	}
	do := toolsClient(t, a)
	raw := do("/api/account-sync", "POST", CodexSyncRequest{SourceProfileID: p.ID, EnvironmentIDs: []string{env.ID}}, 200)
	if !strings.Contains(string(raw), `"state":"done"`) {
		t.Fatal("native switch failed", string(raw))
	}
	after, err := a.store.task(task.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("account switch edited task", err)
	}
	var queuedAfter string
	a.store.QueryRow("SELECT snapshot FROM run_execution WHERE run_id=?", second.ID).Scan(&queuedAfter)
	if queuedAfter != queuedBefore {
		t.Fatal("account switch rewrote queued input")
	}
	var archives, switches int
	a.store.QueryRow("SELECT count(*) FROM task_handoff_context").Scan(&archives)
	a.store.QueryRow("SELECT count(*) FROM task_engine_switches").Scan(&switches)
	if archives != 0 || switches != 0 {
		t.Fatal("account switch generated task handoff")
	}
	if a.store.currentEnvironmentAccount(env.ID, "codex") != p.ID || a.store.currentEnvironmentAccount("unselected", "codex") != "" || a.store.currentEnvironmentAccount(env.ID, "claude") != "" {
		t.Fatal("native account state escaped selected target")
	}
	close(runner.gate)
	waitUntil(t, func() bool {
		var n int
		a.store.QueryRow("SELECT count(*) FROM runs WHERE id IN (?,?) AND status='done'", first.ID, second.ID).Scan(&n)
		return n == 2
	})
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.calls) != 2 || runner.calls[0].auth != oldAuth || runner.calls[1].auth != newAuth {
		t.Fatal("new CLI invocation did not read switched native account")
	}
	for _, call := range runner.calls {
		if len(call.env) != 0 || call.task.Session != "native-session" || call.task.Binding.HistoryID != "" || call.task.Model != "fixed-model" {
			t.Fatal("account switch altered execution/session")
		}
	}
}

func TestAccountEnvironmentMetadataDoesNotMigrateTaskBindings(t *testing.T) {
	a, task := switchFixture(t, &fakeRunner{})
	task.Binding.AccountMode = "pinned"
	task.Binding.Profile = &EngineCredentialProfile{ID: "old", Kind: "codex_home", Reference: "unused-history"}
	saveTestBinding(t, a, task)
	before, _ := a.store.task(task.ID)
	for range 2 {
		if _, err := a.store.Exec(engineBindingSchema); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := a.store.task(task.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("upgrade rewrote tasks")
	}
	if got := a.activeEngineEnvironment(after); len(got) != 0 {
		t.Fatal("legacy account binding controls login")
	}
	if err := a.store.useSyncedNativeAccount(task.Environment.ID, "codex", "chosen"); err != nil {
		t.Fatal(err)
	}
	final, _ := a.store.task(task.ID)
	if !reflect.DeepEqual(after, final) {
		t.Fatal("environment record changed task")
	}
	fresh, err := a.createWithExecution("fresh", task.Workspace, "", "codex", "", task.Environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(fresh.Binding)
	if strings.Contains(string(data), "profile") || strings.Contains(string(data), "account_mode") {
		t.Fatal("new task binds account")
	}
}
