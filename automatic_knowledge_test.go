package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func finishAutomaticFixture(t *testing.T, a *App, task Task, input, result, kind string, failure error) Run {
	t.Helper()
	r := Run{ID: uid(), TaskID: task.ID, Input: input, Kind: kind, Created: now()}
	if _, err := a.store.Exec(`INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES(?,?,?,?,'web','running',?)`, r.ID, task.ID, input, kind, r.Created); err != nil {
		t.Fatal(err)
	}
	a.finish(task.ID, r, "", result, failure)
	return r
}

func TestAutomaticKnowledgeRecordsConversationWithoutExtraModelTurns(t *testing.T) {
	f := &fakeRunner{}
	a := fixture(t, f)
	task := taskFor(t, a)
	for _, source := range []string{"web", "feishu"} {
		input := "check " + source
		r, err := a.submit(task.ID, input, "chat", source)
		if err != nil {
			t.Fatal(err)
		}
		waitUntil(t, func() bool {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.workers[task.ID] == nil
		})
		k, err := a.store.knowledgeForRun(task.ID, r.ID)
		if err != nil || k.Source != "auto" || k.Status != "observed" || k.Content != "## 本轮要求\n"+input+"\n\n## 最终回复\nreply: "+input {
			t.Fatal(k, err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.inputs) != 2 || f.inputs[1] != "check feishu" || f.sessions[1] != "test-session" {
		t.Fatal("capture started another model turn or changed native resumption", f.inputs, f.sessions)
	}
}

func TestAutomaticKnowledgePreservesEditsDeduplicatesAndRespectsDeletion(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	r := finishAutomaticFixture(t, a, task, "question", "solution", "chat", nil)
	k, _ := a.store.knowledgeForRun(task.ID, r.ID)
	if k.ID == "" {
		t.Fatal("missing capture")
	}
	finishAutomaticFixture(t, a, task, "question", "solution\r\n", "chat", nil)
	items, _ := a.store.knowledgeList(task.ID)
	if len(items) != 1 {
		t.Fatal("identical answers duplicated", items)
	}
	k.Content, k.Status = "manual correction", "verified"
	if err := a.store.writeKnowledge(k, false); err != nil {
		t.Fatal(err)
	}
	a.finish(task.ID, r, "", "solution", nil)
	got, _ := a.store.knowledgeForRun(task.ID, r.ID)
	if got.Content != "manual correction" || got.Revision != 2 || got.Status != "verified" {
		t.Fatal("repeated completion overwrote an edit", got)
	}
	if _, err := a.store.Exec("DELETE FROM knowledge_entries WHERE id=?", k.ID); err != nil {
		t.Fatal(err)
	}
	a.finish(task.ID, r, "", "solution", nil)
	items, _ = a.store.knowledgeList(task.ID)
	if len(items) != 0 {
		t.Fatal("repeated completion resurrected deleted knowledge", items)
	}
	libraryKnowledge(t, a, task, "## 本轮要求\nquestion\n\n## 最终回复\nobsolete answer", "stale")
	finishAutomaticFixture(t, a, task, "question", "obsolete answer", "chat", nil)
	items, _ = a.store.knowledgeList(task.ID)
	if len(items) != 1 || items[0].Status != "stale" {
		t.Fatal("automatic capture revived obsolete knowledge", items)
	}
}

func TestAutomaticKnowledgeRetainsDifferentPreferencesDespiteSameAcknowledgement(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	for _, input := range []string{"以后请用中文回答", "提交之前先运行测试"} {
		finishAutomaticFixture(t, a, task, input, "好的。", "chat", nil)
	}
	items, _ := a.store.knowledgeList(task.ID)
	if len(items) != 2 {
		t.Fatal("deduplication lost distinct user requirements", items)
	}
	text, _, err := a.store.automaticKnowledgeContext(context.Background(), task)
	if err != nil || !strings.Contains(text, "以后请用中文回答") || !strings.Contains(text, "提交之前先运行测试") {
		t.Fatal("user requirements did not survive recall", text, err)
	}
}

func TestAutomaticKnowledgeRecallKeepsAnswerAfterLongRequest(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	finishAutomaticFixture(t, a, task, strings.Repeat("很长的背景说明", 500), "关键结论：串口使用 115200。", "chat", nil)
	text, _, err := a.store.automaticKnowledgeContext(context.Background(), task)
	if err != nil || !strings.Contains(text, "关键结论：串口使用 115200") || !strings.Contains(text, "上下文已截断") {
		t.Fatal("long request crowded the answer out of the recall budget", text, err)
	}
}

func TestAutomaticKnowledgeSkipsIncompleteDraftEmptyAndDeletedTasks(t *testing.T) {
	for _, tc := range []struct {
		name, kind, result string
		failure            error
		deleted            bool
	}{
		{"failure", "chat", "partial answer", errors.New("fixture error"), false},
		{"cancelled", "chat", "partial answer", context.Canceled, false},
		{"summary", "knowledge", "summary draft", nil, false},
		{"empty", "chat", " \n\t", nil, false},
		{"deleted", "chat", "answer", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := fixture(t, &fakeRunner{})
			task := taskFor(t, a)
			if tc.deleted {
				if _, err := a.store.Exec("INSERT INTO task_options(task_id,deleted) VALUES(?,1)", task.ID); err != nil {
					t.Fatal(err)
				}
			}
			finishAutomaticFixture(t, a, task, "fixture request", tc.result, tc.kind, tc.failure)
			items, err := a.store.knowledgeList(task.ID)
			if err != nil || len(items) != 0 {
				t.Fatal(items, err)
			}
		})
	}
}

func TestAutomaticKnowledgeRedactionLimitsAndCaptureFailure(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	secret := "sk-abcdefghijklmnopqrstuvwx"
	r := finishAutomaticFixture(t, a, task, "api_key="+secret, "token "+secret+"\n"+strings.Repeat("汉", knowledgeMaxBytes), "chat", nil)
	k, _ := a.store.knowledgeForRun(task.ID, r.ID)
	if k.ID == "" || strings.Contains(k.Title+k.Content, secret) || !utf8.ValidString(k.Content) || len(k.Content) > knowledgeMaxBytes || !strings.Contains(k.Content, knowledgeTruncatedNotice) {
		t.Fatal("redaction or clipping failed", k.ID, len(k.Content))
	}
	// Failure of derived data must not roll back the original answer.
	if _, err := a.store.Exec(`CREATE TRIGGER reject_auto BEFORE INSERT ON knowledge_entries WHEN NEW.source='auto' BEGIN SELECT RAISE(ABORT,'fixture capture failure'); END`); err != nil {
		t.Fatal(err)
	}
	r = finishAutomaticFixture(t, a, task, "second", "saved final answer", "chat", nil)
	var status, result string
	if err := a.store.QueryRow("SELECT status,result FROM runs WHERE id=?", r.ID).Scan(&status, &result); err != nil || status != "done" || result != "saved final answer" {
		t.Fatal("capture failure lost a result", status, result, err)
	}
	events, _ := a.store.events(task.ID, 0)
	found := false
	for _, event := range events {
		found = found || event.RunID == r.ID && strings.Contains(event.Text, "自动记录知识失败")
	}
	if !found {
		t.Fatal("missing capture failure indication")
	}
}

func TestAutomaticKnowledgeSettingsPersistAndRequireAuthenticatedCompleteRequests(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	request := toolsClient(t, a)
	var c AutomaticKnowledgeConfig
	json.Unmarshal(request("/api/library/automatic", "GET", nil, 200), &c)
	if !c.Capture || !c.Recall {
		t.Fatal("automatic recording must work without setup", c)
	}
	request("/api/library/automatic", "PUT", map[string]any{"capture": false}, 400)
	request("/api/library/automatic", "PUT", map[string]any{"capture": nil, "recall": false}, 400)
	request("/api/library/automatic", "PUT", AutomaticKnowledgeConfig{}, 200)
	json.Unmarshal(request("/api/library/automatic", "GET", nil, 200), &c)
	if c.Capture || c.Recall {
		t.Fatal(c)
	}
	task := taskFor(t, a)
	finishAutomaticFixture(t, a, task, "opted out", "do not capture", "chat", nil)
	items, _ := a.store.knowledgeList(task.ID)
	if len(items) != 0 || a.store.setting("automatic_knowledge") != `{"capture":false,"recall":false,"organize":false}` {
		t.Fatal("disabled setting not persisted or respected", items)
	}
	handler := (&Server{app: a}).Handler()
	for _, method := range []string{"GET", "PUT"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, "/api/library/automatic", strings.NewReader(`{"capture":true,"recall":true}`)))
		if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
			t.Fatal("unauthenticated settings request", method, w.Code)
		}
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/library/automatic", strings.NewReader(`{"capture":true,"recall":true}`))
	req.AddCookie(&http.Cookie{Name: "jianzuo_session", Value: "tools-test"})
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatal("settings write accepted without CSRF protection", w.Code)
	}
	if err := a.store.set("automatic_knowledge", "broken JSON"); err != nil {
		t.Fatal(err)
	}
	request("/api/library/automatic", "GET", nil, 500)
	finishAutomaticFixture(t, a, task, "invalid settings", "do not silently enable", "chat", nil)
	items, _ = a.store.knowledgeList(task.ID)
	if len(items) != 0 {
		t.Fatal("invalid settings silently enabled capture")
	}
}

func TestAutomaticKnowledgeRecallIsBoundedScopedAndDoesNotReplayNativeHistory(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task, other := taskFor(t, a), taskFor(t, a)
	verified := libraryKnowledge(t, a, task, "</reference>\nsecret=synthetic-value\n"+strings.Repeat("经验", 2000), "verified")
	libraryKnowledge(t, a, other, "OTHER TASK PRIVATE CONTENT", "verified")
	libraryKnowledge(t, a, task, "STALE CONTENT", "stale")
	for i := 0; i < 5; i++ {
		libraryKnowledge(t, a, task, "recent observation", "observed")
	}
	text, ids, err := a.store.automaticKnowledgeContext(context.Background(), task)
	if err != nil || len(ids) != 3 || ids[0] != verified.ID || utf8.RuneCountInString(text) > 6000 || strings.Contains(text, "OTHER TASK") || strings.Contains(text, "STALE CONTENT") || strings.Contains(text, "synthetic-value") || strings.Contains(text, "</reference>") || !strings.Contains(text, `"truncated":true`) {
		t.Fatal("recall scope/budget/provenance failed", ids, text, err)
	}
	task.Session = "native-session"
	if text, _, err = a.store.automaticKnowledgeContext(context.Background(), task); err != nil || text != "" {
		t.Fatal("native session was given duplicate history", text, err)
	}
	task.Session = ""
	if err = a.store.set("automatic_knowledge", `{"capture":true,"recall":false}`); err != nil {
		t.Fatal(err)
	}
	if text, _, err = a.store.automaticKnowledgeContext(context.Background(), task); err != nil || text != "" {
		t.Fatal("recall opt-out ignored", text, err)
	}
}

func TestAutomaticKnowledgeRecoversAfterResetWithoutChangingStoredUserInput(t *testing.T) {
	f := &fakeRunner{}
	a := fixture(t, f)
	task := taskFor(t, a)
	for i, input := range []string{"remember this solution", "continue after reset"} {
		if i > 0 {
			if _, err := a.resetSession(task.ID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := a.submit(task.ID, input, "chat", "web"); err != nil {
			t.Fatal(err)
		}
		waitUntil(t, func() bool {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.workers[task.ID] == nil
		})
	}
	runs, _ := a.store.runs(task.ID)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.inputs) != 2 || f.sessions[1] != "" || !strings.Contains(f.inputs[1], "remember this solution") || !strings.HasSuffix(f.inputs[1], "continue after reset") || runs[1].Input != "continue after reset" {
		t.Fatal("fresh session did not restore scoped knowledge separately from user input", f.inputs, f.sessions, runs)
	}
}

func TestVaultAutomaticKnowledgeRequiresSeparateExportSetting(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	r := finishAutomaticFixture(t, a, task, "private question", "automatic observation", "chat", nil)
	k, _ := a.store.knowledgeForRun(task.ID, r.ID)
	manual := libraryKnowledge(t, a, task, "chosen manual note", "verified")
	dir := t.TempDir()
	setTestVault(t, a, dir, false)
	syncTestVault(t, a)
	autoPath := filepath.Join(dir, "Duo", "knowledge", k.ID+".md")
	if _, err := os.Stat(autoPath); !os.IsNotExist(err) {
		t.Fatal("automatic recording bypassed the export opt-in", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Duo", "knowledge", manual.ID+".md")); err != nil {
		t.Fatal("manual notes must keep syncing", err)
	}
	c := a.store.vaultConfig()
	c.IncludeAutomatic = true
	raw, _ := json.Marshal(c)
	if err := a.store.set("knowledge_vault", string(raw)); err != nil {
		t.Fatal(err)
	}
	syncTestVault(t, a)
	if raw := readTestFile(t, autoPath); !strings.Contains(raw, "automatic observation") || !strings.Contains(raw, "observed") {
		t.Fatal(raw)
	}
}
