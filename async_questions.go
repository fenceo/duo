package main

import (
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
	rows, err := s.Query("SELECT id,run_id,questions,created FROM async_questions WHERE task_id=? AND session=? AND status='pending' ORDER BY created,id", task.ID, task.Session)
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

// Called under app.mu. Submission updates this row and inserts the queued run
// in one transaction, so retries after a lost HTTP reply cannot send twice.
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
	if status == "answered" && oldAnswer == content {
		return content, answerRun, nil
	}
	if status != "pending" {
		return "", "", errAsyncQuestionExpired
	}
	task, err := s.task(taskID)
	if err != nil || task.Engine != "codex" || task.Session != session {
		return "", "", errAsyncQuestionExpired
	}
	return content, "", nil
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
		run, err := s.app.submitWithOptions(taskID, "", "chat", "web", SubmitOptions{QuestionID: id, QuestionAnswers: answers})
		if err != nil {
			status := 400
			if errors.Is(err, errAsyncQuestionExpired) {
				status = 409
			}
			fail(w, status, err.Error())
			return
		}
		jsonOut(w, 200, map[string]any{"accepted": true, "delivery": "queue", "run_id": run.ID})
	}))
}
