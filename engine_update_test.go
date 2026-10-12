package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func awaitEngineUpdate(t *testing.T, a *App, id string) EngineSetupJob {
	t.Helper()
	waitUntil(t, func() bool { a.engineSetup.mu.Lock(); defer a.engineSetup.mu.Unlock(); return !a.engineSetup.active })
	job, ok := a.engineSetup.snapshot(id)
	if !ok {
		t.Fatal("missing job")
	}
	return job
}

func TestEngineUpdateCommitsVerifiedVersionAndPreservesTask(t *testing.T) {
	a, env := engineSetupFixture(t)
	task, err := a.create("existing", env.Workspaces[0], "fixture-model", env.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.store.Exec("UPDATE tasks SET session=? WHERE id=?", "native-session", task.ID)
	a.engineSetup.verifyVersion = func(context.Context, Environment, string) (string, error) { return "1.2.3", nil }
	calls := 0
	for i := 0; i < 2; i++ {
		current, _ := a.config.get().environment(env.ID)
		path := fmt.Sprintf("fixture-new-%d.exe", i)
		a.engineSetup.install = func(context.Context, string, string, func(string)) (string, error) { calls++; return path, nil }
		request := EngineSetupRequest{ID: fmt.Sprintf("update-%d", i), Action: "update", Engine: "codex", EnvironmentID: env.ID, ExpectedPath: current.Codex}
		job, err := a.startEngineSetup(request)
		if err != nil {
			t.Fatal(err)
		}
		job = awaitEngineUpdate(t, a, job.ID)
		if job.State != "done" || job.Version != "1.2.3" {
			t.Fatal(job)
		}
		if _, err = a.startEngineSetup(request); err != nil || calls != i+1 {
			t.Fatal("idempotent retry changed install", err, calls)
		}
		stored, _ := a.store.task(task.ID)
		if stored.Environment.Codex != env.Codex || stored.Session != "native-session" || stored.Model != task.Model {
			t.Fatal("task snapshot or native identity changed", stored)
		}
		if got := runtimeConfig(a.config.get(), *stored.Environment).Codex; got != path {
			t.Fatal("old task did not follow update", got)
		}
		custom := *stored.Environment
		custom.Codex = "custom.exe"
		if got := runtimeConfig(a.config.get(), custom).Codex; got != "custom.exe" {
			t.Fatal("custom snapshot overwritten", got)
		}
		moved := *stored.Environment
		moved.User = "different"
		if got := runtimeConfig(a.config.get(), moved).Codex; got != env.Codex {
			t.Fatal("different target followed update", got)
		}
	}
	c := a.config.get()
	for i := range c.Environments {
		if c.Environments[i].ID == env.ID {
			c.Environments[i].Codex = "manual.exe"
		}
	}
	if err = a.config.save(c); err != nil {
		t.Fatal(err)
	}
	if runtimeConfig(a.config.get(), env).Codex != env.Codex {
		t.Fatal("manual path change failed to disable alias")
	}
}

func TestEngineUpdateFailureCancelAndStaleConfigPreservePath(t *testing.T) {
	for _, kind := range []string{"download", "version", "cancel", "path", "target", "removed"} {
		t.Run(kind, func(t *testing.T) {
			a, env := engineSetupFixture(t)
			a.engineSetup.verifyVersion = func(context.Context, Environment, string) (string, error) {
				if kind == "version" {
					return "", errors.New("version unavailable")
				}
				return "1.2.3", nil
			}
			a.engineSetup.install = func(ctx context.Context, _ string, _ string, _ func(string)) (string, error) {
				if kind == "download" {
					return "", errors.New("download failed")
				}
				if kind == "cancel" {
					<-ctx.Done()
					return "", ctx.Err()
				}
				if kind == "path" || kind == "target" || kind == "removed" {
					c := a.config.get()
					for i := range c.Environments {
						if c.Environments[i].ID == env.ID {
							if kind == "path" {
								c.Environments[i].Codex = "manual.exe"
							}
							if kind == "target" {
								c.Environments[i].User = "other"
							}
							if kind == "removed" {
								c.Environments = append(c.Environments[:i], c.Environments[i+1:]...)
							}
							break
						}
					}
					if err := a.config.save(c); err != nil {
						return "", err
					}
				}
				return "new.exe", nil
			}
			job, err := a.startEngineSetup(EngineSetupRequest{ID: "update-failure", Action: "update", Engine: "codex", EnvironmentID: env.ID, ExpectedPath: env.Codex})
			if err != nil {
				t.Fatal(err)
			}
			if kind == "cancel" {
				job.cancel()
			}
			job = awaitEngineUpdate(t, a, job.ID)
			if job.State != "failed" {
				t.Fatal(job)
			}
			current, err := a.config.get().environment(env.ID)
			if kind == "removed" {
				if err == nil {
					t.Fatal("removed target recreated")
				}
				return
			}
			want := env.Codex
			if kind == "path" {
				want = "manual.exe"
			}
			if current.Codex != want || len(current.ManagedTools) != 0 {
				t.Fatal("failed update changed configuration", current)
			}
		})
	}
}

func TestEngineUpdateBusyAndSubmissionGuards(t *testing.T) {
	a, env := engineSetupFixture(t)
	task, err := a.create("busy", env.Workspaces[0], "fixture-model", env.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := EngineSetupRequest{ID: "update-busy", Action: "update", Engine: "codex", EnvironmentID: env.ID, ExpectedPath: env.Codex}
	a.mu.Lock()
	a.workers[task.ID] = &worker{}
	a.mu.Unlock()
	_, err = a.startEngineSetup(r)
	a.mu.Lock()
	delete(a.workers, task.ID)
	a.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "任务") {
		t.Fatal("busy engine accepted", err)
	}
	for _, status := range []string{"running", "queued"} {
		if _, err = a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES(?,?,?,?,?,?,?)", "busy-fixture", task.ID, "fixture", "message", "web", status, now()); err != nil {
			t.Fatal(err)
		}
		_, err = a.startEngineSetup(r)
		a.store.Exec("DELETE FROM runs WHERE id='busy-fixture'")
		if err == nil {
			t.Fatal("persisted busy run accepted", status)
		}
	}
	a.engineSetup.install = func(ctx context.Context, _ string, _ string, _ func(string)) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	job, err := a.startEngineSetup(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.submit(task.ID, "do not start", "message", "web"); err == nil || !strings.Contains(err.Error(), "更新 CLI") {
		t.Fatal("submission during update", err)
	}
	other := task
	other.Engine = "claude"
	if a.engineUpdateBlocks(other) {
		t.Fatal("other engine blocked")
	}
	job.cancel()
	awaitEngineUpdate(t, a, job.ID)
	if a.engineUpdateBlocks(task) {
		t.Fatal("failed update left task blocked")
	}
}

func TestEngineUpdateSettingsCannotOverwriteActivation(t *testing.T) {
	a, env := engineSetupFixture(t)
	stale := a.config.get()
	request := toolsClient(t, a)
	release := make(chan struct{})
	a.engineSetup.install = func(ctx context.Context, _ string, _ string, _ func(string)) (string, error) {
		select {
		case <-release:
			return "verified-new.exe", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	a.engineSetup.verifyVersion = func(context.Context, Environment, string) (string, error) { return "1.2.3", nil }
	job, err := a.startEngineSetup(EngineSetupRequest{ID: "settings-update", Action: "update", Engine: "codex", EnvironmentID: env.ID, ExpectedPath: env.Codex})
	if err != nil {
		t.Fatal(err)
	}
	request("/api/settings", "PUT", stale, 409)
	close(release)
	if got := awaitEngineUpdate(t, a, job.ID); got.State != "done" {
		t.Fatal(got)
	}
	request("/api/settings", "PUT", stale, 409)
	current, _ := a.config.get().environment(env.ID)
	if current.Codex != "verified-new.exe" || runtimeConfig(a.config.get(), env).Codex != current.Codex {
		t.Fatal("stale settings undid activation", current)
	}
	// A freshly loaded form may still make an intentional manual path change.
	fresh := a.config.get()
	for i := range fresh.Environments {
		if fresh.Environments[i].ID == env.ID {
			fresh.Environments[i].Codex = "manual.exe"
		}
	}
	request("/api/settings", "PUT", fresh, 200)
	if runtimeConfig(a.config.get(), env).Codex != env.Codex {
		t.Fatal("manual change did not disable automatic replacement")
	}
	fresh = a.config.get()
	for i := range fresh.Environments {
		if fresh.Environments[i].ID == env.ID {
			tool := fresh.Environments[i].ManagedTools["codex"]
			tool.PreviousPaths = append(tool.PreviousPaths, "unverified.exe")
			fresh.Environments[i].ManagedTools["codex"] = tool
		}
	}
	request("/api/settings", "PUT", fresh, 409)
}

func TestEngineUpdateActivationPreservesConcurrentConfiguration(t *testing.T) {
	a, env := engineSetupFixture(t)
	pending := a.config.get()
	expected, err := json.Marshal(pending)
	if err != nil {
		t.Fatal(err)
	}
	for i := range pending.Environments {
		if pending.Environments[i].ID == env.ID {
			pending.Environments[i].Codex = "verified.exe"
			pending.Environments[i].ManagedTools = map[string]ManagedEngineTool{"codex": {Path: "verified.exe", Version: "1.2.3", PreviousPaths: []string{env.Codex}}}
		}
	}
	// A non-settings writer can save after activation prepared its snapshot.
	other := a.config.get()
	other.Feishu.Owner = "synthetic-new-owner"
	if err = a.config.save(other); err != nil {
		t.Fatal(err)
	}
	if err = a.config.saveChecked(pending, expected); !errors.Is(err, errEngineUpdateSettingsStale) {
		t.Fatal("activation did not reject stale snapshot", err)
	}
	current := a.config.get()
	unchanged, _ := current.environment(env.ID)
	if current.Feishu.Owner != other.Feishu.Owner || unchanged.Codex != env.Codex || len(unchanged.ManagedTools) != 0 {
		t.Fatal("activation overwrote concurrent configuration")
	}
}

func TestEngineUpdateHarnessReopensSameDurableSession(t *testing.T) {
	c, task := harnessFixtureSetup(t, "")
	env := Environment{ID: "fixture", Type: "windows", Harness: c.Harness}
	task.Environment = &env
	c.Environments = []Environment{env}
	noop := func(string, string) {}
	session, _, err := harnessFixtureRun(t, c, task, "before update marker", noop)
	if err != nil {
		t.Fatal(err)
	}
	task.Session = session
	data, err := os.ReadFile(c.Harness)
	if err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(t.TempDir(), "updated-fixture.exe")
	if err = os.WriteFile(newPath, data, 0700); err != nil {
		t.Fatal(err)
	}
	// Register cleanup after the new executable directory so the process exits first.
	t.Cleanup(closeHarnessRuntimes)
	c.Environments[0].ManagedTools = map[string]ManagedEngineTool{"deepseek-harness": {Path: newPath, Version: "1.2.3", PreviousPaths: []string{c.Harness}}}
	c.Harness = newPath
	c.Environments[0].Harness = newPath
	// This is a fresh executable copy, not the already loaded test binary.
	// Allow bounded Windows cold startup while retaining all protocol assertions.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	next, result, err := runHarnessACP(ctx, c, task, "after update marker", noop)
	var got harnessFixtureResult
	if err != nil || json.Unmarshal([]byte(result), &got) != nil || next != session || !strings.Contains(result, "before update marker") {
		t.Fatal("update lost native history", next, result, err)
	}
	harnessRuntimes.Lock()
	current := harnessRuntimes.workers[session]
	harnessRuntimes.Unlock()
	if current == nil || current.c.Harness != newPath {
		t.Fatal("old worker still used")
	}
	bad := c
	bad.SSHHost = "different.invalid"
	if managedHarnessReplacement(bad, task, current.c, current.fingerprint) {
		t.Fatal("unrelated configuration change accepted")
	}
}

func TestEngineUpdateRemoteTargetAndVersionProbe(t *testing.T) {
	for _, kind := range []string{"wsl", "ssh"} {
		t.Run(kind, func(t *testing.T) {
			a, env := engineSetupFixture(t)
			c := a.config.get()
			for i := range c.Environments {
				if c.Environments[i].ID == env.ID {
					c.Environments[i].Type = kind
					c.Environments[i].Distro = "Fixture"
					c.Environments[i].User = "selected"
					c.Environments[i].Host = "fixture.invalid"
					c.Environments[i].Codex = "/old/codex"
					c.Environments[i].Workspaces = []string{"/fixture"}
				}
			}
			if err := a.config.save(c); err != nil {
				t.Fatal(err)
			}
			env, _ = a.config.get().environment(env.ID)
			a.engineSetup.remoteInstall = func(_ context.Context, target Environment, engine, id string, _ func(string)) (string, error) {
				if !sameEngineTarget(target, env) || engine != "codex" {
					t.Error("wrong remote target")
				}
				return "/new/codex", nil
			}
			a.engineSetup.verifyVersion = func(_ context.Context, target Environment, path string) (string, error) {
				if !sameEngineTarget(target, env) || path != "/new/codex" {
					t.Error("wrong version target")
				}
				return "1.2.3", nil
			}
			request := toolsClient(t, a)
			request("/api/engine-setup", "POST", EngineSetupRequest{ID: "remote-update", Action: "update", EnvironmentID: env.ID, Engine: "codex", ExpectedPath: env.Codex}, 202)
			if job := awaitEngineUpdate(t, a, "remote-update"); job.State != "done" {
				t.Fatal(job)
			}
		})
	}
	for _, tc := range []struct {
		output, want string
		failed       bool
	}{{"codex-cli 0.165.0\n", "0.165.0", false}, {"2.1.4 (Claude Code)", "2.1.4", false}, {"v1.2.3-beta.1", "1.2.3-beta.1", false}, {"secret raw diagnostic", "", false}, {"1.2.3", "", true}} {
		probe := func(ctx context.Context, _ Environment, args ...string) (string, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Error("unbounded version probe")
			}
			if len(args) != 2 || args[1] != "--version" {
				t.Error(args)
			}
			if tc.failed {
				return tc.output, errors.New("private")
			}
			return tc.output, nil
		}
		version, err := readEngineVersion(context.Background(), probe, Environment{}, "fixture")
		if version != tc.want || (err == nil) != (tc.want != "") {
			t.Fatal(tc, version, err)
		}
		if err != nil && strings.Contains(err.Error(), "private") {
			t.Fatal("raw diagnostic exposed")
		}
	}
}
