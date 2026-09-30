package main

import (
	"reflect"
	"testing"
)

func legacyBindingFixture(t *testing.T) (*App, Task, handoffRequest) {
	t.Helper()
	a, task := switchFixture(t, &fakeRunner{})
	if _, err := a.store.Exec("DELETE FROM task_engine_bindings WHERE task_id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	task, err := a.store.task(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	v := switchRequest(t, a, task)
	v.ProfileID = ""
	v.ModeID = "__current__"
	v.PreserveLegacySession = true
	v.ExpectedLegacySession = task.Session
	return a, task, v
}

func TestLegacyBindingPreservesNativeSessionAndSubmits(t *testing.T) {
	for _, explicitProfile := range []bool{false} {
		t.Run(map[bool]string{false: "native-default", true: "profile"}[explicitProfile], func(t *testing.T) {
			a, original, v := legacyBindingFixture(t)
			profile := EngineCredentialProfile{ID: "original", Name: "Original", EnvironmentID: original.Environment.ID, Engine: "codex", Kind: "codex_home", Reference: t.TempDir(), Updated: 123}
			if explicitProfile {
				if err := a.store.saveEngineProfiles([]EngineCredentialProfile{profile}); err != nil {
					t.Fatal(err)
				}
				v.ProfileID, v.ExpectedProfile = profile.ID, &profile
			}
			result, err := a.switchTaskEngine(original.ID, v)
			if err != nil {
				t.Fatal(err)
			}
			if result.NewSession || result.Task.Session != original.Session || result.Task.Binding == nil || result.Task.Binding.HistoryID != "" || !reflect.DeepEqual(result.Task.Environment, original.Environment) {
				t.Fatalf("migration replaced native context: %+v", result)
			}
			if explicitProfile && !sameEngineProfile(result.Task.Binding.Profile, &profile) {
				t.Fatal("selected original profile was not saved")
			}
			again, err := a.switchTaskEngine(original.ID, v)
			if err != nil || again.NewSession || again.Task.Binding.Revision != result.Task.Binding.Revision {
				t.Fatal("migration retry not idempotent", err)
			}
			var contexts, changes int
			a.store.QueryRow("SELECT count(*) FROM task_handoff_context").Scan(&contexts)
			a.store.QueryRow("SELECT count(*) FROM task_engine_switches").Scan(&changes)
			if contexts != 0 || changes != 1 || a.store.activeEngineProfile(original.Environment.ID, original.Engine) != "" {
				t.Fatal("migration replayed history, duplicated audit, or changed global profile")
			}
			if _, err = a.submit(original.ID, "continue synthetic conversation", "chat", "web"); err != nil {
				t.Fatal("migrated task cannot submit", err)
			}
		})
	}
}

func TestLegacyBindingRejectsChangedContextWithoutMutation(t *testing.T) {
	cases := map[string]func(*App, *handoffRequest){
		"no-confirmation": func(_ *App, v *handoffRequest) { v.Confirm = false },
		"wrong-session":   func(_ *App, v *handoffRequest) { v.ExpectedLegacySession = "another-session" },
		"engine":          func(_ *App, v *handoffRequest) { v.Engine = "claude" },
		"workspace":       func(_ *App, v *handoffRequest) { v.Workspace = t.TempDir() },
		"mode":            func(_ *App, v *handoffRequest) { v.ModeID = "read" },
		"busy": func(a *App, _ *handoffRequest) {
			_, _ = a.store.Exec("UPDATE tasks SET status='queued'")
		},
		"stale-preview": func(_ *App, v *handoffRequest) { v.Fingerprint = "stale" },
		"environment": func(a *App, _ *handoffRequest) {
			c := a.config.get()
			c.Environments[0].Codex = "changed-binary"
			if err := a.config.save(c); err != nil {
				t.Fatal(err)
			}
		},
		"rollback": func(a *App, _ *handoffRequest) {
			if _, err := a.store.Exec("CREATE TRIGGER deny_binding BEFORE INSERT ON task_engine_bindings BEGIN SELECT RAISE(FAIL,'synthetic rejection'); END"); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			a, original, v := legacyBindingFixture(t)
			mutate(a, &v)
			if _, err := a.switchTaskEngine(original.ID, v); err == nil {
				t.Fatal("unsafe migration accepted")
			}
			saved, err := a.store.task(original.ID)
			if err != nil || saved.Binding != nil || saved.Session != original.Session {
				t.Fatal("rejected migration changed session or binding", err)
			}
			var changes int
			a.store.QueryRow("SELECT count(*) FROM task_engine_switches").Scan(&changes)
			if changes != 0 {
				t.Fatal("rejected migration left audit change")
			}
		})
	}
}
