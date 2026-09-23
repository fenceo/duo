package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestProbeModelsUsesReadOnlyClaudeCall(t *testing.T) {
	t.Setenv("JIANZUO_TEST_CLAUDE", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	result, err := probeModels(context.Background(), Config{Claude: exe}, Environment{Type: "windows"}, "claude", workspace, []string{"sonnet", "opus", "sonnet"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 2 || result.Results[0].Status != "available" || result.Results[1].Status != "available" {
		t.Fatalf("unexpected probe result: %#v", result)
	}
}

func TestProbeModelsRejectsUnsafeInputAndDoesNotExposeErrors(t *testing.T) {
	if _, err := normalizeProbeModels([]string{"bad\nmodel"}); err == nil {
		t.Fatal("newline model was accepted")
	}
	tooMany := make([]string, modelProbeMaxModels+1)
	for i := range tooMany {
		tooMany[i] = "model-" + string(rune('a'+i))
	}
	if _, err := normalizeProbeModels(tooMany); err == nil {
		t.Fatal("too many models were accepted")
	}
	message := publicProbeError(errors.New("authorization failed: bearer-secret-should-not-leak"))
	if strings.Contains(message, "bearer-secret-should-not-leak") {
		t.Fatal(message)
	}
}

func TestModelProbeAPI(t *testing.T) {
	t.Setenv("JIANZUO_TEST_CLAUDE", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a := fixture(t, &fakeRunner{})
	config := a.config.get()
	environmentID := ""
	workspace := t.TempDir()
	for i := range config.Environments {
		if config.Environments[i].Type == "windows" {
			environmentID = config.Environments[i].ID
			config.Environments[i].Claude = exe
			config.Environments[i].Workspaces = []string{workspace}
			break
		}
	}
	if environmentID == "" {
		t.Fatal("fixture did not create a Windows environment")
	}
	if err := a.config.save(config); err != nil {
		t.Fatal(err)
	}
	raw := toolsClient(t, a)("/api/environments/"+environmentID+"/models/test", "POST", map[string]any{
		"engine":    "claude",
		"workspace": workspace,
		"models":    []string{"sonnet", "opus"},
	}, 200)
	var response ModelProbeResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response.Engine != "claude" || len(response.Results) != 2 {
		t.Fatalf("unexpected API response: %#v", response)
	}
	for _, result := range response.Results {
		if result.Status != "available" || result.Message != "调用成功" {
			t.Fatalf("unexpected model result: %#v", result)
		}
	}
}

func TestPublicProbeErrorDistinguishesUnsupportedReasoning(t *testing.T) {
	for _, message := range []string{
		"UNSUPPORTED_REASONING_EFFORT",
		`provider "custom" model "demo" does not support reasoning effort "off"`,
		"Harness initialize [UNSUPPORTED_REASONING_EFFORT]：" + harnessUnsupportedReasoningMessage,
	} {
		if got := publicProbeError(errors.New(message)); got != harnessUnsupportedReasoningMessage {
			t.Errorf("unsupported effort misclassified: %q => %q", message, got)
		}
	}
	if got := publicProbeError(errors.New("MISSING_CREDENTIAL alongside UNSUPPORTED_REASONING_EFFORT")); !strings.Contains(got, "缺少 AI 凭据") {
		t.Fatalf("missing credential precedence changed: %q", got)
	}
}

func TestPublicProbeErrorClassifiesCredentialsBeforeModelErrors(t *testing.T) {
	for _, message := range []string{
		"Harness turn error [MISSING_CREDENTIAL]: invalid model configuration",
		"MISSING_CREDENTIAL: API key not found for model deepseek-flash",
		"missing API key: unsupported model",
	} {
		got := publicProbeError(errors.New(message))
		if !strings.Contains(got, "缺少 AI 凭据") || !strings.Contains(got, "对应执行环境") || strings.Contains(got, "模型不受") {
			t.Errorf("missing credentials misclassified: %q => %q", message, got)
		}
	}
	for _, message := range []string{
		"invalid provider configuration", "invalid model configuration", "configuration file not found",
		"unsupported SDK method", "model invocation failed", "invalid response from model service",
		"MODEL_CONFIGURATION_NOT_FOUND", "invalid_model_configuration",
	} {
		if got := publicProbeError(errors.New(message)); got != "调用失败，请检查该环境的登录状态和网络配置" {
			t.Errorf("generic failure misclassified: %q => %q", message, got)
		}
	}
	for _, message := range []string{
		"MODEL_NOT_FOUND", "UNSUPPORTED_MODEL", "invalid_model", "unsupported model: demo",
		"model not found", "model 'demo' does not exist", "model `demo` is not supported",
	} {
		if got := publicProbeError(errors.New(message)); got != "模型不受当前服务支持" {
			t.Errorf("explicit model error misclassified: %q => %q", message, got)
		}
	}
	if got := publicProbeError(&exec.Error{Name: "dsh", Err: exec.ErrNotFound}); got != "找不到 AI 工具程序" {
		t.Fatalf("executable lookup failure misclassified: %q", got)
	}
}

func TestModelProbeRequiresExplicitBoundedModels(t *testing.T) {
	for _, models := range [][]string{nil, {}, {""}, {"good", "  "}} {
		if _, err := normalizeProbeModels(models); err == nil {
			t.Fatalf("empty model ID/list accepted: %#v", models)
		}
	}
	models, err := normalizeProbeModels([]string{" one ", "two", "one"})
	if err != nil || !reflect.DeepEqual(models, []string{"one", "two"}) {
		t.Fatalf("normalization: %v, %v", models, err)
	}
	// A configured default/catalog must never turn an empty request into paid
	// generation; all IDs have to be included in the user's confirmed request.
	_, err = probeModels(context.Background(), Config{}, Environment{Type: "windows", Model: "configured-default"}, "codex", t.TempDir(), nil)
	if err == nil {
		t.Fatal("empty request silently used the configured model")
	}
}

func TestModelProbeStreamOrderSnapshotAndPerModelFailures(t *testing.T) {
	profile := map[string]string{"CODEX_HOME": "original-profile"}
	config := Config{EngineEnv: profile, Codex: "original-executable", HardwareAI: &HardwareRuntime{}}
	plan, err := prepareModelProbe(config, Environment{Type: "windows"}, "codex", t.TempDir(), []string{"a", "b", "c", "a"})
	if err != nil {
		t.Fatal(err)
	}
	profile["CODEX_HOME"] = "changed-profile"
	config.Codex = "changed-executable"
	events := []string{}
	calls := []string{}
	run := func(ctx context.Context, c Config, engine, workspace, model string) ModelProbeResult {
		if c.EngineEnv["CODEX_HOME"] != "original-profile" || c.Codex != "original-executable" || c.HardwareAI != nil {
			t.Fatalf("batch runtime not isolated: %#v", c)
		}
		if len(events) == 0 || events[len(events)-1] != "model_start:"+model {
			t.Fatalf("current model not announced before invocation: %v", events)
		}
		calls = append(calls, model)
		status := map[string]string{"a": "available", "b": "unavailable", "c": "timeout"}[model]
		return ModelProbeResult{Model: model, Status: status, Message: "synthetic", DurationMS: 1}
	}
	response := executeModelProbe(context.Background(), plan, run, func(event ModelProbeEvent) error {
		label := event.Type
		if event.Model != "" {
			label += ":" + event.Model
		}
		if event.Result != nil {
			label += ":" + event.Result.Model
		}
		events = append(events, label)
		return nil
	})
	want := []string{"start", "model_start:a", "result:a", "model_start:b", "result:b", "model_start:c", "result:c", "done"}
	if !reflect.DeepEqual(events, want) || !reflect.DeepEqual(calls, []string{"a", "b", "c"}) || len(response.Results) != 3 {
		t.Fatalf("events=%v calls=%v response=%#v", events, calls, response)
	}
}

func TestModelProbeStopsOnCancellationAndStreamFailure(t *testing.T) {
	plan, err := prepareModelProbe(Config{}, Environment{Type: "windows"}, "codex", t.TempDir(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	for _, cancelAt := range []string{"before", "start", "model_start", "running", "result"} {
		t.Run(cancelAt, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			if cancelAt == "before" {
				cancel()
			}
			response := executeModelProbe(ctx, plan, func(ctx context.Context, _ Config, _, _, model string) ModelProbeResult {
				calls++
				if cancelAt == "running" {
					cancel()
					<-ctx.Done()
				}
				return ModelProbeResult{Model: model, Status: "available"}
			}, func(event ModelProbeEvent) error {
				if event.Type == cancelAt {
					cancel()
					return io.ErrClosedPipe
				}
				if event.Type == "done" {
					t.Fatal("cancelled batch sent successful done")
				}
				return nil
			})
			if calls > 1 || len(response.Results) > 1 {
				t.Fatalf("cancelled batch ran later models: calls=%d results=%v", calls, response.Results)
			}
			if (cancelAt == "before" || cancelAt == "start" || cancelAt == "model_start") && calls != 0 {
				t.Fatal("started a model after disconnection")
			}
		})
	}
}

func modelProbeHTTPFixture(t *testing.T) (*App, *Server, http.Handler, string, string) {
	t.Helper()
	t.Setenv("JIANZUO_TEST_CLAUDE", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a := fixture(t, &fakeRunner{})
	c := a.config.get()
	workspace := t.TempDir()
	id := ""
	for i := range c.Environments {
		if c.Environments[i].Type == "windows" {
			id = c.Environments[i].ID
			c.Environments[i].Claude = exe
			c.Environments[i].Workspaces = []string{workspace}
			break
		}
	}
	if id == "" {
		t.Fatal("no Windows fixture")
	}
	if err := a.config.save(c); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Exec("INSERT INTO sessions VALUES(?,?,?)", hash("probe-test"), "probe-csrf", now()+60000); err != nil {
		t.Fatal(err)
	}
	server := &Server{app: a}
	return a, server, server.Handler(), "/api/environments/" + id + "/models/test", workspace
}

func modelProbeHTTPRequest(path, workspace string, models []string) *http.Request {
	raw, _ := json.Marshal(ModelProbeRequest{Engine: "claude", Workspace: workspace, Models: models})
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	r.Header.Set("Accept", "application/x-ndjson")
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("X-CSRF-Token", "probe-csrf")
	r.AddCookie(&http.Cookie{Name: "jianzuo_session", Value: "probe-test"})
	return r
}

func TestModelProbeStreamingAPICompatibilityAndValidation(t *testing.T) {
	_, server, handler, path, workspace := modelProbeHTTPFixture(t)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, modelProbeHTTPRequest(path, workspace, []string{"one", "two", "one"}))
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/x-ndjson") || !w.Flushed || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("not a flushed stream: status=%d headers=%v", w.Code, w.Header())
	}
	decoder := json.NewDecoder(w.Body)
	events := []ModelProbeEvent{}
	for {
		var event ModelProbeEvent
		if err := decoder.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if len(events) != 6 || events[0].Type != "start" || !reflect.DeepEqual(events[0].Models, []string{"one", "two"}) || events[5].Type != "done" || len(events[5].Results) != 2 {
		t.Fatalf("unexpected stream: %#v", events)
	}
	if events[1].Model != "one" || events[2].Result.Model != "one" || events[2].Result.Status != "available" || events[3].Model != "two" || events[4].Result.Model != "two" {
		t.Fatalf("wrong per-model stream: %#v", events)
	}
	if !server.modelProbeMu.TryLock() {
		t.Fatal("completed stream leaked lock")
	}
	server.modelProbeMu.Unlock()
	for _, models := range [][]string{nil, {""}, {"one", " "}} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, modelProbeHTTPRequest(path, workspace, models))
		if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), `"type":"start"`) {
			t.Fatalf("invalid IDs started stream: %d %s", w.Code, w.Body)
		}
	}
	tooMany := []string{}
	for i := 0; i < modelProbeMaxModels+1; i++ {
		tooMany = append(tooMany, "model-"+string(rune('a'+i)))
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, modelProbeHTTPRequest(path, workspace, tooMany))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversized batch truncated instead of rejected: %d %s", w.Code, w.Body)
	}
}

func TestModelProbeStreamPreservesAuthBusyAndProfileChecks(t *testing.T) {
	a, server, handler, path, workspace := modelProbeHTTPFixture(t)
	for _, scenario := range []string{"unauthenticated", "csrf", "busy", "updating", "changed-profile"} {
		t.Run(scenario, func(t *testing.T) {
			r := modelProbeHTTPRequest(path, workspace, []string{"one"})
			want := http.StatusConflict
			switch scenario {
			case "unauthenticated":
				r.Header.Del("Cookie")
				want = http.StatusUnauthorized
			case "csrf":
				r.Header.Set("X-CSRF-Token", "bad")
				want = http.StatusForbidden
			case "busy":
				server.modelProbeMu.Lock()
				defer server.modelProbeMu.Unlock()
			case "updating":
				a.updating.Store(true)
				defer a.updating.Store(false)
			case "changed-profile":
				raw, _ := json.Marshal(map[string]any{"engine": "claude", "workspace": workspace, "models": []string{"one"}, "expected_profile_id": "old-profile"})
				r.Body = io.NopCloser(bytes.NewReader(raw))
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != want || strings.Contains(w.Body.String(), `"type":"start"`) {
				t.Fatalf("guard failed: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestModelProbeCancelStopsOwnedNativeChild(t *testing.T) {
	c := codexFixtureConfig(t, "cancel")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	result := probeOneModel(ctx, c, "codex", t.TempDir(), "synthetic-model")
	if time.Since(started) > 8*time.Second || result.Status != "timeout" {
		t.Fatalf("native child did not cancel promptly: %#v elapsed=%v", result, time.Since(started))
	}
}

func TestModelProbeStreamFlushesBeforeGenerationAndDisconnectReleasesLock(t *testing.T) {
	a, server, handler, path, workspace := modelProbeHTTPFixture(t)
	native := codexFixtureConfig(t, "cancel")
	c := a.config.get()
	for i := range c.Environments {
		if c.Environments[i].Type == "windows" {
			c.Environments[i].Codex = native.Codex
		}
	}
	if err := a.config.save(c); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()
	raw, _ := json.Marshal(ModelProbeRequest{Engine: "codex", Workspace: workspace, Models: []string{"one", "two"}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+path, bytes.NewReader(raw))
	r.Header.Set("Accept", "application/x-ndjson")
	r.Header.Set("Origin", srv.URL)
	r.Header.Set("X-CSRF-Token", "probe-csrf")
	r.AddCookie(&http.Cookie{Name: "jianzuo_session", Value: "probe-test"})
	client := &http.Client{Timeout: 8 * time.Second}
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	decoder := json.NewDecoder(response.Body)
	for _, expected := range []string{"start", "model_start"} {
		var event ModelProbeEvent
		if err := decoder.Decode(&event); err != nil || event.Type != expected {
			t.Fatalf("event before generation missing: %#v %v", event, err)
		}
	}
	// This native fixture never completes generation until cancellation. A
	// second model cannot be reached and the real HTTP disconnect must release
	// admission after cancelling only the probe-owned child/process group.
	cancel()
	_ = response.Body.Close()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if server.modelProbeMu.TryLock() {
			server.modelProbeMu.Unlock()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("disconnected streaming request retained model/update admission lock")
}

func TestAcceptsModelProbeStream(t *testing.T) {
	for _, value := range []string{"application/x-ndjson", "application/json, application/x-ndjson; charset=utf-8"} {
		if !acceptsModelProbeStream(value) {
			t.Fatalf("stream accept rejected: %s", value)
		}
	}
	for _, value := range []string{"", "application/json", "application/x-ndjson-malicious", "application/x-ndjson;q=0", "application/x-ndjson;q=0.0", "application/x-ndjson;q=NaN"} {
		if acceptsModelProbeStream(value) {
			t.Fatalf("non-stream accept accepted: %s", value)
		}
	}
}
