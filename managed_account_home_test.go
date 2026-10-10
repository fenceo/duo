package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagedAccountHomePreservesExistingFiles(t *testing.T) {
	root := t.TempDir()
	env := Environment{Type: "windows"}
	files := map[string][]byte{"settings.json": []byte(`{"env":{"ANTHROPIC_API_KEY":"synthetic"}}`)}
	dir, err := createManagedAccountHome(context.Background(), env, root, "fixture", "claude", files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = createManagedAccountHome(context.Background(), env, root, "fixture", "claude", map[string][]byte{"settings.json": []byte(`{}`)}); err == nil {
		t.Fatal("existing inventory overwritten")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if !bytes.Equal(got, files["settings.json"]) {
		t.Fatal("existing config changed")
	}
	if _, err = createManagedAccountHome(context.Background(), env, root, "../escape", "claude", files); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestManagedAccountRemoteHelper(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		python, err = exec.LookPath("python3")
	}
	if err != nil {
		t.Skip("Python required for isolated remote helper")
	}
	home := t.TempDir()
	call := func(id, engine string, files map[string][]byte, success bool) string {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"id": id, "engine": engine, "files": files})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Redirect Path.home only in this synthetic subprocess.
		script := "import pathlib,sys; pathlib.Path.home=classmethod(lambda cls:pathlib.Path(sys.argv[1]))\n" + managedAccountHomeScript
		cmd := exec.CommandContext(ctx, python, "-c", script, home)
		cmd.Stdin = bytes.NewReader(raw)
		out, err := cmd.CombinedOutput()
		if (err == nil) != success {
			t.Fatalf("helper success=%v: %v (%s)", success, err, out)
		}
		if strings.Contains(string(out), "synthetic-secret") {
			t.Fatal("secret leaked")
		}
		if !success {
			return ""
		}
		var result struct {
			Directory string `json:"directory"`
		}
		if json.Unmarshal(out, &result) != nil {
			t.Fatal("invalid helper output")
		}
		return result.Directory
	}
	files := map[string][]byte{"settings.json": []byte(`{"env":{"ANTHROPIC_API_KEY":"synthetic-secret"}}`)}
	dir := call("remote-fixture", "claude", files, true)
	if dir != filepath.Join(home, ".local", "share", "duo", "engine-accounts", "remote-fixture") {
		t.Fatal("wrong target home")
	}
	call("remote-fixture", "claude", map[string][]byte{"settings.json": []byte(`{}`)}, false)
	got, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if !bytes.Equal(got, files["settings.json"]) {
		t.Fatal("inventory changed on retry")
	}
	call("../escape", "claude", files, false)
	call("invalid", "codex", files, false)
}

func TestRemoteAccountOnboardingAndConnectionChange(t *testing.T) {
	for _, kind := range []string{"wsl", "ssh"} {
		for _, login := range []string{"apiKey", "chatgptDeviceCode"} {
			t.Run(kind+"-"+login, func(t *testing.T) {
				a, _ := engineSetupFixture(t)
				c := a.config.get()
				env := Environment{ID: "remote", Name: "Remote", Type: kind, Distro: "fixture", User: "fixture", Host: "example.invalid", Codex: "/fixture/codex", Workspaces: []string{"/fixture"}}
				c.Environments = append(c.Environments, env)
				if err := a.config.save(c); err != nil {
					t.Fatal(err)
				}
				home := "/home/fixture/.local/share/duo/engine-accounts/remote-fixture"
				a.engineSetup.accountHome = func(_ context.Context, e Environment, _, id, engine string, files map[string][]byte) (string, error) {
					if e.ID != env.ID || e.Type != kind || id != "remote-fixture" || engine != "codex" || strings.Contains(string(files["config.toml"]), "synthetic-secret") {
						t.Fatal("incorrect target or leaked key")
					}
					return home, nil
				}
				a.engineSetup.account = func(_ context.Context, e Environment, dir, method, key string, waiting func(string, string)) error {
					if e.ID != env.ID || dir != home || method != login {
						t.Error("wrong login target")
					}
					if login == "apiKey" && key != "synthetic-secret" {
						t.Error("missing stdin key")
					}
					if login == "chatgptDeviceCode" {
						waiting("https://auth.openai.com/codex/device", "FIXTURE")
					}
					return nil
				}
				r := EngineSetupRequest{ID: "remote-fixture", Action: "account", Engine: "codex", EnvironmentID: env.ID, Name: "Remote fixture", Login: login}
				if login == "apiKey" {
					r.APIKey = "synthetic-secret"
					r.BaseURL = "https://example.invalid/v1"
				}
				job, err := a.startEngineSetup(r)
				if err != nil {
					t.Fatal(err)
				}
				waitUntil(t, func() bool { j, _ := a.engineSetup.snapshot(job.ID); return j.State == "done" || j.State == "failed" })
				job, _ = a.engineSetup.snapshot(job.ID)
				profiles := a.store.engineProfiles()
				if job.State != "done" || len(profiles) != 1 || profiles[0].EnvironmentID != env.ID || profiles[0].Reference != home {
					t.Fatal(job, profiles)
				}
				if a.store.currentEnvironmentAccount(env.ID, "codex") != "" {
					t.Fatal("adding inventory switched native account")
				}
			})
		}
	}
	a, env := engineSetupFixture(t)
	a.engineSetup.account = func(context.Context, Environment, string, string, string, func(string, string)) error {
		c := a.config.get()
		for i := range c.Environments {
			if c.Environments[i].ID == env.ID {
				c.Environments[i].Type = "wsl"
				c.Environments[i].Distro = "changed"
				c.Environments[i].Workspaces = []string{"/fixture"}
			}
		}
		return a.config.save(c)
	}
	job, err := a.startEngineSetup(EngineSetupRequest{ID: "changed-fixture", Action: "account", Engine: "codex", EnvironmentID: env.ID, Name: "Fixture", Login: "apiKey", APIKey: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { j, _ := a.engineSetup.snapshot(job.ID); return j.State == "failed" })
	if len(a.store.engineProfiles()) != 0 {
		t.Fatal("saved account against changed target")
	}
}

func TestCodexAccountSetupTargetIsolation(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "synthetic-inherited")
	t.Setenv("CODEX_API_KEY", "synthetic-inherited")
	t.Setenv("OPENAI_BASE_URL", "https://example.invalid")
	for _, kind := range []string{"windows", "wsl", "ssh"} {
		env := Environment{Type: kind, Distro: "fixture-distro", User: "fixture-user", Host: "example.invalid", Codex: "/fixture/codex"}
		cmd, c := codexAccountSetupCommand(env, "/fixture/account")
		if c.EngineEnv["CODEX_HOME"] != "/fixture/account" || strings.Contains(strings.Join(cmd.Args, " "), "synthetic-inherited") {
			t.Fatal("lost home or key in argv")
		}
		for _, pair := range cmd.Env {
			if strings.HasPrefix(pair, "OPENAI_API_KEY=") || strings.HasPrefix(pair, "CODEX_API_KEY=") || strings.HasPrefix(pair, "OPENAI_BASE_URL=") {
				t.Fatal("host credentials leaked")
			}
		}
		if kind == "wsl" && (!strings.Contains(strings.Join(cmd.Args, " "), "-u fixture-user --exec sh -lc") || len(cmd.Env) > 2) {
			t.Fatal("wrong WSL user or inherited host environment")
		}
		if kind == "ssh" && !strings.Contains(strings.Join(cmd.Args, " "), "fixture-user@example.invalid") {
			t.Fatal("wrong SSH user")
		}
	}
}
