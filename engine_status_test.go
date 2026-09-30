package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestConfiguredRemoteEngineDetection(t *testing.T) {
	for _, kind := range []string{"wsl", "ssh"} {
		t.Run(kind, func(t *testing.T) {
			env := Environment{ID: "target", Type: kind, User: "selected", Codex: "/custom/codex"}
			probe := func(ctx context.Context, target Environment, args ...string) (string, error) {
				if target.User != "selected" {
					t.Error("changed target user")
				}
				if args[0] == "sh" {
					if !strings.Contains(args[2], "/custom/codex") {
						t.Error("lost configured path")
					}
					return "__DUO_TOOLS__\n/custom/codex\n\n/home/user/dsh\n/home/user/kimi\n\n", nil
				}
				if args[0] != "/custom/codex" {
					t.Errorf("unexpected executable %s", args[0])
				}
				return "Logged in with synthetic-account", nil
			}
			got := inspectEngineEnvironment(context.Background(), probe, env)
			if got.Codex.State != "configured" || got.Claude.State != "missing" || got.Harness.State != "installed" || got.Kimi.State != "installed" || got.Mimo.State != "missing" {
				t.Fatalf("unexpected states: %#v", got)
			}
			failed := inspectEngineEnvironment(context.Background(), func(context.Context, Environment, ...string) (string, error) {
				return "", errors.New("secret raw diagnostic")
			}, env)
			if failed.Codex.State != "unknown" || failed.Mimo.State != "unknown" || strings.Contains(failed.Message, "secret") {
				t.Fatal("connection failure misreported")
			}
		})
	}
}

func TestRemoteInstallValidationAndDestination(t *testing.T) {
	for _, kind := range []string{"windows", "wsl", "ssh"} {
		if err := validateEngineSetup(EngineSetupRequest{ID: "fixture", Action: "install", Engine: "codex"}, Environment{Type: kind}); err != nil {
			t.Fatal(err)
		}
	}
	if err := validateEngineSetup(EngineSetupRequest{ID: "fixture", Action: "install", Engine: "codex;echo bad"}, Environment{Type: "ssh"}); err == nil {
		t.Fatal("non-allowlisted package accepted")
	}
	script := remoteEngineInstallScript("@openai/codex", "codex", "fixture")
	for _, expected := range []string{"--prefix", ".local/share/duo/engine-tools/fixture", "--version", "https://registry.npmjs.org"} {
		if !strings.Contains(script, expected) {
			t.Fatal("missing install contract", expected)
		}
	}
	if strings.Contains(script, "sudo ") {
		t.Fatal("unrequested privilege escalation")
	}
}

func TestClaudeAPIAccountSetupDoesNotCallCLI(t *testing.T) {
	a, env := engineSetupFixture(t)
	a.engineSetup.account = func(context.Context, Environment, string, string, string, func(string, string)) error {
		t.Error("Codex CLI called for Claude")
		return errors.New("unexpected")
	}
	job, err := a.startEngineSetup(EngineSetupRequest{ID: "claude-api-fixture", Action: "account", Engine: "claude", EnvironmentID: env.ID, Name: "Relay", Login: "apiKey", APIKey: "synthetic-token", BaseURL: "https://relay.example", Model: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { j, _ := a.engineSetup.snapshot(job.ID); return j.State == "done" || j.State == "failed" })
	job, _ = a.engineSetup.snapshot(job.ID)
	if job.State != "done" {
		t.Fatal(job.Message)
	}
	profiles := a.store.engineProfiles()
	if len(profiles) != 1 || profiles[0].Kind != "claude_home" {
		t.Fatal(profiles)
	}
	if strings.Contains(a.store.setting(engineProfilesSetting), "synthetic-token") {
		t.Fatal("secret in database")
	}
}

func TestRemoteInstallSavesExactTargetOnly(t *testing.T) {
	a, _ := engineSetupFixture(t)
	c := a.config.get()
	env := Environment{ID: "remote", Name: "Remote", Type: "ssh", Host: "fixture.invalid", User: "fixture", Codex: "/old/codex", Workspaces: []string{"/work"}}
	c.Environments = append(c.Environments, env)
	if err := a.config.save(c); err != nil {
		t.Fatal(err)
	}
	var errInstall error
	a.engineSetup.remoteInstall = func(ctx context.Context, e Environment, engine, id string, progress func(string)) (string, error) {
		if e.User != "fixture" || e.Host != "fixture.invalid" {
			t.Error("wrong target")
		}
		return "/fixture/new/codex", errInstall
	}
	for _, id := range []string{"good", "failed"} {
		if id == "failed" {
			errInstall = errors.New("synthetic failure")
		}
		job, err := a.startEngineSetup(EngineSetupRequest{ID: id, Action: "install", EnvironmentID: env.ID, Engine: "codex"})
		if err != nil {
			t.Fatal(err)
		}
		waitUntil(t, func() bool { a.engineSetup.mu.Lock(); defer a.engineSetup.mu.Unlock(); return !a.engineSetup.active })
		job, _ = a.engineSetup.snapshot(job.ID)
		if (job.State == "done") != (id == "good") {
			t.Fatal(job)
		}
	}
	updated, _ := a.config.get().environment(env.ID)
	if updated.Codex != "/fixture/new/codex" {
		t.Fatal("failed remote install changed successful config")
	}
}
