package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMimoLiveCatalogAndAPIProbeUseSameSavedService(t *testing.T) {
	gets, posts, writes := 0, 0, 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-mimo-key" {
			t.Error("wrong credential")
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			gets++
			w.Write([]byte(`{"data":[{"id":"mimo-v2.5"},{"id":"mimo-v2.5-pro"},{"id":"mimo-v2.6-flash"},{"id":"mimo-v2.6-pro"},{"id":"new-model"},{"id":"mimo-v2.5-asr"}]}`))
			return
		}
		posts++
		if r.URL.Path != "/v1/chat/completions" {
			t.Error("wrong endpoint", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["tools"] != nil || body["stream"] != false || len(body["messages"].([]any)) != 1 {
			t.Error("probe inherited tools/history")
		}
		if body["model"] == "new-model" {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"synthetic-mimo-key must not leak"}`))
			return
		}
		if body["thinking"].(map[string]any)["type"] != "disabled" || body["max_completion_tokens"] != float64(64) {
			t.Error("unbounded reasoning probe", body)
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
	}))
	defer api.Close()
	a := fixture(t, &fakeRunner{})
	c := a.config.get()
	dir := t.TempDir()
	c.Environments = []Environment{{ID: "mimo-api", Name: "Synthetic", Type: "windows", Mimo: "must-not-run-cli.exe", MimoConfig: filepath.Join(dir, "duo-api.json"), MimoModel: "duo-mimo/mimo-v2.5", Workspaces: []string{dir}}}
	c.DefaultEnvironment = "mimo-api"
	if err := a.config.save(c); err != nil {
		t.Fatal(err)
	}
	doc := mimoAPIConfig(EngineSetupRequest{Model: "mimo-v2.5", APIProtocol: "openai-chat", BaseURL: api.URL + "/v1", APIKey: "synthetic-mimo-key"})
	a.engineSetup.harnessRead = func(_ context.Context, _ Environment, directory, kind string) (map[string][]byte, error) {
		if directory != dir || kind != "mimo-api" {
			t.Fatal("wrong config reference")
		}
		return map[string][]byte{"duo-api.json": doc}, nil
	}
	a.engineSetup.harnessWrite = func(context.Context, Environment, string, string, map[string][]byte, map[string][]byte) error {
		writes++
		return nil
	}
	request := toolsClient(t, a)
	var list ModelList
	raw := request("/api/environments/mimo-api/models?engine=mimo&refresh=1", "GET", nil, 200)
	if json.Unmarshal(raw, &list) != nil || len(list.Models) != 5 || list.APITarget == "" || list.APIEndpoint != api.URL+"/v1" || gets != 1 || posts != 0 || writes != 0 || strings.Contains(string(raw), "synthetic-mimo-key") {
		t.Fatal("live GET-only discovery failed", string(raw))
	}
	probe := ModelProbeRequest{Engine: "mimo", Workspace: dir, Models: []string{"duo-mimo/mimo-v2.5", "duo-mimo/new-model"}, ExpectedAPITarget: list.APITarget}
	raw = request("/api/environments/mimo-api/models/test", "POST", probe, 200)
	var response ModelProbeResponse
	_ = json.Unmarshal(raw, &response)
	if len(response.Results) != 2 || response.Results[0].Status != "available" || response.Results[1].Status != "unavailable" || posts != 2 || writes != 0 || strings.Contains(string(raw), "synthetic-mimo-key") {
		t.Fatal("API probe results/privacy", string(raw))
	}
	probe.ExpectedAPITarget = "stale"
	request("/api/environments/mimo-api/models/test", "POST", probe, 409)
	if posts != 2 {
		t.Fatal("stale route called a model")
	}
	probe.ExpectedAPITarget = list.APITarget
	probe.Models = []string{"xiaomi/mimo-v2.5"}
	request("/api/environments/mimo-api/models/test", "POST", probe, 400)
	if posts != 2 {
		t.Fatal("unrelated provider called a model")
	}
}

func TestMimoAPIProbeProtocolsErrorsAndCancellation(t *testing.T) {
	for _, scenario := range []string{"messages", "empty", "redirect", "quota", "rate", "invalid", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			redirected := false
			destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected = true }))
			defer destination.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch scenario {
				case "messages":
					if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "synthetic-key" {
						t.Error("messages protocol mismatch")
					}
					w.Write([]byte(`{"content":[{"type":"text","text":"OK"}]}`))
				case "empty":
					w.Write([]byte(`{"choices":[{"message":{"reasoning_content":"only thinking"}}]}`))
				case "redirect":
					http.Redirect(w, r, destination.URL, 302)
				case "quota":
					w.WriteHeader(402)
				case "rate":
					w.WriteHeader(429)
				case "invalid":
					w.Write([]byte(`{"error":"synthetic-key"}`))
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "cancel" {
				cancel()
			}
			v := EngineAPIRequest{BaseURL: server.URL + "/v1", APIKey: "synthetic-key", APIProtocol: "openai-chat"}
			if scenario == "messages" {
				v.APIProtocol = "anthropic-messages"
			}
			result := probeMimoAPI(ctx, Environment{Type: "windows"}, v, "duo-mimo/custom")
			if redirected || strings.Contains(result.Message, "synthetic-key") || result.DurationMS < 1 {
				t.Fatal(result)
			}
			if (result.Status == "available") != (scenario == "messages") {
				t.Fatal("false availability", result)
			}
		})
	}
}

func TestMimoPythonAPIProbeIsPostOnlyWithoutTools(t *testing.T) {
	python := ""
	for _, candidate := range []string{"python", "python3"} {
		if path, err := exec.LookPath(candidate); err == nil && exec.Command(path, "-c", "import sys;sys.exit(0)").Run() == nil {
			python = path
			break
		}
	}
	if python == "" {
		t.Skip("Python required")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer synthetic-key" {
			t.Error("POST/auth mismatch")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "custom" || body["tools"] != nil {
			t.Error("model/tools mismatch")
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"OK"}}]}`)
	}))
	defer server.Close()
	req, _ := json.Marshal(harnessDiscoveryHTTP{URL: server.URL, API: "openai-chat", APIKey: "synthetic-key", Method: "POST", Payload: json.RawMessage(`{"model":"custom","messages":[{"role":"user","content":"Reply with OK."}],"max_tokens":64}`), TimeoutSeconds: 25})
	cmd := exec.Command(python, "-c", harnessDiscoveryScript)
	cmd.Stdin = bytes.NewReader(req)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal("Python POST failed", err)
	}
	var result harnessDiscoveryResponse
	if json.Unmarshal(raw, &result) != nil || result.Status != 200 || !strings.Contains(string(result.Body), "OK") || strings.Contains(string(raw), "synthetic-key") {
		t.Fatal("Python response mismatch")
	}
}
