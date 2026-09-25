package main

import (
	"context"
	"os"
	"regexp"
	"runtime"
	"testing"
	"time"
)

// This opt-in diagnostic uses the production Windows launcher but only sends
// initialize/shutdown. It never creates a session or submits a model prompt.
func TestHarnessSDKNativeWindowsHandshake(t *testing.T) {
	if runtime.GOOS != "windows" || os.Getenv("DUO_HARNESS_WINDOWS_HANDSHAKE") != "1" {
		t.Skip("opt-in no-prompt Windows SDK diagnostic")
	}
	model := os.Getenv("DUO_HARNESS_MODEL")
	provider := os.Getenv("DUO_HARNESS_PROVIDER")
	safeID := regexp.MustCompile(`^[A-Za-z0-9._:@/+\-]{1,100}$`)
	if !safeID.MatchString(model) || (provider != "" && !safeID.MatchString(provider)) {
		t.Fatal("explicit model and safe route identifiers required")
	}
	c := Config{Harness: os.Getenv("DUO_HARNESS_BINARY"), HarnessProvider: provider}
	if provider != "" {
		c.HarnessModel = model
	}
	task := Task{Workspace: t.TempDir(), Engine: "deepseek-harness", Model: model, Mode: &WorkMode{Permission: "read", Approval: "never", AllowNetwork: boolPtr(true)}}
	if value := os.Getenv("DUO_HARNESS_WORKSPACE"); value != "" {
		task.Workspace = value
	}
	task.ReasoningEffort = os.Getenv("DUO_HARNESS_EFFORT")
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	started := time.Now()
	w, err := startHarness(ctx, c, task, func(string, string) {})
	if err != nil {
		t.Fatalf("native Windows initialize failed (zero prompts): %s", publicProbeError(err))
	}
	defer w.stop()
	if _, err = w.request(ctx, "shutdown", nil); err != nil {
		t.Fatalf("shutdown failed (zero prompts): %s", publicProbeError(err))
	}
	t.Logf("Windows SDK initialize/shutdown passed in %s; zero prompts", time.Since(started).Round(time.Millisecond))
}
