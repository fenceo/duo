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

func TestModelCatalogNestedFormats(t *testing.T) {
	models, err := parseModels([]byte(`{"data":{"models":[{"id":"nested","name":"Nested","visibility":"","reasoning_levels":["low","bad"]},{"slug":"hidden","visibility":"hide"}]}}`))
	if err != nil || len(models) != 1 || models[0].ID != "nested" || models[0].Name != "Nested" || len(models[0].ReasoningLevels) != 1 || models[0].ReasoningLevels[0] != "low" {
		t.Fatalf("unexpected nested catalog: %#v (%v)", models, err)
	}
}

func TestConfiguredModelsMergeAndNormalize(t *testing.T) {
	models := mergeConfiguredModels(
		[]ModelOption{{ID: "gpt", Name: "GPT"}},
		[]ModelOption{{ID: " gpt ", Name: "override"}, {ID: "custom", Name: "", ReasoningLevels: []string{"low", "invalid", "low"}, DefaultReasoning: "bad"}},
	)
	if len(models) != 2 || models[0].ID != "gpt" || models[1].ID != "custom" || models[1].Name != "custom" {
		t.Fatalf("unexpected merged models: %#v", models)
	}
	if len(models[1].ReasoningLevels) != 1 || models[1].ReasoningLevels[0] != "low" || models[1].DefaultReasoning != "" {
		t.Fatalf("unexpected normalized model: %#v", models[1])
	}
}

func TestWSLProbeVariantsUseConfiguredThenDefaultUser(t *testing.T) {
	variants := environmentProbeVariants(Environment{Type: "wsl", Distro: "Ubuntu-22.04", User: "dev"})
	if len(variants) != 2 || variants[0].User != "dev" || variants[1].User != "" {
		t.Fatalf("unexpected WSL probe variants: %#v", variants)
	}
	unchanged := environmentProbeVariants(Environment{Type: "ssh", Host: "host.test", User: "dev"})
	if len(unchanged) != 1 || unchanged[0].User != "dev" {
		t.Fatalf("non-WSL environment should not fall back: %#v", unchanged)
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
