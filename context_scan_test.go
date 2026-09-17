package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSessionStartedReadsCodexUUIDv7(t *testing.T) {
	// The session that answered AHA2_WSL_OK, including its start time.
	if got := sessionStarted("01a0a2dd-08bd-7763-8d86-9ea9924030ee"); got != 1789438789821 {
		t.Fatalf("sessionStarted = %d, want 1789438789821", got)
	}
	for _, id := range []string{"", "opaque-session", "01a0a2dd-08bd-6763-8d86-9ea9924030ee", "01a0a2dd08bd7763"} {
		if got := sessionStarted(id); got != 0 {
			t.Fatalf("sessionStarted(%q) = %d, want 0", id, got)
		}
	}
}

func TestExternalContextFilesNamesForeignInstructions(t *testing.T) {
	dir := t.TempDir()
	if e := os.Mkdir(filepath.Join(dir, ".aha2-context"), 0o755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("rules"), 0o644); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); e != nil {
		t.Fatal(e)
	}
	task := Task{Workspace: dir, Environment: &Environment{Type: "windows"}}
	found := externalContextFiles(context.Background(), task)
	names := make([]string, 0, len(found))
	for _, file := range found {
		names = append(names, file.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{".aha2-context", "AGENTS.md"}) {
		t.Fatalf("found = %v", names)
	}
	clean := externalContextFiles(context.Background(), Task{Workspace: t.TempDir(), Environment: &Environment{Type: "windows"}})
	if len(clean) != 0 {
		t.Fatalf("clean workspace reported %v", clean)
	}
}
