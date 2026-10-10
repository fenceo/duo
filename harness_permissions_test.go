package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"
)

const harnessPermissionTestParams = `{"sessionId":"native-session","toolCall":{"toolCallId":"call-1","title":"bash","rawInput":{"command":"echo synthetic","sandbox_permissions":"danger-full-access","justification":"fixture"}},"options":[{"optionId":"native-allow","kind":"allow_once"},{"optionId":"native-reject","kind":"reject_once"},{"optionId":"permanent","kind":"allow_always"}]}`

func TestHarnessOfficialWorkspacePolicyAndLegacyPreservation(t *testing.T) {
	for _, test := range []struct {
		mode *WorkMode
		want string
	}{
		{&WorkMode{ID: harnessWorkspaceMode, Permission: "workspace", Approval: "request"}, "ask"},
		{nil, "never"},
		{&WorkMode{ID: "work", Permission: "workspace", Approval: "request"}, "never"},
		{&WorkMode{ID: "custom", Permission: "workspace", Approval: "request"}, "never"},
		{&WorkMode{ID: "full", Permission: "full", Approval: "never"}, "never"},
		{&WorkMode{ID: "harness:read", Permission: "read", Approval: "never"}, "never"},
	} {
		patch, err := harnessPolicy(Task{Mode: test.mode, Workspace: "/fixture"})
		var rows []struct {
			ID     string `json:"id"`
			Config struct {
				Policy string `json:"policy"`
			} `json:"config"`
		}
		if err != nil || json.Unmarshal(patch, &rows) != nil || rows[1].Config.Policy != test.want {
			t.Fatalf("unexpected native approval policy %s %v", patch, err)
		}
	}
	a := fixture(t, &fakeRunner{})
	mode, err := a.store.resolveMode(harnessWorkspaceMode, nil)
	if err != nil || !harnessInteractiveMode(&mode) {
		t.Fatal("official preset missing", err)
	}
	for _, engine := range []string{"codex", "claude"} {
		if _, err := a.createWithExecutionAndMode("fixture", a.config.get().Workspaces[0], "", engine, "", &mode); err == nil {
			t.Fatal("Harness preset accepted by", engine)
		}
	}
}

func TestHarnessPermissionHTTPIsolationAndNativeOptionMapping(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task, other := taskFor(t, a), taskFor(t, a)
	request := toolsClient(t, a)
	for _, test := range []struct{ decision, expected string }{
		{"accept", `{"outcome":{"outcome":"selected","optionId":"native-allow"}}`},
		{"decline", `{"outcome":{"outcome":"selected","optionId":"native-reject"}}`},
		{"cancel", `{"outcome":{"outcome":"cancelled"}}`},
	} {
		pending, result, _ := beginCodexApproval(t, a, task, harnessPermissionMethod, harnessPermissionTestParams)
		path := "/api/tasks/" + task.ID + "/approvals/" + pending.ID
		request("/api/tasks/"+other.ID+"/approvals/"+pending.ID, "POST", CodexAnswer{Decision: "accept"}, 409)
		request(path, "POST", CodexAnswer{Decision: "acceptForSession"}, 400)
		request(path, "POST", CodexAnswer{Decision: test.decision}, 200)
		got := awaitCodexInteraction(t, result)
		if got.err != nil {
			t.Fatal(got.err)
		}
		requireCodexJSON(t, got.payload, test.expected)
		request(path, "POST", CodexAnswer{Decision: test.decision}, 409)
	}
	pending, result, cancel := beginCodexApproval(t, a, task, harnessPermissionMethod, harnessPermissionTestParams)
	cancel()
	if got := awaitCodexInteraction(t, result); !errors.Is(got.err, context.Canceled) {
		t.Fatal(got.err)
	}
	request("/api/tasks/"+task.ID+"/approvals/"+pending.ID, "POST", CodexAnswer{Decision: "accept"}, 409)
}

func TestHarnessPermissionDetailsMustMatchLiveTool(t *testing.T) {
	state := harnessACPTurn{session: "native-session"}
	update := func(session, kind, status string) {
		raw, _ := json.Marshal(map[string]any{"sessionId": session, "update": map[string]any{"sessionUpdate": kind, "toolCallId": "call-1", "title": "original-bash", "rawInput": map[string]string{"command": "original command"}, "status": status}})
		state.consume(codexRPC{Method: "session/update", Params: raw}, func(string, string) {})
	}
	if _, _, err := state.permissionParams(json.RawMessage(harnessPermissionTestParams)); err == nil {
		t.Fatal("missing tool approved")
	}
	update("foreign", "tool_call", "")
	if _, _, err := state.permissionParams(json.RawMessage(harnessPermissionTestParams)); err == nil {
		t.Fatal("foreign tool approved")
	}
	update("native-session", "tool_call", "")
	params, _, err := state.permissionParams(json.RawMessage(harnessPermissionTestParams))
	parsed, parseErr := parseHarnessPermission(params)
	if err != nil || parseErr != nil || parsed.ToolCall.Title != "original-bash" {
		t.Fatalf("untrusted request replaced tool details: %s %v", params, err)
	}
	update("native-session", "tool_call_update", "completed")
	if _, _, err := state.permissionParams(json.RawMessage(harnessPermissionTestParams)); err == nil {
		t.Fatal("completed tool still actionable")
	}
	for _, raw := range []string{
		`{"sessionId":"native-session","toolCall":{"toolCallId":"call-1"},"options":[]}`,
		`{"sessionId":"s","toolCall":{"toolCallId":"x","title":"bash","rawInput":{}},"options":[{"optionId":"x","kind":"allow_once"},{"optionId":"x","kind":"reject_once"}]}`,
		`{"sessionId":"s","toolCall":{"toolCallId":"x","title":"bash","rawInput":{}},"options":[{"optionId":"x","kind":"allow_once"},{"optionId":"y","kind":"allow_once"}]}`,
	} {
		if _, err := parseHarnessPermission(json.RawMessage(raw)); err == nil {
			t.Fatal("malformed request accepted", raw)
		}
	}
}

func TestHarnessPermissionPromptKeepsReadingAndExpiresOnDisconnect(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	runID := uid()
	if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES(?,?,'test','chat','web','running',?)", runID, task.ID, now()); err != nil {
		t.Fatal(err)
	}
	w := harnessIOWorker()
	w.session, w.interactive = "native-session", true
	reader, writer := io.Pipe()
	w.in = writer
	t.Cleanup(func() { reader.Close(); writer.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withCodexInteraction(ctx, func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		return a.requestCodexInteraction(ctx, task.ID, runID, method, params)
	})
	go func() { io.Copy(io.Discard, reader) }()
	done := make(chan error, 1)
	go func() { _, _, err := w.prompt(ctx, "fixture", func(string, string) {}); done <- err }()
	w.frames <- codexRPC{Method: "session/update", Params: json.RawMessage(`{"sessionId":"native-session","update":{"sessionUpdate":"tool_call","toolCallId":"call-1","title":"bash","rawInput":{"command":"fixture"}}}`)}
	w.frames <- codexRPC{ID: json.RawMessage(`"permission-1"`), Method: "session/request_permission", Params: json.RawMessage(harnessPermissionTestParams)}
	waitUntil(t, func() bool { return len(a.codexRequests.list(task.ID)) == 1 })
	pending := a.codexRequests.list(task.ID)[0]
	close(w.frames) // An unanswered approval cannot block native EOF handling.
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("approval blocked transport disconnect")
	}
	waitUntil(t, func() bool { return len(a.codexRequests.list(task.ID)) == 0 })
	if err := a.answerCodexInteraction(task.ID, pending.ID, CodexAnswer{Decision: "accept"}); !errors.Is(err, errCodexRequestExpired) {
		t.Fatal("disconnected approval remained usable", err)
	}
}
