package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHarnessACPPermissionSwitchReopensSameHistory(t *testing.T) {
	c, task := harnessFixtureSetup(t, "")
	var pid int
	var history []string
	for _, mode := range []struct {
		id, permission, approval, sandbox, policy string
	}{
		{harnessWorkspaceMode, "workspace", "request", "workspace-write", "ask"},
		{"full", "full", "never", "danger-full-access", "never"},
		{harnessWorkspaceMode, "workspace", "request", "workspace-write", "ask"},
		{"harness:read", "read", "never", "read-only", "never"},
		{"work", "workspace", "request", "workspace-write", "never"},
	} {
		task.Mode = &WorkMode{ID: mode.id, Permission: mode.permission, Approval: mode.approval}
		input := "turn-" + mode.id
		session, result, err := harnessFixtureRun(t, c, task, input, func(string, string) {})
		var got harnessFixtureResult
		if err != nil || json.Unmarshal([]byte(result), &got) != nil || session == "" || (task.Session != "" && session != task.Session) {
			t.Fatalf("permission switch lost native session: %s %v", result, err)
		}
		history = append(history, input)
		if got.PID == pid || strings.Join(got.History, "|") != strings.Join(history, "|") {
			t.Fatalf("old policy worker reused or history lost: %s", result)
		}
		var sandbox, policy string
		for _, row := range got.Patches {
			config, _ := row["config"].(map[string]any)
			switch row["id"] {
			case "sandbox-policy":
				sandbox, _ = config["mode"].(string)
			case "approval":
				policy, _ = config["policy"].(string)
			}
		}
		harnessRuntimes.Lock()
		worker := harnessRuntimes.workers[session]
		harnessRuntimes.Unlock()
		if sandbox != mode.sandbox || policy != mode.policy || worker == nil || worker.interactive != (mode.policy == "ask") {
			t.Fatalf("permission/approval policy not applied: %s", result)
		}
		task.Session, pid = session, got.PID
	}
	if err := closeIdleHarnessSession(task.Session); err != nil {
		t.Fatal(err)
	}
	_, result, err := harnessFixtureRun(t, c, task, "cold resume", func(string, string) {})
	var got harnessFixtureResult
	if err != nil || json.Unmarshal([]byte(result), &got) != nil || got.Turn != len(history)+1 || got.PID == pid {
		t.Fatalf("cold resume after permission change failed: %s %v", result, err)
	}
}

func TestHarnessACPPermissionSwitchLeavesBusyWorkerUntouched(t *testing.T) {
	c, task := harnessFixtureSetup(t, "")
	session, _, err := harnessFixtureRun(t, c, task, "first", func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	task.Session = session
	harnessRuntimes.Lock()
	worker := harnessRuntimes.workers[session]
	harnessRuntimes.Unlock()
	worker.lease.Lock()
	task.Mode = &WorkMode{ID: "full", Permission: "full", Approval: "never"}
	_, _, err = harnessFixtureRun(t, c, task, "must not interrupt", func(string, string) {})
	worker.lease.Unlock()
	if err == nil || worker.retiring {
		t.Fatal("permission switch changed a busy worker", err)
	}
	select {
	case <-worker.done:
		t.Fatal("busy worker was stopped")
	default:
	}
}

func TestHarnessACPPermissionSwitchCloseFailureDoesNotSendPrompt(t *testing.T) {
	c, task := harnessFixtureSetup(t, "close-error")
	session, _, err := harnessFixtureRun(t, c, task, "first", func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	task.Session = session
	task.Mode = &WorkMode{ID: "full", Permission: "full", Approval: "never"}
	if got, _, err := harnessFixtureRun(t, c, task, "must not be sent", func(string, string) {}); err == nil || got != session {
		t.Fatal("close failure did not preserve session and abort", err)
	}
	_, result, err := harnessFixtureRun(t, c, task, "explicit retry", func(string, string) {})
	var got harnessFixtureResult
	if err != nil || json.Unmarshal([]byte(result), &got) != nil || strings.Join(got.History, "|") != "first|explicit retry" {
		t.Fatalf("failed restart sent/replayed input: %s %v", result, err)
	}
}

type harnessModeQueueProbe struct {
	calls chan Task
	gate  chan struct{}
}

func (f *harnessModeQueueProbe) Run(ctx context.Context, _ Config, task Task, _ string, _ func(string, string)) (string, string, error) {
	f.calls <- task
	select {
	case <-f.gate:
		return task.Session, "synthetic result", nil
	case <-ctx.Done():
		return task.Session, "", ctx.Err()
	}
}

func TestHarnessPermissionSwitchHTTPQueueKeepsEachModeSnapshot(t *testing.T) {
	f := &harnessModeQueueProbe{calls: make(chan Task, 4), gate: make(chan struct{})}
	a := fixture(t, f)
	task := createHarnessStoredTask(t, a)
	if _, err := a.store.Exec("UPDATE tasks SET session='durable-permission-session' WHERE id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	request := toolsClient(t, a)
	await := func(id string) {
		t.Helper()
		select {
		case got := <-f.calls:
			if got.Mode == nil || got.Mode.ID != id || got.Session != "durable-permission-session" {
				t.Fatalf("queued run lost its own permission/session: %#v", got)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("queued run did not start")
		}
	}
	request("/api/tasks/"+task.ID+"/messages", "POST", map[string]any{"content": "active", "mode_id": harnessWorkspaceMode}, 202)
	await(harnessWorkspaceMode)
	request("/api/tasks/"+task.ID+"/messages", "POST", map[string]any{"content": "full round", "mode_id": "full"}, 202)
	request("/api/tasks/"+task.ID+"/messages", "POST", map[string]any{"content": "narrow next round", "mode_id": harnessWorkspaceMode}, 202)
	request("/api/tasks/"+task.ID+"/messages", "POST", map[string]any{"content": "unsupported", "mode_id": "codex:auto"}, 400)
	f.gate <- struct{}{}
	await("full")
	f.gate <- struct{}{}
	await(harnessWorkspaceMode)
	close(f.gate)
	waitUntil(t, func() bool {
		runs, _ := a.store.runs(task.ID)
		return len(runs) == 3 && runs[0].Status == "done" && runs[1].Status == "done" && runs[2].Status == "done"
	})
}

func TestHarnessPermissionSwitchInterruptKeepsQueuedMode(t *testing.T) {
	f := &harnessModeQueueProbe{calls: make(chan Task, 4), gate: make(chan struct{})}
	a := fixture(t, f)
	task := createHarnessStoredTask(t, a)
	if _, err := a.store.Exec("UPDATE tasks SET session='interrupt-permission-session' WHERE id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	await := func(id string) {
		t.Helper()
		select {
		case got := <-f.calls:
			if got.Mode == nil || got.Mode.ID != id || got.Session != "interrupt-permission-session" {
				t.Fatalf("interrupt lost mode or native session: %#v", got)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("interrupt followup did not start")
		}
	}
	first, err := a.submitWithOptions(task.ID, "active", "chat", "web", SubmitOptions{ModeID: harnessWorkspaceMode})
	if err != nil {
		t.Fatal(err)
	}
	await(harnessWorkspaceMode)
	if _, err = a.submitWithOptions(task.ID, "earlier queue", "chat", "web", SubmitOptions{ModeID: harnessWorkspaceMode}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.submitWithOptions(task.ID, "interrupt in full", "chat", "web", SubmitOptions{ModeID: "full", Delivery: "interrupt", ExpectedRunID: first.ID}); err != nil {
		t.Fatal(err)
	}
	await("full")
	f.gate <- struct{}{}
	await(harnessWorkspaceMode)
	close(f.gate)
	waitUntil(t, func() bool {
		runs, _ := a.store.runs(task.ID)
		return len(runs) == 3 && runs[0].Status == "interrupted" && runs[1].Status == "done" && runs[2].Status == "done"
	})
}
