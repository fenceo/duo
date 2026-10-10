package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func feishuHarnessTask(t *testing.T, a *App) Task {
	t.Helper()
	mode, err := a.store.resolveMode(harnessWorkspaceMode, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.createWithExecutionAndMode("Harness fixture", a.config.get().Workspaces[0], "deepseek-flash", "deepseek-harness", "", &mode)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestFeishuHarnessPermissionNotificationAndWebAnswer(t *testing.T) {
	for _, test := range []struct{ decision, expected string }{
		{"accept", `{"outcome":{"outcome":"selected","optionId":"native-allow"}}`},
		{"decline", `{"outcome":{"outcome":"selected","optionId":"native-reject"}}`},
		{"cancel", `{"outcome":{"outcome":"cancelled"}}`},
	} {
		t.Run(test.decision, func(t *testing.T) {
			a := fixture(t, &fakeRunner{})
			enableRunCards(t, a)
			a.store.Exec("INSERT OR IGNORE INTO feishu_chat_scopes VALUES('chat','test','owner')")
			task := feishuHarnessTask(t, a)
			if err := a.bind("chat", task.ID); err != nil {
				t.Fatal(err)
			}
			pending, result, _ := beginCodexApproval(t, a, task, harnessPermissionMethod, harnessPermissionTestParams)
			var content string
			creates, updates := 0, 0
			a.feishu.createRunCard = func(_ context.Context, _ FeishuConfig, chat, raw, id string) (string, error) {
				if chat != "chat" {
					t.Fatal("wrong destination", chat)
				}
				creates++
				content = raw
				return "om-harness-permission", nil
			}
			a.feishu.updateRunCard = func(_ context.Context, _ FeishuConfig, id, raw string) error {
				if id != "om-harness-permission" {
					t.Fatal("wrong card", id)
				}
				updates++
				content = raw
				return nil
			}
			at := now()
			if err := a.feishu.questionCardStep(at); err != nil {
				t.Fatal(err)
			}
			var token, state string
			if err := a.store.QueryRow("SELECT id,state FROM feishu_question_cards WHERE request_id=?", pending.ID).Scan(&token, &state); err != nil {
				t.Fatal("Harness permission never received a Feishu reminder", err)
			}
			if creates != 1 || state != "sent" || !strings.Contains(content, "AI 等待网页处理") || !strings.Contains(content, "权限审批") {
				t.Fatal("missing permission reminder", creates, state, content)
			}
			for _, private := range []string{"echo synthetic", "native-allow", "native-reject", "form_submit", "PRIVATE_COMMAND"} {
				if strings.Contains(content, private) {
					t.Fatal("permission details or approval controls forwarded", private)
				}
			}
			// A question callback cannot approve a permission or consume the request.
			r, err := a.feishu.receiveCard(questionCallback(a, "chat", token, map[string]interface{}{"decision": "accept"}))
			if err != nil || r.Toast.Type == "success" || len(a.codexRequests.list(task.ID)) != 1 {
				t.Fatal("Feishu question callback consumed a native permission", r, err)
			}
			request := toolsClient(t, a)
			request("/api/tasks/"+task.ID+"/approvals/"+pending.ID, "POST", CodexAnswer{Decision: test.decision}, 200)
			got := awaitCodexInteraction(t, result)
			if got.err != nil {
				t.Fatal(got.err)
			}
			requireCodexJSON(t, got.payload, test.expected)
			if err := a.feishu.questionCardStep(at + 4000); err != nil {
				t.Fatal(err)
			}
			a.store.QueryRow("SELECT state FROM feishu_question_cards WHERE id=?", token).Scan(&state)
			if state != "closed" || creates != 1 || updates != 1 || !strings.Contains(content, "问题已关闭") {
				t.Fatal("permission reminder not settled", state, creates, updates, content)
			}
			runs, err := a.store.runs(task.ID)
			if err != nil || len(runs) != 1 {
				t.Fatal("reminder created conversation work", runs, err)
			}
		})
	}
}

func TestFeishuHarnessPendingRequestsStayEngineScoped(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := feishuHarnessTask(t, a)
	params := `{"threadId":"thread","turnId":"turn","itemId":"item","questions":[{"id":"q","question":"Codex-only question"}]}`
	_, result, cancel := beginCodexApproval(t, a, task, "item/tool/requestUserInput", params)
	requests, err := a.feishu.pendingQuestions(task.ID)
	if err != nil || len(requests) != 0 {
		t.Fatal("Harness exposed a Codex-only interaction", requests, err)
	}
	cancel()
	awaitCodexInteraction(t, result)
	pending, result, cancel := beginCodexApproval(t, a, task, harnessPermissionMethod, harnessPermissionTestParams)
	requests, err = a.feishu.pendingQuestions(task.ID)
	if err != nil || len(requests) != 1 || requests[0].ID != pending.ID {
		b, _ := json.Marshal(requests)
		t.Fatal("native permission absent", string(b), err)
	}
	for _, state := range []struct{ table, field string }{{"task_preferences", "archived"}, {"task_options", "deleted"}} {
		if _, err := a.store.Exec("INSERT INTO "+state.table+"(task_id,"+state.field+") VALUES(?,1) ON CONFLICT(task_id) DO UPDATE SET "+state.field+"=1", task.ID); err != nil {
			t.Fatal(err)
		}
		requests, err = a.feishu.pendingQuestions(task.ID)
		if err != nil || len(requests) != 0 {
			t.Fatal(state.field+" task exposed a live permission", requests, err)
		}
		a.store.Exec("UPDATE "+state.table+" SET "+state.field+"=0 WHERE task_id=?", task.ID)
	}
	cancel()
	awaitCodexInteraction(t, result)
	requests, err = a.feishu.pendingQuestions(task.ID)
	if err != nil || len(requests) != 0 {
		t.Fatal("cancelled permission still open", requests, err)
	}
}
