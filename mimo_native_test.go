package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// Opt-in, fresh home, fake key, loopback provider. Never sends session/prompt:
// initialize + session/new/load/config only validate the native ACP protocol.
func TestMimoACPNativeNoPrompt(t *testing.T) {
	if os.Getenv("DUO_MIMO_NATIVE_NO_PROMPT") != "1" {
		t.Skip("explicit isolated native diagnostic only")
	}
	binary := os.Getenv("DUO_MIMO_NATIVE_BINARY")
	if binary == "" {
		t.Fatal("provide the exact official binary path")
	}
	home, workspace := t.TempDir(), t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("native handshake attempted inference")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"fixture-model"}]}`))
	}))
	defer server.Close()
	profile, _ := json.Marshal(map[string]any{"model": "duo-mimo/fixture-model", "enabled_providers": []string{"duo-mimo"}, "provider": map[string]any{"duo-mimo": map[string]any{"npm": "@ai-sdk/openai-compatible", "only_configured_models": true, "models": map[string]any{"fixture-model": map[string]string{"name": "fixture-model"}}, "options": map[string]string{"baseURL": server.URL + "/v1", "apiKey": "synthetic-no-model-call"}}}})
	variables := map[string]string{"MIMOCODE_HOME": home, "HOME": home, "USERPROFILE": home, "MIMOCODE_CONFIG_CONTENT": string(profile), "MIMOCODE_CONFIG": "", "MIMOCODE_DISABLE_CLAUDE_CODE": "1", "MIMOCODE_DISABLE_DEFAULT_PLUGINS": "1", "MIMOCODE_DISABLE_MODELS_FETCH": "1", "MIMOCODE_DISABLE_AUTOUPDATE": "1", "MIMO_API_KEY": "", "OPENAI_API_KEY": "", "OPENAI_BASE_URL": "", "ANTHROPIC_API_KEY": "", "ANTHROPIC_AUTH_TOKEN": "", "CLAUDE_CODE_OAUTH_TOKEN": ""}
	c := Config{Mimo: binary}
	task := Task{ID: "native-no-prompt", Engine: "mimo", Workspace: workspace, Model: "duo-mimo/fixture-model"}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	w, err := startMimo(ctx, c, task, variables)
	if err != nil {
		t.Fatal("native initialize failed", err)
	}
	defer finishMimo(w)
	data, err := w.request(ctx, "session/new", map[string]any{"cwd": workspace, "mcpServers": []any{}})
	if err != nil {
		t.Fatal("native session/new failed", err)
	}
	var result struct{ SessionID string }
	if json.Unmarshal(data, &result) != nil || result.SessionID == "" {
		t.Fatal("native session ID missing")
	}
	if _, err = w.request(ctx, "session/set_config_option", map[string]string{"sessionId": result.SessionID, "configId": "model", "value": task.Model}); err != nil {
		t.Fatal("native model configuration failed", err)
	}
	if _, err = w.request(ctx, "session/set_mode", map[string]string{"sessionId": result.SessionID, "modeId": "plan"}); err != nil {
		t.Fatal("native mode configuration failed", err)
	}
	if err = finishMimo(w); err != nil {
		t.Fatal("native process exit could not be confirmed", err)
	}
	w, err = startMimo(ctx, c, task, variables)
	if err != nil {
		t.Fatal("native restart failed", err)
	}
	defer finishMimo(w)
	if _, err = w.request(ctx, "session/load", map[string]any{"cwd": workspace, "sessionId": result.SessionID, "mcpServers": []any{}}); err != nil {
		t.Fatal("native durable session/load failed", err)
	}
	list, err := configuredMimoCatalog(ctx, Environment{Type: "windows", Mimo: binary, Workspaces: []string{workspace}}, variables)
	if err != nil {
		t.Fatal("native model catalog failed", err)
	}
	found := false
	for _, model := range list.Models {
		found = found || model.ID == task.Model
	}
	if !found {
		t.Fatal("native model catalog missed the configured provider/model", list.Status, list.Message)
	}
	t.Log("PASS: official MiMo ACP v1 initialize, model selection, Plan mode, confirmed stop and durable resume; zero prompt submissions, fresh home, dummy key and loopback-only provider")
}
