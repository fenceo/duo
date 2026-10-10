package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func syntheticHarnessDiscovery(_ context.Context, _ Environment, v HarnessAPIRequest) HarnessDiscovery {
	return HarnessDiscovery{Status: "ready", Models: []ModelOption{{ID: v.Model, Name: v.Model}}, Message: "接口认证通过（合成测试）"}
}

func TestHarnessDiscoveryAuthenticationAndFailures(t *testing.T) {
	for _, protocol := range []string{"openai-completions", "openai-responses", "anthropic-messages"} {
		t.Run(protocol, func(t *testing.T) {
			hits := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				if r.Method != "GET" || r.URL.Path != "/v1/models" {
					t.Error("wrong request")
				}
				if protocol == "anthropic-messages" {
					if r.Header.Get("x-api-key") != "synthetic-harness-secret" || r.Header.Get("anthropic-version") != "2023-06-01" {
						t.Error("wrong Anthropic auth")
					}
				} else if r.Header.Get("Authorization") != "Bearer synthetic-harness-secret" {
					t.Error("wrong bearer auth")
				}
				if protocol == "anthropic-messages" && r.URL.Query().Get("after_id") == "" {
					ioJSON(w, `{"data":[{"id":"fixture-a","display_name":"Fixture A"}],"has_more":true,"last_id":"fixture-a"}`)
				} else {
					ioJSON(w, `{"data":[{"id":"fixture-b"},{"id":"fixture-b"}],"has_more":false}`)
				}
			}))
			defer server.Close()
			v := syntheticHarnessAPI()
			v.API = protocol
			v.BaseURL = server.URL + "/v1"
			result := discoverHarnessModels(context.Background(), Environment{Type: "windows"}, v)
			want := 1
			if protocol == "anthropic-messages" {
				want = 2
			}
			if result.Status != "ready" || len(result.Models) != want || hits != want {
				t.Fatalf("discovery: %#v hits=%d", result, hits)
			}
		})
	}
	for _, test := range []struct {
		code         int
		body, status string
	}{
		{401, "reflected synthetic-harness-secret", "failed"}, {403, "denied", "failed"}, {429, "busy", "failed"},
		{404, "no models", "unsupported"}, {405, "no models", "unsupported"}, {500, "secret internal error", "failed"},
		{200, `{"data":[]}`, "failed"}, {200, `{"wrong":"synthetic-harness-secret"}`, "failed"},
		{200, `{"data":[{"id":"synthetic-harness-secret"}]}`, "failed"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(test.code); w.Write([]byte(test.body)) }))
		v := syntheticHarnessAPI()
		v.BaseURL = server.URL
		result := discoverHarnessModels(context.Background(), Environment{Type: "windows"}, v)
		server.Close()
		raw, _ := json.Marshal(result)
		if result.Status != test.status || bytes.Contains(raw, []byte(v.APIKey)) {
			t.Fatalf("unsafe discovery: %#v", result)
		}
	}
	redirectHits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirectHits++ }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer origin.Close()
	v := syntheticHarnessAPI()
	v.BaseURL = origin.URL
	if result := discoverHarnessModels(context.Background(), Environment{Type: "windows"}, v); result.Status != "failed" || redirectHits != 0 {
		t.Fatal("credential redirect followed")
	}
}

func ioJSON(w http.ResponseWriter, raw string) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(raw))
}

func TestHarnessDiscoveryRemoteHelperLoopback(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		python, err = exec.LookPath("python3")
	}
	if err != nil {
		t.Skip("Python unavailable")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer synthetic-harness-secret" {
			t.Error("incorrect request")
		}
		ioJSON(w, `{"data":[{"id":"fixture-model"}]}`)
	}))
	defer server.Close()
	raw, _ := json.Marshal(harnessDiscoveryHTTP{URL: server.URL + "/models", API: "openai-completions", APIKey: "synthetic-harness-secret"})
	cmd := exec.Command(python, "-c", harnessDiscoveryScript)
	cmd.Stdin = bytes.NewReader(raw)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal("remote discovery helper failed")
	}
	var result harnessDiscoveryResponse
	if json.Unmarshal(out, &result) != nil || result.Status != 200 || !bytes.Contains(result.Body, []byte("fixture-model")) || bytes.Contains(out, []byte("synthetic-harness-secret")) {
		t.Fatal("unsafe remote result")
	}
}

func TestHarnessAPIListsSavedKeyAndRejectsInvalidSave(t *testing.T) {
	a, env := engineSetupFixture(t)
	files, err := mergeHarnessAPI(nil, syntheticHarnessAPI())
	if err != nil {
		t.Fatal(err)
	}
	a.engineSetup.harnessRead = func(context.Context, Environment, string, string) (map[string][]byte, error) { return files, nil }
	writes := 0
	a.engineSetup.harnessWrite = func(_ context.Context, _ Environment, _, _ string, old, next map[string][]byte) error {
		writes++
		files = next
		return nil
	}
	a.engineSetup.harnessDiscover = func(_ context.Context, e Environment, v HarnessAPIRequest) HarnessDiscovery {
		if e.ID != env.ID || v.APIKey != "synthetic-harness-secret" {
			t.Fatal("wrong environment or saved key")
		}
		return HarnessDiscovery{Status: "ready", Models: []ModelOption{{ID: "fixture-a", Name: "Fixture A"}, {ID: "fixture-b", Name: "Fixture B"}}, Message: "认证通过"}
	}
	v := syntheticHarnessAPI()
	v.APIKey = ""
	v.Target = harnessAPITarget(env)
	request := toolsClient(t, a)
	path := "/api/environments/" + env.ID + "/harness-api"
	raw := request(path+"/models", "POST", v, 200)
	if !bytes.Contains(raw, []byte("fixture-b")) || bytes.Contains(raw, []byte("synthetic-harness-secret")) || writes != 0 {
		t.Fatal("listing wrote config or leaked key")
	}
	v.Model = "fixture-b"
	request(path, "PUT", v, 200)
	view, err := harnessAPIView(env, files)
	if err != nil || len(view.Models) != 2 || view.Model != "fixture-b" || writes != 1 {
		t.Fatalf("saved catalog missing: %#v %v", view, err)
	}
	env, _ = a.config.get().environment(env.ID)
	v.Target = harnessAPITarget(env)
	a.engineSetup.harnessDiscover = func(context.Context, Environment, HarnessAPIRequest) HarnessDiscovery {
		return HarnessDiscovery{Status: "failed", Message: "认证被拒绝"}
	}
	before := files["cordis.patch.yml"]
	request(path, "PUT", v, 400)
	if writes != 1 || !bytes.Equal(before, files["cordis.patch.yml"]) {
		t.Fatal("invalid auth changed config")
	}
}

func TestHarnessModernCatalogAndEndpointReplacement(t *testing.T) {
	files, err := mergeHarnessAPI(nil, syntheticHarnessAPI())
	if err != nil {
		t.Fatal(err)
	}
	v := syntheticHarnessAPI()
	v.Models = []ModelOption{{ID: "fixture-b", Name: "Fixture B"}, {ID: "fixture-c", Name: "Fixture C"}}
	v.Model = "fixture-b"
	files, err = mergeHarnessAPI(files, v)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "cordis.patch.yml"), files["cordis.patch.yml"], 0600)
	list, err := modelsForEngine(context.Background(), Environment{Type: "windows", HarnessProvider: harnessAPIProvider, HarnessModel: v.Model}, "deepseek-harness", map[string]string{"DSH_HOME": home})
	if err != nil || list.Status != "ready" || len(list.Models) != 2 || list.DefaultModel != "fixture-b" {
		t.Fatalf("modern catalog %#v %v", list, err)
	}
	v.BaseURL = "https://another.example/v1"
	v.Models = nil
	v.Model = "new-manual-model"
	next, err := mergeHarnessAPI(files, v)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := parseHarnessModelFiles(next)
	if err != nil || len(settings.Routes) != 1 || len(settings.Routes[0].Models) != 1 || settings.Routes[0].Models[0].ID != v.Model {
		t.Fatal("old endpoint models retained")
	}
}
