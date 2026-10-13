package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeAndMimoCustomAPIConfigKeepsNativeDataAndAliases(t *testing.T) {
	for _, test := range []struct{ engine, protocol, file, sdk string }{{"claude", "anthropic-messages", "settings.json", ""}, {"mimo", "openai-chat", "mimocode.json", "@ai-sdk/openai-compatible"}, {"mimo", "anthropic-messages", "mimocode.json", "@ai-sdk/anthropic"}} {
		t.Run(test.engine+test.protocol, func(t *testing.T) {
			a := fixture(t, &fakeRunner{})
			config := a.config.get()
			config.Environments[0].Type = "windows"
			config.Environments[0].Distro = ""
			config.Environments[0].Workspaces = []string{t.TempDir()}
			if err := a.config.save(config); err != nil {
				t.Fatal(err)
			}
			env := a.config.get().Environments[0]
			saved := map[string][]byte{}
			directory := t.TempDir()
			a.engineSetup.accountHome = func(_ context.Context, target Environment, _, _, engine string, files map[string][]byte) (string, error) {
				if target.ID != env.ID || engine != test.engine {
					t.Fatal("wrong configuration target")
				}
				saved = files
				return directory, nil
			}
			req := EngineSetupRequest{ID: "synthetic-config", Action: "account", Engine: test.engine, EnvironmentID: env.ID, Name: "Custom service", Login: "apiKey", APIKey: "synthetic-private-key", BaseURL: "https://custom.example/provider/v1", Model: "org/custom-model", APIProtocol: test.protocol}
			job, err := a.startEngineSetup(req)
			if err != nil {
				t.Fatal(err)
			}
			waitUntil(t, func() bool {
				got, _ := a.engineSetup.snapshot(job.ID)
				return got.State == "done" || got.State == "failed"
			})
			job, _ = a.engineSetup.snapshot(job.ID)
			if job.State != "done" || strings.Contains(job.Message, req.APIKey) {
				t.Fatal("API configuration did not complete safely", job.State, job.Message)
			}
			var doc map[string]any
			if len(saved) != 1 || json.Unmarshal(saved[test.file], &doc) != nil {
				t.Fatal("unexpected native config files")
			}
			if test.engine == "claude" {
				env := doc["env"].(map[string]any)
				for _, key := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL"} {
					if env[key] != req.Model {
						t.Fatal("custom model alias not pinned", key)
					}
				}
				if env["ANTHROPIC_AUTH_TOKEN"] != req.APIKey || env["ANTHROPIC_API_KEY"] != "" {
					t.Fatal("wrong gateway authentication")
				}
			} else {
				provider := doc["provider"].(map[string]any)["duo-mimo"].(map[string]any)
				options := provider["options"].(map[string]any)
				if provider["npm"] != test.sdk || options["baseURL"] != req.BaseURL || options["apiKey"] != req.APIKey || doc["model"] != "duo-mimo/"+req.Model {
					t.Fatal("native MiMo routing damaged")
				}
				profiles := a.store.engineProfiles()
				runtime := engineProfileEnv(profiles[0])
				if runtime["MIMOCODE_CONFIG"] == "" || runtime["MIMOCODE_HOME"] != "" {
					t.Fatal("API switching moved native conversation storage")
				}
			}
			if a.store.activeEngineProfile(env.ID, test.engine) != "" {
				t.Fatal("creating config silently activated it")
			}
		})
	}
}

func TestEngineAPIModelsUsesTargetAndActualAuthWithoutSavingKey(t *testing.T) {
	for _, engine := range []string{"claude", "mimo"} {
		t.Run(engine, func(t *testing.T) {
			a := fixture(t, &fakeRunner{})
			c := a.config.get()
			c.Environments = []Environment{{ID: "local", Name: "Synthetic local", Type: "windows", Workspaces: []string{t.TempDir()}}}
			c.DefaultEnvironment = "local"
			if err := a.config.save(c); err != nil {
				t.Fatal(err)
			}
			env := a.config.get().Environments[0]
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/v1/models" {
					t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
				}
				if engine == "claude" {
					if r.Header.Get("Authorization") != "Bearer synthetic-key" || r.Header.Get("X-Api-Key") != "" {
						t.Error("gateway authentication did not match Claude AUTH_TOKEN")
					}
				} else if r.Header.Get("Authorization") != "Bearer synthetic-key" {
					t.Error("wrong OpenAI authentication")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"data":[{"id":"custom-model"}]}`))
			}))
			defer server.Close()
			base := server.URL
			if engine == "mimo" {
				base += "/v1"
			}
			protocol := "anthropic-messages"
			if engine == "mimo" {
				protocol = "openai-chat"
			}
			request := toolsClient(t, a)
			path := "/api/environments/" + env.ID + "/engine-api-models"
			req := map[string]any{"engine": engine, "api_protocol": protocol, "base_url": base, "api_key": "synthetic-key", "target": env}
			body := request(path, "POST", req, 200)
			if !strings.Contains(string(body), "custom-model") || strings.Contains(string(body), "synthetic-key") || calls != 1 || len(a.store.engineProfiles()) != 0 {
				t.Fatal("read-only model discovery failed or persisted/exposed a key")
			}
			env.User = "changed"
			req["target"] = env
			request(path, "POST", req, 409)
			if calls != 1 {
				t.Fatal("stale target sent credentials")
			}
		})
	}
}

func TestMimoInstallArchiveRejectsUnexpectedPayload(t *testing.T) {
	for _, name := range []string{"mimo.exe", "../mimo.exe", "private-config.json", "nested/mimo.exe"} {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			var archive bytes.Buffer
			writer := zip.NewWriter(&archive)
			file, err := writer.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			file.Write([]byte("synthetic binary"))
			writer.Close()
			dir := t.TempDir()
			path := filepath.Join(dir, "mimo.zip")
			os.WriteFile(path, archive.Bytes(), 0600)
			binary, err := extractMimoBinary(path, dir)
			if name == "mimo.exe" {
				if err != nil || binary != filepath.Join(dir, name) {
					t.Fatal("official payload rejected", err)
				}
			} else if err == nil {
				t.Fatal("unexpected payload admitted")
			}
		})
	}
}

func TestClaudeAPIAuthModeSupportsOfficialAndGatewayKeys(t *testing.T) {
	for _, test := range []struct {
		base, mode string
		want       bool
	}{{"", "", false}, {"https://api.anthropic.com", "", false}, {"https://relay.example", "", true}, {"https://relay.example", "api-key", false}, {"https://relay.example", "auth-token", true}} {
		if got := claudeUsesToken(EngineSetupRequest{BaseURL: test.base, AuthMode: test.mode}); got != test.want {
			t.Fatal("wrong auth header selection", test.mode, test.base)
		}
	}
}
