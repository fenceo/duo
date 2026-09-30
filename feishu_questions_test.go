package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	cb "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

// Check the documented JSON 1.0 nesting contract on the serialized wire card.
// The message API accepting a card does not establish that clients render it.
// https://open.feishu.cn/document/feishu-cards/card-components/containers/form-container
func checkQuestionFormNesting(raw string) error {
	var card map[string]any
	if err := json.Unmarshal([]byte(raw), &card); err != nil {
		return err
	}
	var walk func(any, string) error
	walk = func(value any, parent string) error {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				if err := walk(child, parent); err != nil {
					return err
				}
			}
		case map[string]any:
			tag, _ := node["tag"].(string)
			if tag == "form" && parent != "" {
				return errors.New("form must be at the card root")
			}
			if parent == "form" && (tag == "div" || tag == "table" || tag == "chart") {
				return fmt.Errorf("JSON 1.0 form cannot directly contain %s", tag)
			}
			for _, key := range []string{"elements", "columns"} {
				if err := walk(node[key], tag); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(card, "")
}

func TestFeishuQuestionCardJSON1Nesting(t *testing.T) {
	// Reproduce the previous wire structure: the heading was a direct form child.
	legacy := `{"elements":[{"tag":"form","elements":[{"tag":"div","text":{"tag":"plain_text","content":"问题"}}]}]}`
	if checkQuestionFormNesting(legacy) == nil {
		t.Fatal("invalid legacy form accepted by contract check")
	}
	a := fixture(t, &fakeRunner{})
	params := `{"questions":[{"id":"choice","question":"能看到题目吗？","isOther":true,"options":[{"label":"能看到","description":"题目和选项都显示"},{"label":"看不到"}]},{"id":"text","question":"补充说明"}]}`
	for _, method := range []string{"duo/asyncQuestion", "item/tool/requestUserInput"} {
		card := a.feishu.questionCard("task", "delivery", CodexPendingRequest{Method: method, Params: json.RawMessage(params)}, true)
		raw, err := json.Marshal(card)
		if err != nil {
			t.Fatal(err)
		}
		if err = checkQuestionFormNesting(string(raw)); err != nil {
			t.Fatal(method, err)
		}
		for _, expected := range []string{"1. 能看到题目吗？", "2. 补充说明", "能看到：题目和选项都显示", `"name":"choice_0"`, `"name":"text_0"`, `"name":"text_1"`, `"value":"0"`, `"value":"1"`, `"action_type":"form_submit"`, `"duo_question":"delivery"`} {
			if !strings.Contains(string(raw), expected) {
				t.Fatalf("%s: lost question/control %s", method, expected)
			}
		}
	}
}

func questionCallback(a *App, chat, token string, form map[string]interface{}) *cb.CardActionTriggerEvent {
	return &cb.CardActionTriggerEvent{Event: &cb.CardActionTriggerRequest{Operator: &cb.Operator{OpenID: a.config.get().Feishu.Owner}, Context: &cb.Context{OpenChatID: chat}, Action: &cb.CallBackAction{Tag: "button", Name: "duo_question_submit", Value: map[string]interface{}{"duo_question": token}, FormValue: form}}}
}

func seedQuestionDelivery(t *testing.T, a *App) (Task, CodexPendingRequest, string, *string) {
	t.Helper()
	enableRunCards(t, a)
	a.store.Exec("INSERT OR IGNORE INTO feishu_chat_scopes VALUES('chat','test','owner')")
	task := taskFor(t, a)
	if err := a.bind("chat", task.ID); err != nil {
		t.Fatal(err)
	}
	p := addAsyncQuestion(t, a, task)
	content := ""
	a.feishu.createRunCard = func(_ context.Context, _ FeishuConfig, chat, raw, id string) (string, error) {
		if err := checkQuestionFormNesting(raw); err != nil {
			t.Fatal(err)
		}
		content = raw
		return "om-question", nil
	}
	a.feishu.updateRunCard = func(_ context.Context, _ FeishuConfig, id, raw string) error { content = raw; return nil }
	if err := a.feishu.questionCardStep(now()); err != nil {
		t.Fatal(err)
	}
	var token string
	if err := a.store.QueryRow("SELECT id FROM feishu_question_cards WHERE request_id=?", p.ID).Scan(&token); err != nil {
		t.Fatal(err)
	}
	return task, p, token, &content
}

func TestFeishuQuestionFormAndAsyncAnswer(t *testing.T) {
	runner := &fakeRunner{gate: make(chan struct{})}
	a := fixture(t, runner)
	task, p, token, content := seedQuestionDelivery(t, a)
	if !strings.Contains(*content, "安装范围") || !strings.Contains(*content, "form_submit") || !strings.Contains(*content, "select_static") || !strings.Contains(*content, token) {
		t.Fatal(*content)
	}
	forms := map[string]interface{}{"choice_0": "0", "text_1": "无需下载本地模型"}
	bad := questionCallback(a, "forwarded", token, forms)
	if r, _ := a.feishu.receiveCard(bad); r.Toast.Type != "error" {
		t.Fatal("forwarded card accepted")
	}
	bad = questionCallback(a, "chat", token, forms)
	bad.Event.Operator.OpenID = "stranger"
	if r, _ := a.feishu.receiveCard(bad); r.Toast.Type != "error" {
		t.Fatal("wrong owner accepted")
	}
	if r, _ := a.feishu.receiveCard(questionCallback(a, "chat", token, map[string]interface{}{"choice_0": "0"})); r.Toast.Type != "error" {
		t.Fatal("partial answers accepted")
	}
	if r, _ := a.feishu.receiveCard(questionCallback(a, "chat", token, forms)); r.Toast.Type != "success" || r.Card == nil {
		t.Fatal(r)
	}
	if r, _ := a.feishu.receiveCard(questionCallback(a, "chat", token, forms)); r.Toast.Type == "success" {
		t.Fatal("duplicate card answer acknowledged twice")
	}
	runs, err := a.store.runs(task.ID)
	if err != nil || len(runs) != 1 || runs[0].Source != "feishu" || !strings.Contains(runs[0].Input, "云端") || !strings.Contains(runs[0].Input, "无需下载本地模型") {
		t.Fatal(runs, err)
	}
	if err = a.feishu.questionCardStep(now() + 3000); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(*content, "form_submit") || !strings.Contains(*content, "问题已关闭") {
		t.Fatal(*content)
	}
	var state string
	a.store.QueryRow("SELECT status FROM async_questions WHERE id=?", p.ID).Scan(&state)
	if state != "answered" {
		t.Fatal(state)
	}
	close(runner.gate)
	waitUntil(t, func() bool { r, _ := a.store.runs(task.ID); return len(r) == 1 && r[0].Status == "done" })
}

func TestFeishuQuestionNativeAndWebRace(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	enableRunCards(t, a)
	a.store.Exec("INSERT OR IGNORE INTO feishu_chat_scopes VALUES('chat','test','owner')")
	task := taskFor(t, a)
	a.bind("chat", task.ID)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &codexPending{CodexPendingRequest: CodexPendingRequest{ID: uid(), TaskID: task.ID, RunID: "synthetic-run", Method: "item/tool/requestUserInput", Params: json.RawMessage(`{"questions":[{"id":"q","question":"选择范围","options":[{"label":"仅当前"}],"isOther":false}]}`)}, ctx: ctx, answer: make(chan json.RawMessage, 1)}
	a.codexRequests.pending[p.ID] = p
	a.feishu.createRunCard = func(context.Context, FeishuConfig, string, string, string) (string, error) { return "om-native", nil }
	if err := a.feishu.questionCardStep(now()); err != nil {
		t.Fatal(err)
	}
	var token string
	a.store.QueryRow("SELECT id FROM feishu_question_cards WHERE request_id=?", p.ID).Scan(&token)
	if r, _ := a.feishu.receiveCard(questionCallback(a, "chat", token, map[string]interface{}{"choice_0": "0", "text_0": "越界"})); r.Toast.Type != "error" {
		t.Fatal("unlisted custom answer accepted")
	}
	if r, _ := a.feishu.receiveCard(questionCallback(a, "chat", token, map[string]interface{}{"choice_0": "0"})); r.Toast.Type != "success" {
		t.Fatal(r)
	}
	select {
	case raw := <-p.answer:
		if !strings.Contains(string(raw), "仅当前") {
			t.Fatal(string(raw))
		}
	default:
		t.Fatal("native answer missing")
	}
	if err := a.answerCodexInteraction(task.ID, p.ID, CodexAnswer{Answers: map[string]CodexQuestionAnswer{"q": {Answers: []string{"仅当前"}}}}); !errors.Is(err, errCodexRequestExpired) {
		t.Fatal("web replay accepted", err)
	}
}

func TestFeishuQuestionActiveAnswerSteersWithoutQueue(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task, question, token, _ := seedQuestionDelivery(t, a)
	control := newCodexTurnControl(nil)
	control.setReady()
	a.mu.Lock()
	a.workers[task.ID] = &worker{runID: "active", runKind: "chat", steer: control}
	a.mu.Unlock()
	t.Cleanup(func() { a.mu.Lock(); delete(a.workers, task.ID); a.mu.Unlock(); control.close() })
	go func() { r := <-control.requests; control.resolve(r, r.record(r.Text), "") }()
	r, _ := a.feishu.receiveCard(questionCallback(a, "chat", token, map[string]interface{}{"choice_0": "1", "text_1": "立即引导"}))
	if r.Toast.Type != "success" {
		t.Fatal(r)
	}
	runs, _ := a.store.runs(task.ID)
	if len(runs) != 0 {
		t.Fatal("answer queued", runs)
	}
	var state string
	a.store.QueryRow("SELECT status FROM async_questions WHERE id=?", question.ID).Scan(&state)
	if state != "steered" {
		t.Fatal(state)
	}
}

func TestFeishuQuestionUnconfirmedStaysReserved(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task, question, token, _ := seedQuestionDelivery(t, a)
	control := newCodexTurnControl(nil)
	control.setReady()
	a.mu.Lock()
	a.workers[task.ID] = &worker{runID: "active", runKind: "chat", steer: control}
	a.mu.Unlock()
	t.Cleanup(func() { a.mu.Lock(); delete(a.workers, task.ID); a.mu.Unlock(); control.close() })
	go func() { <-control.requests; control.close() }()
	r, _ := a.feishu.receiveCard(questionCallback(a, "chat", token, map[string]interface{}{"choice_0": "1", "text_1": "原答案"}))
	if r.Toast.Type != "error" {
		t.Fatal(r)
	}
	var state string
	a.store.QueryRow("SELECT status FROM async_questions WHERE id=?", question.ID).Scan(&state)
	if state != "steering" {
		t.Fatal(state)
	}
	runs, _ := a.store.runs(task.ID)
	if len(runs) != 0 {
		t.Fatal("unknown answer queued")
	}
}

func TestFeishuQuestionScopeSecretAndSize(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task, _, token, _ := seedQuestionDelivery(t, a)
	other := taskFor(t, a)
	a.bind("chat", other.ID)
	form := map[string]interface{}{"choice_0": "0", "text_1": "supplement"}
	if r, _ := a.feishu.receiveCard(questionCallback(a, "chat", token, form)); r.Toast.Type != "error" {
		t.Fatal("old task card accepted")
	}
	a.bind("chat", task.ID)
	a.store.Exec("UPDATE tasks SET session='new-session' WHERE id=?", task.ID)
	if r, _ := a.feishu.receiveCard(questionCallback(a, "chat", token, form)); r.Toast.Type == "success" {
		t.Fatal("old session accepted")
	}
	for _, params := range []string{`{"questions":[{"id":"q","question":"SECRET_DESCRIPTION","isSecret":true}]}`, `{"command":"PRIVATE_COMMAND"}`} {
		p := CodexPendingRequest{Method: "item/tool/requestUserInput", Params: json.RawMessage(params)}
		card := a.feishu.questionCard(task.ID, token, p, true)
		raw, _ := json.Marshal(card)
		if strings.Contains(string(raw), "SECRET_DESCRIPTION") || strings.Contains(string(raw), "PRIVATE_COMMAND") || strings.Contains(string(raw), "form_submit") {
			t.Fatal("sensitive request forwarded", string(raw))
		}
	}
	questions := []map[string]any{}
	for i := 0; i < 8; i++ {
		questions = append(questions, map[string]any{"id": uid(), "question": strings.Repeat("问", 1900), "isOther": true})
	}
	params, _ := json.Marshal(map[string]any{"questions": questions})
	card := a.feishu.questionCard(task.ID, token, CodexPendingRequest{Method: "item/tool/requestUserInput", Params: params}, true)
	raw, _ := json.Marshal(card)
	wire, _ := json.Marshal(map[string]string{"content": string(raw)})
	if len(wire) >= 24*1024 || strings.Contains(string(raw), "form_submit") {
		t.Fatal("oversize form not replaced", len(wire))
	}
}

func TestFeishuQuestionDeliveryRetriesAndFallback(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	_, _, token, _ := seedQuestionDelivery(t, a)
	// Simulate an ambiguous first create and verify every retry keeps its UUID.
	a.store.Exec("UPDATE feishu_question_cards SET message_id='',last_hash='',state='pending',first_attempt=0,next_at=0 WHERE id=?", token)
	creates := 0
	a.feishu.createRunCard = func(_ context.Context, _ FeishuConfig, chat, raw, id string) (string, error) {
		creates++
		if id != token {
			t.Fatal("new UUID on retry")
		}
		return "", errors.New("synthetic card denied")
	}
	texts := 0
	a.feishu.deliver = func(_ context.Context, _ FeishuConfig, chat, text, id string) error {
		texts++
		if !strings.Contains(text, "请打开 Duo") || id != token+"-question" {
			t.Fatal(text, id)
		}
		return nil
	}
	at := now()
	for i := 0; i < 6; i++ {
		if a.feishu.questionCardStep(at+int64(i)*60000) == nil {
			t.Fatal("error lost")
		}
	}
	if err := a.feishu.questionCardStep(at + 360000); err != nil {
		t.Fatal(err)
	}
	if err := a.feishu.questionCardStep(at + 420000); err != nil {
		t.Fatal(err)
	}
	if creates != 6 || texts != 1 {
		t.Fatal(creates, texts)
	}
	// Replacing the bot does not authorize the previous bot's old card.
	c := a.config.get()
	c.Feishu.AppID = "replacement"
	a.config.save(c)
	a.feishu.createRunCard = func(context.Context, FeishuConfig, string, string, string) (string, error) {
		t.Fatal("new bot inherited the old chat")
		return "", nil
	}
	if err := a.feishu.questionCardStep(at + 480000); err != nil {
		t.Fatal(err)
	}
	if r, _ := a.feishu.receiveCard(questionCallback(a, "chat", token, map[string]interface{}{"choice_0": "0", "text_1": "x"})); r.Toast.Type != "error" {
		t.Fatal("replacement bot answered old card")
	}
}

func TestFeishuQuestionSwitchAwayDoesNotBlockOtherDelivery(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	first, _, token, _ := seedQuestionDelivery(t, a)
	other := taskFor(t, a)
	a.bind("chat", other.ID)
	p := addAsyncQuestion(t, a, other)
	at := now() + 3000
	if err := a.feishu.questionCardStep(at); err != nil {
		t.Fatal(err)
	}
	if err := a.feishu.questionCardStep(at + 3000); err != nil {
		t.Fatal(err)
	}
	var state string
	a.store.QueryRow("SELECT state FROM feishu_question_cards WHERE id=?", token).Scan(&state)
	if state != "closed" {
		t.Fatal("old question not closed", state)
	}
	var message string
	a.store.QueryRow("SELECT message_id FROM feishu_question_cards WHERE request_id=?", p.ID).Scan(&message)
	if message == "" {
		t.Fatal("old binding starved new task question")
	}
	a.bind("chat", first.ID)
	if err := a.feishu.questionCardStep(at + 6000); err != nil {
		t.Fatal(err)
	}
	a.store.QueryRow("SELECT state FROM feishu_question_cards WHERE id=?", token).Scan(&state)
	if state != "sent" {
		t.Fatal("still-pending question not restored on return", state)
	}
}

func TestFeishuProgressRetainedAtCompletion(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	enableRunCards(t, a)
	task, run, _ := seedRunCard(t, a, "running")
	for i := 0; i < 8; i++ {
		a.store.event(task.ID, run, "progress", strings.Repeat("过程", i+1))
	}
	a.store.event(task.ID, run, "tool", "PRIVATE_TOOL")
	a.store.event(task.ID, run, "log", "PRIVATE_LOG")
	d := runCardDelivery{title: "fixture", run: Run{ID: run, TaskID: task.ID, Status: "done", Result: "最终回复"}}
	raw, err := a.feishu.runCardContent(d)
	if err != nil || !strings.Contains(raw, "中间过程") || !strings.Contains(raw, "最终回复") || strings.Contains(raw, "PRIVATE_") {
		t.Fatal(raw, err)
	}
	if strings.Count(raw, "过程过程过程过程过程过程过程过程") != 1 {
		t.Fatal("latest progress absent")
	}
	// No card redraw when the pending question stays unchanged: preserve input.
	_, _, _, _ = seedQuestionDelivery(t, a)
	a.feishu.updateRunCard = func(context.Context, FeishuConfig, string, string) error {
		t.Fatal("unchanged question redrawn")
		return nil
	}
	if err = a.feishu.questionCardStep(now() + int64((3*time.Second)/time.Millisecond)); err != nil {
		t.Fatal(err)
	}
}
