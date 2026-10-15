package main

import (
	"slices"
	"testing"
)

func TestBuiltinEngineDefinitionsAndInstallPlan(t *testing.T) {
	if len(builtinEngineDefinitions()) != 6 {
		t.Fatalf("unexpected engine count: %d", len(builtinEngineDefinitions()))
	}
	e, ok := builtinEngine("codex")
	if !ok || !e.Runnable || e.Transport != "codex_app_server" {
		t.Fatalf("unexpected Codex definition: %+v", e)
	}
	harness, ok := builtinEngine("deepseek-harness")
	if !ok || !harness.Runnable || harness.Transport != "acp" || !slices.Contains(harness.Capabilities, "approval") {
		t.Fatalf("Harness must declare its implemented ACP approval: %+v", harness)
	}
	opencode, ok := builtinEngine("opencode")
	if !ok || !opencode.Runnable || opencode.Transport != "acp" || !slices.Contains(opencode.Capabilities, "mcp") || !slices.Contains(opencode.CredentialKinds, "native") {
		t.Fatalf("OpenCode must declare its implemented ACP MCP support: %+v", opencode)
	}
	if !slices.Contains(opencode.Targets, "windows") || !slices.Contains(opencode.Targets, "wsl") || !slices.Contains(opencode.Targets, "ssh") {
		t.Fatalf("OpenCode must support every execution target: %+v", opencode.Targets)
	}
	if env := (Environment{Type: "wsl"}); runtimeConfig(Config{}, env).OpenCode != "" {
		t.Fatal("OpenCode must not invent an executable for an unconfigured environment")
	}
	if !validEngine("opencode") || validEngineReasoning("opencode", "high") || !validEngineReasoning("opencode", "") {
		t.Fatal("OpenCode engine/reasoning admission is inconsistent")
	}
	env := Environment{ID: "wsl", Name: "WSL", Type: "wsl", Codex: "codex", Workspaces: []string{"/work"}}
	plan, err := engineInstallPlan(env, "codex")
	if err != nil || plan.Executable != "codex" || len(plan.Steps) == 0 {
		t.Fatalf("unexpected install plan: %+v, %v", plan, err)
	}
	if _, err = engineInstallPlan(env, "missing"); err == nil {
		t.Fatal("unknown engine accepted")
	}
}

func TestEngineProfileValidationAndEnvironment(t *testing.T) {
	envs := []Environment{{ID: "wsl", Name: "WSL", Type: "wsl"}}
	p := EngineCredentialProfile{ID: "codex-main", Name: "主账号", Engine: "codex", EnvironmentID: "wsl", Kind: "codex_home", Reference: "/home/libao/.codex-main"}
	if err := validateEngineProfile(p, envs); err != nil {
		t.Fatal(err)
	}
	if got := engineProfileEnv(p)["CODEX_HOME"]; got != p.Reference {
		t.Fatalf("CODEX_HOME = %q", got)
	}
	p.Reference = "C:\\\\bad"
	if err := validateEngineProfile(p, envs); err == nil {
		t.Fatal("Windows path accepted for WSL profile")
	}
}
