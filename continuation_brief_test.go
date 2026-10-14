package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func seedBriefHistory(t *testing.T, a *App, task Task) {
	t.Helper()
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("brief-run-%d", i)
		result := fmt.Sprintf("recent-reply-%d", i)
		if i == 4 {
			result += "\n" + memoryMarker(id) + `{"summary":"excluded-raw-memory"}` + "<!-- /duo-memory:" + id + " -->"
		}
		if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,created,finished) VALUES(?,?,?,'chat','web','done',?,?,?)", id, task.ID, fmt.Sprintf("recent-request-%d api_key=secret-for-redaction", i), result, i+1, i+2); err != nil {
			t.Fatal(err)
		}
		if err := a.store.event(task.ID, id, "tool", "excluded-tool-log"); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"failed", "knowledge"} {
		status, runKind := "failed", "chat"
		if kind == "knowledge" {
			status, runKind = "done", "knowledge"
		}
		if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,created,finished) VALUES(?,?,?,?,'web',?,'excluded-reply',99,100)", "excluded-"+kind, task.ID, "excluded-"+kind, runKind, status); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		status := "observed"
		if i == 4 {
			status = "stale"
		}
		if _, err := a.store.Exec("INSERT INTO knowledge_entries(id,task_id,title,content,status,source,revision,created,updated) VALUES(?,?,?,?,?,'manual',1,?,?)", fmt.Sprintf("brief-note-%d", i), task.ID, fmt.Sprintf("brief-note-%d", i), fmt.Sprintf("note-content-%d", i), status, i+1, i+1); err != nil {
			t.Fatal(err)
		}
	}
	m := taskMemory{Summary: "selected-memory-summary", Decisions: []string{"selected-decision"}, OpenQuestions: []string{"selected-open-question"}, Insights: []memoryInsight{{Evidence: "excluded-memory-evidence", Conclusion: "excluded-memory-insight"}}}
	raw, _ := json.Marshal(m)
	if _, err := a.store.Exec("INSERT INTO task_memories(run_id,task_id,payload,updated) VALUES('brief-run-4',?,?,1)", task.ID, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Exec("INSERT INTO run_options(run_id,mode,attachments) VALUES('brief-run-4','null','[{\"name\":\"excluded-attachment\"}]')"); err != nil {
		t.Fatal(err)
	}
}

func restartBriefRequest(t *testing.T, a *App, task Task) handoffRequest {
	t.Helper()
	p, err := a.continuationPreview(task.ID, "brief")
	if err != nil {
		t.Fatal(err)
	}
	current, err := a.store.task(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	revision := "legacy"
	if current.Binding != nil {
		revision = current.Binding.Revision
	}
	return handoffRequest{ExpectedBindingRevision: revision, Confirm: true, EnvironmentID: current.Environment.ID, Workspace: current.Workspace, Engine: "codex", Model: current.Model, ReasoningEffort: current.ReasoningEffort, ModeID: "__current__", ProfileID: "__current__", ContextMode: "brief", Fingerprint: p.Fingerprint, RestartSession: true}
}

func TestContinuationBriefSelectsBoundedReadableHistoryOnly(t *testing.T) {
	a, task := switchFixture(t, &switchRunner{})
	seedBriefHistory(t, a, task)
	p, archive, err := a.buildContinuation(task, "brief")
	if err != nil {
		t.Fatal(err)
	}
	if p.Context != archive || utf8.RuneCountInString(archive) > continuationBriefLimit || p.ArchiveBytes != len(archive) || p.Runs != 3 || p.Knowledge != 3 || !p.Truncated {
		t.Fatal("invalid brief metadata", p)
	}
	for _, want := range []string{"selected-memory-summary", "selected-decision", "selected-open-question", "recent-request-4", "recent-reply-2", "note-content-3", "note-content-1"} {
		if !strings.Contains(archive, want) {
			t.Fatal("selected content missing", want)
		}
	}
	for _, excluded := range []string{"excluded-", "recent-request-0", "recent-request-1", "note-content-0", "note-content-4", "secret-for-redaction", task.Session} {
		if strings.Contains(archive, excluded) {
			t.Fatal("excluded/private source in brief", excluded)
		}
	}
	p2, archive2, err := a.buildContinuation(task, "brief")
	if err != nil || p.Fingerprint != p2.Fingerprint || archive != archive2 {
		t.Fatal("unstable brief fingerprint", err)
	}
	// Even very large selected text remains bounded before database hydration.
	if _, err = a.store.Exec("UPDATE runs SET input=?,result=? WHERE id='brief-run-4'", strings.Repeat("新", 20000), strings.Repeat("答", 20000)); err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.Exec("UPDATE knowledge_entries SET content=? WHERE task_id=?", strings.Repeat("知", 20000), task.ID); err != nil {
		t.Fatal(err)
	}
	p, archive, err = a.buildContinuation(task, "brief")
	if err != nil || utf8.RuneCountInString(archive) > continuationBriefLimit || p.Context != archive {
		t.Fatal("large brief escaped cap", err)
	}
	if got, _ := briefContinuationText("-----BEGIN PRIVATE KEY-----\n"+strings.Repeat("PRIVATE", 10000), 1000); strings.Contains(got, "PRIVATE") {
		t.Fatal("truncated private key leaked")
	}
}

func TestContinuationBriefBypassesExcludedHistoryOver64MiB(t *testing.T) {
	a, task := switchFixture(t, &switchRunner{})
	if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,created,finished) VALUES('huge-brief-history',?,'selected request','chat','web','done','selected reply',1,2)", task.ID); err != nil {
		t.Fatal(err)
	}
	payload := "excluded-large-tool:" + strings.Repeat("x", 2<<20)
	for i := 0; i < 33; i++ {
		if err := a.store.event(task.ID, "huge-brief-history", "tool", payload); err != nil {
			t.Fatal(err)
		}
	}
	var size int64
	if err := a.store.QueryRow("SELECT sum(length(text)) FROM events WHERE task_id=?", task.ID).Scan(&size); err != nil || size <= continuationArchiveLimit {
		t.Fatal("fixture does not exceed full archive limit", size, err)
	}
	v := restartBriefRequest(t, a, task)
	result, err := a.switchTaskEngine(task.ID, v)
	if err != nil || !result.NewSession {
		t.Fatal("large history blocked brief recovery", err)
	}
	var archive string
	if err = a.store.QueryRow("SELECT archive FROM task_handoff_context WHERE id=?", result.Task.Binding.HistoryID).Scan(&archive); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(archive, "selected reply") || strings.Contains(archive, "excluded-large-tool") || utf8.RuneCountInString(archive) > continuationBriefLimit {
		t.Fatal("hidden full history retained in brief")
	}
	var after int64
	if err = a.store.QueryRow("SELECT sum(length(text)) FROM events WHERE task_id=?", task.ID).Scan(&after); err != nil || after != size {
		t.Fatal("original history modified", err)
	}
}

func TestCodexExplicitRestartPreservesHistoryAndStartsFreshOnNextSend(t *testing.T) {
	runner := &switchRunner{}
	a, task := switchFixture(t, runner)
	seedBriefHistory(t, a, task)
	v := restartBriefRequest(t, a, task)
	result, err := a.switchTaskEngine(task.ID, v)
	if err != nil || !result.NewSession || result.Task.Session != "" || result.Task.ID != task.ID || result.Task.Binding.HistoryID == "" || result.Task.Binding.HistoryMode != "brief" {
		t.Fatal(result, err)
	}
	if len(runner.calls) != 0 {
		t.Fatal("switch called a model")
	}
	var raw, archive, context string
	if err = a.store.QueryRow("SELECT previous FROM task_engine_switches WHERE id=?", result.Task.Binding.Revision).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var previous Task
	if json.Unmarshal([]byte(raw), &previous) != nil || previous.Session != task.Session || previous.Binding.Revision != task.Binding.Revision {
		t.Fatal("old native identity not retained")
	}
	if err = a.store.QueryRow("SELECT context,archive FROM task_handoff_context WHERE id=?", result.Task.Binding.HistoryID).Scan(&context, &archive); err != nil || context != archive {
		t.Fatal("hidden archive differs from reviewed brief", err)
	}
	again, err := a.switchTaskEngine(task.ID, v)
	if err != nil || again.Task.Binding.HistoryID != result.Task.Binding.HistoryID || again.Task.Binding.Revision != result.Task.Binding.Revision {
		t.Fatal("retry was not idempotent", err)
	}
	var switches int
	a.store.QueryRow("SELECT count(*) FROM task_engine_switches WHERE task_id=?", task.ID).Scan(&switches)
	if switches != 1 {
		t.Fatal("retry inserted another recovery")
	}
	run, err := a.submit(task.ID, "continue fresh", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		var status string
		a.store.QueryRow("SELECT status FROM runs WHERE id=?", run.ID).Scan(&status)
		return status == "done"
	})
	runner.mu.Lock()
	call := runner.calls[0]
	runner.mu.Unlock()
	if call.task.Session != "" || call.task.Engine != "codex" || call.archive != archive || !strings.Contains(call.input, context) || !strings.Contains(call.input, "本轮用户要求：\ncontinue fresh") || strings.Contains(call.archive, "excluded-") {
		t.Fatal("wrong next-send recovery context")
	}
	if strings.Count(call.input, "selected-memory-summary") != 1 {
		t.Fatal("brief and automatic recall duplicated memory")
	}
	var history int
	a.store.QueryRow("SELECT count(*) FROM runs WHERE task_id=?", task.ID).Scan(&history)
	if history != 8 {
		t.Fatal("existing turns were changed", history)
	}
}

func TestCodexRestartRequiresExplicitSafeConfirmation(t *testing.T) {
	a, task := switchFixture(t, &switchRunner{})
	for _, scenario := range []string{"unconfirmed", "full", "preserve", "other-engine", "worker", "queue", "stale-binding", "stale-preview"} {
		t.Run(scenario, func(t *testing.T) {
			v := restartBriefRequest(t, a, task)
			switch scenario {
			case "unconfirmed":
				v.Confirm = false
			case "full":
				v.ContextMode = "full"
			case "preserve":
				v.PreserveLegacySession = true
			case "other-engine":
				v.Engine = "claude"
			case "worker":
				a.mu.Lock()
				a.workers[task.ID] = &worker{}
				a.mu.Unlock()
				defer func() { a.mu.Lock(); delete(a.workers, task.ID); a.mu.Unlock() }()
			case "queue":
				a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES('pending-restart',?,'pending','chat','web','queued',1)", task.ID)
				defer a.store.Exec("DELETE FROM runs WHERE id='pending-restart'")
			case "stale-binding":
				v.ExpectedBindingRevision = "stale"
			case "stale-preview":
				a.store.Exec("UPDATE tasks SET updated=updated+1 WHERE id=?", task.ID)
			}
			if _, err := a.switchTaskEngine(task.ID, v); err == nil {
				t.Fatal("unsafe restart accepted")
			}
			current, _ := a.store.task(task.ID)
			if current.Session != task.Session || current.Binding.Revision != task.Binding.Revision {
				t.Fatal("failed request mutated task")
			}
		})
	}
	// The source must also be Codex, even when the selected target is Codex.
	a.store.Exec("UPDATE task_execution SET engine='claude' WHERE task_id=?", task.ID)
	v := restartBriefRequest(t, a, task)
	if _, err := a.switchTaskEngine(task.ID, v); err == nil {
		t.Fatal("non-Codex source restarted")
	}
}

func TestContinuationBriefFingerprintTracksReviewedState(t *testing.T) {
	a, task := switchFixture(t, &switchRunner{})
	seedBriefHistory(t, a, task)
	v := restartBriefRequest(t, a, task)
	if _, err := a.store.Exec("UPDATE knowledge_entries SET content='changed selected note' WHERE id='brief-note-3'"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.switchTaskEngine(task.ID, v); !errors.Is(err, errHandoffChanged) {
		t.Fatal("stale selected text was accepted", err)
	}
	base, _, err := a.buildContinuation(task, "brief")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"updated", "session", "binding"} {
		changed := task
		switch kind {
		case "updated":
			changed.Updated++
		case "session":
			changed.Session = "private-replacement-native"
		case "binding":
			binding := *task.Binding
			binding.Revision += "-changed"
			changed.Binding = &binding
		}
		p, archive, err := a.buildContinuation(changed, "brief")
		if err != nil || p.Fingerprint == base.Fingerprint || strings.Contains(archive, changed.Session) {
			t.Fatal("identity not privately fingerprinted", kind, err)
		}
	}
	// A transaction snapshot can build the same bounded mode without trying to
	// acquire Store's sole connection recursively.
	tx, err := a.store.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	p, archive, err := a.buildContinuation(task, "brief", tx)
	if err != nil || p.Context != archive || !strings.Contains(archive, "selected-memory-summary") {
		t.Fatal("snapshot brief failed", err)
	}
}

func TestCodexRestartHTTPPreviewAndAccessGuards(t *testing.T) {
	runner := &switchRunner{}
	a, task := switchFixture(t, runner)
	seedBriefHistory(t, a, task)
	request := toolsClient(t, a)
	base := "/api/tasks/" + task.ID
	var preview ContinuationPreview
	if err := json.Unmarshal(request(base+"/continuation/preview?mode=brief", "GET", nil, 200), &preview); err != nil || preview.Fingerprint == "" || utf8.RuneCountInString(preview.Context) > continuationBriefLimit {
		t.Fatal(preview, err)
	}
	archive := request(base+"/continuation/archive?mode=brief", "GET", nil, 200)
	if string(archive) != preview.Context {
		t.Fatal("preview omitted hidden archive text")
	}
	v := restartBriefRequest(t, a, task)
	v.Fingerprint = preview.Fingerprint
	raw, _ := json.Marshal(v)
	handler := (&Server{app: a}).Handler()
	for _, scenario := range []string{"unauthenticated", "csrf"} {
		r := httptest.NewRequest(http.MethodPost, base+"/handoff", bytes.NewReader(raw))
		r.Header.Set("Origin", "http://example.com")
		want := http.StatusUnauthorized
		if scenario == "csrf" {
			r.AddCookie(&http.Cookie{Name: "jianzuo_session", Value: "tools-test"})
			r.Header.Set("X-CSRF-Token", "invalid")
			want = http.StatusForbidden
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatal(scenario, w.Code, w.Body)
		}
	}
	v.Confirm = false
	request(base+"/handoff", "POST", v, http.StatusPreconditionRequired)
	v.Confirm = true
	var result handoffResult
	if err := json.Unmarshal(request(base+"/handoff", "POST", v, 200), &result); err != nil || !result.NewSession || result.Task.Session != "" || result.Task.Binding.HistoryMode != "brief" {
		t.Fatal(result, err)
	}
	if len(runner.calls) != 0 {
		t.Fatal("HTTP recovery invoked model")
	}
	// Ordinary model-only switching still reuses the current native session
	// and preserves the recovery mode for any not-yet-submitted continuation.
	if _, err := a.store.Exec("UPDATE tasks SET session='fresh-native-reused' WHERE id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	current, err := a.store.task(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	normal := switchRequest(t, a, current)
	normal.Model = "different-model"
	changed, err := a.switchTaskEngine(task.ID, normal)
	if err != nil || changed.NewSession || changed.Task.Session != "fresh-native-reused" || changed.Task.Binding.HistoryID != result.Task.Binding.HistoryID || changed.Task.Binding.HistoryMode != "brief" {
		t.Fatal("ordinary switch lost native session or recovery mode", changed, err)
	}
}
