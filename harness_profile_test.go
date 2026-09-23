package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Switching an environment's active account applies to future sessions, not to
// the live SDK process that already owns an existing conversation.
func TestHarnessLiveSessionKeepsOriginalProfileAfterAccountSwitch(t *testing.T) {
	c, task := harnessFixtureSetup(t, "")
	profileA, profileB := t.TempDir(), t.TempDir()
	c.EngineEnv = map[string]string{"DSH_HOME": profileA}
	emit := func(string, string) {}
	session, result, err := harnessFixtureRun(t, c, task, "profile A first turn", emit)
	if err != nil {
		t.Fatalf("first turn failed: %v", err)
	}
	var first harnessFixtureResult
	if err := json.Unmarshal([]byte(result), &first); err != nil {
		t.Fatal(err)
	}
	harnessRuntimes.Lock()
	original := harnessRuntimes.workers[session]
	harnessRuntimes.Unlock()
	if original == nil || original.c.EngineEnv["DSH_HOME"] != profileA {
		t.Fatal("first runtime did not retain profile A")
	}

	task.Session = session
	c.EngineEnv = map[string]string{"DSH_HOME": profileB}
	continued, result, err := harnessFixtureRun(t, c, task, "continue after switching defaults to B", emit)
	if err != nil {
		t.Fatalf("switching the default account broke a live session: %v", err)
	}
	var second harnessFixtureResult
	if err := json.Unmarshal([]byte(result), &second); err != nil {
		t.Fatal(err)
	}
	harnessRuntimes.Lock()
	current := harnessRuntimes.workers[session]
	harnessRuntimes.Unlock()
	if continued != session || current != original || second.PID != first.PID || second.Turn != 2 {
		t.Fatalf("profile switch replaced the conversation: session=%q worker=%p original=%p turn=%d pid=%d previous=%d", continued, current, original, second.Turn, second.PID, first.PID)
	}
	if current.c.EngineEnv["DSH_HOME"] != profileA || c.EngineEnv["DSH_HOME"] != profileB {
		t.Fatal("continuation rewrote the worker profile or the caller's new default")
	}

	changedWorkspace := task
	changedWorkspace.Workspace = t.TempDir()
	if _, _, err := harnessFixtureRun(t, c, changedWorkspace, "invalid workspace switch", emit); err == nil || !strings.Contains(err.Error(), "不能更换") {
		t.Fatalf("ignoring a profile switch also ignored a workspace change: %v", err)
	}
	changedBinary := c
	changedBinary.Harness += ".different"
	if _, _, err := harnessFixtureRun(t, changedBinary, task, "invalid engine switch", emit); err == nil || !strings.Contains(err.Error(), "不能更换") {
		t.Fatalf("ignoring a profile switch also ignored an executable change: %v", err)
	}

	// A genuinely new task still picks up the new account rather than inheriting
	// another task's worker or credentials.
	newTask := task
	newTask.ID += "-new-account"
	newTask.Session = ""
	newSession, _, err := harnessFixtureRun(t, c, newTask, "new task uses profile B", emit)
	if err != nil {
		t.Fatalf("new profile session failed: %v", err)
	}
	harnessRuntimes.Lock()
	newWorker := harnessRuntimes.workers[newSession]
	harnessRuntimes.Unlock()
	if newSession == session || newWorker == nil || newWorker == original || newWorker.c.EngineEnv["DSH_HOME"] != profileB {
		t.Fatal("new task did not use a distinct profile B runtime")
	}
}
