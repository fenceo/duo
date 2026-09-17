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
