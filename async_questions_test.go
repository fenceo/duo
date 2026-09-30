package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestAsyncQuestionNativeProtocol(t *testing.T) {
	for _, scenario := range []string{"async-question", "async-question-only"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := codexFixtureConfig(t, scenario)
			messages, questions, finals := 0, 0, 0
			_, result, err := runCodexAppServer(context.Background(), cfg, Task{Engine: "codex", Workspace: t.TempDir()}, "fixture", func(kind, text string) {
				switch kind {
				case "async_question":
					var message AsyncQuestionMessage
					if json.Unmarshal([]byte(text), &message) != nil || message.ItemID != "async-question-1" || message.Session == "" || len(message.Questions) != 2 || len(message.Questions[0].Options) != 2 {
						t.Errorf("lost structured questions: %s", text)
					}
					messages++
				case "question":
					questions++
				case "assistant":
					finals++
				}
			})
			if err != nil || messages != 1 || questions != 1 {
				t.Fatal(err, messages, questions)
			}
			if scenario == "async-question-only" {
				if result != "选择云端或补充需求" || finals != 0 {
					t.Fatal(result, finals)
				}
			} else if !json.Valid([]byte(result)) || finals != 1 {
				t.Fatal("async question replaced final reply", result, finals)
			}
		})
	}
}

func addAsyncQuestion(t *testing.T, a *App, task Task) CodexPendingRequest {
	t.Helper()
	_, err := a.store.Exec("UPDATE tasks SET session='test-session' WHERE id=?", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	message, _ := json.Marshal(AsyncQuestionMessage{ItemID: uid(), Session: "test-session", Questions: []AsyncQuestion{{Title: "安装范围？", Options: []string{"云端", "本地"}}, {Title: "补充说明"}}})
	if err = a.store.saveAsyncQuestion(task.ID, "origin-run", string(message)); err != nil {
		t.Fatal(err)
	}
	// A repeated native notification must not duplicate the card.
	if err = a.store.saveAsyncQuestion(task.ID, "origin-run", string(message)); err != nil {
		t.Fatal(err)
	}
	task, _ = a.store.task(task.ID)
	pending, err := a.store.pendingAsyncQuestions(task)
	if err != nil || len(pending) == 0 {
		t.Fatal(err, pending)
	}
	return pending[len(pending)-1]
}

func TestAsyncQuestionDurableQueueAndHTTPRetry(t *testing.T) {
	runner := &fakeRunner{gate: make(chan struct{})}
	a := fixture(t, runner)
	task := taskFor(t, a)
	question := addAsyncQuestion(t, a, task)
	request := toolsClient(t, a)
	path := "/api/tasks/" + task.ID + "/questions/" + question.ID
	body := map[string]any{"answers": map[string]any{"0": map[string]any{"answers": []string{"云端"}}, "1": map[string]any{"answers": []string{"不下载本地模型"}}}}
	detail := request("/api/tasks/"+task.ID+"?recent=1", "GET", nil, 200)
	if !strings.Contains(string(detail), "duo/asyncQuestion") {
		t.Fatal("question missing outside pagination", string(detail))
	}
	first := request(path, "POST", body, 200)
	second := request(path, "POST", body, 200)
	if string(first) != string(second) {
		t.Fatal("retry returned different delivery", string(first), string(second))
	}
	runs, err := a.store.runs(task.ID)
	if err != nil || len(runs) != 1 || !strings.Contains(runs[0].Input, "不下载本地模型") {
		t.Fatal(err, runs)
	}
	task, _ = a.store.task(task.ID)
	pending, _ := a.store.pendingAsyncQuestions(task)
	if len(pending) != 0 {
		t.Fatal(pending)
	}
	close(runner.gate)
	waitUntil(t, func() bool { runs, _ := a.store.runs(task.ID); return runs[0].Status == "done" })
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.inputs) != 1 || runner.sessions[0] != "test-session" {
		t.Fatal("wrong session or duplicate answer", runner.sessions)
	}
}

func TestAsyncQuestionRestartScopeAndDismiss(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	question := addAsyncQuestion(t, a, task)
	reopened, err := openStore(a.store.directory)
	if err != nil {
		t.Fatal(err)
	}
	task, _ = a.store.task(task.ID)
	pending, err := reopened.pendingAsyncQuestions(task)
	reopened.Close()
	if err != nil || len(pending) != 1 {
		t.Fatal("question lost on restart", err, pending)
	}
	_, _, err = a.store.questionAnswer(task.ID, question.ID, []string{"云端"})
	if err == nil {
		t.Fatal("incomplete answer accepted")
	}
	other := taskFor(t, a)
	_, _, err = a.store.questionAnswer(other.ID, question.ID, []string{"云端", "说明"})
	if !errors.Is(err, errAsyncQuestionExpired) {
		t.Fatal(err)
	}
	_, err = a.store.Exec("UPDATE tasks SET session='new-session' WHERE id=?", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, _ = a.store.task(task.ID)
	pending, _ = a.store.pendingAsyncQuestions(task)
	if len(pending) != 0 {
		t.Fatal("old question leaked into new session")
	}
	_, err = a.submitWithOptions(task.ID, "", "chat", "web", SubmitOptions{QuestionID: question.ID, QuestionAnswers: []string{"云端", "说明"}})
	if !errors.Is(err, errAsyncQuestionExpired) {
		t.Fatal(err)
	}
	request := toolsClient(t, a)
	request("/api/tasks/"+task.ID+"/questions/"+question.ID, "POST", map[string]bool{"dismiss": true}, 200)
	var status string
	a.store.QueryRow("SELECT status FROM async_questions WHERE id=?", question.ID).Scan(&status)
	if status != "dismissed" {
		t.Fatal(status)
	}
}

func TestAsyncAnswerSteersNativeTurnWithoutQueue(t *testing.T) {
	a := fixture(t, codexSteerBridgeRunner{config: codexFixtureConfig(t, "steer-question"), workspace: t.TempDir()})
	task := taskFor(t, a)
	first, err := a.submit(task.ID, "first", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { state := a.liveInteraction(task.ID); return state != nil && state.CanSteer })
	question := addAsyncQuestion(t, a, task)
	request := toolsClient(t, a)
	path := "/api/tasks/" + task.ID + "/questions/" + question.ID
	body := map[string]any{"answers": map[string]any{"0": map[string]any{"answers": []string{"云端"}}, "1": map[string]any{"answers": []string{"不下载本地模型"}}}}
	response := request(path, "POST", body, 200)
	var result AsyncAnswerDelivery
	if json.Unmarshal(response, &result) != nil || !result.Accepted || result.Delivery != "steer" || result.RunID != first.ID {
		t.Fatal("answer did not steer the current turn", string(response))
	}
	waitUntil(t, func() bool { runs, _ := a.store.runs(task.ID); return len(runs) == 1 && runs[0].Status == "done" })
	if retry := request(path, "POST", body, 200); string(response) != string(retry) {
		t.Fatal("retry after completion changed delivery")
	}
	runs, _ := a.store.runs(task.ID)
	if len(runs) != 1 {
		t.Fatal("answer created a queued turn")
	}
	events, _ := a.store.events(task.ID, 0)
	count := 0
	for _, event := range events {
		if event.Kind == "user" && strings.Contains(event.Text, "不下载本地模型") {
			count++
			if event.RunID != first.ID {
				t.Fatal("answer stored in wrong turn")
			}
		}
	}
	if count != 1 {
		t.Fatal("answer must be recorded once after native acceptance", count)
	}
	var state string
	a.store.QueryRow("SELECT status FROM async_questions WHERE id=?", question.ID).Scan(&state)
	if state != "steered" {
		t.Fatal(state)
	}
}

func TestAsyncAnswerUnconfirmedNeverFallsBackAndRejectCanRetry(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "rejected"}[reject], func(t *testing.T) {
			a := fixture(t, &fakeRunner{})
			task := taskFor(t, a)
			question := addAsyncQuestion(t, a, task)
			control := newCodexTurnControl(nil)
			control.setReady()
			a.mu.Lock()
			a.workers[task.ID] = &worker{runID: "active", runKind: "chat", steer: control}
			a.mu.Unlock()
			t.Cleanup(func() { a.mu.Lock(); delete(a.workers, task.ID); a.mu.Unlock(); control.close() })
			ack := make(chan struct{})
			go func() {
				r := <-control.requests
				if reject {
					control.resolve(r, errCodexSteerRejected, "")
				} else {
					control.close()
				}
				close(ack)
			}()
			answers := []string{"云端", "不下载本地模型"}
			if _, err := a.submitAsyncAnswer(context.Background(), task.ID, question.ID, answers); err == nil {
				t.Fatal("unconfirmed answer accepted")
			}
			<-ack
			var state string
			a.store.QueryRow("SELECT status FROM async_questions WHERE id=?", question.ID).Scan(&state)
			if reject {
				if state != "pending" {
					t.Fatal("rejected answer cannot be retried", state)
				}
				control.mu.Lock()
				attempts := len(control.attempts)
				control.mu.Unlock()
				if attempts != 0 {
					t.Fatal("rejected attempt retained")
				}
			} else {
				if state != "steering" {
					t.Fatal("unknown delivery reservation lost", state)
				}
				a.mu.Lock()
				delete(a.workers, task.ID)
				a.mu.Unlock()
				if _, err := a.submitAsyncAnswer(context.Background(), task.ID, question.ID, answers); err == nil {
					t.Fatal("unknown answer retried as new turn")
				}
				if _, _, err := a.store.questionAnswer(task.ID, question.ID, []string{"本地", "changed"}); err == nil {
					t.Fatal("pending delivery accepts changed answer")
				}
			}
			runs, _ := a.store.runs(task.ID)
			if len(runs) != 0 {
				t.Fatal("failed steer was queued")
			}
		})
	}
}
