package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAccountRenamePreservesRouteAndRejectsStaleEdits(t *testing.T) {
	a, env := engineSetupFixture(t)
	do := toolsClient(t, a)
	home := t.TempDir()
	file := filepath.Join(home, "auth.json")
	original := []byte(`{"OPENAI_API_KEY":"synthetic-only"}`)
	if err := os.WriteFile(file, original, 0600); err != nil {
		t.Fatal(err)
	}
	profile := EngineCredentialProfile{ID: "fixture", Name: "Before", Engine: "codex", EnvironmentID: env.ID, Kind: "codex_home", Reference: home, Created: 10, Updated: 20}
	if err := a.store.saveEngineProfiles([]EngineCredentialProfile{profile}); err != nil {
		t.Fatal(err)
	}
	if err := a.store.activateEngineProfile(env.ID, "codex", profile.ID); err != nil {
		t.Fatal(err)
	}
	do("/api/engine-profiles/fixture", "PATCH", map[string]any{"name": "Renamed", "expected_updated": 20, "reference": "must-not-change"}, 200)
	updated := a.store.engineProfiles()[0]
	if updated.Name != "Renamed" || updated.Reference != home || updated.Created != 10 || updated.Updated <= 20 || a.store.activeEngineProfile(env.ID, "codex") != "fixture" {
		t.Fatal("rename changed routing/default or lost revision")
	}
	do("/api/engine-profiles/fixture", "PATCH", map[string]any{"name": "Stale", "expected_updated": 20}, 409)
	if a.store.engineProfiles()[0].Name != "Renamed" {
		t.Fatal("stale rename overwritten")
	}
	do("/api/engine-profiles/fixture", "DELETE", nil, 200)
	if len(a.store.engineProfiles()) != 0 || a.store.activeEngineProfile(env.ID, "codex") != "" {
		t.Fatal("removed reference remains active")
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != string(original) {
		t.Fatal("reference removal touched native account files")
	}
}
