package main

import (
	"strings"
	"testing"
)

func TestTaskEnvironmentSurvivesConfigurationChange(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	original := taskFor(t, a)
	c := a.config.get()
	c.Environments = []Environment{{ID: "remote", Name: "Remote", Type: "ssh", Host: "example.test", User: "dev", Port: 22, Codex: "/opt/codex", Workspaces: []string{"/work"}}}
	c.DefaultEnvironment = "remote"
	if err := a.config.save(c); err != nil {
		t.Fatal(err)
	}
	saved, err := a.store.task(original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Environment == nil || saved.Environment.ID != original.Environment.ID || saved.Environment.Codex != original.Environment.Codex {
		t.Fatal("existing task changed environment")
	}
	remote, err := a.create("remote", "/work", "chosen-model", "remote")
	if err != nil {
		t.Fatal(err)
	}
	runtime := runtimeConfig(c, *remote.Environment)
	if runtime.SSHHost != "example.test" || runtime.Distro != "" || remote.Model != "chosen-model" {
		t.Fatal("wrong remote runtime")
	}
	custom, err := a.create("custom", "/other/project with spaces", "", "remote")
	if err != nil || custom.Workspace != "/other/project with spaces" {
		t.Fatal("custom directory rejected", err)
	}
	for _, bad := range []string{"relative", "", "C:\\work", "/work\ninvalid"} {
		if _, err = a.create("bad", bad, "", "remote"); err == nil {
			t.Fatal("invalid path accepted", bad)
		}
	}
	if _, err = a.create("bad", "/work", "", "missing"); err == nil {
		t.Fatal("missing environment accepted")
	}
}

func TestSSHArgumentsAndModelFiltering(t *testing.T) {
	cmd := sshCommand(Config{SSHHost: "host.test", User: "dev", SSHPort: 2222}, "python3", "-c", "print('hello'); $(evil)")
	args := strings.Join(cmd.Args, "|")
	if !strings.Contains(args, "StrictHostKeyChecking=yes") || !strings.Contains(args, "BatchMode=yes") || !strings.Contains(args, "dev@host.test") || !strings.Contains(args, "'\"'\"'") {
		t.Fatal(args)
	}
	models, err := parseModels([]byte(`{"models":[{"slug":"visible","visibility":"list"},{"slug":"secret","visibility":"hide"},{"slug":"spark","visibility":"list","supported_in_api":false}]}`))
	if err != nil || len(models) != 2 || models[1].ID != "spark" {
		t.Fatal(models, err)
	}
}

func TestLegacyPinIsIdempotent(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	if _, err := a.store.Exec("DELETE FROM task_environments WHERE task_id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.store.pinLegacyEnvironment(a.config.get()); err != nil {
		t.Fatal(err)
	}
	if err := a.store.pinLegacyEnvironment(a.config.get()); err != nil {
		t.Fatal(err)
	}
	saved, err := a.store.task(task.ID)
	if err != nil || saved.Environment == nil || saved.Environment.ID != task.Environment.ID {
		t.Fatal(saved, err)
	}
}
