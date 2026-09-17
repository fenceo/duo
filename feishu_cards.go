package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	cb "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	im "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func plainCardText(s string) map[string]any { return map[string]any{"tag": "plain_text", "content": s} }
func cardLine(s string) map[string]any      { return map[string]any{"tag": "div", "text": plainCardText(s)} }
func taskCard(title string, elements []any) map[string]any {
	return map[string]any{"config": map[string]any{"wide_screen_mode": true, "enable_forward": false}, "header": map[string]any{"template": "blue", "title": plainCardText(title)}, "elements": elements}
}

func (f *Feishu) cardButton(chat, task, action, label, query string, offset int) (map[string]any, error) {
	token := uid()
	_, err := f.app.store.Exec("INSERT INTO feishu_card_actions(id,chat_id,task_id,action,query,offset,expires) VALUES(?,?,?,?,?,?,?)", token, chat, task, action, query, offset, time.Now().Add(7*24*time.Hour).UnixMilli())
	return map[string]any{"tag": "button", "text": plainCardText(label), "type": "text", "size": "small", "width": "default", "value": map[string]string{"jianzuo_action": token}}, err
}
func (f *Feishu) taskListCard(chat, query string, offset int) (map[string]any, error) {
	all, err := f.app.store.tasks()
	if err != nil {
		return nil, err
	}
	tasks := []Task{}
	for _, t := range all {
		if t.Archived {
			continue
		}
		match := query == "" || strings.Contains(strings.ToLower(t.Title), strings.ToLower(query)) || strings.HasPrefix(t.ID, query)
		switch query {
		case "status:active":
			match = t.Status == "running" || t.Status == "queued"
		case "status:waiting":
			match = t.Status == "idle" || t.Status == "done" || t.Status == "interrupted"
		case "status:failed":
			match = t.Status == "failed"
		}
		if match {
			tasks = append(tasks, t)
		}
	}
	if offset < 0 || offset >= len(tasks) {
		offset = 0
	}
	end := min(offset+8, len(tasks))
	current := f.app.bound(chat)
	elements := []any{}
	for _, t := range tasks[offset:end] {
		mark := ""
		if current == t.ID {
			mark = "✓ "
		}
		label := []rune(t.Title)
		if len(label) > 40 {
			label = append(label[:40], '…')
		}
		button, e := f.cardButton(chat, t.ID, "enter", mark+string(label)+" · "+feishuTaskStatus(t.Status), "", 0)
		if e != nil {
			return nil, e
		}
		if current == t.ID {
			button["type"] = "primary_text"
		}
		elements = append(elements, map[string]any{"tag": "action", "actions": []any{button}})
	}
	if len(tasks) == 0 {
		elements = append(elements, cardLine("暂无任务"))
	}
	nav := []any{}
	for _, n := range []struct {
		show  bool
		label string
		page  int
	}{{offset > 0, "上一页", max(0, offset-8)}, {end < len(tasks), "下一页", end}} {
		if n.show {
			b, e := f.cardButton(chat, "", "list", n.label, query, n.page)
			if e != nil {
				return nil, e
			}
			nav = append(nav, b)
		}
	}
	if len(nav) > 0 {
		elements = append(elements, map[string]any{"tag": "action", "actions": nav})
	}
	elements = append(elements, f.panelLinks("")...)
	return taskCard(fmt.Sprintf("我的任务 · 共 %d 项", len(tasks)), elements), nil
}
func (f *Feishu) taskDetailCard(chat string, t Task) (map[string]any, error) {
	selected := f.app.bound(chat) == t.ID
	banner := feishuTaskStatus(t.Status)
	if t.Archived {
		banner = "已归档"
	}
	elements := []any{cardLine(banner)}
	runs, err := f.app.store.runs(t.ID)
	if err != nil {
		return nil, err
	}
	view := ""
	if len(runs) > 0 {
		r := runs[len(runs)-1]
		out := r.Result
		if r.Error != "" {
			out = r.Error
		}
		if out != "" {
			elements = append(elements, cardLine(cardExcerpt(out, 500)))
		}
		if r.Kind == "knowledge" && r.Status == "done" {
			controls, e := f.knowledgeControls(chat, r)
			if e != nil {
				return nil, e
			}
			elements = append(elements, controls...)
			view = "note"
		}
	}
	specs := []struct{ action, label string }{}
	if !selected && !t.Archived {
		specs = append(specs, struct{ action, label string }{"enter", "进入此任务"})
	}
	if selected && (t.Status == "running" || t.Status == "queued") {
		specs = append(specs, struct{ action, label string }{"stop", "停止执行"})
	}
	specs = append(specs, struct{ action, label string }{"list", "切换任务"})
	if selected {
		specs = append(specs, struct{ action, label string }{"exit", "退出任务"})
	}
	buttons := []any{}
	for _, b := range specs {
		expected := ""
		if b.action == "stop" {
			runs, e := f.app.store.runs(t.ID)
			if e != nil {
				return nil, e
			}
			if len(runs) > 0 {
				expected = runs[len(runs)-1].ID
			}
		}
		button, err := f.cardButton(chat, t.ID, b.action, b.label, expected, 0)
		if err != nil {
			return nil, err
		}
		if b.action == "enter" {
			button["type"] = "primary_text"
		}
		buttons = append(buttons, button)
		if len(buttons) == 3 {
			elements = append(elements, map[string]any{"tag": "action", "actions": buttons})
			buttons = []any{}
		}
	}
	if len(buttons) > 0 {
		elements = append(elements, map[string]any{"tag": "action", "actions": buttons})
	}
	title := "查看任务"
	if selected {
		title = "当前任务"
	}
	elements = append(elements, f.panelLinks(t.ID, view)...)
	return taskCard(title+" · "+t.Title, elements), nil
}
func (f *Feishu) enqueueCard(chat string, card map[string]any) error {
	raw, err := json.Marshal(card)
	if err != nil {
		return err
	}
	id := uid()
	tx, err := f.app.store.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO outbox(id,chat_id,text) VALUES(?,?,?)", id, chat, "任务选项卡片"); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO outbox_cards VALUES(?,?)", id, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}
func sendFeishuCard(ctx context.Context, c FeishuConfig, chat, content, id string) error {
	_, err := createFeishuCard(ctx, c, chat, content, id)
	return err
}
func createFeishuCard(ctx context.Context, c FeishuConfig, chat, content, id string) (string, error) {
	return createCardMessage(ctx, lark.NewClient(c.AppID, c.Secret), chat, content, id)
}
func createCardMessage(ctx context.Context, client *lark.Client, chat, content, id string) (string, error) {
	r, err := client.Im.Message.Create(ctx, im.NewCreateMessageReqBuilder().ReceiveIdType("chat_id").Body(im.NewCreateMessageReqBodyBuilder().ReceiveId(chat).MsgType("interactive").Content(content).Uuid(id).Build()).Build())
	if err != nil {
		return "", err
	}
	if !r.Success() {
		return "", fmt.Errorf("卡片发送失败（%d）", r.Code)
	}
	if r.Data == nil || value(r.Data.MessageId) == "" {
		return "", errors.New("飞书未返回卡片消息 ID")
	}
	return value(r.Data.MessageId), nil
}
func updateFeishuCard(ctx context.Context, c FeishuConfig, messageID, content string) error {
	return patchCardMessage(ctx, lark.NewClient(c.AppID, c.Secret), messageID, content)
}
func patchCardMessage(ctx context.Context, client *lark.Client, messageID, content string) error {
	r, err := client.Im.Message.Patch(ctx, im.NewPatchMessageReqBuilder().MessageId(messageID).Body(im.NewPatchMessageReqBodyBuilder().Content(content).Build()).Build())
	if err != nil {
		return err
	}
	if !r.Success() {
		return fmt.Errorf("卡片更新失败（%d）", r.Code)
	}
	return nil
}
func cardToast(kind, text string) *cb.CardActionTriggerResponse {
	return &cb.CardActionTriggerResponse{Toast: &cb.Toast{Type: kind, Content: text}}
}
func (f *Feishu) receiveCard(event *cb.CardActionTriggerEvent) (*cb.CardActionTriggerResponse, error) {
	if event == nil || event.Event == nil || event.Event.Operator == nil || event.Event.Context == nil || event.Event.Action == nil {
		return cardToast("error", "无效操作"), nil
	}
	v := event.Event
	cfg := f.app.config.get().Feishu
	if !cfg.Enabled || cfg.Owner == "" || v.Operator.OpenID != cfg.Owner {
		return cardToast("error", "仅已绑定账号可以操作"), nil
	}
	token, _ := v.Action.Value["jianzuo_action"].(string)
	return f.applyCardAction(v.Context.OpenChatID, token)
}
func (f *Feishu) applyCardAction(chat, token string) (*cb.CardActionTriggerResponse, error) {
	f.inbound.Lock()
	defer f.inbound.Unlock()
	var savedChat, taskID, action, query string
	var offset, used int
	var expires int64
	err := f.app.store.QueryRow("SELECT chat_id,task_id,action,query,offset,expires,used FROM feishu_card_actions WHERE id=?", token).Scan(&savedChat, &taskID, &action, &query, &offset, &expires, &used)
	if err != nil || chat == "" || chat != savedChat || expires < now() {
		return cardToast("error", "选项已失效，请重新点击“我的任务”"), nil
	}
	if used != 0 && action == "continue" {
		return cardToast("info", "这次继续操作已处理，请刷新状态"), nil
	}
	var card map[string]any
	message := "已打开"
	if action == "list" {
		card, err = f.taskListCard(chat, query, offset)
	} else {
		var task Task
		task, err = f.app.store.task(taskID)
		if err == nil {
			switch action {
			case "save_knowledge":
				var v knowledgeAction
				if json.Unmarshal([]byte(query), &v) != nil {
					return cardToast("error", "知识选项已失效"), nil
				}
				_, err = f.app.store.adoptKnowledge(task.ID, v.Run, v.Revision)
				if errors.Is(err, errConflict) {
					return cardToast("error", "知识已修改，请在网页合并草稿"), nil
				}
				if err != nil {
					return cardToast("error", err.Error()), nil
				}
				f.app.changed()
				card, err = f.knowledgeResultCard(chat, task.ID, v.Run)
				if err != nil {
					return cardToast("success", "知识已保存"), nil
				}
				return &cb.CardActionTriggerResponse{Toast: &cb.Toast{Type: "success", Content: "知识已保存"}, Card: &cb.Card{Type: "raw", Data: card}}, nil
			case "enter":
				err = f.app.bind(chat, task.ID)
				message = "已进入任务，直接发要求即可"
			case "continue":
				if task.Status == "running" || task.Status == "queued" {
					return cardToast("info", "任务正在执行，请等待完成"), nil
				}
				// Persist the one-shot claim before starting execution. Repeated delivery
				// or double-clicking this button cannot enqueue another run.
				_, err = f.app.store.Exec("UPDATE feishu_card_actions SET used=1 WHERE id=?", token)
				if err == nil {
					err = f.app.bind(chat, task.ID)
				}
				if err == nil {
					_, err = f.app.submit(task.ID, "继续", "chat", "feishu")
				}
				if err == nil {
					task, _ = f.app.store.task(task.ID)
				}
				message = "已开始继续执行"
			case "exit":
				if f.app.bound(chat) != task.ID {
					return cardToast("info", "当前任务已改变，这张旧卡片不会退出新任务"), nil
				}
				_, err = f.app.store.Exec("DELETE FROM bindings WHERE chat_id=? AND task_id=?", chat, task.ID)
				if err == nil {
					card, err = f.taskListCard(chat, "", 0)
				}
				if err != nil {
					return cardToast("error", "退出未完成，请重试"), nil
				}
				return &cb.CardActionTriggerResponse{Toast: &cb.Toast{Type: "success", Content: "已退出任务，原任务保持原状态"}, Card: &cb.Card{Type: "raw", Data: card}}, nil
			case "stop":
				if f.app.bound(chat) != task.ID {
					return cardToast("info", "当前任务已改变，请在当前任务卡片中操作"), nil
				}
				runs, e := f.app.store.runs(task.ID)
				if e != nil || len(runs) == 0 || query == "" || runs[len(runs)-1].ID != query {
					return cardToast("info", "执行轮次已改变，请刷新进展后操作"), nil
				}
				if task.Status != "running" && task.Status != "queued" {
					return cardToast("info", "任务已不在执行中"), nil
				}
				err = f.app.stop(task.ID)
				message = "已请求停止执行"
			case "view":
			default:
				err = errors.New("unknown action")
			}
			if err == nil {
				card, err = f.taskDetailCard(chat, task)
			}
		}
	}
	if err != nil {
		return cardToast("error", "操作未完成，请刷新任务列表后重试"), nil
	}
	return &cb.CardActionTriggerResponse{Toast: &cb.Toast{Type: "success", Content: message}, Card: &cb.Card{Type: "raw", Data: card}}, nil
}
