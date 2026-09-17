package main

import (
	"encoding/json"
	cb "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	"strings"
	"testing"
)

func TestTaskCardSelectionAndOneShotContinue(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	c := a.config.get()
	c.Feishu = FeishuConfig{Enabled: true, AppID: "test", Secret: "test", Owner: "owner"}
	a.config.save(c)
	card, err := a.feishu.taskListCard("chat", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(card)
	if !strings.Contains(string(raw), task.Title) || !strings.Contains(string(raw), "jianzuo_action") {
		t.Fatal("no actions")
	}
	var extra int
	a.store.QueryRow("SELECT count(*) FROM feishu_card_actions WHERE task_id=? AND action IN ('view','continue')", task.ID).Scan(&extra)
	if extra != 0 {
		t.Fatal("list should only offer direct entry")
	}
	// Old cards remain safe, but new cards no longer show duplicate controls.
	if _, err = a.feishu.taskDetailCard("chat", task); err != nil {
		t.Fatal(err)
	}
	newRaw, _ := json.Marshal(card)
	if strings.Contains(string(newRaw), "继续执行") || strings.Contains(string(newRaw), "刷新进展") {
		t.Fatal("duplicate controls")
	}
	a.feishu.cardButton("chat", task.ID, "continue", "legacy", "", 0)
	a.feishu.cardButton("chat", task.ID, "view", "legacy", "", 0)
	token := func(action string) string {
		var v string
		a.store.QueryRow("SELECT id FROM feishu_card_actions WHERE task_id=? AND action=? LIMIT 1", task.ID, action).Scan(&v)
		return v
	}
	click := func(action, owner, chat string) *cb.CardActionTriggerResponse {
		r, e := a.feishu.receiveCard(&cb.CardActionTriggerEvent{Event: &cb.CardActionTriggerRequest{Operator: &cb.Operator{OpenID: owner}, Context: &cb.Context{OpenChatID: chat}, Action: &cb.CallBackAction{Value: map[string]interface{}{"jianzuo_action": token(action)}}}})
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	if click("continue", "stranger", "chat").Toast.Type != "error" {
		t.Fatal("unbound user accepted")
	}
	if click("continue", "owner", "forwarded").Toast.Type != "error" {
		t.Fatal("forwarded card accepted")
	}
	if click("view", "owner", "chat").Card == nil || a.bound("chat") != "" {
		t.Fatal("view changes selection")
	}
	if click("enter", "owner", "chat").Card == nil || a.bound("chat") != task.ID {
		t.Fatal("enter failed")
	}
	r, _ := a.store.runs(task.ID)
	if len(r) != 0 {
		t.Fatal("enter ran task")
	}
	if click("continue", "owner", "chat").Toast.Type != "success" {
		t.Fatal("continue failed")
	}
	waitUntil(t, func() bool { r, _ := a.store.runs(task.ID); return len(r) == 1 && r[0].Status == "done" })
	click("continue", "owner", "chat")
	r, _ = a.store.runs(task.ID)
	if len(r) != 1 {
		t.Fatal("double-click ran twice")
	}
	if err = a.feishu.enqueueCard("chat", card); err != nil {
		t.Fatal(err)
	}
	var count int
	a.store.QueryRow("SELECT count(*) FROM outbox_cards").Scan(&count)
	if count != 1 {
		t.Fatal("card not persisted")
	}
}

func TestExpiredCardAndPagination(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	for i := 0; i < 10; i++ {
		taskFor(t, a)
	}
	card, err := a.feishu.taskListCard("chat", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(card)
	if !strings.Contains(string(raw), "下一页") {
		t.Fatal("missing pagination")
	}
	var token string
	a.store.QueryRow("SELECT id FROM feishu_card_actions LIMIT 1").Scan(&token)
	a.store.Exec("UPDATE feishu_card_actions SET expires=0 WHERE id=?", token)
	r, _ := a.feishu.applyCardAction("chat", token)
	if r.Toast.Type != "error" {
		t.Fatal("expired token accepted")
	}
}

func TestTaskExitKeepsRunAndStaleCardCannotExitAnotherTask(t *testing.T) {
	runner := &fakeRunner{gate: make(chan struct{})}
	a := fixture(t, runner)
	first := taskFor(t, a)
	second := taskFor(t, a)
	a.bind("chat", first.ID)
	_, err := a.submit(first.ID, "work", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { r, _ := a.store.runs(first.ID); return len(r) == 1 && r[0].Status == "running" })
	current, _ := a.store.task(first.ID)
	card, err := a.feishu.taskDetailCard("chat", current)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(card)
	if !strings.Contains(string(raw), "停止执行") || !strings.Contains(string(raw), "退出任务") {
		t.Fatal("missing controls")
	}
	var exitToken, stopToken string
	a.store.QueryRow("SELECT id FROM feishu_card_actions WHERE task_id=? AND action='exit'", first.ID).Scan(&exitToken)
	a.store.QueryRow("SELECT id FROM feishu_card_actions WHERE task_id=? AND action='stop'", first.ID).Scan(&stopToken)
	a.bind("chat", second.ID)
	a.feishu.applyCardAction("chat", exitToken)
	a.feishu.applyCardAction("chat", stopToken)
	if a.bound("chat") != second.ID {
		t.Fatal("stale card changed current task")
	}
	r, _ := a.store.runs(first.ID)
	if r[0].Status != "running" {
		t.Fatal("stale card stopped run")
	}
	a.bind("chat", first.ID)
	response, _ := a.feishu.applyCardAction("chat", exitToken)
	if response.Toast.Type != "success" || a.bound("chat") != "" {
		t.Fatal("exit failed")
	}
	r, _ = a.store.runs(first.ID)
	if r[0].Status != "running" {
		t.Fatal("exit stopped run")
	}
	close(runner.gate)
	waitUntil(t, func() bool { r, _ := a.store.runs(first.ID); return r[0].Status == "done" })
}

func TestTaskStatusFilterPreservesSelection(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	a.bind("chat", task.ID)
	card, err := a.feishu.taskListCard("chat", "status:failed", 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(card)
	if !strings.Contains(string(raw), "共 0 项") || strings.Contains(string(raw), task.Title) {
		t.Fatal("filter or context missing")
	}
	if a.bound("chat") != task.ID {
		t.Fatal("browse cleared selection")
	}
}
