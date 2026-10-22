package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Only the owned test binary enters this fixture. It reads one ACP request and
// neither loads a native Harness installation nor contacts a model provider.
func init() {
	scenario := os.Getenv("DUO_TEST_HARNESS_STARTUP_FIXTURE")
	if (scenario != "wait" && scenario != "exit") || len(os.Args) < 3 || os.Args[1] != "--profile" || os.Args[2] != "acp" {
		return
	}
	var request struct {
		Method string `json:"method"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil || request.Method != "initialize" {
		fmt.Fprintln(os.Stderr, "synthetic Harness expected initialize")
		os.Exit(38)
	}
	if scenario == "exit" {
		fmt.Fprintln(os.Stderr, "synthetic Harness startup rejected")
		// Mirror the startup diagnostic on stdout so its scan observes it
		// before EOF; stderr and stdout readers otherwise finish concurrently.
		fmt.Fprintln(os.Stdout, "synthetic Harness startup rejected")
		os.Exit(37)
	}
	fmt.Fprintln(os.Stderr, "synthetic Harness waiting for initialization")
	for {
		time.Sleep(time.Hour)
	}
}

func TestHarnessStartupUsesEffectiveHandshakeTimeout(t *testing.T) {
	c, task := harnessFixtureSetup(t, "")
	t.Setenv("DUO_TEST_HARNESS_STARTUP_FIXTURE", "wait")
	// The internal override keeps the real-process test fast. Production probe
	// input validation separately requires a timeout of at least 60 seconds.
	parent := context.WithValue(context.Background(), modelProbeOptionsKey{}, modelProbeOptions{mode: "latency", seconds: 1})
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	started := time.Now()
	worker, err := startHarness(ctx, c, task, func(string, string) {})
	elapsed := time.Since(started)
	if worker != nil {
		_ = worker.stop()
		t.Fatal("silent startup unexpectedly completed")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("startup error = %v, want deadline exceeded", err)
	}
	if !strings.Contains(err.Error(), "Harness ACP 握手超时") || !strings.Contains(err.Error(), "最多等待 1s") || strings.Contains(err.Error(), "60 秒") {
		t.Fatalf("timeout did not report the effective wait: %v", err)
	}
	if elapsed < 900*time.Millisecond || elapsed >= 8*time.Second {
		t.Fatalf("startup waited %s, want the selected one-second budget plus process cleanup", elapsed)
	}
}

func TestHarnessStartupPreservesChildFailureBeforeTimeout(t *testing.T) {
	c, task := harnessFixtureSetup(t, "")
	t.Setenv("DUO_TEST_HARNESS_STARTUP_FIXTURE", "exit")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	worker, err := startHarness(ctx, c, task, func(string, string) {})
	if worker != nil {
		_ = worker.stop()
		t.Fatal("rejected startup unexpectedly completed")
	}
	if err == nil || !strings.Contains(err.Error(), "synthetic Harness startup rejected") {
		t.Fatalf("startup lost the child diagnostic: %v", err)
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "握手超时") {
		t.Fatalf("child startup failure was reported as a timeout: %v", err)
	}
}
