package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func harnessMCPFixture(t *testing.T) (*HardwareAI, string) {
	t.Helper()
	ai := newHardwareAI()
	s := &Server{app: &App{hardwareAI: ai}}
	server := httptest.NewServer(http.HandlerFunc(s.hardwareMCP))
	t.Cleanup(server.Close)
	return ai, server.URL + "/mcp/hardware"
}

func TestHarnessMCPUsesFreshRunAuthorizationAndSameNativeHistory(t *testing.T) {
	c, task := harnessFixtureSetup(t, "")
	ai, address := harnessMCPFixture(t)
	run := func(input string) harnessFixtureResult {
		session, result, err := harnessFixtureRun(t, c, task, input, func(string, string) {})
		var got harnessFixtureResult
		if err != nil || json.Unmarshal([]byte(result), &got) != nil || session == "" || (task.Session != "" && session != task.Session) {
			t.Fatalf("MCP turn failed: %v %s", err, result)
		}
		task.Session = session
		return got
	}
	plain := run("plain")
	for i, input := range []string{"granted", "renewed"} {
		token, release := ai.issue(context.Background(), task.ID, input, nil)
		c.HardwareAI = &HardwareRuntime{URL: address, Token: token}
		got := run(input)
		if got.PID == plain.PID || got.Turn != i+2 || len(got.MCPServers) != 1 {
			t.Fatal("MCP did not reopen the native session with original history")
		}
		mcp := got.MCPServers[0]
		if mcp.Type != "http" || mcp.Name != "duo_hardware" || mcp.URL != address || len(mcp.Headers) != 1 || mcp.Headers[0].Name != "Authorization" || mcp.Headers[0].Value != "Bearer "+token {
			t.Fatal("fresh ACP MCP declaration was not sent")
		}
		if strings.Contains(strings.Join(got.Args, " "), token) {
			t.Fatal("MCP credential entered argv")
		}
		patch, _ := json.Marshal(got.Patches)
		if strings.Contains(string(patch), token) {
			t.Fatal("MCP credential entered policy file")
		}
		harnessRuntimes.Lock()
		cached := harnessRuntimes.workers[task.Session]
		harnessRuntimes.Unlock()
		if cached != nil {
			t.Fatal("per-run MCP worker remained cached after completion")
		}
		release()
		if err := probeHardwareMCP(context.Background(), c.HardwareAI); err == nil {
			t.Fatal("released credential still admitted by Duo")
		}
		plain = got
	}
	c.HardwareAI = nil
	without := run("revoked")
	if without.Turn != 4 || without.PID == plain.PID || len(without.MCPServers) != 0 || strings.Join(without.History, ",") != "plain,granted,renewed,revoked" {
		t.Fatal("revoked tools or previous context leaked into resumed session")
	}
}

func TestHarnessMCPPreflightAndUnsupportedTransportNeverSendPrompt(t *testing.T) {
	for _, scenario := range []string{"expired", "no-http-mcp", "mcp-mount-failure"} {
		t.Run(scenario, func(t *testing.T) {
			c, task := harnessFixtureSetup(t, scenario)
			ai, address := harnessMCPFixture(t)
			token, release := ai.issue(context.Background(), task.ID, "synthetic-run", nil)
			defer release()
			if scenario == "expired" {
				release()
			}
			c.HardwareAI = &HardwareRuntime{URL: address, Token: token}
			_, _, err := harnessFixtureRun(t, c, task, "must-not-run", func(string, string) {})
			if err == nil {
				t.Fatal("unavailable MCP silently fell back to a model request")
			}
			entries, _ := os.ReadDir(task.Workspace)
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "fixture-") {
					data, _ := os.ReadFile(filepath.Join(task.Workspace, entry.Name()))
					if strings.Contains(string(data), "must-not-run") {
						t.Fatal("prompt was sent before MCP readiness")
					}
				}
			}
		})
	}
}

func TestHarnessMCPCancellationRevokesLeaseAndResumesOnce(t *testing.T) {
	c, task := harnessFixtureSetup(t, "wait")
	ai, address := harnessMCPFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	token, release := ai.issue(ctx, task.ID, "cancelled-run", nil)
	defer release()
	c.HardwareAI = &HardwareRuntime{URL: address, Token: token}
	ready, done := make(chan struct{}, 1), make(chan struct{})
	var session string
	var runErr error
	go func() {
		defer close(done)
		session, _, runErr = runHarnessACP(ctx, c, task, "wait", func(kind, text string) {
			if kind == "tool" && strings.Contains(text, "wait") {
				select {
				case ready <- struct{}{}:
				default:
				}
			}
		})
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("synthetic MCP prompt did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("MCP cancellation did not close the worker")
	}
	if runErr == nil || session == "" || ai.lease(token) != nil {
		t.Fatal("cancelled run retained authorization")
	}
	task.Session = session
	token, releaseNext := ai.issue(context.Background(), task.ID, "next-run", nil)
	defer releaseNext()
	c.HardwareAI.Token = token
	continued, result, err := harnessFixtureRun(t, c, task, "after-cancel", func(string, string) {})
	var got harnessFixtureResult
	if err != nil || json.Unmarshal([]byte(result), &got) != nil || continued != session || strings.Join(got.History, ",") != "wait,after-cancel" {
		t.Fatal("cancellation lost native context or replayed a prompt", err)
	}
}
