package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

const codexApprovalTestParams = `{"threadId":"thread-native","turnId":"turn-native","itemId":"item-native","command":"echo test"}`

type codexApprovalTestResult struct {
	payload json.RawMessage
	err     error
}

func beginCodexApproval(t *testing.T, a *App, task Task, method, params string) (CodexPendingRequest, <-chan codexApprovalTestResult, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(a.ctx)
	t.Cleanup(cancel)
	runID := uid()
	if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES(?,?,'test','chat','web','running',?)", runID, task.ID, now()); err != nil {
		t.Fatal(err)
	}
	result := make(chan codexApprovalTestResult, 1)
	go func() {
		payload, err := a.requestCodexInteraction(ctx, task.ID, runID, method, json.RawMessage(params))
		result <- codexApprovalTestResult{payload, err}
	}()
	var pending CodexPendingRequest
	waitUntil(t, func() bool {
		items := a.codexRequests.list(task.ID)
		if len(items) != 1 {
			return false
		}
		pending = items[0]
		return true
	})
	return pending, result, cancel
}

func awaitCodexInteraction(t *testing.T, result <-chan codexApprovalTestResult) codexApprovalTestResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("native request was not released")
		return codexApprovalTestResult{}
	}
}

func requireCodexJSON(t *testing.T, got json.RawMessage, expected string) {
	t.Helper()
	var actualValue, expectedValue any
	if err := json.Unmarshal(got, &actualValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(expected), &expectedValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actualValue, expectedValue) {
		t.Fatalf("payload = %s, want %s", got, expected)
	}
}

func TestCodexApprovalHTTPTaskIsolationAndOneTimeAnswer(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task, other := taskFor(t, a), taskFor(t, a)
	pending, result, _ := beginCodexApproval(t, a, task, "item/commandExecution/requestApproval", codexApprovalTestParams)
	request := toolsClient(t, a)
	base := "/api/tasks/" + task.ID + "/approvals/" + pending.ID
	if len(a.codexRequests.list(other.ID)) != 0 {
		t.Fatal("cross-task request exposed")
	}
	request("/api/tasks/"+other.ID+"/approvals/"+pending.ID, "POST", CodexAnswer{Decision: "accept"}, 409)
	request(base, "POST", CodexAnswer{Decision: "acceptForSession"}, 400)
	if len(a.codexRequests.list(task.ID)) != 1 {
		t.Fatal("invalid answer consumed pending request")
	}
	request(base, "POST", CodexAnswer{Decision: "accept"}, 200)
	got := awaitCodexInteraction(t, result)
	if got.err != nil {
		t.Fatal(got.err)
	}
	requireCodexJSON(t, got.payload, `{"decision":"accept"}`)
	request(base, "POST", CodexAnswer{Decision: "accept"}, 409)
	if len(a.codexRequests.list(task.ID)) != 0 {
		t.Fatal("answered request remains actionable")
	}
}

func TestCodexApprovalHTTPRequiresLoginCSRFAndSameOrigin(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	pending, result, _ := beginCodexApproval(t, a, task, "item/fileChange/requestApproval", codexApprovalTestParams)
	if _, err := a.store.Exec("INSERT INTO sessions VALUES(?,?,?)", hash("approval-test"), "approval-csrf", now()+60000); err != nil {
		t.Fatal(err)
	}
	handler := (&Server{app: a}).Handler()
	for _, tc := range []struct {
		name, cookie, csrf, origin string
		want                       int
	}{
		{"anonymous", "", "approval-csrf", "http://localhost", 401},
		{"missing csrf", "approval-test", "", "http://localhost", 403},
		{"wrong csrf", "approval-test", "wrong", "http://localhost", 403},
		{"foreign origin", "approval-test", "approval-csrf", "https://evil.example", 403},
		{"valid", "approval-test", "approval-csrf", "http://localhost", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://localhost/api/tasks/"+task.ID+"/approvals/"+pending.ID, strings.NewReader(`{"decision":"decline"}`))
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "jianzuo_session", Value: tc.cookie})
			}
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("X-CSRF-Token", tc.csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", res.Code, tc.want, res.Body.String())
			}
		})
	}
	got := awaitCodexInteraction(t, result)
	if got.err != nil {
		t.Fatal(got.err)
	}
	requireCodexJSON(t, got.payload, `{"decision":"decline"}`)
}

func TestCodexApprovalCancellationExpiresRequest(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	pending, result, cancel := beginCodexApproval(t, a, task, "item/fileChange/requestApproval", codexApprovalTestParams)
	cancel()
	if got := awaitCodexInteraction(t, result); !errors.Is(got.err, context.Canceled) {
		t.Fatalf("cancellation = %v", got.err)
	}
	if len(a.codexRequests.list(task.ID)) != 0 {
		t.Fatal("cancelled native request remains visible")
	}
	if err := a.answerCodexInteraction(task.ID, pending.ID, CodexAnswer{Decision: "accept"}); !errors.Is(err, errCodexRequestExpired) {
		t.Fatalf("late approval accepted: %v", err)
	}
}

func TestCodexUnsupportedAndMalformedRequestsFailClosed(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	for _, tc := range []struct{ method, params string }{
		{"future/requestApproval", codexApprovalTestParams},
		{"item/fileChange/requestApproval", `{}`},
		{"item/commandExecution/requestApproval", `{"threadId":"t","turnId":"r"}`},
		{"item/permissions/requestApproval", `not-json`},
		{"item/tool/requestUserInput", strings.Repeat("x", 256*1024+1)},
	} {
		if payload, err := a.requestCodexInteraction(context.Background(), task.ID, "", tc.method, json.RawMessage(tc.params)); err == nil || len(payload) != 0 {
			t.Fatalf("unsafe request %q accepted: %s %v", tc.method, payload, err)
		}
	}
	if len(a.codexRequests.list(task.ID)) != 0 {
		t.Fatal("invalid request became actionable")
	}
	if _, err := handleCodexInteraction(context.Background(), "item/fileChange/requestApproval", json.RawMessage(codexApprovalTestParams)); err == nil {
		t.Fatal("request without interactive handler was approved")
	}
	payload, err := a.requestCodexInteraction(context.Background(), task.ID, "", "mcpServer/elicitation/request", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	requireCodexJSON(t, payload, `{"action":"decline","content":null}`)
}

func TestCodexPermissionAnswerOnlyGrantsOriginalRequestForTurn(t *testing.T) {
	pending := CodexPendingRequest{Method: "item/permissions/requestApproval", Params: json.RawMessage(`{"permissions":{"network":{"enabled":true},"fileSystem":{"read":["/tmp/read"],"write":["/tmp/write"]},"futureRootAccess":true},"scope":"session"}`)}
	var browserAnswer CodexAnswer
	if err := json.Unmarshal([]byte(`{"decision":"accept","permissions":{"fileSystem":{"write":["/"]}},"scope":"session","execPolicyAmendment":["*"]}`), &browserAnswer); err != nil {
		t.Fatal(err)
	}
	response, err := codexAnswerPayload(pending, browserAnswer)
	if err != nil {
		t.Fatal(err)
	}
	requireCodexJSON(t, response, `{"permissions":{"network":{"enabled":true},"fileSystem":{"read":["/tmp/read"],"write":["/tmp/write"]}},"scope":"turn"}`)
	response, err = codexAnswerPayload(pending, CodexAnswer{Decision: "decline"})
	if err != nil {
		t.Fatal(err)
	}
	requireCodexJSON(t, response, `{"permissions":{},"scope":"turn"}`)
	for _, decision := range []string{"acceptForSession", "cancel", "acceptWithExecpolicyAmendment", ""} {
		if _, err = codexAnswerPayload(pending, CodexAnswer{Decision: decision}); err == nil {
			t.Fatalf("unsupported permissions decision %q accepted", decision)
		}
	}
}

func TestCodexCommandApprovalHonorsNativeAvailableDecisions(t *testing.T) {
	pending := CodexPendingRequest{Method: "item/commandExecution/requestApproval", Params: json.RawMessage(`{"availableDecisions":["decline","cancel",{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["git"]}}]}`)}
	for _, answer := range []CodexAnswer{
		{Decision: "accept"}, {Decision: "acceptForSession"}, {Decision: "acceptWithExecpolicyAmendment"},
		{Decision: "decline", Answers: map[string]CodexQuestionAnswer{"q": {Answers: []string{"unexpected"}}}},
	} {
		if _, err := codexAnswerPayload(pending, answer); err == nil {
			t.Fatalf("unsupported command answer accepted: %#v", answer)
		}
	}
	response, err := codexAnswerPayload(pending, CodexAnswer{Decision: "cancel"})
	if err != nil {
		t.Fatal(err)
	}
	requireCodexJSON(t, response, `{"decision":"cancel"}`)
}

func TestCodexUserInputValidatesAnswersAndDoesNotPersistSecret(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	params := `{"threadId":"t","turnId":"r","itemId":"i","questions":[{"id":"secret","isSecret":true,"question":"credential"}]}`
	pending, result, _ := beginCodexApproval(t, a, task, "item/tool/requestUserInput", params)
	for _, answer := range []CodexAnswer{
		{Decision: "accept"}, {},
		{Answers: map[string]CodexQuestionAnswer{"other": {Answers: []string{"not-requested"}}}},
		{Answers: map[string]CodexQuestionAnswer{"secret": {Answers: []string{" "}}}},
		{Answers: map[string]CodexQuestionAnswer{"secret": {Answers: []string{strings.Repeat("x", 16001)}}}},
		{Answers: map[string]CodexQuestionAnswer{"secret": {Answers: make([]string, 11)}}},
		{Answers: map[string]CodexQuestionAnswer{"secret": {Answers: []string{"ok"}}, "extra": {Answers: []string{"unexpected"}}}},
	} {
		if err := a.answerCodexInteraction(task.ID, pending.ID, answer); err == nil {
			t.Fatalf("invalid user answer accepted: %#v", answer)
		}
	}
	const secret = "test-secret-do-not-store-84729"
	if err := a.answerCodexInteraction(task.ID, pending.ID, CodexAnswer{Answers: map[string]CodexQuestionAnswer{"secret": {Answers: []string{secret}}}}); err != nil {
		t.Fatal(err)
	}
	got := awaitCodexInteraction(t, result)
	if got.err != nil || !strings.Contains(string(got.payload), secret) {
		t.Fatalf("answer not delivered to native engine: %s %v", got.payload, got.err)
	}
	events, err := a.store.events(task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if strings.Contains(event.Text, secret) {
			t.Fatal("secret answer persisted in audit events")
		}
	}
}

func TestCodexUserInputRejectsAmbiguousQuestionIDs(t *testing.T) {
	for _, params := range []string{`{"questions":[{"id":""}]}`, `{"questions":[{"id":"q"},{"id":"q"}]}`} {
		pending := CodexPendingRequest{Method: "item/tool/requestUserInput", Params: json.RawMessage(params)}
		answer := CodexAnswer{Answers: map[string]CodexQuestionAnswer{"": {Answers: []string{"value"}}}}
		if strings.Contains(params, `"q"`) {
			answer.Answers = map[string]CodexQuestionAnswer{"q": {Answers: []string{"value"}}, "injected": {Answers: []string{"unrequested"}}}
		}
		if _, err := codexAnswerPayload(pending, answer); err == nil {
			t.Fatalf("ambiguous question IDs accepted: %s", params)
		}
	}
}

type codexApprovalWorkflowRunner struct{}

func (codexApprovalWorkflowRunner) Run(ctx context.Context, _ Config, _ Task, _ string, _ func(string, string)) (string, string, error) {
	_, err := handleCodexInteraction(ctx, "item/commandExecution/requestApproval", json.RawMessage(codexApprovalTestParams))
	return "native-workflow-session", "", err
}

func TestCodexStopCancelsPendingNativeApprovalAndQueue(t *testing.T) {
	a := fixture(t, codexApprovalWorkflowRunner{})
	task := taskFor(t, a)
	if _, err := a.submit(task.ID, "first", "chat", "web"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return len(a.codexRequests.list(task.ID)) == 1 })
	pending := a.codexRequests.list(task.ID)[0]
	if _, err := a.submit(task.ID, "second", "chat", "web"); err != nil {
		t.Fatal(err)
	}
	if err := a.stop(task.ID); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		runs, _ := a.store.runs(task.ID)
		return len(runs) == 2 && runs[0].Status == "interrupted" && runs[1].Status == "interrupted" && len(a.codexRequests.list(task.ID)) == 0
	})
	if err := a.answerCodexInteraction(task.ID, pending.ID, CodexAnswer{Decision: "accept"}); !errors.Is(err, errCodexRequestExpired) {
		t.Fatalf("stopped task accepted stale approval: %v", err)
	}
}
