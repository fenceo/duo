package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func syntheticHarnessAPI() HarnessAPIRequest {
	return HarnessAPIRequest{API: "openai-completions", BaseURL: "https://api.example/v1", Model: "fixture-model", APIKey: "synthetic-harness-secret"}
}

func TestHarnessAPIMergePreservesNativeFiles(t *testing.T) {
	old := map[string][]byte{
		"cordis.patch.yml":  []byte("# existing tools\n- id: sandbox-policy\n  config: {mode: read-only}\n- id: llm-pi-ai\n  config:\n    providers:\n      existing:\n        api: openai-responses\n        baseURL: https://existing.example/v1\n        models: [{id: existing-model}]\n"),
		".credentials.yaml": []byte("version: 1\n# retain other keys\nrefs:\n  EXISTING_KEY: synthetic-other-key\nrecords:\n  llm-pi-ai/another:\n    kind: grant\n    payload: {type: oauth, access: synthetic-oauth}\n"),
	}
	next, err := mergeHarnessAPI(old, syntheticHarnessAPI())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(next["cordis.patch.yml"]), "existing-model") || !strings.Contains(string(next["cordis.patch.yml"]), "read-only") || !strings.Contains(string(next[".credentials.yaml"]), "synthetic-oauth") || !strings.Contains(string(next[".credentials.yaml"]), "# retain other keys") {
		t.Fatal("unrelated configuration was lost")
	}
	if bytes.Contains(next["cordis.patch.yml"], []byte("synthetic-harness-secret")) {
		t.Fatal("key entered model configuration")
	}
	v := syntheticHarnessAPI()
	v.APIKey = ""
	v.Model = "fixture-b"
	edited, err := mergeHarnessAPI(next, v)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(edited[".credentials.yaml"], []byte("synthetic-harness-secret")) || !bytes.Contains(edited["cordis.patch.yml"], []byte("fixture-model")) {
		t.Fatal("blank key or old model lost")
	}
	view, err := harnessAPIView(Environment{ID: "fixture"}, edited)
	if err != nil || view.Model != "fixture-b" || !view.KeyConfigured {
		t.Fatal("wrong safe projection", err)
	}
	raw, _ := json.Marshal(view)
	if bytes.Contains(raw, []byte("synthetic-")) {
		t.Fatal("secret returned by view")
	}
}

func TestHarnessAPIRejectsInvalidConfiguration(t *testing.T) {
	for _, url := range []string{"", "http://remote.example/v1", "https://key:secret@example.com", "https://example.com/v1?key=secret", "javascript:alert(1)", "https://example.com/#secret"} {
		v := syntheticHarnessAPI()
		v.BaseURL = url
		if _, err := mergeHarnessAPI(nil, v); err == nil {
			t.Fatal("invalid URL accepted")
		}
	}
	for _, change := range []func(*HarnessAPIRequest){func(v *HarnessAPIRequest) { v.API = "shell" }, func(v *HarnessAPIRequest) { v.Model = "" }, func(v *HarnessAPIRequest) { v.APIKey = "key\nsecret" }, func(v *HarnessAPIRequest) { v.APIKey = " key" }, func(v *HarnessAPIRequest) { v.APIKey = "" }} {
		v := syntheticHarnessAPI()
		change(&v)
		if _, err := mergeHarnessAPI(nil, v); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
	for _, raw := range []string{"secret: broken\n", "- id: llm-pi-ai\n  config: broken\n", "- id: llm-pi-ai\n- id: llm-pi-ai\n", "[]\n---\n[]\n", "- id: llm-pi-ai\n  config:\n    providers: {duo-api: {api: secret, api: secret}}\n"} {
		_, err := mergeHarnessAPI(map[string][]byte{"cordis.patch.yml": []byte(raw)}, syntheticHarnessAPI())
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("invalid file accepted or error exposed values")
		}
	}
	for _, raw := range []string{"version: 2\n", "UNKNOWN: secret\n", "version: 1\nrefs: secret\n"} {
		if _, err := mergeHarnessAPI(map[string][]byte{".credentials.yaml": []byte(raw)}, syntheticHarnessAPI()); err == nil {
			t.Fatal("invalid credentials format accepted")
		}
	}
	for _, protocol := range []string{"openai-completions", "openai-responses", "anthropic-messages"} {
		v := syntheticHarnessAPI()
		v.API = protocol
		v.BaseURL = "http://127.0.0.1:1234/v1"
		if _, err := mergeHarnessAPI(nil, v); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHarnessAPIHTTPSelectedEnvironmentAndSecretIsolation(t *testing.T) {
	a, env := engineSetupFixture(t)
	a.engineSetup.harnessDiscover = syntheticHarnessDiscovery
	c := a.config.get()
	env.Type = "wsl"
	env.Distro = "synthetic-distro"
	env.User = "selected-user"
	env.Workspaces = []string{"/synthetic-workspace"}
	c.Environments[len(c.Environments)-1] = env
	if err := a.config.save(c); err != nil {
		t.Fatal(err)
	}
	env, _ = a.config.get().environment(env.ID)
	oldConfig := a.config.get()
	files := map[string][]byte{"cordis.patch.yml": nil, ".credentials.yaml": nil}
	writes := 0
	a.engineSetup.harnessRead = func(_ context.Context, e Environment, dir, engine string) (map[string][]byte, error) {
		if e.Type != "wsl" || e.User != "selected-user" || dir != "" || engine != "deepseek-harness" {
			t.Fatal("wrong environment")
		}
		return files, nil
	}
	a.engineSetup.harnessWrite = func(_ context.Context, e Environment, dir, engine string, old, next map[string][]byte) error {
		writes++
		files = next
		return nil
	}
	request := toolsClient(t, a)
	path := "/api/environments/" + env.ID + "/harness-api"
	var view HarnessAPIView
	json.Unmarshal(request(path, "GET", nil, 200), &view)
	v := syntheticHarnessAPI()
	v.Target = view.Target
	response := request(path, "PUT", v, 200)
	if bytes.Contains(response, []byte(v.APIKey)) {
		t.Fatal("key in HTTP response")
	}
	if writes != 1 {
		t.Fatal("write count")
	}
	updated, _ := a.config.get().environment(env.ID)
	if updated.HarnessProvider != harnessAPIProvider || updated.HarnessModel != v.Model {
		t.Fatal("default route not saved")
	}
	if len(a.store.engineProfiles()) != 0 {
		t.Fatal("account or task binding changed")
	}
	for _, other := range oldConfig.Environments {
		if other.ID != env.ID {
			current, _ := a.config.get().environment(other.ID)
			if harnessAPITarget(current) != harnessAPITarget(other) {
				t.Fatal("other environment changed")
			}
		}
	}
	raw, _ := os.ReadFile(a.config.path)
	if bytes.Contains(raw, []byte(v.APIKey)) {
		t.Fatal("key entered Duo configuration")
	}
	request(path, "PUT", v, 409)
	if writes != 1 {
		t.Fatal("stale target wrote files")
	}
	json.Unmarshal(request(path, "GET", nil, 200), &view)
	v.Target = view.Target
	v.APIKey = ""
	a.engineSetup.active = true
	request(path, "PUT", v, 409)
	a.engineSetup.active = false
	a.updating.Store(true)
	request(path, "PUT", v, 409)
	a.updating.Store(false)
	// A selected-user change between read and write cannot redirect the save.
	a.engineSetup.harnessRead = func(_ context.Context, _ Environment, _, _ string) (map[string][]byte, error) {
		c := a.config.get()
		for i := range c.Environments {
			if c.Environments[i].ID == env.ID {
				c.Environments[i].User = "changed-user"
			}
		}
		a.config.save(c)
		return files, nil
	}
	request(path, "PUT", v, 409)
	if writes != 1 {
		t.Fatal("changed target wrote files")
	}
}

func TestHarnessAPIConfigFailureRollsBackNativeFiles(t *testing.T) {
	a, env := engineSetupFixture(t)
	a.engineSetup.harnessDiscover = syntheticHarnessDiscovery
	writes := 0
	var written map[string][]byte
	a.engineSetup.harnessRead = func(context.Context, Environment, string, string) (map[string][]byte, error) {
		return map[string][]byte{"cordis.patch.yml": nil, ".credentials.yaml": nil}, nil
	}
	a.engineSetup.harnessWrite = func(_ context.Context, _ Environment, _, _ string, old, next map[string][]byte) error {
		writes++
		written = next
		return nil
	}
	a.config.path = filepath.Join(t.TempDir(), "missing-parent", "config.json")
	v := syntheticHarnessAPI()
	v.Target = harnessAPITarget(env)
	toolsClient(t, a)("/api/environments/"+env.ID+"/harness-api", "PUT", v, 500)
	if writes != 2 || written[".credentials.yaml"] != nil || written["cordis.patch.yml"] != nil {
		t.Fatal("native files not restored")
	}
}

func TestHarnessAPIFileLocksAndBackup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DSH_HOME", dir)
	next, err := mergeHarnessAPI(nil, syntheticHarnessAPI())
	if err != nil {
		t.Fatal(err)
	}
	env := Environment{Type: "windows"}
	old, err := readNativeAccountFiles(context.Background(), env, "", "deepseek-harness")
	if err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(dir, ".credentials.yaml.lock")
	os.WriteFile(lock, []byte("foreign-holder\n"), 0600)
	if writeNativeAccountFiles(context.Background(), env, "", "deepseek-harness", old, next) == nil {
		t.Fatal("foreign native lock ignored")
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatal("foreign lock removed")
	}
	os.Remove(lock)
	if err := writeNativeAccountFiles(context.Background(), env, "", "deepseek-harness", old, next); err != nil {
		t.Fatal(err)
	}
	if writeNativeAccountFiles(context.Background(), env, "", "deepseek-harness", old, next) == nil {
		t.Fatal("stale snapshot accepted")
	}
	backups, _ := filepath.Glob(filepath.Join(dir, ".duo-account-backup-*"))
	if len(backups) != 1 {
		t.Fatal("backup not retained")
	}
}

func TestHarnessAPIRemoteHelperSynthetic(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		python, err = exec.LookPath("python3")
	}
	if err != nil {
		t.Skip("Python unavailable")
	}
	for _, script := range []string{nativeAccountScript, harnessLauncher, harnessDiscoveryScript} {
		cmd := exec.Command(python, "-c", "import sys; compile(sys.stdin.read(), '<embedded>', 'exec')")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("invalid embedded Python syntax: %s", out)
		}
	}
	dir := t.TempDir()
	old := map[string][]byte{"cordis.patch.yml": nil, ".credentials.yaml": nil}
	next, err := mergeHarnessAPI(old, syntheticHarnessAPI())
	if err != nil {
		t.Fatal(err)
	}
	run := func(req nativeAccountRequest) error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		raw, _ := json.Marshal(req)
		cmd := exec.CommandContext(ctx, python, "-c", nativeAccountScript)
		cmd.Stdin = bytes.NewReader(raw)
		if out, err := cmd.CombinedOutput(); err != nil {
			return errors.New(string(out))
		}
		return nil
	}
	request := nativeAccountRequest{Operation: "write", Engine: "deepseek-harness", Directory: dir, Expected: old, Files: next}
	if err := run(request); err != nil {
		t.Fatal(err)
	}
	if err := run(request); err == nil {
		t.Fatal("remote stale snapshot accepted")
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, "cordis.patch.yml")); bytes.Contains(raw, []byte("synthetic-harness-secret")) {
		t.Fatal("remote key placement")
	}
	lock := filepath.Join(dir, "cordis.patch.yml.lock")
	os.WriteFile(lock, []byte("foreign\n"), 0600)
	request.Expected = next
	if run(request) == nil {
		t.Fatal("remote native lock ignored")
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatal("remote removed foreign lock")
	}
}
