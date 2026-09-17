package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	cb "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

func knowledgeFixture(t *testing.T, a *App) (Task, Run) {
	t.Helper()
	task := taskFor(t, a)
	r := Run{ID: uid(), TaskID: task.ID, Kind: "knowledge", Status: "done", Result: "# 串口知识\n已验证 115200 8N1。", Created: now()}
	_, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,created) VALUES(?,?,'整理知识','knowledge','feishu','done',?,?)", r.ID, task.ID, r.Result, r.Created)
	if err != nil {
		t.Fatal(err)
	}
	return task, r
}

func TestKnowledgeCardSavesExactDraftOnceAfterTaskSwitch(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	enableRunCards(t, a)
	task, r := knowledgeFixture(t, a)
	other := taskFor(t, a)
	a.bind("chat", other.ID)
	card, err := a.feishu.knowledgeResultCard("chat", task.ID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(card)
	if !strings.Contains(string(raw), "保存知识") || !strings.Contains(string(raw), "view=note") {
		t.Fatal(string(raw))
	}
	var token string
	a.store.QueryRow("SELECT id FROM feishu_card_actions WHERE task_id=? AND action='save_knowledge'", task.ID).Scan(&token)
	// Forwarded cards and another user must not write any knowledge.
	response, _ := a.feishu.applyCardAction("another-chat", token)
	if response.Toast.Type != "error" {
		t.Fatal("forwarded card accepted")
	}
	response, _ = a.feishu.receiveCard(&cb.CardActionTriggerEvent{Event: &cb.CardActionTriggerRequest{Operator: &cb.Operator{OpenID: "stranger"}, Context: &cb.Context{OpenChatID: "chat"}, Action: &cb.CallBackAction{Value: map[string]interface{}{"jianzuo_action": token}}}})
	if response.Toast.Type != "error" {
		t.Fatal("stranger accepted")
	}
	for i := 0; i < 2; i++ {
		response, err = a.feishu.applyCardAction("chat", token)
		if err != nil || response.Toast.Type != "success" {
			t.Fatal(response, err)
		}
	}
	n, _ := a.store.note(task.ID)
	if n.Content != r.Result || n.Revision != 1 || a.bound("chat") != other.ID {
		t.Fatal(n, "selection changed")
	}
	n, _ = a.store.note(other.ID)
	if n.Revision != 0 {
		t.Fatal("saved into selected task instead of card's task")
	}
	raw, _ = json.Marshal(response.Card.Data)
	if !strings.Contains(string(raw), "已保存到任务知识") || strings.Contains(string(raw), "保存知识\"") {
		t.Fatal(string(raw))
	}
}

func TestKnowledgeSaveRejectsModifiedNotesAndWrongRuns(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task, r := knowledgeFixture(t, a)
	other := taskFor(t, a)
	if _, err := a.store.adoptKnowledge(other.ID, r.ID, 0); err == nil {
		t.Fatal("cross-task run saved")
	}
	n, err := a.store.saveNote(task.ID, "手工维护的知识", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Make the edit unequivocally newer than the generation, even on a fast clock.
	a.store.Exec("UPDATE notes SET updated=? WHERE task_id=?", r.Created+1, task.ID)
	for _, revision := range []int64{0, n.Revision} {
		if _, err = a.store.adoptKnowledge(task.ID, r.ID, revision); !errors.Is(err, errConflict) {
			t.Fatal("stale generation overwrote newer knowledge", err)
		}
	}
	for _, status := range []string{"queued", "failed", "interrupted"} {
		a.store.Exec("UPDATE runs SET status=? WHERE id=?", status, r.ID)
		if _, err = a.store.adoptKnowledge(task.ID, r.ID, n.Revision); err == nil {
			t.Fatal("saved incomplete draft", status)
		}
	}
	n, _ = a.store.note(task.ID)
	if n.Content != "手工维护的知识" || n.Revision != 1 {
		t.Fatal(n)
	}
}

func TestKnowledgeAdoptAPIAndCardRetryStayConsistent(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task, r := knowledgeFixture(t, a)
	request := toolsClient(t, a)
	first, _ := a.feishu.knowledgeResultCard("chat", task.ID, r.ID)
	second, _ := a.feishu.knowledgeResultCard("chat", task.ID, r.ID)
	one, _ := json.Marshal(first)
	two, _ := json.Marshal(second)
	if string(one) != string(two) {
		t.Fatal("new action token on retry")
	}
	request("/api/tasks/"+task.ID+"/note/adopt", "POST", map[string]any{"run_id": r.ID, "revision": 0}, 200)
	request("/api/tasks/"+task.ID+"/note/adopt", "POST", map[string]any{"run_id": r.ID, "revision": 0}, 200)
	var n Note
	json.Unmarshal(request("/api/tasks/"+task.ID+"/note", "GET", nil, 200), &n)
	if n.Content != r.Result || n.Revision != 1 {
		t.Fatal(n)
	}
	request("/api/tasks/"+task.ID+"/note", "PUT", Note{Content: "新知识", Revision: 1}, 200)
	request("/api/tasks/"+task.ID+"/note/adopt", "POST", map[string]any{"run_id": r.ID, "revision": 0}, 409)
}
