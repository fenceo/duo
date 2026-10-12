package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func syntheticOutputImage(t *testing.T, source string) string {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	v, _ := json.Marshal(harnessImage{Type: "image", Data: base64.StdEncoding.EncodeToString(b.Bytes()), Mime: "image/png", Source: source})
	return string(v)
}

func TestHarnessContextAndImageProtocol(t *testing.T) {
	s := harnessACPTurn{session: "own"}
	var kinds, texts []string
	emit := func(kind, text string) { kinds = append(kinds, kind); texts = append(texts, text) }
	consume := func(session string, update any) {
		raw, _ := json.Marshal(map[string]any{"sessionId": session, "update": update})
		s.consume(codexRPC{Method: "session/update", Params: raw}, emit)
	}
	for _, u := range []map[string]any{
		{"sessionUpdate": "usage_update", "used": 25, "size": 100},
		{"sessionUpdate": "usage_update", "used": -1, "size": 100},
		{"sessionUpdate": "usage_update", "used": 25},
		{"sessionUpdate": "usage_update", "used": 1.5, "size": 100},
	} {
		consume("own", u)
	}
	consume("foreign", map[string]any{"sessionUpdate": "usage_update", "used": 90, "size": 100})
	if len(kinds) != 1 || kinds[0] != "context" || texts[0] != `{"used":25,"size":100}` {
		t.Fatal("context admitted invalid facts or billing", kinds, texts)
	}
	picture := json.RawMessage(syntheticOutputImage(t, "assistant"))
	consume("own", map[string]any{"sessionUpdate": "agent_message_chunk", "content": picture})
	if s.finalImages != 1 || s.result != "" || kinds[len(kinds)-1] != "output_image" {
		t.Fatal("image-only final was not admitted")
	}
	consume("own", map[string]any{"sessionUpdate": "tool_call", "title": "synthetic", "rawInput": map[string]any{}, "toolCallId": "call"})
	consume("own", map[string]any{"sessionUpdate": "tool_call_update", "status": "completed", "toolCallId": "call", "content": []any{map[string]any{"type": "content", "content": picture}, map[string]any{"type": "content", "content": map[string]string{"type": "text", "text": "safe text"}}, map[string]any{"type": "resource", "content": map[string]string{"data": "must-not-leak"}}}})
	if texts[len(texts)-1] != "safe text" {
		t.Fatal("tool content leaked inline bytes", texts[len(texts)-1])
	}
	consume("own", map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": "final"}})
	if s.finalImages != 0 || s.result != "final" || !strings.Contains(strings.Join(kinds, ","), "output_image_reset") {
		t.Fatal("preamble image became final")
	}
	for _, v := range []harnessImage{{Type: "image", Source: "assistant", Mime: "image/svg+xml", Data: base64.StdEncoding.EncodeToString([]byte("<svg/>"))}, {Type: "image", Source: "assistant", Mime: "image/png", Data: "invalid"}} {
		if _, _, err := decodeOutputImage(v); err == nil {
			t.Fatal("invalid image accepted")
		}
	}
}

func TestHarnessImageOnlyFinalResponse(t *testing.T) {
	c, task := harnessFixtureSetup(t, "output-image")
	var imageCount int
	_, result, err := runHarnessACP(t.Context(), c, task, "return a synthetic image", func(kind, text string) {
		if kind == "output_image" {
			imageCount++
		}
	})
	if err != nil || result != "" || imageCount != 1 {
		t.Fatal("image-only final was treated as empty response", err, result, imageCount)
	}
}

func TestOutputImagesPersistScopeForkAndLimit(t *testing.T) {
	a, task := switchFixture(t, &switchRunner{})
	_, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES('image-run',?,'test','chat','web','done',1)", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.store.Exec("INSERT INTO run_context VALUES('image-run','{\"used\":30,\"size\":100}')")
	if err != nil {
		t.Fatal(err)
	}
	raw := syntheticOutputImage(t, "assistant")
	for i := 0; i < outputImageCount; i++ {
		if err = a.store.saveOutputImage(task.ID, "image-run", raw); err != nil {
			t.Fatal(err)
		}
	}
	if a.store.saveOutputImage(task.ID, "image-run", raw) == nil || a.store.saveOutputImage("foreign", "image-run", raw) == nil {
		t.Fatal("image count/ownership not enforced")
	}
	runs, err := a.store.runs(task.ID)
	if err != nil || len(runs) != 1 || len(runs[0].Images) != 12 || runs[0].Context.Used != 30 || runs[0].Usage != nil {
		t.Fatal("output projection/billing separation", runs, err)
	}
	imageID := runs[0].Images[0].ID
	branch, err := a.forkTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	copyRuns, err := a.store.runs(branch.ID)
	if err != nil || len(copyRuns) != 1 || len(copyRuns[0].Images) != 12 || copyRuns[0].Images[0].ID == imageID || copyRuns[0].Context.Used != 30 {
		t.Fatal("branch image/context copy failed", err)
	}
	_, _ = a.store.Exec("DELETE FROM output_images WHERE task_id=?", task.ID)
	copyRuns, _ = a.store.runs(branch.ID)
	if len(copyRuns[0].Images) != 12 {
		t.Fatal("parent deletion affected copy")
	}
	s := &Server{app: a}
	mux := http.NewServeMux()
	s.outputImageRoutes(mux)
	// Handler uses real authentication. Anonymous requests cannot see bytes.
	url := fmt.Sprintf("/api/tasks/%s/runs/%s/images/%s", branch.ID, copyRuns[0].ID, copyRuns[0].Images[0].ID)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest("GET", url, nil))
	if response.Code == 200 {
		t.Fatal("anonymous image route bypassed authentication")
	}
	request := toolsClient(t, a)
	request(url, "GET", nil, 200)
	request(strings.Replace(url, branch.ID, task.ID, 1), "GET", nil, 404)
	request(strings.Replace(url, copyRuns[0].ID, "image-run", 1), "GET", nil, 404)
	before, err := a.store.usageSummary(t.Context(), "", 1, time.UTC, time.Now())
	if err != nil || before.Total.Total != 0 {
		t.Fatal("context contaminated token totals", err)
	}
}

func TestHarnessSummaryRequiresConfirmedGuard(t *testing.T) {
	c, task := harnessFixtureSetup(t, "")
	task.ID, task.NoTools = "", true
	_, _, err := runHarnessACP(t.Context(), c, task, "must never reach provider", func(string, string) {})
	if err == nil || !strings.Contains(err.Error(), "未发送模型请求") {
		t.Fatal("unconfirmed guard admitted prompt", err)
	}
}

type harnessSummaryProbe struct{ calls chan switchedCall }

type harnessOutputFixtureRunner struct {
	config Config
	task   Task
}

func (r *harnessOutputFixtureRunner) Run(ctx context.Context, _ Config, _ Task, input string, emit func(string, string)) (string, string, error) {
	return runHarnessACP(ctx, r.config, r.task, input, emit)
}
func TestHarnessOutputRunPersistsWithoutInlineEvents(t *testing.T) {
	c, native := harnessFixtureSetup(t, "output-image")
	a := fixture(t, &harnessOutputFixtureRunner{config: c, task: native})
	task := createHarnessStoredTask(t, a)
	run, err := a.submit(task.ID, "synthetic picture", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		var status string
		_ = a.store.QueryRow("SELECT status FROM runs WHERE id=?", run.ID).Scan(&status)
		return status == "done" || status == "failed"
	})
	runs, err := a.store.runs(task.ID)
	if err != nil || len(runs) != 1 || runs[0].Status != "done" || runs[0].Context == nil || runs[0].Context.Used != 10 || runs[0].Usage != nil || len(runs[0].Images) != 1 || runs[0].Result != "" {
		t.Fatal("ACP to store pipeline failed", runs, err)
	}
	events, err := a.store.events(task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind == "output_image" || strings.Contains(event.Text, "iVBORw0") {
			t.Fatal("raw image leaked into text events")
		}
	}
}

func (r *harnessSummaryProbe) Run(_ context.Context, c Config, t Task, input string, emit func(string, string)) (string, string, error) {
	r.calls <- switchedCall{task: t, profile: c.EngineEnv, input: input}
	emit("session", "must-not-replace-original")
	return "must-not-replace-original", "synthetic summary", nil
}
func TestHarnessKnowledgeUsesIsolatedBoundedSnapshot(t *testing.T) {
	runner := &harnessSummaryProbe{calls: make(chan switchedCall, 1)}
	a := fixture(t, runner)
	task := createHarnessStoredTask(t, a)
	_, _ = a.store.Exec("UPDATE tasks SET session='original-native' WHERE id=?", task.ID)
	original, _ := a.store.task(task.ID)
	for i := 0; i < 10; i++ {
		_, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,created) VALUES(?,? ,?,'chat','web','done',?,?)", fmt.Sprint(i), task.ID, fmt.Sprintf("input-%d", i), "reply "+strings.Repeat("测", 3500), i)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, _ = a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,created) VALUES('bad',?,'excluded-failure','chat','web','failed','bad-result',100)", task.ID)
	run, err := a.knowledge(task.ID, "web")
	if err != nil {
		t.Fatal(err)
	}
	call := <-runner.calls
	if call.task.ID != "" || call.task.Session != "" || !call.task.NoTools || call.task.Mode.Permission != "read" || len(call.task.Files) != 0 || strings.Contains(call.input, "input-0") || strings.Contains(call.input, "excluded-failure") || !strings.Contains(call.input, "input-9") || len([]rune(call.input)) > 26000 {
		t.Fatal("summary scope or bound failed", call.task)
	}
	waitUntil(t, func() bool {
		var status string
		_ = a.store.QueryRow("SELECT status FROM runs WHERE id=?", run.ID).Scan(&status)
		return status == "done"
	})
	after, _ := a.store.task(task.ID)
	beforeMode, _ := json.Marshal(original.Mode)
	afterMode, _ := json.Marshal(after.Mode)
	if after.Session != "original-native" || string(beforeMode) != string(afterMode) {
		t.Fatal("summary changed chat identity or permissions", after)
	}
}
