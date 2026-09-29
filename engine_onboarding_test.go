package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func engineSetupFixture(t *testing.T) (*App, Environment) {
	a := fixture(t, &fakeRunner{})
	c := a.config.get()
	env := Environment{ID: "onboarding", Name: "Windows fixture", Type: "windows", Codex: "fixture-codex.exe", Workspaces: []string{t.TempDir()}}
	c.Environments = append(c.Environments, env)
	if err := a.config.save(c); err != nil {
		t.Fatal(err)
	}
	env, _ = a.config.get().environment(env.ID)
	return a, env
}
func TestEngineSetupAccountIsolationAndRetry(t *testing.T) {
	a, env := engineSetupFixture(t)
	var calls atomic.Int32
	a.engineSetup.account = func(ctx context.Context, e Environment, home, login, key string, waiting func(string, string)) error {
		calls.Add(1)
		if e.ID != env.ID || login != "apiKey" || key != "synthetic-secret" {
			t.Error("wrong setup request")
		}
		if !strings.HasPrefix(home, filepath.Join(a.store.directory, "engine-accounts")+string(os.PathSeparator)) {
			t.Error("global credentials touched")
		}
		return os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"key":"synthetic-secret"}`), 0600)
	}
	input := EngineSetupRequest{ID: "fixture-account", Action: "account", EnvironmentID: env.ID, Engine: "codex", Name: "Fixture API", Login: "apiKey", APIKey: "synthetic-secret", BaseURL: "https://provider.example/v1", Model: "fixture-model"}
	job, err := a.startEngineSetup(input)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { j, _ := a.engineSetup.snapshot(job.ID); return j.State == "done" || j.State == "failed" })
	job, _ = a.engineSetup.snapshot(job.ID)
	if job.State != "done" {
		t.Fatal(job.Message)
	}
	if _, err = a.startEngineSetup(input); err != nil || calls.Load() != 1 {
		t.Fatal("retry started another login", err, calls.Load())
	}
	profiles := a.store.engineProfiles()
	if len(profiles) != 1 || profiles[0].ID != job.ProfileID || profiles[0].Kind != "codex_home" {
		t.Fatal(profiles)
	}
	config, _ := os.ReadFile(filepath.Join(profiles[0].Reference, "config.toml"))
	if !strings.Contains(string(config), "wire_api = \"responses\"") || strings.Contains(string(config), input.APIKey) {
		t.Fatal("provider configuration or key isolation failed")
	}
	raw, _ := json.Marshal(job)
	if strings.Contains(string(raw), input.APIKey) || strings.Contains(a.store.setting(engineProfilesSetting), input.APIKey) {
		t.Fatal("key leaked into public state or database")
	}
	input.Name = "changed"
	if _, err = a.startEngineSetup(input); err == nil {
		t.Fatal("ID reused for another account")
	}
}
func TestEngineSetupInstallCommitAndFailure(t *testing.T) {
	a, env := engineSetupFixture(t)
	a.engineSetup.install = func(ctx context.Context, dir, engine string, progress func(string)) (string, error) {
		if engine != "codex" {
			return "", errors.New("synthetic installer failure")
		}
		return filepath.Join(a.store.directory, "fixture-codex.exe"), nil
	}
	for _, engine := range []string{"codex", "claude"} {
		job, err := a.startEngineSetup(EngineSetupRequest{ID: "install-" + engine, Action: "install", Engine: engine, EnvironmentID: env.ID})
		if err != nil {
			t.Fatal(err)
		}
		waitUntil(t, func() bool { a.engineSetup.mu.Lock(); defer a.engineSetup.mu.Unlock(); return !a.engineSetup.active })
		job, _ = a.engineSetup.snapshot(job.ID)
		if (job.State == "done") != (engine == "codex") {
			t.Fatal(job)
		}
	}
	updated, _ := a.config.get().environment(env.ID)
	if updated.Codex != filepath.Join(a.store.directory, "fixture-codex.exe") || updated.Claude != env.Claude {
		t.Fatal("partial failure changed environment", updated)
	}
}
func TestEngineSetupValidationAndCancellation(t *testing.T) {
	a, env := engineSetupFixture(t)
	input := EngineSetupRequest{ID: "fixture-cancel", Action: "account", Engine: "codex", EnvironmentID: env.ID, Name: "test", Login: "apiKey", APIKey: "synthetic"}
	for _, address := range []string{"http://remote.example", "https://user:pass@example.com", "https://example.com?key=secret", "javascript:alert(1)"} {
		bad := input
		bad.BaseURL = address
		if validateEngineSetup(bad, env) == nil {
			t.Fatal(address)
		}
	}
	input.ID = "../escape"
	if validateEngineSetup(input, env) == nil {
		t.Fatal("invalid directory ID accepted")
	}
	input.ID = "fixture-cancel"
	a.engineSetup.account = func(ctx context.Context, _ Environment, _, _, _ string, _ func(string, string)) error {
		<-ctx.Done()
		return ctx.Err()
	}
	request := toolsClient(t, a)
	data := request("/api/engine-setup", "POST", input, 202)
	if strings.Contains(string(data), "synthetic") {
		t.Fatal("secret exposed")
	}
	data = request("/api/engine-setup", "GET", nil, 200)
	if !strings.Contains(string(data), input.ID) {
		t.Fatal("active login not restorable")
	}
	request("/api/engine-setup/"+input.ID, "DELETE", nil, 200)
	waitUntil(t, func() bool { job, _ := a.engineSetup.snapshot(input.ID); return job.State == "failed" })
}
func TestManagedRuntimeRejectsArchiveTraversal(t *testing.T) {
	for _, name := range []string{"../escape", "C:/escape", "node/../../escape", "node\\escape"} {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			dir := t.TempDir()
			archive := filepath.Join(dir, "fixture.zip")
			f, _ := os.Create(archive)
			z := zip.NewWriter(f)
			entry, _ := z.Create(name)
			entry.Write([]byte("fixture"))
			z.Close()
			f.Close()
			if extractManagedRuntime(archive, filepath.Join(dir, "out")) == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}

func runCodexAccountFixture() {
	decoder, encoder := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	read := func(method string) codexRPC {
		var m codexRPC
		if decoder.Decode(&m) != nil || m.Method != method {
			os.Exit(31)
		}
		return m
	}
	reply := func(m codexRPC, v any) { encoder.Encode(map[string]any{"id": m.ID, "result": v}) }
	reply(read("initialize"), map[string]any{})
	read("initialized")
	login := read("account/login/start")
	var p map[string]string
	json.Unmarshal(login.Params, &p)
	if os.Getenv("CODEX_HOME") != os.Getenv("DUO_TEST_ACCOUNT_HOME") {
		os.Exit(32)
	}
	if p["type"] == "apiKey" {
		if p["apiKey"] != "synthetic" {
			os.Exit(33)
		}
		reply(login, map[string]string{"type": "apiKey"})
	} else {
		reply(login, map[string]string{"type": "chatgptDeviceCode", "loginId": "fixture-login", "verificationUrl": "https://auth.openai.com/codex/device", "userCode": "TEST-1234"})
		encoder.Encode(map[string]any{"method": "account/login/completed", "params": map[string]any{"loginId": "fixture-login", "success": true}})
	}
	// A login must never proceed to a model turn.
	var more codexRPC
	if err := decoder.Decode(&more); err != io.EOF {
		os.Exit(34)
	}
}
func TestCodexAccountNativeProtocol(t *testing.T) {
	t.Setenv("DUO_TEST_CODEX_ACCOUNT", "1")
	exe, _ := os.Executable()
	home := t.TempDir()
	t.Setenv("DUO_TEST_ACCOUNT_HOME", home)
	for _, login := range []string{"apiKey", "chatgptDeviceCode"} {
		calls := 0
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := setupCodexAccount(ctx, Environment{Type: "windows", Codex: exe}, home, login, "synthetic", func(link, code string) {
			calls++
			if link != "https://auth.openai.com/codex/device" || code != "TEST-1234" {
				t.Error("lost device login")
			}
		})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if (calls == 1) != (login == "chatgptDeviceCode") {
			t.Fatal(calls)
		}
	}
}

func TestManagedEngineInstallIntegration(t *testing.T) {
	engine := os.Getenv("DUO_TEST_INSTALL_ENGINE")
	if engine == "" {
		t.Skip("explicit temporary-directory download test only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	binary, err := installManagedEngine(ctx, filepath.Join(t.TempDir(), "install"), engine, func(message string) { t.Log(message) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(binary); err != nil {
		t.Fatal(err)
	}
	t.Log("installed and verified", engine, filepath.Base(binary))
}
