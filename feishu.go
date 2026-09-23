package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	"github.com/larksuite/oapi-sdk-go/v3/scene/registration"
	application "github.com/larksuite/oapi-sdk-go/v3/service/application/v6"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
	"strings"
	"sync"
	"time"
)

type Feishu struct {
	setupMu         sync.Mutex
	setup           *FeishuSetup
	register        func(context.Context, *registration.Options) (*registration.RegisterAppResult, error)
	initializeMenu  func(context.Context, FeishuConfig) error
	welcome         func(context.Context, FeishuConfig, string) (string, error)
	startRegistered func()

	app        *App
	mu         sync.Mutex
	lifecycle  sync.Mutex
	inbound    sync.Mutex
	client     *larkws.Client
	cancel     context.CancelFunc
	state      string
	generation int
	pair       string
	pairUntil  time.Time
	// Injected by tests; production uses the official SDK.
	deliver       func(context.Context, FeishuConfig, string, string, string) error
	createRunCard func(context.Context, FeishuConfig, string, string, string) (string, error)
	updateRunCard func(context.Context, FeishuConfig, string, string) error
}

func newFeishu(a *App) *Feishu {
	f := &Feishu{app: a, state: "未启用", deliver: sendFeishu}
	f.createRunCard = createFeishuCard
	f.updateRunCard = updateFeishuCard
	f.register = registration.RegisterApp
	f.initializeMenu = initializeFeishuMenu
	f.welcome = welcomeFeishu
	f.startRegistered = f.restart
	a.feishu = f
	return f
}
func (f *Feishu) status() string { f.mu.Lock(); defer f.mu.Unlock(); return f.state }
func (f *Feishu) setStatus(g int, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if g == f.generation {
		f.state = text
	}
}
func (f *Feishu) pairCode() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pair = uid()[:12]
	f.pairUntil = time.Now().Add(10 * time.Minute)
	return f.pair
}
func (f *Feishu) restart() {
	f.lifecycle.Lock()
	defer f.lifecycle.Unlock()
	f.mu.Lock()
	old := f.client
	cancel := f.cancel
	f.generation++
	g := f.generation
	f.client = nil
	f.cancel = nil
	f.state = "未启用"
	f.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if old != nil {
		ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		_ = old.CloseAndWait(ctx)
		c()
	}
	cfg := f.app.config.get().Feishu
	if !cfg.Enabled {
		return
	}
	ctx, stop := context.WithCancel(f.app.ctx)
	handler := dispatcher.NewEventDispatcher("", "").OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
		if event == nil || event.Event == nil || event.Event.Message == nil || event.Event.Sender == nil || event.Event.Sender.SenderId == nil {
			return nil
		}
		m := event.Event.Message
		sender := event.Event.Sender
		if value(m.ChatType) != "p2p" || value(sender.SenderType) != "user" {
			return nil
		}
		var content struct {
			Text string `json:"text"`
		}
		if value(m.MessageType) == "text" {
			_ = json.Unmarshal([]byte(value(m.Content)), &content)
		} else {
			content.Text = "/不支持的附件"
		}
		return f.receive(value(m.MessageId), value(sender.SenderId.OpenId), value(m.ChatId), content.Text)
	})
	handler.OnP2CardActionTrigger(func(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
		return f.receiveCard(event)
	})
	handler.OnP2BotMenuV6(func(ctx context.Context, event *application.P2BotMenuV6) error {
		if event == nil || event.Event == nil || event.Event.Operator == nil || event.Event.Operator.OperatorId == nil || event.EventV2Base == nil || event.EventV2Base.Header == nil {
			return nil
		}
		return f.receiveMenu(event.EventV2Base.Header.EventID, value(event.Event.Operator.OperatorId.OpenId), value(event.Event.EventKey))
	})
	cli := larkws.NewClient(cfg.AppID, cfg.Secret, larkws.WithEventHandler(handler), larkws.WithLogLevel(larkcore.LogLevelError), larkws.WithOnReady(func() { f.setStatus(g, "已连接") }), larkws.WithOnReconnecting(func() { f.setStatus(g, "正在重连") }), larkws.WithOnReconnected(func() { f.setStatus(g, "已连接") }), larkws.WithOnError(func(err error) { f.setStatus(g, "连接异常："+err.Error()) }))
	f.mu.Lock()
	f.client = cli
	f.cancel = stop
	f.state = "正在连接"
	f.mu.Unlock()
	go func() {
		if e := cli.Start(ctx); e != nil && ctx.Err() == nil {
			f.setStatus(g, "连接失败："+e.Error())
		}
	}()
}
func value(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (f *Feishu) shutdown() {
	f.lifecycle.Lock()
	defer f.lifecycle.Unlock()
	f.mu.Lock()
	c := f.client
	cancel := f.cancel
	f.client = nil
	f.cancel = nil
	f.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if c != nil {
		ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = c.CloseAndWait(ctx)
	}
}
func (f *Feishu) receive(messageID, sender, chat, text string) error {
	f.inbound.Lock()
	defer f.inbound.Unlock()
	if messageID == "" || sender == "" || chat == "" {
		return nil
	}
	c := f.app.config.get()
	if !c.Feishu.Enabled {
		return nil
	}
	text = strings.TrimSpace(text)
	if c.Feishu.Owner == "" {
		f.mu.Lock()
		valid := f.pair != "" && time.Now().Before(f.pairUntil) && subtle.ConstantTimeCompare([]byte(text), []byte("/配对 "+f.pair)) == 1
		if valid {
			f.pair = ""
		}
		f.mu.Unlock()
		if !valid {
			return nil
		}
		c.Feishu.Owner = sender
		if e := f.app.config.save(c); e != nil {
			return e
		}
		if e := f.app.store.set("feishu_chat", chat); e != nil {
			return e
		}
		return f.enqueue(chat, "已配对Duo。发送 /任务 查看网页任务；发送 /新建 标题 | 要求 创建任务。")
	}
	if c.Feishu.Owner != sender {
		return nil
	}
	res, e := f.app.store.Exec("INSERT OR IGNORE INTO seen VALUES(?,?)", messageID, now())
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil
	}
	_ = f.app.store.set("feishu_chat", chat)
	query := ""
	list := text == "/任务" || text == "我的任务" || text == "查看任务" || text == "任务列表"
	if strings.HasPrefix(text, "/任务 ") {
		list = true
		query = strings.TrimSpace(strings.TrimPrefix(text, "/任务 "))
	}
	if f.app.bound(chat) == "" && (text == "/继续" || text == "继续" || text == "继续任务") {
		list = true
	}
	if list {
		card, err := f.taskListCard(chat, query, 0)
		if err != nil {
			return err
		}
		return f.enqueueCard(chat, card)
	}
	reply, e := f.handle(chat, text)
	if e != nil {
		reply = "操作失败：" + e.Error()
	}
	if reply != "" {
		return f.enqueue(chat, reply)
	}
	return nil
}
func (f *Feishu) handle(chat, text string) (string, error) {
	a := f.app
	id := a.bound(chat)
	text = strings.TrimSpace(text)
	switch text {
	case "我的任务", "查看任务", "任务列表":
		text = "/任务"
	case "继续", "继续任务":
		text = "/继续"
	}
	command, arg, _ := strings.Cut(text, " ")
	arg = strings.TrimSpace(arg)
	switch command {
	case "/帮助":
		return "直接发消息：继续当前任务\n/新建 标题 | 要求\n/任务：列出任务\n/查看 任务ID：查看进展\n/切换 任务ID：选择已有任务\n/继续 [任务ID或要求]\n/停止：停止当前任务及排队消息\n/知识：整理当前任务知识（网页检查后保存）", nil
	case "/任务":
		return f.taskList(chat, arg)
	case "/查看", "/切换":
		target, err := f.findTask(arg, id)
		if err != nil {
			return err.Error(), nil
		}
		if command == "/切换" {
			if err = a.bind(chat, target.ID); err != nil {
				return "", err
			}
			return "已切换到：" + target.Title + "\n" + f.taskSummary(target) + "\n直接发送要求即可继续。", nil
		}
		return f.taskSummary(target) + "\n切换到此任务：/切换 " + target.ID[:8], nil
	case "/新建":
		if arg == "" {
			return "用法：/新建 标题 | 要求", nil
		}
		title, input, ok := strings.Cut(arg, "|")
		if !ok {
			input = title
		}
		c := a.config.get()
		model := c.Model
		if env, err := c.environment(""); err == nil {
			switch env.DefaultEngine {
			case "claude":
				model = env.ClaudeModel
			case "deepseek-harness":
				model = env.HarnessModel
			}
		}
		t, e := a.create(strings.TrimSpace(title), c.Workspaces[0], model)
		if e != nil {
			return "", e
		}
		if e = a.bind(chat, t.ID); e != nil {
			return "", e
		}
		if strings.TrimSpace(input) == "" {
			return "已创建：" + t.Title, nil
		}
		_, e = a.submit(t.ID, input, "chat", "feishu")
		return "", e
	case "/停止":
		if id == "" {
			return "尚未选择任务，请发送 /任务。", nil
		}
		return "已请求停止当前任务。", a.stop(id)
	case "/知识":
		if id == "" {
			return "尚未选择任务，请发送 /任务。", nil
		}
		_, e := a.knowledge(id, "feishu")
		return "", e
	case "/继续":
		if arg != "" {
			if target, err := f.findTask(arg, ""); err == nil {
				if err = a.bind(chat, target.ID); err != nil {
					return "", err
				}
				id = target.ID
				arg = ""
			}
		}
		if id == "" {
			list, err := f.taskList(chat, "")
			return "请先选择要继续的任务。\n" + list, err
		}
		if arg == "" {
			text = "继续"
		} else {
			text = arg
		}
	default:
		if strings.HasPrefix(command, "/") {
			return "暂不支持此命令或附件。发送 /帮助 查看文本任务用法。", nil
		}
	}
	if text == "" {
		return "", nil
	}
	if id == "" {
		list, err := f.taskList(chat, "")
		return "尚未选择任务。请先切换已有任务；新任务请发送 /新建 标题 | 要求。\n" + list, err
	}
	_, e := a.submit(id, text, "chat", "feishu")
	return "", e
}
func (f *Feishu) enqueue(chat, text string) error {
	runes := []rune(text)
	for len(runes) > 0 {
		n := min(2000, len(runes))
		_, e := f.app.store.Exec("INSERT INTO outbox(id,chat_id,text) VALUES(?,?,?)", uid(), chat, string(runes[:n]))
		if e != nil {
			return e
		}
		runes = runes[n:]
	}
	return nil
}
func sendFeishu(ctx context.Context, c FeishuConfig, chat, text, id string) error {
	client := lark.NewClient(c.AppID, c.Secret)
	content, _ := json.Marshal(map[string]string{"text": text})
	resp, e := client.Im.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().ReceiveIdType("chat_id").Body(larkim.NewCreateMessageReqBodyBuilder().ReceiveId(chat).MsgType("text").Content(string(content)).Uuid(id).Build()).Build())
	if e != nil {
		return e
	}
	if !resp.Success() {
		return fmt.Errorf("飞书返回错误 %d：%s", resp.Code, resp.Msg)
	}
	return nil
}
func (f *Feishu) outboxLoop() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-f.app.ctx.Done():
			return
		case <-tick.C:
		}
		c := f.app.config.get().Feishu
		if !c.Enabled {
			continue
		}
		var id, chat, text, card string
		var tries int
		e := f.app.store.QueryRow("SELECT outbox.id,chat_id,text,attempts,COALESCE(outbox_cards.content,'') FROM outbox LEFT JOIN outbox_cards ON outbox.id=outbox_cards.outbox_id WHERE status='pending' AND next_at<=? ORDER BY outbox.rowid LIMIT 1", now()).Scan(&id, &chat, &text, &tries, &card)
		if e != nil {
			continue
		}
		// Stream fallbacks retain the original bot/owner even after being queued.
		// Their IDs are <run-card ID>-f<number>; ordinary IDs are hex only.
		if streamID, _, scoped := strings.Cut(id, "-f"); scoped {
			var appID, owner string
			e = f.app.store.QueryRow("SELECT app_id,owner FROM feishu_run_cards WHERE id=?", streamID).Scan(&appID, &owner)
			if e != nil || appID != c.AppID || owner != c.Owner {
				_, _ = f.app.store.Exec("UPDATE outbox SET status='cancelled' WHERE id=?", id)
				continue
			}
		}
		ctx, cancel := context.WithTimeout(f.app.ctx, 15*time.Second)
		if card != "" {
			e = sendFeishuCard(ctx, c, chat, card, id)
		} else {
			e = f.deliver(ctx, c, chat, text, id)
		}
		cancel()
		if e == nil {
			_, _ = f.app.store.Exec("UPDATE outbox SET status='sent' WHERE id=?", id)
		} else {
			tries++
			status := "pending"
			if tries >= 6 {
				status = "failed"
			}
			_, _ = f.app.store.Exec("UPDATE outbox SET attempts=?,status=?,next_at=? WHERE id=?", tries, status, time.Now().Add(time.Duration(tries*tries)*time.Second).UnixMilli(), id)
			f.mu.Lock()
			f.state = "消息发送失败：" + e.Error()
			f.mu.Unlock()
		}
	}
}

func (f *Feishu) receiveMenu(eventID, sender, key string) error {
	command := map[string]string{"jianzuo.tasks": "/任务", "jianzuo.continue": "/继续", "jianzuo.knowledge": "/知识"}[key]
	if command == "" || eventID == "" || sender == "" || sender != f.app.config.get().Feishu.Owner {
		return nil
	}
	chat := f.app.store.setting("feishu_chat")
	if chat == "" {
		return nil
	}
	return f.receive("menu:"+eventID, sender, chat, command)
}

func (f *Feishu) taskList(chat, query string) (string, error) {
	tasks, err := f.app.store.tasks()
	if err != nil {
		return "", err
	}
	lines := []string{"任务列表（与网页共用）："}
	count := 0
	for _, t := range tasks {
		if t.Archived {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(t.Title), strings.ToLower(query)) && !strings.HasPrefix(t.ID, query) {
			continue
		}
		if count >= 20 {
			lines = append(lines, "更多任务可发送 /任务 关键词 筛选")
			break
		}
		count++
		mark := ""
		if t.ID == f.app.bound(chat) {
			mark = " ← 当前"
		}
		lines = append(lines, t.Title+" ["+feishuTaskStatus(t.Status)+"]"+mark, "查看：/查看 "+t.ID[:8], "选择：/切换 "+t.ID[:8], "")
	}
	if count == 0 {
		lines = append(lines, "没有匹配任务。新建：/新建 标题 | 要求")
	}
	lines = append(lines, "选择任务后直接发送要求。也可发送 /继续 任务ID。")
	return strings.Join(lines, "\n"), nil
}
func feishuTaskStatus(s string) string {
	v := map[string]string{"idle": "待开始", "done": "等待下一步", "failed": "执行失败", "running": "执行中", "queued": "排队中", "interrupted": "已停止"}[s]
	if v == "" {
		return s
	}
	return v
}
func (f *Feishu) findTask(query, current string) (Task, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		if current != "" {
			return f.app.store.task(current)
		}
		return Task{}, fmt.Errorf("请发送 /任务 查看列表，再提供任务 ID")
	}
	tasks, err := f.app.store.tasks()
	if err != nil {
		return Task{}, err
	}
	matches := []Task{}
	for _, t := range tasks {
		if strings.HasPrefix(t.ID, query) || t.Title == query {
			matches = append(matches, t)
		}
	}
	if len(matches) != 1 {
		return Task{}, fmt.Errorf("未找到唯一任务，请使用 /任务 列表中的任务 ID")
	}
	return matches[0], nil
}
func (f *Feishu) taskSummary(t Task) string {
	status := feishuTaskStatus(t.Status)
	if t.Archived {
		status = "已归档"
	}
	lines := []string{t.Title, "任务 ID：" + t.ID[:8], "状态：" + status, "目录：" + t.Workspace}
	runs, err := f.app.store.runs(t.ID)
	if err != nil {
		return strings.Join(lines, "\n")
	}
	if len(runs) > 0 {
		r := runs[len(runs)-1]
		out := r.Result
		if r.Error != "" {
			out = r.Error
		}
		if out == "" {
			out = "本轮尚无最终结果"
		}
		rs := []rune(out)
		if len(rs) > 1000 {
			out = string(rs[:1000]) + "…（完整结果见网页）"
		}
		lines = append(lines, "最近结果：", out)
	}
	return strings.Join(lines, "\n")
}
