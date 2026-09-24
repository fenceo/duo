package main

import (
	"encoding/json"
	"golang.org/x/crypto/bcrypt"
	"os"
	"strings"
	"testing"
)

func TestPortableInitDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	c := Config{Listen: "127.0.0.1:8789", DefaultEnvironment: "windows", Environments: []Environment{windowsEnvironment()}}
	raw, _ := json.Marshal(map[string]any{"password": "test-password", "config": c})
	if e := initializePortable(dir, strings.NewReader(string(raw))); e != nil {
		t.Fatal(e)
	}
	s, e := openStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	encoded := s.setting("password_hash")
	s.Close()
	if bcrypt.CompareHashAndPassword([]byte(encoded), []byte("test-password")) != nil {
		t.Fatal("password missing")
	}
	if e = initializePortable(dir, strings.NewReader(string(raw))); e == nil {
		t.Fatal("overwrote initialized data")
	}
	stored, e := loadConfig(dir)
	if e != nil || stored.get().Environments[0].Type != "windows" || stored.get().Feishu.Enabled {
		t.Fatal("unexpected defaults", e)
	}
}

func TestPortableInitAcceptsMinimalConfig(t *testing.T) {
	dir := t.TempDir()
	raw, _ := json.Marshal(map[string]any{"password": "test-password", "config": map[string]any{}})
	if err := initializePortable(dir, strings.NewReader(string(raw))); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.get()
	if got.Listen != "127.0.0.1:8789" {
		t.Fatalf("minimal setup should bind locally, got %q", got.Listen)
	}
	if got.DefaultEnvironment != "default" || len(got.Environments) != 1 {
		t.Fatalf("minimal setup should create one default environment: %+v", got)
	}
	env := got.Environments[0]
	if env.Type != "windows" || env.Codex == "" || len(env.Workspaces) != 1 || env.Workspaces[0] == "" {
		t.Fatalf("minimal setup produced incomplete environment: %+v", env)
	}
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if bcrypt.CompareHashAndPassword([]byte(s.setting("password_hash")), []byte("test-password")) != nil {
		t.Fatal("minimal setup did not save password hash")
	}
}

func TestNormalizeEnvironmentFillsSafeDefaults(t *testing.T) {
	c := Config{Listen: "127.0.0.1:8789", DefaultEnvironment: "win", Environments: []Environment{{ID: "win", Type: "windows"}}}
	if err := normalizeEnvironments(&c); err != nil {
		t.Fatal(err)
	}
	if c.Environments[0].Name == "" || c.Environments[0].Codex == "" || len(c.Environments[0].Workspaces) != 1 {
		t.Fatalf("defaults not filled: %+v", c.Environments[0])
	}
}

func TestDataDirectoryLock(t *testing.T) {
	dir := t.TempDir()
	unlock, e := lockData(dir)
	if e != nil {
		t.Fatal(e)
	}
	another, e := lockData(dir)
	if e == nil {
		another()
		unlock()
		t.Fatal("accepted concurrent owner")
	}
	unlock()
	unlock, e = lockData(dir)
	if e != nil {
		t.Fatal(e)
	}
	unlock()
	if _, e = os.Stat(dir); e != nil {
		t.Fatal(e)
	}
}
