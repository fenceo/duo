package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

// The CLI emits completed reply segments and tool events, not guaranteed token
// deltas. Poll the durable run/events, coalesce changes, and patch ONE message.
// IM card API: https://open.feishu.cn/document/server-docs/im-v1/message-card/patch
const runCardInterval = 2 * time.Second

type runCardDelivery struct {
	id, chat, message, hash, state string
	attempts                       int
	firstAttempt                   int64
	run                            Run
	title                          string
}

func (f *Feishu) runCardsLoop() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-f.app.ctx.Done():
			return
		case <-tick.C:
			if err := f.runCardStep(now()); err != nil && f.app.ctx.Err() == nil {
				log.Printf("Feishu run card: %v", err)
			}
		}
	}
}

// One serial consumer, at most one network operation per step. next_at provides
// fair scheduling between runs; unchanged content never calls the remote API.
func (f *Feishu) runCardStep(at int64) error {
	cfg := f.app.config.get().Feishu
	if !cfg.Enabled || cfg.Owner == "" || f.app.ctx.Err() != nil {
		return nil
	}
	s := f.app.store
	if _, err := s.Exec("UPDATE feishu_run_cards SET state='cancelled' WHERE state IN ('pending','fallback') AND (app_id<>? OR owner<>?)", cfg.AppID, cfg.Owner); err != nil {
		return err
	}
	var d runCardDelivery
	err := s.QueryRow(`SELECT c.id,c.chat_id,c.message_id,c.last_hash,c.state,c.attempts,c.first_attempt,
	 r.id,r.task_id,r.input,r.kind,r.status,r.result,r.error,r.created,t.title
	 FROM feishu_run_cards c JOIN runs r ON r.id=c.run_id JOIN tasks t ON t.id=r.task_id
	 WHERE c.state IN ('pending','fallback') AND c.next_at<=? AND c.app_id=? AND c.owner=?
	 ORDER BY c.next_at,c.rowid LIMIT 1`, at, cfg.AppID, cfg.Owner).Scan(
		&d.id, &d.chat, &d.message, &d.hash, &d.state, &d.attempts, &d.firstAttempt,
		&d.run.ID, &d.run.TaskID, &d.run.Input, &d.run.Kind, &d.run.Status, &d.run.Result, &d.run.Error, &d.run.Created, &d.title)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	final := d.run.Status != "running" && d.run.Status != "queued"
	next := at + runCardInterval.Milliseconds()
	// A create response can be lost. Never retry a create outside its UUID dedup
	// window, or patch messages close to the platform's 14-day update limit.
	if d.firstAttempt != 0 && ((d.message == "" && at-d.firstAttempt >= (50*time.Minute).Milliseconds()) ||
		(d.message != "" && at-d.firstAttempt >= (13*24*time.Hour).Milliseconds())) {
		d.state = "fallback"
	}
	if d.state == "fallback" {
		if final {
			return f.runCardFallback(d)
		}
		_, err = s.Exec("UPDATE feishu_run_cards SET state='fallback',next_at=? WHERE id=? AND state IN ('pending','fallback')", next, d.id)
		return err
	}
	content, err := f.runCardContent(d)
	if err != nil {
		return err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	state := "pending"
	if final {
		state = "done"
	}
	if hash == d.hash && d.message != "" {
		_, err = s.Exec("UPDATE feishu_run_cards SET state=?,next_at=? WHERE id=? AND state='pending'", state, next, d.id)
		return err
	}
	if d.firstAttempt == 0 {
		if _, err = s.Exec("UPDATE feishu_run_cards SET first_attempt=? WHERE id=? AND state='pending'", at, d.id); err != nil {
			return err
		}
	}
	// Re-read before I/O so disabling/replacing the bot while rendering is honored.
	current := f.app.config.get().Feishu
	if !current.Enabled || current.AppID != cfg.AppID || current.Owner != cfg.Owner {
		return nil
	}
	ctx, cancel := context.WithTimeout(f.app.ctx, 15*time.Second)
	defer cancel()
	if d.message == "" {
		d.message, err = f.createRunCard(ctx, current, d.chat, content, d.id)
		if err == nil && d.message == "" {
			err = fmt.Errorf("飞书未返回卡片消息 ID")
		}
	} else {
		err = f.updateRunCard(ctx, current, d.message, content)
	}
	if f.app.ctx.Err() != nil {
		return nil // Leave the durable job for recovery, without consuming a retry.
	}
	if err != nil {
		d.attempts++
		state = "pending"
		if d.attempts >= 6 {
			state = "fallback"
		}
		_, dbErr := s.Exec("UPDATE feishu_run_cards SET attempts=?,state=?,next_at=? WHERE id=? AND state='pending'",
			d.attempts, state, at+int64(max(2, d.attempts*d.attempts))*1000, d.id)
		if dbErr != nil {
			return dbErr
		}
		if state == "fallback" {
			f.mu.Lock()
			f.state = "卡片更新暂不可用；完成后改用文字通知"
			f.mu.Unlock()
		}
		return err
	}
	_, err = s.Exec("UPDATE feishu_run_cards SET message_id=?,last_hash=?,state=?,attempts=0,next_at=? WHERE id=? AND state='pending'",
		d.message, hash, state, next, d.id)
	return err
}

func cardExcerpt(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…（完整内容见网页）"
}

func (f *Feishu) runCardContent(d runCardDelivery) (string, error) {
	r := d.run
	status := map[string]string{"queued": "排队中", "running": "执行中", "done": "已完成", "failed": "执行失败", "interrupted": "已停止"}[r.Status]
	color := map[string]string{"queued": "blue", "running": "blue", "done": "green", "failed": "red", "interrupted": "grey"}[r.Status]
	request := r.Input
	if r.Kind == "knowledge" {
		request = "整理本任务的知识草稿"
	}
	reply := r.Result
	progress := ""
	if r.Status == "running" {
		var tools int
		if err := f.app.store.QueryRow("SELECT count(*) FROM events WHERE run_id=? AND kind='tool'", r.ID).Scan(&tools); err != nil {
			return "", err
		}
		progress = "正在执行…"
		if tools > 0 {
			progress = fmt.Sprintf("执行中 · %d 条记录", tools)
		}
		err := f.app.store.QueryRow("SELECT substr(text,1,6001) FROM events WHERE run_id=? AND kind='assistant' ORDER BY seq DESC LIMIT 1", r.ID).Scan(&reply)
		if err != nil && err != sql.ErrNoRows {
			return "", err
		}
	} else if r.Status == "queued" {
		progress = "等待执行…"
	}
	if r.Error != "" {
		reply = r.Error + "\n\n" + reply
	}
	if strings.TrimSpace(reply) == "" && progress == "" {
		reply = "本轮已结束。"
	}
	// Plain text avoids interpreting model output as card actions or mentions.
	// Bound the *serialized HTTP body*, including escaped JSON and UTF-8, below
	// the API's 30 KB limit. The complete result stays in the task's database.
	view := ""
	controls := []any{}
	if r.Kind == "knowledge" {
		view = "note"
		if r.Status == "done" {
			var err error
			controls, err = f.knowledgeControls(d.chat, r)
			if err != nil {
				return "", err
			}
		}
	}
	for limit := 6000; ; limit /= 2 {
		elements := []any{}
		if r.Status == "running" || r.Status == "queued" {
			elements = append(elements, cardLine(cardExcerpt(request, 120)))
		}
		if progress != "" {
			elements = append(elements, cardLine(progress))
		}
		if reply != "" {
			elements = append(elements, cardLine(cardExcerpt(strings.TrimSpace(reply), limit)))
		}
		elements = append(elements, controls...)
		elements = append(elements, f.panelLinks(r.TaskID, view)...)
		card := taskCard(status+" · "+cardExcerpt(d.title, 80), elements)
		card["config"].(map[string]any)["update_multi"] = true
		card["header"].(map[string]any)["template"] = color
		raw, err := json.Marshal(card)
		if err != nil {
			return "", err
		}
		wire, err := json.Marshal(map[string]string{"content": string(raw)})
		if err != nil {
			return "", err
		}
		if len(wire) < 24*1024 {
			return string(raw), nil
		}
		if limit <= 128 {
			return "", fmt.Errorf("飞书卡片超出大小限制")
		}
	}
}

// A permanently unavailable card API degrades to one durable final notification,
// never one text message per progress event. The transaction prevents duplicates.
func (f *Feishu) runCardFallback(d runCardDelivery) error {
	text := "卡片更新暂不可用，本轮结果：\n" + d.title + " [" + feishuTaskStatus(d.run.Status) + "]\n\n"
	text += cardExcerpt(strings.TrimSpace(d.run.Error+"\n"+d.run.Result), 6000)
	if d.run.Kind == "knowledge" && d.run.Status == "done" {
		text += "\n\n知识草稿已生成，请到网页检查并保存。"
	}
	for _, block := range f.panelLinks(d.run.TaskID) {
		if actions, ok := block.(map[string]any)["actions"].([]any); ok {
			for _, item := range actions {
				text += "\n" + item.(map[string]any)["url"].(string)
			}
		}
	}
	tx, err := f.app.store.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE feishu_run_cards SET state='done' WHERE id=? AND state IN ('pending','fallback')", d.id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 0 {
		return err
	}
	runes := []rune(text)
	for i := 0; len(runes) > 0; i++ {
		n := min(2000, len(runes))
		if _, err = tx.Exec("INSERT INTO outbox(id,chat_id,text) VALUES(?,?,?)", fmt.Sprintf("%s-f%d", d.id, i), d.chat, string(runes[:n])); err != nil {
			return err
		}
		runes = runes[n:]
	}
	return tx.Commit()
}
