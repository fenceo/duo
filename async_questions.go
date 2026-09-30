package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Async agent messages are notifications, not RPC requests awaiting a response.
// Keep their questions independently of event pagination and final-answer folding.
type AsyncQuestion struct {
	Title   string   `json:"title"`
	Options []string `json:"options"`
}
type AsyncQuestionMessage struct {
	ItemID    string          `json:"item_id"`
	Session   string          `json:"session"`
	Questions []AsyncQuestion `json:"questions"`
}

var errAsyncQuestionExpired = errors.New("问题已关闭或会话已切换，请刷新后查看当前问题")

func (s *Store) saveAsyncQuestion(taskID, runID, raw string) error {
	var message AsyncQuestionMessage
	if len(raw) > 256*1024 || json.Unmarshal([]byte(raw), &message) != nil || message.ItemID == "" || message.Session == "" || len(message.Questions) == 0 || len(message.Questions) > 8 {
		return errors.New("异步问题格式无效")
	}
	for _, q := range message.Questions {
		if strings.TrimSpace(q.Title) == "" || len(q.Title) > 16000 || len(q.Options) > 32 {
			return errors.New("异步问题内容无效")
		}
		for _, option := range q.Options {
			if len(option) > 16000 {
				return errors.New("异步问题选项过长")
			}
		}
	}
	questions, _ := json.Marshal(message.Questions)
	_, err := s.Exec("INSERT INTO async_questions(id,task_id,run_id,item_id,session,questions,created) VALUES(?,?,?,?,?,?,?) ON CONFLICT(task_id,run_id,item_id) DO NOTHING", uid(), taskID, runID, message.ItemID, message.Session, string(questions), now())
	return err
}

func (s *Store) pendingAsyncQuestions(task Task) ([]CodexPendingRequest, error) {
	out := []CodexPendingRequest{}
	if task.Engine != "codex" || task.Session == "" {
		return out, nil
	}
	rows, err := s.Query("SELECT id,run_id,questions,created FROM async_questions WHERE task_id=? AND session=? AND status IN ('pending','steering') ORDER BY created,id", task.ID, task.Session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		request := CodexPendingRequest{TaskID: task.ID, Method: "duo/asyncQuestion"}
		var raw string
		if err = rows.Scan(&request.ID, &request.RunID, &raw, &request.Created); err != nil {
			return nil, err
		}
		var questions []AsyncQuestion
		if err = json.Unmarshal([]byte(raw), &questions); err != nil {
			return nil, err
		}
		items := []any{}
		for i, q := range questions {
			options := []any{}
			for _, option := range q.Options {
				options = append(options, map[string]string{"label": option, "description": ""})
			}
			items = append(items, map[string]any{"id": strconv.Itoa(i), "header": fmt.Sprintf("问题 %d", i+1), "question": q.Title, "isOther": true, "isSecret": false, "options": options})
		}
		request.Params, _ = json.Marshal(map[string]any{"isBlocking": false, "questions": items})
		out = append(out, request)
	}
	return out, rows.Err()
}

// Called under app.mu. A queued answer and a confirmed live answer are durable;
// an unconfirmed steer remains reserved and must never fall back to a new turn.
func (s *Store) questionAnswer(taskID, id string, answers []string) (string, string, error) {
	var session, raw, status, oldAnswer, answerRun string
	err := s.QueryRow("SELECT session,questions,status,answer,answer_run FROM async_questions WHERE task_id=? AND id=?", taskID, id).Scan(&session, &raw, &status, &oldAnswer, &answerRun)
	if err != nil {
		return "", "", errAsyncQuestionExpired
	}
	var questions []AsyncQuestion
	if json.Unmarshal([]byte(raw), &questions) != nil || len(answers) != len(questions) {
		return "", "", errors.New("请回答每一个问题")
	}
	var text strings.Builder
	text.WriteString("对 AI 提问的回答：\n")
	for i, q := range questions {
		answer := strings.TrimSpace(answers[i])
		if answer == "" || len(answer) > 16000 {
			return "", "", errors.New("答案不能为空，且不能超过 16 KB")
		}
		fmt.Fprintf(&text, "\n%d. %s\n回答：%s\n", i+1, q.Title, answer)
	}
	content := strings.TrimSpace(text.String())
	if (status == "answered" || status == "steered") && oldAnswer == content {
		return content, answerRun, nil
	}
	if status == "steering" && oldAnswer != content {
		return "", "", errors.New("此答案正在确认接收，请先重试原答案或检查对话记录")
	}
	if status != "pending" && status != "steering" {
		return "", "", errAsyncQuestionExpired
	}
	task, err := s.task(taskID)
	if err != nil || task.Engine != "codex" || task.Session != session {
		return "", "", errAsyncQuestionExpired
	}
	return content, "", nil
}

type AsyncAnswerDelivery struct {
	Accepted bool   `json:"accepted"`
	Delivery string `json:"delivery"`
	RunID    string `json:"run_id"`
	Warning  string `json:"warning,omitempty"`
}

func (a *App) submitAsyncAnswer(ctx context.Context, taskID, id string, answers []string) (AsyncAnswerDelivery, error) {
	return a.submitAsyncAnswerFrom(ctx, taskID, id, answers, "web")
}

func (a *App) submitAsyncAnswerFrom(ctx context.Context, taskID, id string, answers []string, source string) (AsyncAnswerDelivery, error) {
	empty := AsyncAnswerDelivery{}
	a.mu.Lock()
	content, previous, err := a.store.questionAnswer(taskID, id, answers)
	if err != nil {
		a.mu.Unlock()
		return empty, err
	}
	if len(content) > 200000 {
		a.mu.Unlock()
		return empty, errors.New("答案总长度不能超过 200 KB")
	}
	var status, reservedRun string
	err = a.store.QueryRow("SELECT status,answer_run FROM async_questions WHERE task_id=? AND id=?", taskID, id).Scan(&status, &reservedRun)
	if err != nil {
		a.mu.Unlock()
		return empty, err
	}
	if previous != "" {
		a.mu.Unlock()
		delivery := "start"
		if status == "steered" {
			delivery = "steer"
		}
		return AsyncAnswerDelivery{Accepted: true, Delivery: delivery, RunID: previous}, nil
	}
	if a.updating.Load() || a.ctx.Err() != nil {
		a.mu.Unlock()
		return empty, errUpdateBusy
	}
	w := a.workers[taskID]
	if status == "steering" && (w == nil || w.runID != reservedRun || w.steer == nil) {
		a.mu.Unlock()
		return empty, errors.New("上次答案的引导结果未确认，未自动排队；请先检查当前对话记录")
	}
	if w == nil || w.runID == "" {
		a.mu.Unlock()
		run, err := a.submitWithOptions(taskID, "", "chat", source, SubmitOptions{QuestionID: id, QuestionAnswers: answers, QuestionImmediate: true})
		return AsyncAnswerDelivery{Accepted: err == nil, Delivery: "start", RunID: run.ID}, err
	}
	if w.runKind != "chat" || w.stopping || w.interrupting || w.steer == nil {
		a.mu.Unlock()
		return empty, errors.New("当前执行暂不能接收答案，请稍后重试；答案未排队")
	}
	control, runID := w.steer, w.runID
	if status == "pending" {
		_, err = a.store.Exec("UPDATE async_questions SET status='steering',answer=?,answer_run=? WHERE task_id=? AND id=? AND status='pending'", content, runID, taskID, id)
		if err != nil {
			a.mu.Unlock()
			return empty, err
		}
	}
	requestID := "question-" + id
	request, err := control.beginRecorded(requestID, content, func(text string) error {
		// Native acceptance, question completion and the user event share one
		// transaction. A lost HTTP response cannot create another delivery.
		a.mu.Lock()
		defer a.mu.Unlock()
		tx, err := a.store.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		result, err := tx.Exec("UPDATE async_questions SET status='steered' WHERE task_id=? AND id=? AND status='steering' AND answer=? AND answer_run=?", taskID, id, text, runID)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			return errors.New("问题状态已改变")
		}
		if _, err = tx.Exec("INSERT INTO events(task_id,run_id,kind,text,created) VALUES(?,?,?,?,?)", taskID, runID, "user", text, now()); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err != nil && status == "pending" {
		_, _ = a.store.Exec("UPDATE async_questions SET status='pending',answer='',answer_run='' WHERE task_id=? AND id=? AND status='steering'", taskID, id)
	}
	a.mu.Unlock()
	if err != nil {
		return empty, err
	}
	a.changed()
	warning, err := a.waitForSteer(ctx, request)
	if errors.Is(err, errCodexSteerRejected) || errors.Is(err, errLiveTurnChanged) {
		// These failures are known to precede native acceptance. Unknown/timeout
		// failures retain the reservation, including across service restarts.
		a.mu.Lock()
		control.mu.Lock()
		if control.attempts[requestID] == request {
			_, _ = a.store.Exec("UPDATE async_questions SET status='pending',answer='',answer_run='' WHERE task_id=? AND id=? AND status='steering' AND answer=?", taskID, id, content)
			delete(control.attempts, requestID)
		}
		control.mu.Unlock()
		a.mu.Unlock()
		err = errors.New("当前执行未接收答案，请稍后重试；答案未自动排队")
	}
	return AsyncAnswerDelivery{Accepted: err == nil, Delivery: "steer", RunID: runID, Warning: warning}, err
}

func (s *Server) asyncQuestionRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/tasks/{id}/questions/{question}", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Answers map[string]struct {
				Answers []string `json:"answers"`
			} `json:"answers"`
			Dismiss bool `json:"dismiss"`
		}
		if !body(w, r, &v) {
			return
		}
		taskID, id := r.PathValue("id"), r.PathValue("question")
		if v.Dismiss {
			s.app.mu.Lock()
			var state string
			_ = s.app.store.QueryRow("SELECT status FROM async_questions WHERE task_id=? AND id=?", taskID, id).Scan(&state)
			if state == "steering" {
				s.app.mu.Unlock()
				fail(w, 400, "答案已经开始提交，不能改为暂不回答；请检查接收结果")
				return
			}
			_, err := s.app.store.Exec("UPDATE async_questions SET status='dismissed' WHERE task_id=? AND id=? AND status='pending'", taskID, id)
			s.app.mu.Unlock()
			if err != nil {
				fail(w, 500, err.Error())
				return
			}
			s.app.changed()
			jsonOut(w, 200, map[string]bool{"ok": true})
			return
		}
		answers := make([]string, len(v.Answers))
		for i := range answers {
			value, ok := v.Answers[strconv.Itoa(i)]
			if !ok || len(value.Answers) != 1 {
				fail(w, 400, "答案格式无效")
				return
			}
			answers[i] = value.Answers[0]
		}
		delivery, err := s.app.submitAsyncAnswer(r.Context(), taskID, id, answers)
		if err != nil {
			status := 400
			if errors.Is(err, errAsyncQuestionExpired) {
				status = 409
			}
			fail(w, status, err.Error())
			return
		}
		jsonOut(w, 200, delivery)
	}))
}
