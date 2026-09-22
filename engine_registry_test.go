package main

import "testing"

func TestBuiltinEngineDefinitionsAndInstallPlan(t *testing.T) {
	if len(builtinEngineDefinitions()) != 5 {
		t.Fatalf("unexpected engine count: %d", len(builtinEngineDefinitions()))
	}
	e, ok := builtinEngine("codex")
	if !ok || !e.Runnable || e.Transport != "codex_app_server" {
		t.Fatalf("unexpected Codex definition: %+v", e)
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
