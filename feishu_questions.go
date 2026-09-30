package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	cb "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

type feishuQuestion struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	Other    bool   `json:"isOther"`
	Secret   bool   `json:"isSecret"`
	Options  []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
}

func feishuQuestions(p CodexPendingRequest) ([]feishuQuestion, bool) {
	if p.Method != "duo/asyncQuestion" && p.Method != "item/tool/requestUserInput" {
		return nil, false
	}
	var body struct {
		Questions []feishuQuestion `json:"questions"`
	}
	if json.Unmarshal(p.Params, &body) != nil || len(body.Questions) == 0 || len(body.Questions) > 8 {
		return nil, false
	}
	seen := map[string]bool{}
	for _, q := range body.Questions {
		if q.ID == "" || seen[q.ID] || q.Secret || strings.TrimSpace(q.Question) == "" || len([]rune(q.Question)) > 2000 || len(q.Options) > 32 {
			return nil, false
		}
		seen[q.ID] = true
		for _, option := range q.Options {
			if option.Label == "" || len([]rune(option.Label)) > 100 || len([]rune(option.Description)) > 600 {
				return nil, false
			}
		}
	}
	return body.Questions, true
}

func (f *Feishu) pendingQuestions(taskID string) ([]CodexPendingRequest, error) {
	task, err := f.app.store.task(taskID)
	if err != nil {
		return nil, err
	}
	if task.Archived || task.Deleted || task.Engine != "codex" {
		return nil, nil
	}
	async, err := f.app.store.pendingAsyncQuestions(task)
	if err != nil {
		return nil, err
	}
	return append(f.app.codexRequests.list(taskID), async...), nil
}

func (f *Feishu) questionCardsLoop() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-f.app.ctx.Done():
			return
		case <-tick.C:
		}
		if err := f.questionCardStep(now()); err != nil && f.app.ctx.Err() == nil {
			log.Printf("Feishu question card: %v", err)
		}
	}
}

// Question notifications have their own durable delivery, including async
// questions which remain open after the run's final card was already delivered.
func (f *Feishu) questionCardStep(at int64) error {
	cfg := f.app.config.get().Feishu
	if !cfg.Enabled || cfg.Owner == "" || f.app.ctx.Err() != nil {
		return nil
	}
	s := f.app.store
	if _, err := s.Exec("UPDATE feishu_question_cards SET state='cancelled' WHERE state IN ('pending','sent','fallback') AND (app_id<>? OR owner<>?)", cfg.AppID, cfg.Owner); err != nil {
		return err
	}
	rows, err := s.Query("SELECT b.chat_id,b.task_id FROM bindings b JOIN feishu_chat_scopes c ON c.chat_id=b.chat_id WHERE c.app_id=? AND c.owner=?", cfg.AppID, cfg.Owner)
	if err != nil {
		return err
	}
	type binding struct{ chat, task string }
	var bindings []binding
	for rows.Next() {
		var b binding
		if err = rows.Scan(&b.chat, &b.task); err != nil {
			rows.Close()
			return err
		}
		bindings = append(bindings, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	pending := map[string]CodexPendingRequest{}
	for _, b := range bindings {
		requests, err := f.pendingQuestions(b.task)
		if err != nil {
			return err
		}
		for _, p := range requests {
			pending[b.chat+"/"+p.ID] = p
			if _, err = s.Exec("INSERT INTO feishu_question_cards(id,request_id,task_id,chat_id,app_id,owner) VALUES(?,?,?,?,?,?) ON CONFLICT(request_id,chat_id,app_id,owner) DO UPDATE SET state='pending',next_at=0,last_hash='' WHERE state='closed'", uid(), p.ID, b.task, b.chat, cfg.AppID, cfg.Owner); err != nil {
				return err
			}
		}
	}
	var id, request, task, chat, message, lastHash, state string
	var attempts int
	var first int64
	err = s.QueryRow("SELECT id,request_id,task_id,chat_id,message_id,last_hash,state,attempts,first_attempt FROM feishu_question_cards WHERE state IN ('pending','sent','fallback') AND next_at<=? AND app_id=? AND owner=? ORDER BY next_at,rowid LIMIT 1", at, cfg.AppID, cfg.Owner).Scan(&id, &request, &task, &chat, &message, &lastHash, &state, &attempts, &first)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	p, open := pending[chat+"/"+request]
	if !open && message == "" {
		_, err = s.Exec("UPDATE feishu_question_cards SET state='closed' WHERE id=?", id)
		return err
	}
	if first != 0 && ((message == "" && at-first >= (50*time.Minute).Milliseconds()) || (message != "" && at-first >= (13*24*time.Hour).Milliseconds())) {
		state = "fallback"
	}
	if state == "fallback" && !open {
		_, err = s.Exec("UPDATE feishu_question_cards SET state='closed' WHERE id=?", id)
		return err
	}
	card := f.questionCard(task, id, p, open)
	raw, err := json.Marshal(card)
	if err != nil {
		return err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	next := at + runCardInterval.Milliseconds()
	if hash == lastHash && message != "" && state != "fallback" {
		_, err = s.Exec("UPDATE feishu_question_cards SET next_at=? WHERE id=?", next, id)
		return err
	}
	current := f.app.config.get().Feishu
	if !current.Enabled || current.AppID != cfg.AppID || current.Owner != cfg.Owner || (open && f.app.bound(chat) != task) {
		return nil
	}
	if first == 0 {
		if _, err = s.Exec("UPDATE feishu_question_cards SET first_attempt=? WHERE id=?", at, id); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(f.app.ctx, 15*time.Second)
	defer cancel()
	if state == "fallback" {
		text := "AI 有问题需要你回答，但飞书卡片发送失败。请打开 Duo 当前任务回答；答案不会自动排队。"
		for _, block := range f.panelLinks(task) {
			if actions, ok := block.(map[string]any)["actions"].([]any); ok {
				for _, action := range actions {
					text += "\n" + action.(map[string]any)["url"].(string)
				}
			}
		}
		err = f.deliver(ctx, current, chat, text, id+"-question")
	} else if message == "" {
		message, err = f.createRunCard(ctx, current, chat, string(raw), id)
		if err == nil && message == "" {
			err = errors.New("飞书未返回问题卡片 ID")
		}
	} else {
		err = f.updateRunCard(ctx, current, message, string(raw))
	}
	if f.app.ctx.Err() != nil {
		return nil
	}
	if err != nil {
		attempts++
		if attempts >= 12 {
			state = "failed"
		} else if attempts >= 6 {
			state = "fallback"
		}
		_, dbErr := s.Exec("UPDATE feishu_question_cards SET state=?,attempts=?,next_at=? WHERE id=?", state, attempts, at+int64(max(2, attempts*attempts))*1000, id)
		if dbErr != nil {
			return dbErr
		}
		return err
	}
	if !open {
		state = "closed"
	} else if state == "fallback" {
		state = "settled"
	} else {
		state = "sent"
	}
	_, err = s.Exec("UPDATE feishu_question_cards SET state=?,message_id=?,last_hash=?,attempts=0,next_at=? WHERE id=?", state, message, hash, next, id)
	return err
}

func (f *Feishu) questionCard(task, token string, p CodexPendingRequest, open bool) map[string]any {
	title := "AI 需要你回答"
	elements := []any{}
	if !open {
		title = "问题已关闭"
		elements = append(elements, cardLine("此问题已回答、取消或失效，请以 Duo 当前任务为准。"))
	} else if questions, ok := feishuQuestions(p); ok {
		form := []any{}
		for i, q := range questions {
			fields := []any{cardLine(fmt.Sprintf("%d. %s", i+1, q.Question))}
			if len(q.Options) > 0 {
				options := []any{}
				for j, o := range q.Options {
					options = append(options, map[string]any{"text": plainCardText(o.Label), "value": strconv.Itoa(j)})
					if o.Description != "" {
						fields = append(fields, cardLine(o.Label+"："+o.Description))
					}
				}
				fields = append(fields, map[string]any{"tag": "select_static", "name": fmt.Sprintf("choice_%d", i), "placeholder": plainCardText("请选择"), "options": options})
			}
			if q.Other || len(q.Options) == 0 {
				fields = append(fields, map[string]any{"tag": "input", "name": fmt.Sprintf("text_%d", i), "placeholder": plainCardText("填写答案或补充（填写后优先提交）"), "max_length": 1000})
			}
			// Card JSON 1.0 forbids div directly inside form. Keep each question
			// and its controls in a supported single-column layout instead.
			// https://open.feishu.cn/document/feishu-cards/card-components/containers/form-container
			form = append(form, map[string]any{"tag": "column_set", "flex_mode": "none", "columns": []any{
				map[string]any{"tag": "column", "width": "weighted", "weight": 1, "vertical_align": "top", "elements": fields},
			}})
		}
		form = append(form, map[string]any{"tag": "button", "name": "duo_question_submit", "action_type": "form_submit", "text": map[string]any{"tag": "lark_md", "content": "提交答案"}, "type": "primary", "value": map[string]string{"duo_question": token}})
		elements = append(elements, cardLine("选择或填写全部问题后提交。执行中立即引导；本轮已结束则开始下一轮。"), map[string]any{"tag": "form", "name": "duo_questions", "elements": form})
	} else {
		title = "AI 等待网页处理"
		elements = append(elements, cardLine("有待处理的问题或操作审批。涉及敏感输入、权限审批或内容较长，请在 Duo 网页中查看并处理。"))
	}
	elements = append(elements, f.panelLinks(task)...)
	card := taskCard(title, elements)
	card["config"].(map[string]any)["update_multi"] = true
	raw, _ := json.Marshal(card)
	wire, _ := json.Marshal(map[string]string{"content": string(raw)})
	if len(wire) >= 24*1024 {
		card["elements"] = append([]any{cardLine("问题内容较长，请在 Duo 网页中完整查看并回答。")}, f.panelLinks(task)...)
	}
	return card
}

func (f *Feishu) answerQuestionCard(chat, token string, action *cb.CallBackAction) (*cb.CardActionTriggerResponse, error) {
	if !f.inbound.TryLock() {
		return cardToast("info", "正在处理上一条请求，请稍后重试原答案"), nil
	}
	defer f.inbound.Unlock()
	if f.app.updating.Load() {
		return cardToast("error", errUpdateBusy.Error()), nil
	}
	cfg := f.app.config.get().Feishu
	var request, task, savedChat, appID, owner string
	err := f.app.store.QueryRow("SELECT request_id,task_id,chat_id,app_id,owner FROM feishu_question_cards WHERE id=?", token).Scan(&request, &task, &savedChat, &appID, &owner)
	if err != nil || chat == "" || chat != savedChat || appID != cfg.AppID || owner != cfg.Owner || f.app.bound(chat) != task {
		return cardToast("error", "问题卡片不属于当前任务或已失效"), nil
	}
	if action.Tag != "button" || action.Name != "duo_question_submit" {
		return cardToast("error", "请使用提交答案按钮"), nil
	}
	requests, err := f.pendingQuestions(task)
	if err != nil {
		return cardToast("error", "无法读取当前问题，请稍后重试"), nil
	}
	var p CodexPendingRequest
	for _, candidate := range requests {
		if candidate.ID == request {
			p = candidate
			break
		}
	}
	questions, ok := feishuQuestions(p)
	if !ok {
		return cardToast("info", "问题已处理、失效或需要在网页回答"), nil
	}
	answer := CodexAnswer{Answers: map[string]CodexQuestionAnswer{}}
	values := make([]string, len(questions))
	for i, q := range questions {
		text, _ := action.FormValue[fmt.Sprintf("text_%d", i)].(string)
		text = strings.TrimSpace(text)
		if text != "" && len(q.Options) > 0 && !q.Other {
			return cardToast("error", "此问题只能选择给定选项"), nil
		}
		if text == "" {
			if choice, ok := action.FormValue[fmt.Sprintf("choice_%d", i)].(string); ok {
				if index, err := strconv.Atoi(choice); err == nil && index >= 0 && index < len(q.Options) {
					text = q.Options[index].Label
				}
			}
		}
		if text == "" || len(text) > 16000 {
			return cardToast("error", fmt.Sprintf("请完整回答问题 %d，且答案不要过长", i+1)), nil
		}
		values[i] = text
		answer.Answers[q.ID] = CodexQuestionAnswer{Answers: []string{text}}
	}
	message := "答案已交回当前执行"
	if p.Method == "duo/asyncQuestion" {
		// Card callbacks have a short response deadline. Native acceptance may
		// finish later; the existing durable reservation prevents queue fallback.
		ctx, cancel := context.WithTimeout(f.app.ctx, 1500*time.Millisecond)
		result, e := f.app.submitAsyncAnswerFrom(ctx, task, request, values, "feishu")
		cancel()
		err = e
		if err == nil && result.Warning != "" {
			return cardToast("warning", "AI 已接收答案，但记录尚未确认："+result.Warning), nil
		}
		if err == nil && result.Delivery == "start" {
			message = "答案已接收，开始下一轮"
		}
	} else {
		err = f.app.answerCodexInteraction(task, request, answer)
	}
	if err != nil {
		return cardToast("error", "尚未确认答案送达，未自动排队；请查看任务状态或重试原答案。"+err.Error()), nil
	}
	_, _ = f.app.store.Exec("UPDATE feishu_question_cards SET next_at=0 WHERE id=?", token)
	return &cb.CardActionTriggerResponse{Toast: &cb.Toast{Type: "success", Content: message}, Card: &cb.Card{Type: "raw", Data: f.questionCard(task, token, p, false)}}, nil
}
