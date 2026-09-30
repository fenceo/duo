package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAccountFilesLocalBackupAndConcurrentChange(t *testing.T) {
	dir := t.TempDir()
	old := map[string][]byte{"auth.json": []byte(`{"OPENAI_API_KEY":"synthetic-old"}`), "config.toml": []byte("model=\"old\"\n")}
	for name, raw := range old {
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	next := map[string][]byte{"auth.json": []byte(`{"OPENAI_API_KEY":"synthetic-new"}`), "config.toml": []byte("model=\"new\"\n")}
	if err := writeLocalAccountFiles(dir, "codex", old, next); err != nil {
		t.Fatal(err)
	}
	got, err := readLocalAccountFiles(dir, "codex")
	if err != nil || !bytes.Equal(got["auth.json"], next["auth.json"]) {
		t.Fatal("account not written", err)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, ".duo-account-backup-*"))
	if len(backups) != 1 {
		t.Fatal("backup missing")
	}
	for name, expected := range old {
		raw, err := os.ReadFile(filepath.Join(backups[0], name))
		if err != nil || !bytes.Equal(raw, expected) {
			t.Fatal("incomplete backup", name)
		}
	}
	if err := writeLocalAccountFiles(dir, "codex", old, next); err == nil {
		t.Fatal("concurrent modification ignored")
	}
	if _, err := os.Stat(filepath.Join(dir, ".duo-account-sync.lock")); !os.IsNotExist(err) {
		t.Fatal("lock left behind")
	}
	if err := os.Mkdir(filepath.Join(dir, ".duo-account-sync.lock"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeLocalAccountFiles(dir, "codex", next, old); err == nil {
		t.Fatal("lock ignored")
	}
}

func TestAccountFilesRemoteHelperSynthetic(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		python, err = exec.LookPath("python3")
	}
	if err != nil {
		t.Skip("Python required for isolated remote helper test")
	}
	dir := t.TempDir()
	call := func(req nativeAccountRequest, success bool) map[string][]byte {
		t.Helper()
		req.Directory = dir
		req.Engine = "claude"
		raw, _ := json.Marshal(req)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, python, "-c", nativeAccountScript)
		cmd.Stdin = bytes.NewReader(raw)
		out, err := cmd.CombinedOutput()
		if (err == nil) != success {
			t.Fatalf("helper success=%v: %v (%s)", success, err, out)
		}
		if !success {
			if strings.Contains(string(out), "synthetic") {
				t.Fatal("secret in diagnostics")
			}
			return nil
		}
		var files map[string][]byte
		if json.Unmarshal(out, &files) != nil {
			t.Fatal("invalid helper result")
		}
		return files
	}
	empty := call(nativeAccountRequest{Operation: "read"}, true)
	files := map[string][]byte{".credentials.json": []byte(`{"claudeAiOauth":{"accessToken":"synthetic"}}`), "settings.json": []byte(`{"theme":"dark"}`)}
	call(nativeAccountRequest{Operation: "write", Expected: empty, Files: files}, true)
	read := call(nativeAccountRequest{Operation: "read"}, true)
	if !bytes.Equal(read[".credentials.json"], files[".credentials.json"]) {
		t.Fatal("helper changed credential bytes")
	}
	call(nativeAccountRequest{Operation: "write", Expected: empty, Files: files}, false)
	relay := map[string][]byte{".credentials.json": nil, "settings.json": []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"synthetic-relay"}}`)}
	call(nativeAccountRequest{Operation: "write", Expected: read, Files: relay}, true)
	if _, err := os.Stat(filepath.Join(dir, ".credentials.json")); !os.IsNotExist(err) {
		t.Fatal("old subscription not removed")
	}
	backups, _ := filepath.Glob(filepath.Join(dir, ".duo-account-backup-*", ".credentials.json"))
	if len(backups) != 1 {
		t.Fatal("subscription backup missing")
	}
}

func TestAccountSyncAPIUsesSourceEnvironmentAndHidesSecrets(t *testing.T) {
	a, env := engineSetupFixture(t)
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(source, "auth.json"), []byte(`{"OPENAI_API_KEY":"synthetic-api-key"}`), 0600)
	os.WriteFile(filepath.Join(source, "config.toml"), []byte("model=\"fixture\"\n"), 0600)
	t.Setenv("CODEX_HOME", target)
	p := EngineCredentialProfile{ID: "sync-source", Name: "Fixture", Engine: "codex", EnvironmentID: env.ID, Kind: "codex_home", Reference: source}
	if err := a.store.saveEngineProfiles([]EngineCredentialProfile{p}); err != nil {
		t.Fatal(err)
	}
	if err := a.store.activateEngineProfile(env.ID, "codex", p.ID); err != nil {
		t.Fatal(err)
	}
	request := toolsClient(t, a)
	raw := request("/api/account-sync", "POST", CodexSyncRequest{SourceProfileID: p.ID, EnvironmentIDs: []string{env.ID}}, 200)
	if strings.Contains(string(raw), "synthetic-api-key") || !strings.Contains(string(raw), `"state":"done"`) {
		t.Fatal("bad public result", string(raw))
	}
	got, _ := os.ReadFile(filepath.Join(target, "auth.json"))
	if !strings.Contains(string(got), "synthetic-api-key") {
		t.Fatal("target not updated")
	}
	if a.store.activeEngineProfile(env.ID, "codex") != "" || a.store.nativeAccountRevision(env.ID, "codex") == "" {
		t.Fatal("sync did not select native environment account")
	}
	// A queued task prevents writing the shared native credential files.
	task, err := a.createWithExecution("queued sync guard", env.Workspaces[0], "", "codex", "", env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES('guard',?,'synthetic','chat','web','queued',?)", task.ID, now()); err != nil {
		t.Fatal(err)
	}
	raw = request("/api/account-sync", "POST", CodexSyncRequest{SourceProfileID: p.ID, EnvironmentIDs: []string{env.ID}}, 200)
	if !strings.Contains(string(raw), `"state":"failed"`) || !strings.Contains(string(raw), "排队任务") {
		t.Fatal("busy account sync was not blocked", string(raw))
	}
}

func TestLimitedAccountOutput(t *testing.T) {
	b := &limitedBuffer{limit: 4}
	if _, err := b.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("5")); err == nil || b.String() != "1234" {
		t.Fatal("output limit bypassed")
	}
}
