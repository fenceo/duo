package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHarnessEnvironmentDefaultsAndRuntimeConfig(t *testing.T) {
	c := Config{DefaultEnvironment: "windows", Environments: []Environment{
		{ID: "windows", Name: "Windows", Type: "windows", Workspaces: []string{t.TempDir()}},
		{ID: "wsl", Name: "WSL", Type: "wsl", Distro: "Ubuntu", Workspaces: []string{"/home/dev/work"}},
		{ID: "ssh", Name: "SSH", Type: "ssh", Host: "example.test", Workspaces: []string{"/home/dev/work"}, Harness: "/opt/dsh", HarnessModel: "custom-model", HarnessProvider: "custom-provider"},
	}}
	if err := normalizeEnvironments(&c); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"dsh.cmd", "dsh", "/opt/dsh"} {
		e := c.Environments[i]
		if e.Harness != want {
			t.Errorf("environment %s executable = %q, want %q", e.ID, e.Harness, want)
		}
		if i < 2 && (e.HarnessModel != "deepseek-flash" || e.HarnessProvider != "deepseek-official") {
			t.Errorf("missing Harness route defaults: %#v", e)
		}
		runtime := runtimeConfig(c, e)
		if runtime.Harness != e.Harness || runtime.HarnessModel != e.HarnessModel || runtime.HarnessProvider != e.HarnessProvider {
			t.Errorf("runtime lost Harness settings for %s", e.ID)
		}
	}
	custom := c.Environments[2]
	if custom.HarnessModel != "custom-model" || custom.HarnessProvider != "custom-provider" {
		t.Fatal("normalization replaced custom route")
	}
	raw, err := json.Marshal(Config{Harness: "runtime-binary", HarnessModel: "runtime-model", HarnessProvider: "runtime-provider"})
	if err != nil || strings.Contains(string(raw), "runtime-") {
		t.Fatalf("runtime-only Harness settings serialized: %s (%v)", raw, err)
	}
}

func TestHarnessModelCatalogAndReasoning(t *testing.T) {
	list, err := modelsForEngine(context.Background(), Environment{HarnessModel: "deepseek-flash", Models: []ModelOption{
		{ID: "custom-model", Name: "Custom", DefaultReasoning: "medium"},
		{ID: "deepseek-flash", Name: "Duplicate"},
	}}, "deepseek-harness")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Models) != 2 || list.Models[0].ID != "deepseek-flash" || list.Models[1].ID != "custom-model" {
		t.Fatalf("wrong native/custom model merge: %#v", list.Models)
	}
	for _, model := range list.Models {
		if !reflect.DeepEqual(model.ReasoningLevels, []string{"off", "low", "high", "max"}) {
			t.Errorf("model has non-Harness reasoning: %#v", model)
		}
		if model.DefaultReasoning != "" {
			t.Errorf("unsupported default reasoning retained: %#v", model)
		}
	}
	for _, effort := range []string{"", "off", "low", "high", "max"} {
		if !validEngineReasoning("deepseek-harness", effort) {
			t.Errorf("supported Harness effort rejected: %q", effort)
		}
	}
	for _, effort := range []string{"none", "minimal", "medium", "xhigh", "ultra"} {
		if validEngineReasoning("deepseek-harness", effort) {
			t.Errorf("unsupported Harness effort accepted: %q", effort)
		}
	}
	if !validEngineReasoning("codex", "medium") || validEngineReasoning("codex", "off") {
		t.Fatal("Harness support changed Codex reasoning validation")
	}
}

func TestHarnessRegistryAndNativeProfile(t *testing.T) {
	engine, ok := builtinEngine("deepseek-harness")
	if !ok || !engine.Runnable || engine.Transport != "sdk_jsonrpc" {
		t.Fatalf("Harness SDK not registered: %#v", engine)
	}
	if !reflect.DeepEqual(engine.Capabilities, []string{"stream", "live_session", "cancel", "sandbox"}) {
		t.Fatalf("Harness advertises unsupported capabilities: %v", engine.Capabilities)
	}
	if !validEngineCredentialKind(engine.ID, "dsh_home") || validEngineCredentialKind(engine.ID, "env_file") {
		t.Fatal("Harness profile kinds are not native directory references")
	}
	for _, env := range []Environment{{ID: "windows", Type: "windows"}, {ID: "wsl", Type: "wsl"}, {ID: "ssh", Type: "ssh"}} {
		ref := "/home/dev/.dsh-personal"
		if env.Type == "windows" {
			ref = filepath.Join(t.TempDir(), "dsh-personal")
		}
		profile := EngineCredentialProfile{ID: "profile", Name: "Personal", Engine: engine.ID, EnvironmentID: env.ID, Kind: "dsh_home", Reference: ref}
		if err := validateEngineProfile(profile, []Environment{env}); err != nil {
			t.Errorf("valid %s profile rejected: %v", env.Type, err)
		}
		if got := engineProfileEnv(profile); len(got) != 1 || got["DSH_HOME"] != ref {
			t.Errorf("profile does not map to DSH_HOME: %v", got)
		}
		profile.Reference = "relative/path"
		if err := validateEngineProfile(profile, []Environment{env}); err == nil {
			t.Errorf("relative %s profile accepted", env.Type)
		}
	}
	if _, err := engineCommand(Config{}, Task{Engine: engine.ID}); err == nil || !strings.Contains(err.Error(), "SDK") {
		t.Fatalf("Harness fell through to generic CLI runner: %v", err)
	}
}

func TestHarnessReadModePreservesOfflinePlan(t *testing.T) {
	modes := map[string]WorkMode{}
	for _, mode := range builtinModes() {
		modes[mode.ID] = mode
	}
	mode, found := modes["harness:read"]
	if !found || !mode.Builtin || mode.Name != "只读·可联网" || mode.Permission != "read" || mode.Approval != "never" || mode.AllowNetwork == nil || !*mode.AllowNetwork {
		t.Fatalf("wrong Harness read-only mode: %#v", mode)
	}
	if !isBuiltinMode(mode.ID) || safeWorkbenchID(mode.ID) {
		t.Fatal("Harness built-in mode is missing or collides with custom IDs")
	}
	plan := modes["plan"]
	if plan.Permission != "read" || plan.Approval != "never" || plan.AllowNetwork == nil || *plan.AllowNetwork {
		t.Fatalf("Harness support changed the offline plan mode: %#v", plan)
	}
	if err := validateCatalog(&WorkCatalog{Modes: []WorkMode{mode}}); err == nil {
		t.Fatal("custom mode can replace a built-in Harness mode")
	}
}

func TestHarnessUsageUsesDisjointNativeTokenBuckets(t *testing.T) {
	raw := json.RawMessage(`{"inputTokens":100,"outputTokens":20,"cacheReadTokens":50,"cacheWriteTokens":5,"reasoningTokens":10,"totalTokens":175}`)
	want := &RunUsage{Input: 100, Output: 20, Cached: 50, CacheWrite: 5, Total: 175}
	if got := parseUsage("deepseek-harness", raw); !reflect.DeepEqual(got, want) {
		t.Fatalf("Harness usage = %#v, want %#v", got, want)
	}
	if got := parseUsage("deepseek-harness", json.RawMessage(`{"inputTokens":0,"outputTokens":2}`)); !reflect.DeepEqual(got, &RunUsage{Output: 2, Total: 2}) {
		t.Fatalf("optional cache fields not handled: %#v", got)
	}
	for _, invalid := range []string{
		`{}`, `null`, `{"input_tokens":10,"output_tokens":2}`,
		`{"inputTokens":null}`, `{"inputTokens":"10"}`, `{"inputTokens":1.5}`,
		`{"inputTokens":-1}`, `{"outputTokens":-1}`,
		`{"inputTokens":1,"cacheReadTokens":-1}`, `{"inputTokens":1,"cacheWriteTokens":null}`,
	} {
		if got := parseUsage("deepseek-harness", json.RawMessage(invalid)); got != nil {
			t.Errorf("invalid Harness usage accepted: %s => %#v", invalid, got)
		}
	}
	for _, engine := range []string{"codex", "claude"} {
		if got := parseUsage(engine, raw); got != nil {
			t.Errorf("Harness camelCase affected %s usage: %#v", engine, got)
		}
	}
	if got := parseUsage("codex", json.RawMessage(`{"input_tokens":100,"output_tokens":20,"cached_input_tokens":50}`)); !reflect.DeepEqual(got, &RunUsage{Input: 100, Output: 20, Cached: 50, Total: 120}) {
		t.Fatalf("Codex usage semantics changed: %#v", got)
	}
	if got := parseUsage("claude", json.RawMessage(`{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":50,"cache_creation_input_tokens":5}`)); !reflect.DeepEqual(got, want) {
		t.Fatalf("Claude usage semantics changed: %#v", got)
	}
}
