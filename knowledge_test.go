package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

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

func TestKnowledgeTitleFollowsRunKind(t *testing.T) {
	stamp := int64(1758000000000)
	// A settled chat turn is named by what it was about.
	if got := runKnowledgeTitle("chat", "修复串口乱码", stamp); got != "修复串口乱码" {
		t.Fatal(got)
	}
	// The knowledge prompt is an instruction, so the entry is named by the moment.
	if got := runKnowledgeTitle("knowledge", "请整理已有对话", stamp); !strings.HasPrefix(got, "执行总结 · ") {
		t.Fatal(got)
	}
	if got := runKnowledgeTitle("chat", strings.Repeat("长", 61), stamp); !strings.HasPrefix(got, "执行总结 · ") {
		t.Fatal(got)
	}
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
	saved, _ := a.store.knowledgeForRun(task.ID, r.ID)
	if saved.Content != r.Result || saved.Revision != 1 || saved.Status != "observed" || saved.Source != "run" || a.bound("chat") != other.ID {
		t.Fatal(saved, "selection changed")
	}
	if stray, _ := a.store.knowledgeForRun(other.ID, r.ID); stray.ID != "" {
		t.Fatal("saved into selected task instead of card's task")
	}
	raw, _ = json.Marshal(response.Card.Data)
	if !strings.Contains(string(raw), "已保存到任务知识") || strings.Contains(string(raw), "保存知识\"") {
		t.Fatal(string(raw))
	}
}

func TestKnowledgeAdoptGuardsIncompleteDraftsAndConflicts(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task, r := knowledgeFixture(t, a)
	other := taskFor(t, a)
	if _, err := a.store.adoptKnowledge(other.ID, r.ID, 0); err == nil {
		t.Fatal("cross-task run saved")
	}
	saved, err := a.store.adoptKnowledge(task.ID, r.ID, 0)
	if err != nil || saved.Revision != 1 || saved.Content != r.Result || saved.Status != "observed" || saved.Source != "run" {
		t.Fatal(saved, err)
	}
	// A repeated callback reads the same entry instead of writing a second one.
	again, err := a.store.adoptKnowledge(task.ID, r.ID, 0)
	if err != nil || again.Revision != saved.Revision || again.ID != saved.ID {
		t.Fatal(again, err)
	}
	// A manual edit is newer than the generation, so a stale save must conflict.
	edited := saved
	edited.Content = "手工维护的知识"
	if err = a.store.writeKnowledge(edited, false); err != nil {
		t.Fatal(err)
	}
	for _, revision := range []int64{0, saved.Revision} {
		if _, err = a.store.adoptKnowledge(task.ID, r.ID, revision); !errors.Is(err, errConflict) {
			t.Fatal("stale generation overwrote newer knowledge", revision, err)
		}
	}
	merged, err := a.store.adoptKnowledge(task.ID, r.ID, saved.Revision+1)
	if err != nil || merged.Revision != saved.Revision+2 || merged.Content != r.Result {
		t.Fatal(merged, err)
	}
	for _, status := range []string{"queued", "running", "failed", "interrupted"} {
		a.store.Exec("UPDATE runs SET status=? WHERE id=?", status, r.ID)
		if _, err = a.store.adoptKnowledge(task.ID, r.ID, merged.Revision); err == nil {
			t.Fatal("saved incomplete draft", status)
		}
	}
	items, _ := a.store.knowledgeList(task.ID)
	if len(items) != 1 || items[0].ID != saved.ID {
		t.Fatal(items)
	}
}

func TestKnowledgeAPIStoresManyEntriesPerTask(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	enableRunCards(t, a)
	task, r := knowledgeFixture(t, a)
	request := toolsClient(t, a)
	base := "/api/tasks/" + task.ID + "/knowledge"
	// A delivery retry reuses the action token and renders the same card.
	first, _ := a.feishu.knowledgeResultCard("chat", task.ID, r.ID)
	second, _ := a.feishu.knowledgeResultCard("chat", task.ID, r.ID)
	one, _ := json.Marshal(first)
	two, _ := json.Marshal(second)
	if string(one) != string(two) {
		t.Fatal("new action token on retry")
	}
	adopt := "/api/tasks/" + task.ID + "/note/adopt"
	request(adopt, "POST", map[string]any{"run_id": r.ID, "revision": 0}, 200)
	request(adopt, "POST", map[string]any{"run_id": r.ID, "revision": 0}, 200)
	var items []Knowledge
	json.Unmarshal(request(base, "GET", nil, 200), &items)
	if len(items) != 1 || items[0].Content != r.Result {
		t.Fatal(items)
	}
	// A task keeps several entries: drafts, manual notes and verified findings.
	var manual Knowledge
	json.Unmarshal(request(base, "POST", Knowledge{Title: "接线记录", Content: "USB 转串口接 COM5。", Status: "verified", Source: "manual"}, 201), &manual)
	if manual.ID == "" || manual.Status != "verified" || manual.Source != "manual" || manual.Revision != 1 {
		t.Fatal(manual)
	}
	request(base+"/"+manual.ID, "PUT", Knowledge{Title: "接线记录", Content: "USB 转串口接 COM3。", Status: "verified", Revision: 1}, 200)
	request(base+"/"+manual.ID, "PUT", Knowledge{Title: "接线记录", Content: "并发覆盖", Revision: 1}, 409)
	items = nil
	json.Unmarshal(request(base, "GET", nil, 200), &items)
	if len(items) != 2 {
		t.Fatal(items)
	}
	body := string(request(base+"?download=1", "GET", nil, 200))
	if !strings.Contains(body, "任务知识") || !strings.Contains(body, "接线记录") {
		t.Fatal(body)
	}
	request(base+"/"+manual.ID, "DELETE", map[string]any{"revision": 1}, 409)
	request(base+"/"+manual.ID, "DELETE", map[string]any{"revision": 2}, 200)
	items = nil
	json.Unmarshal(request(base, "GET", nil, 200), &items)
	if len(items) != 1 {
		t.Fatal(items)
	}
}

func TestKnowledgeFromFinishedRunIsIdempotent(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	request := toolsClient(t, a)
	base := "/api/tasks/" + task.ID + "/knowledge/from-run"
	run := Run{ID: uid(), TaskID: task.ID, Kind: "chat", Status: "done", Result: "找到 RX 没接地的原因。", Created: now()}
	a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,created) VALUES(?,?,'修复串口乱码','chat','web','done',?,?)", run.ID, task.ID, run.Result, run.Created)
	var saved Knowledge
	json.Unmarshal(request(base, "POST", map[string]string{"run_id": run.ID}, 200), &saved)
	if saved.ID == "" || saved.Title != "修复串口乱码" || saved.Source != "run" || saved.Status != "observed" {
		t.Fatal(saved)
	}
	var again Knowledge
	json.Unmarshal(request(base, "POST", map[string]string{"run_id": run.ID}, 200), &again)
	if again.ID != saved.ID {
		t.Fatal("duplicate entry", again)
	}
	// Once settled, the entry stays even if the run record is later disturbed.
	a.store.Exec("UPDATE runs SET status='running' WHERE id=?", run.ID)
	request(base, "POST", map[string]string{"run_id": run.ID}, 200)
	request(base, "POST", map[string]string{"run_id": "missing"}, 400)
	if _, err := a.store.knowledgeFromRun(task.ID, "missing"); err == nil {
		t.Fatal("missing run settled")
	}
	pending := Run{ID: uid(), TaskID: task.ID, Kind: "chat", Status: "queued", Created: now()}
	a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,created) VALUES(?,?,'还没跑完','chat','web','queued','',?)", pending.ID, task.ID, pending.Created)
	if _, err := a.store.knowledgeFromRun(task.ID, pending.ID); err == nil {
		t.Fatal("unfinished run settled")
	}
}

func TestKnowledgeTruncationPreservesUTF8AndLimit(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	content := strings.Repeat("😀", 100000)
	run := Run{ID: uid(), TaskID: task.ID, Kind: "chat", Status: "done", Result: content, Created: now()}
	if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,created) VALUES(?,?,'大结果','chat','web','done',?,?)", run.ID, task.ID, content, run.Created); err != nil {
		t.Fatal(err)
	}
	saved, err := a.store.knowledgeFromRun(task.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Content) > knowledgeMaxBytes {
		t.Fatalf("knowledge is %d bytes, limit is %d", len(saved.Content), knowledgeMaxBytes)
	}
	if !utf8.ValidString(saved.Content) {
		t.Fatal("truncated knowledge is not valid UTF-8")
	}
	if !strings.HasSuffix(saved.Content, knowledgeTruncatedNotice) {
		t.Fatal("truncated knowledge is missing its notice")
	}
}
