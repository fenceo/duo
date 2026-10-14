package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClaudeRoleSettingsUseIndependentAliases(t *testing.T) {
	r := EngineSetupRequest{Engine: "claude", APIKey: "synthetic-key", BaseURL: "https://relay.example", Model: "main-model", AuthMode: "auth-token", ClaudeRoles: &ClaudeRoleModels{Mode: "custom", Opus: "reasoner", Sonnet: "coder", Haiku: "fast", Fable: "long", Subagent: "worker"}}
	var doc struct{ Env map[string]string }
	if json.Unmarshal(claudeAPISettings(r), &doc) != nil {
		t.Fatal("invalid settings")
	}
	want := map[string]string{"ANTHROPIC_DEFAULT_OPUS_MODEL": "reasoner", "ANTHROPIC_DEFAULT_SONNET_MODEL": "coder", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "fast", "ANTHROPIC_DEFAULT_FABLE_MODEL": "long", "CLAUDE_CODE_SUBAGENT_MODEL": "worker", "ANTHROPIC_MODEL": "main-model", "ANTHROPIC_API_KEY": "", "ANTHROPIC_SMALL_FAST_MODEL": ""}
	for k, v := range want {
		if doc.Env[k] != v {
			t.Errorf("role %s incorrect", k)
		}
	}
	for _, mode := range []string{"uniform", "native"} {
		r.ClaudeRoles.Mode = mode
		json.Unmarshal(claudeAPISettings(r), &doc)
		for _, k := range []string{"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL"} {
			v := ""
			if mode == "uniform" {
				v = r.Model
			}
			if doc.Env[k] != v {
				t.Fatal("role mode not applied", mode, k)
			}
		}
	}
	for _, bad := range []*ClaudeRoleModels{{Mode: "unknown"}, {Mode: "custom", Opus: "bad\nmodel"}, {Mode: "native", Haiku: strings.Repeat("x", 121)}} {
		if validateClaudeRoles("claude", bad) == nil {
			t.Fatal("invalid roles accepted")
		}
	}
	if validateClaudeRoles("mimo", &ClaudeRoleModels{Mode: "uniform"}) == nil {
		t.Fatal("Claude roles reached another engine")
	}
}

func claudeScopeFixture(t *testing.T) (*App, Environment, string) {
	t.Helper()
	a := fixture(t, &fakeRunner{})
	dir := t.TempDir()
	c := a.config.get()
	c.Environments = []Environment{{ID: "scope", Name: "Synthetic", Type: "windows", Claude: "claude.exe", Workspaces: []string{dir}}}
	c.DefaultEnvironment = "scope"
	if err := a.config.save(c); err != nil {
		t.Fatal(err)
	}
	env, _ := a.config.get().environment("scope")
	for name, body := range map[string]string{"settings.json": `{"env":{"ANTHROPIC_AUTH_TOKEN":"synthetic-key","ANTHROPIC_BASE_URL":"https://relay.example","ANTHROPIC_MODEL":"native-model"},"permissions":{"deny":["Read(private)"]},"hooks":{"keep":[]}}`, ".credentials.json": `{"claudeAiOauth":{"accessToken":"synthetic-native-login"}}`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a.engineSetup.claudeAPIDirectory = func(context.Context, Environment) (string, error) { return dir, nil }
	a.engineSetup.harnessRead = func(_ context.Context, _ Environment, root, kind string) (map[string][]byte, error) {
		if root == "" {
			root = dir
		}
		return readLocalAccountFiles(root, kind)
	}
	a.engineSetup.harnessWrite = func(_ context.Context, _ Environment, root, kind string, old, next map[string][]byte) error {
		if root == "" {
			root = dir
		}
		return writeLocalAccountFiles(root, kind, old, next)
	}
	a.engineSetup.harnessDiscover = func(_ context.Context, _ Environment, v HarnessAPIRequest) HarnessDiscovery {
		if v.APIKey != "synthetic-key" {
			t.Fatal("wrong synthetic key")
		}
		return HarnessDiscovery{Status: "ready", Models: []ModelOption{{ID: "main-model"}, {ID: "fast-model"}}}
	}
	return a, env, dir
}

func TestClaudeDuoScopePreservesNativeLoginAndUpdatesExistingTasks(t *testing.T) {
	a, env, dir := claudeScopeFixture(t)
	native, _ := readLocalAccountFiles(dir, "claude")
	a.store.useSyncedNativeAccount(env.ID, "claude", "native-source")
	task := Task{Engine: "claude", Environment: &env, Session: "existing-session", Workspace: dir, Model: "task-model"}
	do := toolsClient(t, a)
	route := "/api/environments/scope/engines/claude/api"
	var view EngineAPIView
	json.Unmarshal(do(route, "GET", nil, 200), &view)
	v := EngineAPIRequest{Target: view.Target, Engine: "claude", APIScope: "duo", APIProtocol: "anthropic-messages", AuthMode: "auth-token", BaseURL: view.BaseURL, Model: "main-model", ClaudeRoles: &ClaudeRoleModels{Mode: "custom", Opus: "reasoner", Sonnet: "coder", Haiku: "fast-model"}}
	raw := do(route, "PUT", v, 200)
	if strings.Contains(string(raw), "synthetic-key") {
		t.Fatal("key exposed")
	}
	json.Unmarshal(raw, &view)
	if view.APIScope != "duo" || view.ClaudeConfigPath != filepath.Join(dir, "duo-api.json") || view.ClaudeRoles.Haiku != "fast-model" {
		t.Fatal("Duo scope not returned")
	}
	after, _ := readLocalAccountFiles(dir, "claude")
	if !reflect.DeepEqual(native, after) || a.store.currentEnvironmentAccount(env.ID, "claude") != "native-source" {
		t.Fatal("Duo save changed native login or settings")
	}
	vars := a.activeEngineEnvironment(task)
	if vars["DUO_CLAUDE_SETTINGS"] != view.ClaudeConfigPath || vars["CLAUDE_CONFIG_DIR"] != "" {
		t.Fatal("existing task did not adopt override or history was relocated")
	}
	cmd, err := engineCommand(Config{Claude: "claude.exe", EngineEnv: vars}, task)
	if err != nil || !strings.Contains(strings.Join(cmd.Args, "|"), "--settings|"+view.ClaudeConfigPath) || !strings.Contains(strings.Join(cmd.Args, "|"), "--resume|existing-session") || strings.Contains(strings.Join(cmd.Args, "|"), "synthetic-key") {
		t.Fatal("scope path/session/secret transport incorrect", err)
	}
	t.Setenv("ANTHROPIC_MODEL", "ambient-old-model")
	list, err := modelsForEngine(context.Background(), env, "claude", vars)
	if err != nil || list.DefaultModel != "main-model" || list.APITarget == "" {
		t.Fatal("Duo catalog did not read override", err)
	}
	for _, m := range list.Models {
		if m.ID == "ambient-old-model" || m.ID == "native-model" {
			t.Fatal("native account leaked into override catalog")
		}
	}
	active, _ := a.config.get().environment(env.ID)
	snapshotTask := task
	snapshotTask.Environment = &active
	v.Target = view.Target
	v.APIScope = "environment"
	view = EngineAPIView{}
	json.Unmarshal(do(route, "PUT", v, 200), &view)
	if view.APIScope != "environment" || view.ClaudeConfigPath != "" {
		t.Fatal("native scope did not clear override pointer")
	}
	if got := a.activeEngineEnvironment(snapshotTask); got["DUO_CLAUDE_SETTINGS"] != "" || claudeScopedSettingsPath(active, got) != "" {
		t.Fatal("old task restored disabled override")
	}
	if _, err := os.Stat(filepath.Join(dir, "duo-api.json")); err != nil {
		t.Fatal("scope switch deleted saved override")
	}
}

func TestClaudeDuoScopeRollbackRestoresOnlyItsFile(t *testing.T) {
	a, env, dir := claudeScopeFixture(t)
	native, _ := readLocalAccountFiles(dir, "claude")
	do := toolsClient(t, a)
	v := EngineAPIRequest{Target: a.engineAPITarget(env, "claude"), Engine: "claude", APIScope: "duo", APIProtocol: "anthropic-messages", AuthMode: "auth-token", BaseURL: "https://relay.example", Model: "main-model", APIKey: "synthetic-key"}
	a.config.path = filepath.Join(t.TempDir(), "missing-parent", "config.json")
	do("/api/environments/scope/engines/claude/api", "PUT", v, 500)
	after, _ := readLocalAccountFiles(dir, "claude")
	if !reflect.DeepEqual(native, after) {
		t.Fatal("rollback changed native config")
	}
	if _, err := os.Stat(filepath.Join(dir, "duo-api.json")); !os.IsNotExist(err) {
		t.Fatal("failed pointer save left active override")
	}
	current, _ := a.config.get().environment(env.ID)
	if current.ClaudeAPIConfig != "" {
		t.Fatal("failed pointer save changed environment")
	}
}

func TestClaudeScopedProbeRejectsChangedRouteBeforeCallingCLI(t *testing.T) {
	a, env, dir := claudeScopeFixture(t)
	c := a.config.get()
	c.Environments[0].ClaudeAPIConfig = filepath.Join(dir, "duo-api.json")
	a.config.save(c)
	data := claudeAPISettings(EngineSetupRequest{Engine: "claude", APIKey: "synthetic-key", BaseURL: "https://relay.example", Model: "main-model"})
	os.WriteFile(c.Environments[0].ClaudeAPIConfig, data, 0600)
	do := toolsClient(t, a)
	body := ModelProbeRequest{Engine: "claude", Workspace: env.Workspaces[0], Models: []string{"main-model"}, ExpectedAPITarget: "stale"}
	raw := do("/api/environments/scope/models/test", "POST", body, 409)
	if !strings.Contains(string(raw), "Duo Claude") || strings.Contains(string(raw), "synthetic-key") {
		t.Fatal("stale target error unclear or leaked key")
	}
	c.Environments[0].ClaudeAPIConfig = ""
	if err := a.config.save(c); err != nil {
		t.Fatal(err)
	}
	raw = do("/api/environments/scope/models/test", "POST", body, 409)
	if !strings.Contains(string(raw), "已停用") {
		t.Fatal("disabled scope reused a stale API probe confirmation")
	}
}
