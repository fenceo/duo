package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Official CLI, isolated home and loopback-only synthetic model/tool outputs.
// No credentials or requests to any real inference service are used.
func TestMimoACPNativeLocalProvider(t *testing.T) {
	if os.Getenv("DUO_MIMO_NATIVE_LOCAL_PROVIDER") != "1" {
		t.Skip("explicit isolated native regression only")
	}
	binary := os.Getenv("DUO_MIMO_NATIVE_BINARY")
	if binary == "" {
		t.Fatal("provide exact official binary")
	}
	home, workspace := t.TempDir(), t.TempDir()
	path := filepath.Join(workspace, "fixture.txt")
	if err := os.WriteFile(path, []byte("synthetic tool result"), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	requests := map[string]int{}
	sawReasoning := false
	sawHistory := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":[{"id":"mimo-v2.5"},{"id":"mimo-v2.5-pro"},{"id":"bad-model"}]}`))
			return
		}
		var body struct {
			Model    string
			Messages []struct {
				Role             string
				Content          any
				ReasoningContent string `json:"reasoning_content"`
			}
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid model request")
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		requests[body.Model]++
		if body.Model == "mimo-v2.5-pro" {
			raw, _ := json.Marshal(body.Messages)
			sawHistory = sawHistory || strings.Contains(string(raw), "synthetic turn mimo-v2.5\"")
		}
		mu.Unlock()
		if body.Model == "bad-model" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			w.Write([]byte(`{"error":{"message":"synthetic-secret must not be projected","type":"authentication_error"}}`))
			return
		}
		toolReply := false
		for _, message := range body.Messages {
			toolReply = toolReply || message.Role == "tool"
		}
		if toolReply {
			for _, m := range body.Messages {
				if m.Role == "assistant" && m.ReasoningContent == "planning" {
					mu.Lock()
					sawReasoning = true
					mu.Unlock()
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := func(delta any, finish any) {
			raw, _ := json.Marshal(map[string]any{"id": "fixture-completion", "object": "chat.completion.chunk", "created": 1, "model": body.Model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\n", raw)
			w.(http.Flusher).Flush()
		}
		chunk(map[string]string{"role": "assistant"}, nil)
		if body.Model == "mimo-v2.5-pro" && !toolReply {
			chunk(map[string]string{"reasoning_content": "plan"}, nil)
			chunk(map[string]string{"reasoning_content": "ning"}, nil)
			args, _ := json.Marshal(map[string]string{"file_path": path})
			chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "fixture-read", "type": "function", "function": map[string]string{"name": "read", "arguments": string(args)}}}}, nil)
			chunk(map[string]any{}, "tool_calls")
		} else {
			chunk(map[string]string{"content": "o"}, nil)
			chunk(map[string]string{"content": "k"}, nil)
			chunk(map[string]any{}, "stop")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	profile, err := mergeEngineAPI(map[string][]byte{}, "mimo-api", EngineAPIRequest{Engine: "mimo", Model: "mimo-v2.5", BaseURL: server.URL + "/v1", APIKey: "synthetic-key", APIProtocol: "openai-chat", Models: []ModelOption{{ID: "mimo-v2.5-pro"}, {ID: "bad-model"}}})
	if err != nil {
		t.Fatal(err)
	}
	variables := map[string]string{"MIMOCODE_HOME": home, "HOME": home, "USERPROFILE": home, "MIMOCODE_CONFIG_CONTENT": string(profile["duo-api.json"]), "MIMOCODE_CONFIG": "", "MIMOCODE_DISABLE_DEFAULT_PLUGINS": "1", "MIMOCODE_DISABLE_MODELS_FETCH": "1", "MIMO_API_KEY": "", "OPENAI_API_KEY": "", "OPENAI_BASE_URL": "", "ANTHROPIC_API_KEY": "", "ANTHROPIC_AUTH_TOKEN": "", "CLAUDE_CODE_OAUTH_TOKEN": ""}
	c := Config{Mimo: binary, EngineEnv: variables}
	task := Task{ID: "native-loopback", Engine: "mimo", Workspace: workspace, Mode: &WorkMode{Permission: "full"}}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	for _, model := range []string{"mimo-v2.5", "mimo-v2.5-pro", "new-model", "bad-model"} {
		task.Model = "duo-mimo/" + model
		var events []string
		session, result, err := runMimoACP(ctx, c, task, "synthetic turn "+model, func(kind, text string) { events = append(events, kind+":"+text) })
		if session == "" || task.Session != "" && task.Session != session {
			t.Fatal("durable session changed", session)
		}
		task.Session = session
		if model == "bad-model" {
			if err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatal("native error not projected safely", err)
			}
			continue
		}
		if err != nil || result != "ok" {
			t.Fatalf("%s native short reply lost: %q %v", model, result, err)
		}
		if model == "mimo-v2.5-pro" && (!strings.Contains(strings.Join(events, "|"), "thought:plan") || !strings.Contains(strings.Join(events, "|"), "fixture.txt")) {
			t.Fatal("native thought/tool stream missing", events)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if requests["mimo-v2.5"] == 0 || requests["mimo-v2.5-pro"] < 2 || requests["new-model"] == 0 || requests["bad-model"] == 0 || !sawReasoning || !sawHistory {
		t.Fatal("model switching or reasoning/tool/history continuation mismatch", requests, sawReasoning, sawHistory)
	}
}

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
	defer finishNativeACP(w)
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
	if err = finishNativeACP(w); err != nil {
		t.Fatal("native process exit could not be confirmed", err)
	}
	w, err = startMimo(ctx, c, task, variables)
	if err != nil {
		t.Fatal("native restart failed", err)
	}
	defer finishNativeACP(w)
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
