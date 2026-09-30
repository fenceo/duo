package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
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

func TestEnvironmentAccountSwitchBetweenQueuedTurns(t *testing.T) {
	runner := &switchRunner{gate: make(chan struct{})}
	a, original := switchFixture(t, runner)
	profiles := []EngineCredentialProfile{}
	for _, id := range []string{"a", "b"} {
		profiles = append(profiles, EngineCredentialProfile{ID: id, Name: id, Engine: "codex", EnvironmentID: original.Environment.ID, Kind: "codex_home", Reference: filepath.Join(t.TempDir(), id)})
	}
	if err := a.store.saveEngineProfiles(profiles); err != nil {
		t.Fatal(err)
	}
	if err := a.store.activateEngineProfile(original.Environment.ID, "codex", "a"); err != nil {
		t.Fatal(err)
	}
	task, err := a.createWithExecution("following", original.Workspace, "model-a", "codex", "", original.Environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Binding.AccountMode != "environment" {
		t.Fatal("new task not following environment")
	}
	first, err := a.submit(task.ID, "first-completed-history", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { runner.mu.Lock(); defer runner.mu.Unlock(); return len(runner.calls) == 1 })
	second, err := a.submit(task.ID, "second", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	third, err := a.submit(task.ID, "third", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.Exec("UPDATE runs SET created=? WHERE id=?", first.Created+1, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.Exec("UPDATE runs SET created=? WHERE id=?", first.Created+2, third.ID); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	err = a.store.activateEngineProfile(task.Environment.ID, "codex", "b")
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	close(runner.gate)
	waitUntil(t, func() bool {
		var n int
		a.store.QueryRow("SELECT count(*) FROM runs WHERE task_id=? AND status='done'", task.ID).Scan(&n)
		return n == 3
	})
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.calls) != 3 {
		t.Fatal("missing turns")
	}
	if runner.calls[0].profile["CODEX_HOME"] != profiles[0].Reference {
		t.Fatal("active turn redirected")
	}
	for _, call := range runner.calls[1:] {
		if call.profile["CODEX_HOME"] != profiles[1].Reference || call.task.Model != "model-a" {
			t.Fatal("queued turn did not follow environment")
		}
	}
	if runner.calls[1].task.Session != "" || !strings.Contains(runner.calls[1].archive, "first-completed-history") {
		t.Fatal("new account reused old session or lost preceding history")
	}
	if runner.calls[2].task.Session != "target-native" || runner.calls[2].task.Binding.HistoryID != runner.calls[1].task.Binding.HistoryID {
		t.Fatal("later queued turn lost current account session/history")
	}
	var snapshot string
	if err = a.store.QueryRow("SELECT snapshot FROM run_execution WHERE run_id=?", third.ID).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	var saved RunExecution
	if err = json.Unmarshal([]byte(snapshot), &saved); err != nil || saved.Binding.Profile.ID != "b" {
		t.Fatal("run audit reports submitted instead of actual account", err)
	}
}

func TestEnvironmentAccountMigrationPreservesOverrides(t *testing.T) {
	a, automatic := switchFixture(t, &fakeRunner{})
	manual, err := a.createWithExecution("manual", automatic.Workspace, "", "codex", "", automatic.Environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range []Task{automatic, manual} {
		task.Binding.AccountMode = ""
		saveTestBinding(t, a, task)
	}
	if _, err = a.store.Exec("INSERT INTO task_engine_switches VALUES(?,?,?,?)", "explicit", manual.ID, "{}", now()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = a.store.Exec(engineBindingSchema); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ id, mode string }{{automatic.ID, "environment"}, {manual.ID, "pinned"}} {
		task, err := a.store.task(tc.id)
		if err != nil || task.Binding.AccountMode != tc.mode {
			t.Fatal("migration changed account intent", err)
		}
	}
	migrated, _ := a.store.task(automatic.ID)
	if migrated.Session != automatic.Session || migrated.Binding.Revision != automatic.Binding.Revision {
		t.Fatal("migration rewrote session ownership")
	}
}

func TestEnvironmentAccountOverrideAndReturnToFollowing(t *testing.T) {
	a, task := switchFixture(t, &fakeRunner{})
	p := EngineCredentialProfile{ID: "chosen", Engine: "codex", EnvironmentID: task.Environment.ID, Kind: "codex_home", Reference: filepath.Join(t.TempDir(), "chosen")}
	if err := a.store.saveEngineProfiles([]EngineCredentialProfile{p}); err != nil {
		t.Fatal(err)
	}
	v := switchRequest(t, a, task)
	v.ProfileID = p.ID
	v.ExpectedProfile = &p
	result, err := a.switchTaskEngine(task.ID, v)
	if err != nil || result.Task.Binding.AccountMode != "pinned" {
		t.Fatal(err)
	}
	if err = a.store.activateEngineProfile(task.Environment.ID, "codex", p.ID); err != nil {
		t.Fatal(err)
	}
	v = switchRequest(t, a, result.Task)
	v.ProfileID = "__environment__"
	v.ExpectedProfile = &p
	result, err = a.switchTaskEngine(task.ID, v)
	if err != nil || result.NewSession || result.Task.Binding.AccountMode != "environment" {
		t.Fatal("returning to same environment account reset session", err)
	}
	// Changing the environment default after review must reject the old preview.
	v = switchRequest(t, a, result.Task)
	v.ProfileID = "__environment__"
	v.ExpectedProfile = &p
	if err = a.store.activateEngineProfile(task.Environment.ID, "codex", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = a.switchTaskEngine(task.ID, v); err == nil {
		t.Fatal("stale environment account accepted")
	}
}

func TestEnvironmentAccountNativeSyncAndRollback(t *testing.T) {
	a, task := switchFixture(t, &fakeRunner{})
	if err := a.store.useSyncedNativeAccount(task.Environment.ID, "claude"); err != nil {
		t.Fatal(err)
	}
	if got := a.store.nativeAccountRevision(task.Environment.ID, "codex"); got != "" {
		t.Fatal("cross-engine native revision")
	}
	if err := a.store.useSyncedNativeAccount("different", "codex"); err != nil {
		t.Fatal(err)
	}
	if got := a.store.nativeAccountRevision(task.Environment.ID, "codex"); got != "" {
		t.Fatal("cross-environment native revision")
	}
	if err := a.store.useSyncedNativeAccount(task.Environment.ID, "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES('sync-run',?,'synthetic','chat','web','queued',?)", task.ID, now()); err != nil {
		t.Fatal(err)
	}
	if err := a.environmentAccountIdle(task.Environment.ID, "codex"); err == nil {
		t.Fatal("sync allowed against queued work")
	}
	if err := a.environmentAccountIdle(task.Environment.ID, "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Exec("CREATE TRIGGER block_account_handoff BEFORE INSERT ON task_handoff_context BEGIN SELECT RAISE(ABORT,'synthetic rollback'); END"); err != nil {
		t.Fatal(err)
	}
	if err := a.resolveRunAccount("sync-run", &task); err == nil {
		t.Fatal("failed archive accepted")
	}
	unchanged, _ := a.store.task(task.ID)
	if unchanged.Session != task.Session || unchanged.Binding.HistoryID != "" {
		t.Fatal("failed switch changed task")
	}
	if _, err := a.store.Exec("DROP TRIGGER block_account_handoff"); err != nil {
		t.Fatal(err)
	}
	if err := a.resolveRunAccount("sync-run", &task); err != nil {
		t.Fatal(err)
	}
	if task.Session != "" || task.Binding.NativeRevision == "" || task.Binding.HistoryID == "" {
		t.Fatal("same native directory login change reused old session")
	}
}

func TestEnvironmentAccountChangeRespectsBlankSessionReset(t *testing.T) {
	a, task := switchFixture(t, &fakeRunner{})
	var err error
	task, err = a.resetSession(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.store.useSyncedNativeAccount(task.Environment.ID, "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES('blank-run',?,'synthetic','chat','web','queued',?)", task.ID, now()); err != nil {
		t.Fatal(err)
	}
	if err = a.resolveRunAccount("blank-run", &task); err != nil {
		t.Fatal(err)
	}
	if task.Session != "" || task.Binding.HistoryID != "" {
		t.Fatal("account change undid explicit blank-session reset")
	}
}
