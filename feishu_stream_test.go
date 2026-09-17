package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	lark "github.com/larksuite/oapi-sdk-go/v3"
)

func enableRunCards(t *testing.T, a *App) {
	t.Helper()
	c := a.config.get()
	c.Feishu = FeishuConfig{Enabled: true, AppID: "test", Secret: "fake-secret", Owner: "owner"}
	c.Access = AccessConfig{LAN: "http://192.168.50.1:8789", Tailscale: "http://100.64.0.1:8789"}
	if err := a.config.save(c); err != nil {
		t.Fatal(err)
	}
	// Tests must fail instead of accidentally reaching the real platform.
	a.feishu.createRunCard = func(context.Context, FeishuConfig, string, string, string) (string, error) {
		t.Error("unexpected create")
		return "", errors.New("unexpected create")
	}
	a.feishu.updateRunCard = func(context.Context, FeishuConfig, string, string) error {
		t.Error("unexpected update")
		return errors.New("unexpected update")
	}
}

func stepRunCards(t *testing.T, a *App, at int64) {
	t.Helper()
	if err := a.feishu.runCardStep(at); err != nil {
		t.Fatal(err)
	}
}

func TestRunCardCoalescesAndKeepsOriginalRecipient(t *testing.T) {
	runner := &fakeRunner{gate: make(chan struct{}), started: make(chan struct{}, 1)}
	a := fixture(t, runner)
	enableRunCards(t, a)
	task := taskFor(t, a)
	other := taskFor(t, a)
	a.bind("chat-original", task.ID)
	if err := a.feishu.receive("message", "owner", "chat-original", "检查串口"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.started:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	runs, _ := a.store.runs(task.ID)
	run := runs[0]
	created, patched := 0, 0
	latest := ""
	a.feishu.createRunCard = func(_ context.Context, _ FeishuConfig, chat, content, id string) (string, error) {
		created++
		latest = content
		if chat != "chat-original" || id == "" || !strings.Contains(content, "执行中") {
			t.Fatal(chat, content)
		}
		return "om-same-card", nil
	}
	a.feishu.updateRunCard = func(_ context.Context, _ FeishuConfig, id, content string) error {
		patched++
		latest = content
		if id != "om-same-card" {
			t.Fatal("changed message", id)
		}
		return nil
	}
	at := now()
	stepRunCards(t, a, at)
	for i := 0; i < 100; i++ {
		a.store.event(task.ID, run.ID, "tool", "command --secret PRIVATE_TOOL_OUTPUT")
	}
	a.store.event(task.ID, run.ID, "log", "PRIVATE_LOG")
	a.store.event(task.ID, run.ID, "assistant", "已检查第一步。")
	stepRunCards(t, a, at+1000)
	if patched != 0 {
		t.Fatal("not throttled")
	}
	stepRunCards(t, a, at+2000)
	if patched != 1 || !strings.Contains(latest, "100 条") || !strings.Contains(latest, "已检查第一步") || strings.Contains(latest, "PRIVATE_") {
		t.Fatal(latest)
	}
	stepRunCards(t, a, at+4000)
	if patched != 1 {
		t.Fatal("unchanged content patched")
	}
	a.bind("chat-original", other.ID)
	a.bind("chat-late", task.ID)
	close(runner.gate)
	waitUntil(t, func() bool { r, _ := a.store.runs(task.ID); return r[0].Status == "done" })
	stepRunCards(t, a, at+6000)
	stepRunCards(t, a, at+8000)
	if created != 1 || patched != 2 || !strings.Contains(latest, "已完成") || !strings.Contains(latest, "reply: 检查串口") || !strings.Contains(latest, task.ID) {
		t.Fatal(created, patched, latest)
	}
	var count int
	a.store.QueryRow("SELECT count(*) FROM outbox").Scan(&count)
	if count != 0 {
		t.Fatal("duplicate acknowledgement/final text", count)
	}
}

// Insert a durable run without invoking Codex, to control crash/retry scenarios.
func seedRunCard(t *testing.T, a *App, status string) (Task, string, string) {
	t.Helper()
	task := taskFor(t, a)
	run, delivery := uid(), uid()
	_, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES(?,?,'要求','chat','feishu',?,?)", run, task.ID, status, now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.store.Exec("INSERT INTO feishu_run_cards(id,run_id,chat_id,app_id,owner) VALUES(?,?,'chat','test','owner')", delivery, run)
	if err != nil {
		t.Fatal(err)
	}
	return task, run, delivery
}

func TestRunCardRetryUsesStableUUIDAndMessageID(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	enableRunCards(t, a)
	_, run, delivery := seedRunCard(t, a, "running")
	creates, patches := 0, 0
	a.feishu.createRunCard = func(_ context.Context, _ FeishuConfig, chat, content, id string) (string, error) {
		creates++
		if id != delivery {
			t.Fatal("unstable idempotency key")
		}
		if creates == 1 {
			return "", errors.New("create response lost")
		}
		return "om-recovered", nil
	}
	at := now()
	if a.feishu.runCardStep(at) == nil {
		t.Fatal("missing failure")
	}
	stepRunCards(t, a, at+1000)
	if creates != 1 {
		t.Fatal("retry too soon")
	}
	stepRunCards(t, a, at+2000)
	a.store.Exec("UPDATE runs SET status='done',result='最终答案' WHERE id=?", run)
	a.feishu.updateRunCard = func(_ context.Context, _ FeishuConfig, id, content string) error {
		patches++
		if id != "om-recovered" || !strings.Contains(content, "最终答案") {
			t.Fatal(id, content)
		}
		if patches == 1 {
			return errors.New("patch timeout")
		}
		return nil
	}
	if a.feishu.runCardStep(at+4000) == nil {
		t.Fatal("missing failure")
	}
	stepRunCards(t, a, at+6000)
	stepRunCards(t, a, at+8000)
	if creates != 2 || patches != 2 {
		t.Fatal(creates, patches)
	}
}

func TestRunCardPermanentFailureFallsBackOnceAfterCompletion(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	enableRunCards(t, a)
	_, run, delivery := seedRunCard(t, a, "running")
	creates := 0
	a.feishu.createRunCard = func(context.Context, FeishuConfig, string, string, string) (string, error) {
		creates++
		return "", errors.New("permission denied")
	}
	at := now()
	for i := 0; i < 6; i++ {
		if a.feishu.runCardStep(at+int64(i)*60000) == nil {
			t.Fatal("missing failure")
		}
	}
	stepRunCards(t, a, at+360000)
	var count int
	a.store.QueryRow("SELECT count(*) FROM outbox").Scan(&count)
	if count != 0 {
		t.Fatal("progress spam")
	}
	a.store.Exec("UPDATE runs SET status='failed',error='真实执行错误' WHERE id=?", run)
	stepRunCards(t, a, at+420000)
	stepRunCards(t, a, at+480000)
	var text string
	a.store.QueryRow("SELECT text FROM outbox WHERE id=?", delivery+"-f0").Scan(&text)
	a.store.QueryRow("SELECT count(*) FROM outbox").Scan(&count)
	if creates != 6 || count != 1 || !strings.Contains(text, "真实执行错误") || !strings.Contains(text, "http://100.64.0.1") {
		t.Fatal(creates, count, text)
	}
}

func TestRunCardStopsCurrentAndQueuedRuns(t *testing.T) {
	runner := &fakeRunner{gate: make(chan struct{}), started: make(chan struct{}, 1)}
	a := fixture(t, runner)
	enableRunCards(t, a)
	task := taskFor(t, a)
	a.bind("chat", task.ID)
	a.submit(task.ID, "first", "chat", "feishu")
	select {
	case <-runner.started:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	a.submit(task.ID, "second", "chat", "web")
	if err := a.stop(task.ID); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		r, _ := a.store.runs(task.ID)
		return len(r) == 2 && r[0].Status == "interrupted" && r[1].Status == "interrupted"
	})
	cards := []string{}
	a.feishu.createRunCard = func(_ context.Context, _ FeishuConfig, chat, content, id string) (string, error) {
		cards = append(cards, content)
		return "om-" + id, nil
	}
	at := now()
	stepRunCards(t, a, at)
	stepRunCards(t, a, at+1000)
	if len(cards) != 2 || !strings.Contains(strings.Join(cards, ""), "已取消排队") || !strings.Contains(cards[0], "已停止") || !strings.Contains(cards[1], "已停止") {
		t.Fatal(cards)
	}
}

func TestRunCardRestartRecoversInterruptedCard(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	enableRunCards(t, a)
	_, _, delivery := seedRunCard(t, a, "running")
	dir := filepath.Dir(a.config.path)
	a.store.Exec("UPDATE feishu_run_cards SET message_id='om-before-restart',first_attempt=? WHERE id=?", now(), delivery)
	// No active worker in this fixture: closing/reopening emulates a crashed store.
	a.store.Close()
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.store = s
	t.Cleanup(func() { s.Close() })
	patched := 0
	a.feishu.updateRunCard = func(_ context.Context, _ FeishuConfig, id, content string) error {
		patched++
		if id != "om-before-restart" || !strings.Contains(content, "服务重启") || !strings.Contains(content, "已停止") {
			t.Fatal(id, content)
		}
		return nil
	}
	stepRunCards(t, a, now())
	stepRunCards(t, a, now()+4000)
	if patched != 1 {
		t.Fatal(patched)
	}
}

func TestRunCardsPauseAndCancelOnOwnerChange(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	enableRunCards(t, a)
	_, _, delivery := seedRunCard(t, a, "done")
	c := a.config.get()
	c.Feishu.Enabled = false
	a.config.save(c)
	stepRunCards(t, a, now())
	var state string
	a.store.QueryRow("SELECT state FROM feishu_run_cards WHERE id=?", delivery).Scan(&state)
	if state != "pending" {
		t.Fatal(state)
	}
	c.Feishu.Enabled = true
	c.Feishu.Owner = "another-owner"
	a.config.save(c)
	stepRunCards(t, a, now())
	a.store.QueryRow("SELECT state FROM feishu_run_cards WHERE id=?", delivery).Scan(&state)
	if state != "cancelled" {
		t.Fatal(state)
	}
}

func TestRunCardDoesNotRecreateAmbiguousOldMessage(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	enableRunCards(t, a)
	_, _, delivery := seedRunCard(t, a, "done")
	a.store.Exec("UPDATE feishu_run_cards SET first_attempt=? WHERE id=?", now()-(time.Hour).Milliseconds(), delivery)
	stepRunCards(t, a, now())
	var count int
	a.store.QueryRow("SELECT count(*) FROM outbox").Scan(&count)
	if count != 1 {
		t.Fatal("expected one final fallback", count)
	}
}

func TestRunCardLateBindingDoesNotNotifyOldRun(t *testing.T) {
	runner := &fakeRunner{gate: make(chan struct{}), started: make(chan struct{}, 1)}
	a := fixture(t, runner)
	enableRunCards(t, a)
	task := taskFor(t, a)
	if _, err := a.submit(task.ID, "unbound work", "chat", "web"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.started:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	a.bind("chat", task.ID)
	close(runner.gate)
	waitUntil(t, func() bool { r, _ := a.store.runs(task.ID); return r[0].Status == "done" })
	stepRunCards(t, a, now())
	var count int
	a.store.QueryRow("SELECT count(*) FROM feishu_run_cards").Scan(&count)
	if count != 0 {
		t.Fatal("late binding sent historical run", count)
	}
}

func TestRunCardFallbackCannotBeSentByReplacementBot(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	enableRunCards(t, a)
	_, _, delivery := seedRunCard(t, a, "done")
	a.store.Exec("UPDATE feishu_run_cards SET state='fallback' WHERE id=?", delivery)
	stepRunCards(t, a, now())
	c := a.config.get()
	c.Feishu.AppID = "new-app"
	a.config.save(c)
	a.feishu.deliver = func(context.Context, FeishuConfig, string, string, string) error {
		t.Error("sent previous bot's notification")
		return nil
	}
	a.wg.Add(1)
	go func() { defer a.wg.Done(); a.feishu.outboxLoop() }()
	waitUntil(t, func() bool {
		var state string
		a.store.QueryRow("SELECT status FROM outbox WHERE id=?", delivery+"-f0").Scan(&state)
		return state == "cancelled"
	})
}

func TestRunCardSerializedBudgetAndKnowledgeFailure(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	enableRunCards(t, a)
	d := runCardDelivery{title: strings.Repeat("标题", 100), run: Run{TaskID: "task-123", Status: "done", Kind: "knowledge", Result: strings.Repeat("中文🚀<>&\"\\\x01\n", 12000)}}
	raw, err := a.feishu.runCardContent(d)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"content": raw})
	if len(body) >= 24*1024 || !utf8.ValidString(raw) || !json.Valid([]byte(raw)) || !strings.Contains(raw, "完整内容见网页") || !strings.Contains(raw, "保存知识") || !strings.Contains(raw, "update_multi\":true") || !strings.Contains(raw, "?task=task-123") || !strings.Contains(raw, "view=note") {
		t.Fatal("invalid knowledge card or size", len(body))
	}
	d.run.Status = "failed"
	d.run.Error = "整理失败"
	d.run.Result = ""
	raw, err = a.feishu.runCardContent(d)
	if err != nil || strings.Contains(raw, "保存知识") || !strings.Contains(raw, "整理失败") {
		t.Fatal(raw, err)
	}
}

func TestCardSDKCreateAndPatchAgainstMockServer(t *testing.T) {
	creates, patches := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "token") {
			w.Write([]byte(`{"code":0,"tenant_access_token":"mock-token","expire":7200}`))
			return
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		switch r.Method {
		case "POST":
			creates++
			if r.URL.Path != "/open-apis/im/v1/messages" || r.URL.Query().Get("receive_id_type") != "chat_id" || body["receive_id"] != "chat" || body["uuid"] != "stable-key" || body["msg_type"] != "interactive" {
				t.Error(r.URL, body)
			}
			w.Write([]byte(`{"code":0,"data":{"message_id":"om-mock"}}`))
		case "PATCH":
			patches++
			if r.URL.Path != "/open-apis/im/v1/messages/om-mock" || body["content"] != `{"config":{"update_multi":true}}` {
				t.Error(r.URL, body)
			}
			if patches == 1 {
				w.Write([]byte(`{"code":0}`))
			} else {
				w.Write([]byte(`{"code":230001,"msg":"mock denied"}`))
			}
		default:
			t.Error("unexpected request", r.Method, r.URL)
		}
	}))
	defer srv.Close()
	client := lark.NewClient(uid(), "mock-secret", lark.WithOpenBaseUrl(srv.URL))
	content := `{"config":{"update_multi":true}}`
	id, err := createCardMessage(context.Background(), client, "chat", content, "stable-key")
	if err != nil || id != "om-mock" {
		t.Fatal(id, err)
	}
	if err = patchCardMessage(context.Background(), client, id, content); err != nil {
		t.Fatal(err)
	}
	if err = patchCardMessage(context.Background(), client, id, content); err == nil {
		t.Fatal("SDK error ignored")
	}
	if creates != 1 || patches != 2 {
		t.Fatal(creates, patches)
	}
}
