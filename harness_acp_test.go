package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestHarnessModelEditPreservesSessionAndClearsOldEffort(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := createHarnessStoredTask(t, a)
	_, _ = a.store.Exec("UPDATE tasks SET session='durable-session' WHERE id=?", task.ID)
	_, _ = a.store.Exec("UPDATE task_execution SET reasoning_effort='max' WHERE task_id=?", task.ID)
	request := toolsClient(t, a)
	request("/api/tasks/"+task.ID, "PATCH", map[string]any{"model": "new-model"}, 200)
	updated, err := a.store.task(task.ID)
	if err != nil || updated.Session != "durable-session" || updated.Model != "new-model" || updated.ReasoningEffort != "" {
		t.Fatalf("model edit damaged session or retained stale effort: %#v %v", updated, err)
	}
	request("/api/tasks/"+task.ID, "PATCH", map[string]any{"model": "must-not-commit", "reasoning_effort": "invalid"}, 409)
	unchanged, _ := a.store.task(task.ID)
	if unchanged.Model != updated.Model {
		t.Fatal("invalid combined edit partially committed")
	}
	for _, status := range []string{"running", "queued"} {
		_, _ = a.store.Exec("UPDATE tasks SET status=? WHERE id=?", status, task.ID)
		request("/api/tasks/"+task.ID, "PATCH", map[string]any{"model": "busy-edit"}, 409)
	}
	_, _ = a.store.Exec("UPDATE tasks SET status='done' WHERE id=?", task.ID)
	// Cold persisted tasks remain sendable; only the native resume operation can
	// establish whether their log is present. An absent process is not a failure.
	request("/api/tasks/"+task.ID+"/messages", "POST", map[string]any{"content": "continue the cold task"}, 202)
}

func TestHarnessACPChunksAndForeignSessions(t *testing.T) {
	state := harnessACPTurn{session: "root"}
	var output []string
	consume := func(session, kind string, content any) {
		raw, _ := json.Marshal(map[string]any{"sessionId": session, "update": map[string]any{"sessionUpdate": kind, "content": content}})
		state.consume(codexRPC{Method: "session/update", Params: raw}, func(kind, text string) { output = append(output, kind+":"+text) })
	}
	consume("foreign", "agent_message_chunk", map[string]string{"type": "text", "text": "secret"})
	consume("root", "agent_message_chunk", map[string]string{"type": "text", "text": "hello "})
	consume("root", "agent_thought_chunk", map[string]string{"type": "text", "text": "thinking"})
	consume("root", "agent_message_chunk", map[string]string{"type": "text", "text": "world"})
	if state.result != "hello world" || strings.Contains(strings.Join(output, "|"), "secret") {
		t.Fatalf("chunk/session isolation failed: %q %v", state.result, output)
	}
	consume("root", "tool_call", nil)
	consume("root", "agent_message_chunk", map[string]string{"type": "text", "text": "final answer"})
	if state.result != "final answer" {
		t.Fatal("tool preamble contaminated final result")
	}
}

func TestHarnessACPDoesNotGrantClientPrivileges(t *testing.T) {
	for _, method := range []string{"session/request_permission", "fs/write_text_file", "terminal/create"} {
		w := harnessIOWorker()
		writer := &harnessErrorCaptureWriter{}
		w.in = writer
		if err := w.rejectClientRequest(context.Background(), codexRPC{ID: json.RawMessage(`"client-1"`), Method: method}); err != nil {
			t.Fatal(err)
		}
		var response struct {
			ID     string
			Result struct{ Outcome struct{ Outcome string } }
			Error  *struct{ Code int }
		}
		if err := json.Unmarshal(writer.Bytes(), &response); err != nil || response.ID != "client-1" {
			t.Fatalf("bad client response: %s %v", writer.String(), err)
		}
		if method == "session/request_permission" {
			if response.Result.Outcome.Outcome != "cancelled" {
				t.Fatal("unexpected approval")
			}
		} else if response.Error == nil || response.Error.Code != -32601 {
			t.Fatal("unsupported client operation was granted")
		}
	}
}

func TestHarnessACPModelSwitchAndResumeKeepWholeHistory(t *testing.T) {
	c, task := harnessFixtureSetup(t, "")
	noop := func(string, string) {}
	session, _, err := harnessFixtureRun(t, c, task, "first secret marker", noop)
	if err != nil {
		t.Fatal(err)
	}
	task.Session = session
	task.Model = "second-model"
	task.ReasoningEffort = ""
	_, result, err := harnessFixtureRun(t, c, task, "second", noop)
	var live harnessFixtureResult
	if err != nil || json.Unmarshal([]byte(result), &live) != nil || live.Initialize["model"] != "second-model" || live.Initialize["reasoningEffort"] != nil {
		t.Fatalf("model/effort switch failed: %s %v", result, err)
	}
	if err := closeIdleHarnessSession(session); err != nil {
		t.Fatal(err)
	}
	resumed, result, err := harnessFixtureRun(t, c, task, "third", noop)
	var cold harnessFixtureResult
	if err != nil || json.Unmarshal([]byte(result), &cold) != nil || resumed != session || cold.PID == live.PID || strings.Join(cold.History, "|") != "first secret marker|second|third" {
		t.Fatalf("resume discarded history: %s %v", result, err)
	}
}

func TestHarnessACPWaitsForColdProviderWithoutReplayingPrompt(t *testing.T) {
	c, task := harnessFixtureSetup(t, "cold-provider")
	_, result, err := harnessFixtureRun(t, c, task, "only one prompt", func(string, string) {})
	var actual harnessFixtureResult
	if err != nil || json.Unmarshal([]byte(result), &actual) != nil || len(actual.History) != 1 || actual.History[0] != "only one prompt" {
		t.Fatalf("cold provider readiness failed or repeated a prompt: %s %v", result, err)
	}
}
