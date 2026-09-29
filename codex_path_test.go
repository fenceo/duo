package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedCodexPathRecoversDesktopUpdates(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	root := filepath.Join(local, "OpenAI", "Codex", "bin")
	old := filepath.Join(root, "0123456789abcdef", "codex.exe")
	current := filepath.Join(root, "fedcba9876543210", "codex.exe")
	if err := os.MkdirAll(filepath.Dir(current), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, []byte("synthetic executable, never run"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := command(Config{Codex: old}, old, "app-server"); got.Path != current || len(got.Args) != 2 || got.Args[1] != "app-server" {
		t.Fatal("stale task path was not resolved", got)
	}
	for _, path := range []string{"codex", filepath.Join(local, "custom", "codex.exe"), filepath.Join(root, "not-a-version", "codex.exe"), filepath.Join(root, "0123456789abcdef", "other.exe")} {
		if got := resolveManagedCodexPath(path); got != path {
			t.Fatal("changed explicit executable", path, got)
		}
	}
	for _, c := range []Config{{Codex: old, Distro: "SyntheticLinux"}, {Codex: old, SSHHost: "fixture.invalid"}} {
		cmd := command(c, old, "app-server")
		if strings.Contains(strings.Join(cmd.Args, " "), current) {
			t.Fatal("remote path resolved on host")
		}
	}
	if err := os.MkdirAll(filepath.Dir(old), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("retained version"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := resolveManagedCodexPath(old); got != old {
		t.Fatal("overrode existing pinned executable")
	}
}
