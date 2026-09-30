package main

import (
	"context"
	"reflect"
	"sync"
	"testing"
)

func TestUnifiedEnvironmentScanPreservesExactTargets(t *testing.T) {
	saved := []Environment{{ID: "win", Type: "windows", Codex: "custom.exe"}, {ID: "dev", Type: "wsl", Distro: "Ubuntu", User: "selected", Codex: "/custom/codex"}, {ID: "remote", Type: "ssh", Host: "fixture.invalid", User: "exact"}}
	before := append([]Environment(nil), saved...)
	candidates := []Environment{{ID: "local", Type: "windows"}, {ID: "ubuntu", Type: "wsl", Distro: "ubuntu"}, {ID: "new", Type: "wsl", Distro: "Debian"}, {ID: "duplicate", Type: "wsl", Distro: "debian"}, {ID: "unexpected", Type: "ssh", Host: "must-not-probe.invalid"}}
	var mu sync.Mutex
	calls := map[string]int{}
	inspect := func(ctx context.Context, env Environment) detectedEnvironment {
		mu.Lock()
		calls[env.ID]++
		mu.Unlock()
		if env.ID == "dev" && (env.User != "selected" || env.Codex != "/custom/codex") {
			t.Error("saved WSL identity replaced by discovery defaults")
		}
		return detectedEnvironment{Environment: env, Codex: detectedTool{Path: env.Codex, State: "configured"}}
	}
	result := scanEngineEnvironments(context.Background(), saved, candidates, inspect, inspect)
	if !reflect.DeepEqual(calls, map[string]int{"win": 1, "dev": 1, "remote": 1, "new": 1}) {
		t.Fatalf("duplicated or unexpected probes: %#v", calls)
	}
	if len(result.Items) != 3 || len(result.Discovered) != 1 || result.Discovered[0].Environment.Distro != "Debian" || result.Items[1].User != "selected" {
		t.Fatalf("unexpected unified results: %#v", result)
	}
	if !reflect.DeepEqual(saved, before) {
		t.Fatal("scan modified saved configuration")
	}
}

func TestUnifiedEnvironmentScanCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	inspect := func(context.Context, Environment) detectedEnvironment {
		t.Error("cancelled scan started a probe")
		return detectedEnvironment{}
	}
	result := scanEngineEnvironments(ctx, []Environment{{ID: "fixture", Type: "ssh"}}, nil, inspect, inspect)
	if len(result.Items) != 1 || result.Items[0].Codex.State != "unknown" || result.Items[0].Mimo.State != "unknown" {
		t.Fatal("cancellation misreported as missing installation")
	}
}
