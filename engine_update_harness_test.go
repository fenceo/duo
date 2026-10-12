package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEngineUpdateHarnessKeepsVisionMCPAndMemoryContract(t *testing.T) {
	c, task := harnessFixtureSetup(t, "attachments")
	originalProfile := t.TempDir()
	c.EngineEnv = map[string]string{"DSH_HOME": originalProfile}
	env := Environment{ID: "updated-harness", Type: "windows", Harness: c.Harness}
	task.Environment = &env
	c.Environments = []Environment{env}
	task.Files = []RuntimeAttachment{{Attachment: Attachment{Name: "history.md", Mime: "text/plain"}, Data: []byte("before-update-attachment")}}
	runIDs := []string{uid(), uid(), uid(), uid()}
	run := func(turn int, input string) harnessFixtureResult {
		t.Helper()
		// A copied test executable has a cold Windows startup. This remains a
		// bounded, synthetic ACP exchange; no installed CLI or model is used.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		next, result, err := runHarnessACP(ctx, c, task, memoryPrompt(runIDs[turn])+input, func(string, string) {})
		var got harnessFixtureResult
		if err != nil || json.Unmarshal([]byte(result), &got) != nil || next == "" || (task.Session != "" && next != task.Session) {
			t.Fatalf("updated Harness turn failed: %v %s", err, result)
		}
		task.Session = next
		if got.Turn != turn+1 || !strings.Contains(got.Input, memoryMarker(runIDs[turn])) {
			t.Fatal("update changed native turn count or dropped current memory contract")
		}
		return got
	}
	first := run(0, "Before updating the CLI.")

	data, err := os.ReadFile(c.Harness)
	if err != nil {
		t.Fatal(err)
	}
	updatedPath := filepath.Join(t.TempDir(), "updated-harness-fixture.exe")
	if err = os.WriteFile(updatedPath, data, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeHarnessRuntimes)
	c.Environments[0].ManagedTools = map[string]ManagedEngineTool{"deepseek-harness": {Path: updatedPath, Version: "1.2.3", PreviousPaths: []string{c.Harness}}}
	c.Environments[0].Harness = updatedPath
	c = runtimeConfig(c, *task.Environment)
	if c.Harness != updatedPath || task.Environment.Harness == updatedPath {
		t.Fatal("old task snapshot did not resolve the verified replacement independently")
	}
	c.EngineEnv = map[string]string{"DSH_HOME": t.TempDir()}
	task.Model = "fixture-vision"
	task.Files = []RuntimeAttachment{{Attachment: Attachment{Name: "image.png", Mime: "image/png"}, Data: []byte("fixture image bytes")}}
	second := run(1, "Read the image after upgrading.")
	if second.PID == first.PID || len(second.Prompt) != 2 || second.Prompt[1].Type != "image" || !strings.Contains(second.History[0], "before-update-attachment") || !strings.Contains(second.History[0], memoryMarker(runIDs[0])) {
		t.Fatal("CLI replacement lost negotiated image blocks or native attachment/memory history")
	}
	harnessRuntimes.Lock()
	worker := harnessRuntimes.workers[task.Session]
	harnessRuntimes.Unlock()
	if worker == nil || worker.c.Harness != updatedPath || worker.c.EngineEnv["DSH_HOME"] != originalProfile {
		t.Fatal("replacement kept the old executable or changed the original native account profile")
	}

	ai, address := harnessMCPFixture(t)
	for turn := 2; turn < 4; turn++ {
		token, release := ai.issue(context.Background(), task.ID, runIDs[turn], nil)
		c.HardwareAI = &HardwareRuntime{URL: address, Token: token}
		got := run(turn, "Use this run's freshly granted tools.")
		release()
		if len(got.MCPServers) != 1 || len(got.MCPServers[0].Headers) != 1 || got.MCPServers[0].Headers[0].Value != "Bearer "+token || got.MCPServers[0].URL != address {
			t.Fatal("updated CLI did not receive current per-run MCP authorization")
		}
		if got.PID == second.PID || len(got.Prompt) != 2 || got.Prompt[1].Type != "image" || !strings.Contains(got.History[1], "[fixture native image]") || !strings.Contains(got.History[0], "before-update-attachment") {
			t.Fatal("MCP reopening after CLI update lost image capability or native history")
		}
		patch, _ := json.Marshal(got.Patches)
		if strings.Contains(strings.Join(got.Args, " "), token) || strings.Contains(string(patch), token) {
			t.Fatal("updated CLI persisted the per-run MCP credential in argv or policy")
		}
		harnessRuntimes.Lock()
		cached := harnessRuntimes.workers[task.Session]
		harnessRuntimes.Unlock()
		if cached != nil || ai.lease(token) != nil {
			t.Fatal("completed updated CLI turn retained MCP worker or authorization")
		}
		second = got
	}
}

func TestEngineUpdateHarnessOnlyRelaxesVerifiedExecutableChange(t *testing.T) {
	old, task := harnessFixtureSetup(t, "")
	env := Environment{ID: "updated-harness", Type: "windows", Harness: old.Harness}
	task.Environment = &env
	old.Environments = []Environment{env}
	fingerprint := harnessFingerprint(old, task)
	updated := old
	updated.Harness = filepath.Join(task.Workspace, "verified-new-cli.exe")
	updated.Environments = []Environment{env}
	updated.Environments[0].Harness = updated.Harness
	updated.Environments[0].ManagedTools = map[string]ManagedEngineTool{"deepseek-harness": {Path: updated.Harness, PreviousPaths: []string{old.Harness}}}
	if !managedHarnessReplacement(updated, task, old, fingerprint) {
		t.Fatal("verified executable-only change rejected")
	}
	for _, change := range []string{"workspace", "permissions", "summary-tools", "ssh", "profile", "unverified"} {
		t.Run(change, func(t *testing.T) {
			c, changed := updated, task
			switch change {
			case "workspace":
				changed.Workspace = filepath.Join(task.Workspace, "different")
			case "permissions":
				mode := *task.Mode
				mode.Permission = "full"
				changed.Mode = &mode
			case "summary-tools":
				changed.NoTools = true
			case "ssh":
				c.SSHHost = "different.invalid"
			case "profile":
				c.EngineEnv = map[string]string{"DSH_HOME": "different-profile"}
			case "unverified":
				c.Environments = []Environment{env}
			}
			if managedHarnessReplacement(c, changed, old, fingerprint) {
				t.Fatal("verified update also authorized an unrelated native-session change")
			}
		})
	}
}
