package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Re-exec the test binary, not an installed CLI. This fixture rejects every
// request other than the metadata handshake and paginated model/list.
func init() {
	if os.Getenv("DUO_TEST_MODEL_CATALOG") != "1" || len(os.Args) < 2 || os.Args[len(os.Args)-1] != "app-server" {
		return
	}
	decoder, encoder := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	fail := func() { os.Exit(31) }
	read := func(method string) codexRPC {
		var msg codexRPC
		if decoder.Decode(&msg) != nil || msg.Method != method {
			fail()
		}
		return msg
	}
	reply := func(msg codexRPC, value any) { _ = encoder.Encode(map[string]any{"id": msg.ID, "result": value}) }
	initial := read("initialize")
	if os.Getenv("DUO_TEST_MODEL_SCENARIO") == "hang" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	reply(initial, map[string]any{"userAgent": "synthetic"})
	read("initialized")
	first := read("model/list")
	var params map[string]any
	if json.Unmarshal(first.Params, &params) != nil || params["includeHidden"] != false || params["cursor"] != nil {
		fail()
	}
	scenario := os.Getenv("DUO_TEST_MODEL_SCENARIO")
	switch scenario {
	case "unsupported":
		_ = encoder.Encode(map[string]any{"id": first.ID, "error": map[string]any{"code": -32601, "message": "secret-token-must-not-leak"}})
	case "interaction":
		_ = encoder.Encode(map[string]any{"id": "unexpected", "method": "item/tool/requestUserInput", "params": map[string]any{}})
	case "malformed":
		fmt.Fprintln(os.Stdout, "not-json")
	case "empty":
		reply(first, map[string]any{"data": []any{}, "nextCursor": nil})
	default:
		profile := filepath.Base(os.Getenv("CODEX_HOME"))
		reply(first, map[string]any{"data": []any{
			map[string]any{"id": "row-id", "model": "model-" + profile, "displayName": "Selected account", "isDefault": true, "supportedReasoningEfforts": []any{map[string]string{"reasoningEffort": "medium"}, map[string]string{"reasoningEffort": "invalid"}}, "defaultReasoningEffort": "medium"},
			map[string]any{"id": "hidden", "hidden": true},
		}, "nextCursor": "page-2"})
		second := read("model/list")
		if json.Unmarshal(second.Params, &params) != nil || params["cursor"] != "page-2" {
			fail()
		}
		reply(second, map[string]any{"data": []any{map[string]any{"id": "second", "displayName": "Second"}, map[string]any{"id": "row-id", "model": "model-" + profile}}, "nextCursor": nil})
	}
	if scenario == "pages" || scenario == "empty" || scenario == "configured" {
		config := read("config/read")
		var configParams map[string]any
		if json.Unmarshal(config.Params, &configParams) != nil || configParams["includeLayers"] != false {
			fail()
		}
		model := ""
		if scenario == "configured" {
			model = "custom-active-model"
		}
		reply(config, map[string]any{"config": map[string]any{"model": model, "api_key": "secret-must-never-leak"}})
	}
	var extra codexRPC
	if err := decoder.Decode(&extra); err != io.EOF {
		fail()
	}
	os.Exit(0)
}

func modelFixtureEnvironment(t *testing.T) (Environment, map[string]string) {
	t.Helper()
	t.Setenv("DUO_TEST_MODEL_CATALOG", "1")
	t.Setenv("DUO_TEST_MODEL_SCENARIO", "pages")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Environment{ID: "fixture", Type: "windows", Codex: binary, Workspaces: []string{t.TempDir()}}, map[string]string{"CODEX_HOME": filepath.Join(t.TempDir(), "selected-profile")}
}

func TestNativeModelCatalogMetadataOnlyAndPagination(t *testing.T) {
	env, selected := modelFixtureEnvironment(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list, err := nativeCodexCatalog(ctx, env, selected)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Models) != 2 || list.Models[0].ID != "model-selected-profile" || list.Models[1].ID != "second" || list.DefaultModel != "model-selected-profile" {
		t.Fatalf("wrong native model response: %#v", list)
	}
	if list.Models[0].Origin != "native" || len(list.Models[0].ReasoningLevels) != 1 || list.Models[0].ReasoningLevels[0] != "medium" {
		t.Fatal(list.Models[0])
	}
}

func TestNativeModelCatalogRejectsProtocolErrors(t *testing.T) {
	env, selected := modelFixtureEnvironment(t)
	for _, scenario := range []string{"unsupported", "interaction", "malformed"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("DUO_TEST_MODEL_SCENARIO", scenario)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := nativeCodexCatalog(ctx, env, selected)
			if err == nil || strings.Contains(err.Error(), "secret-token") {
				t.Fatalf("unsafe error: %v", err)
			}
		})
	}
}

func TestNativeModelCatalogIncludesCurrentCustomModelWithoutSecrets(t *testing.T) {
	env, selected := modelFixtureEnvironment(t)
	t.Setenv("DUO_TEST_MODEL_SCENARIO", "configured")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list, err := nativeCodexCatalog(ctx, env, selected)
	if err != nil || list.DefaultModel != "custom-active-model" || len(list.Models) != 3 || list.Models[2].Origin != "configured" {
		t.Fatalf("%#v %v", list, err)
	}
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), "secret-must") || strings.Contains(string(raw), "api_key") {
		t.Fatal("config secrets leaked")
	}
}

func TestNativeModelCatalogCancellationIsBounded(t *testing.T) {
	env, selected := modelFixtureEnvironment(t)
	t.Setenv("DUO_TEST_MODEL_SCENARIO", "hang")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := nativeCodexCatalog(ctx, env, selected)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 4*time.Second {
		t.Fatalf("unbounded cancellation: %v after %s", err, time.Since(started))
	}
}

func TestModelCatalogFallbackKeepsExplicitModelAndEngineBoundary(t *testing.T) {
	env, selected := modelFixtureEnvironment(t)
	t.Setenv("DUO_TEST_MODEL_SCENARIO", "empty")
	if err := os.MkdirAll(selected["CODEX_HOME"], 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(selected["CODEX_HOME"], "config.toml"), []byte("model = 'gateway-model'\n[profiles.other]\nmodel = 'wrong-account'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env.Models = []ModelOption{{ID: "codex-only", Engine: "codex"}, {ID: "claude-only", Engine: "claude"}, {ID: "legacy-shared"}}
	list, err := modelsForEnvironment(context.Background(), env, selected)
	if err != nil || len(list.Models) != 3 || list.DefaultModel != "gateway-model" || list.Status != "fallback" {
		t.Fatalf("%#v %v", list, err)
	}
	for _, model := range list.Models {
		if model.Origin != "configured" || model.ID == "claude-only" || model.ID == "wrong-account" {
			t.Fatal(model)
		}
	}
}

func TestModelCatalogCommandsKeepTargetAndAccount(t *testing.T) {
	for _, env := range []Environment{
		{Type: "wsl", Distro: "TargetDistro", User: "selected-user", Codex: "/opt/codex", Workspaces: []string{"/work one"}},
		{Type: "ssh", Host: "target.test", Port: 2222, User: "selected-user", Codex: "/opt/codex", Workspaces: []string{"/work one"}},
	} {
		cmd, _ := codexCatalogCommand(env, map[string]string{"CODEX_HOME": "/profiles/selected"})
		args := strings.Join(cmd.Args, "|")
		if !strings.Contains(args, "selected-user") || !strings.Contains(args, "CODEX_HOME=/profiles/selected") || !strings.Contains(args, "/work one") || !strings.Contains(args, "app-server") {
			t.Fatal(args)
		}
		if env.Type == "wsl" && (!strings.Contains(args, "--exec") || len(cmd.Env) > 2) {
			t.Fatalf("WSL leaked host env: %#v", cmd)
		}
	}
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if roots := codexHomes(); len(roots) != 1 || roots[0] != home {
		t.Fatalf("cross-account fallback: %v", roots)
	}
	models := mergeConfiguredModels(nil, []ModelOption{{ID: "same", Engine: "codex"}, {ID: "same", Engine: "claude"}})
	if len(models) != 2 {
		t.Fatalf("engine-specific custom names collapsed: %#v", models)
	}
}

func TestModelCatalogClaudeAndHarnessOnlyConfiguredModels(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv("ANTHROPIC_MODEL", "")
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{"model":"account-model","availableModels":["account-second"],"env":{"ANTHROPIC_API_KEY":"synthetic-secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	env := Environment{Type: "windows", Workspaces: []string{workspace}, HarnessModel: "gateway-harness", Models: []ModelOption{{ID: "only-codex", Engine: "codex"}, {ID: "only-claude", Engine: "claude"}}}
	list, err := modelsForEngine(context.Background(), env, "claude", map[string]string{"CLAUDE_CONFIG_DIR": home})
	if err != nil || len(list.Models) != 3 || list.DefaultModel != "account-model" {
		t.Fatalf("%#v %v", list, err)
	}
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), "synthetic-secret") || strings.Contains(string(raw), "only-codex") {
		t.Fatal("wrong target/secret in model output")
	}
	list, err = modelsForEngine(context.Background(), env, "deepseek-harness")
	if err != nil || len(list.Models) != 1 || list.Models[0].ID != "gateway-harness" || list.Models[0].Origin != "configured" {
		t.Fatalf("%#v %v", list, err)
	}
}

func TestModelCatalogAdmissionCancelsAndBlocksMaintenance(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	s := &Server{app: a}
	s.modelProbeMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s.admitModelCatalog(ctx) {
		t.Fatal("cancelled catalog admitted")
	}
	s.modelProbeMu.Unlock()
	a.updating.Store(true)
	if s.admitModelCatalog(context.Background()) {
		t.Fatal("catalog admitted during maintenance")
	}
	a.updating.Store(false)
	if !s.admitModelCatalog(context.Background()) {
		t.Fatal("idle catalog denied")
	}
	s.modelProbeMu.Unlock()
}

func TestNativeModelCatalogRealWSLMetadata(t *testing.T) {
	if os.Getenv("DUO_TEST_REAL_WSL_MODEL_METADATA") != "1" {
		t.Skip("explicit read-only metadata opt-in required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	env := Environment{Type: "wsl", Distro: "Ubuntu-22.04", User: "libao", Codex: "/home/libao/.local/bin/codex", Workspaces: []string{"/home/libao/work"}}
	list, err := nativeCodexCatalog(ctx, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, model := range list.Models {
		ids = append(ids, model.ID)
	}
	t.Logf("metadata only: %d models, default=%s, ids=%v", len(ids), list.DefaultModel, ids)
}
