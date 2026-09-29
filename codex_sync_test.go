package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadCodexAuthValidatesJSONObject(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "auth.json"), []byte(`{"tokens":{"access_token":"secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := readCodexAuth(root)
	if err != nil || !strings.Contains(string(raw), "access_token") {
		t.Fatalf("valid auth.json rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "auth.json"), []byte(`[]`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCodexAuth(root); err == nil {
		t.Fatal("array auth.json accepted")
	}
}

func TestWriteLocalCodexAuthBacksUpAndReplaces(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".codex")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "auth.json")
	old := []byte(`{"account":"old"}`)
	if err := os.WriteFile(target, old, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeLocalCodexAuth(root, []byte(`{"account":"new"}`)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != `{"account":"new"}` {
		t.Fatalf("new auth.json not installed: %q (%v)", got, err)
	}
	backup, err := os.ReadFile(target + ".duo-backup")
	if err != nil || string(backup) != string(old) {
		t.Fatalf("old auth.json was not backed up: %q (%v)", backup, err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, ".auth.json.duo-*")); len(leftovers) != 0 {
		t.Fatal("temporary auth file left behind")
	}
}

func TestRemoteCodexSyncScriptUsesStdinAndNoSecret(t *testing.T) {
	const secret = "test-secret-must-not-be-in-script"
	script := remoteCodexSyncScript()
	if strings.Contains(script, secret) || !strings.Contains(script, "cat >\"$tmp\"") || !strings.Contains(script, "chmod 600") {
		t.Fatalf("remote sync script does not safely consume stdin: %s", script)
	}
}

func TestSyncCodexAuthRejectsUnsupportedEnvironment(t *testing.T) {
	err := syncCodexAuth(context.Background(), Environment{Type: "other"}, []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "不支持") {
		t.Fatalf("unexpected unsupported environment result: %v", err)
	}
}
