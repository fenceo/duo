package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

type worker struct {
	cancel   context.CancelFunc
	stopping bool
}
type App struct {
	hardwareAI      *HardwareAI
	hardwareAddress string
	discovery       *SSHDiscovery
	hardware        *Hardware
	terminals       *Terminals
	store           *Store
	config          *ConfigFile
	runner          Runner
	mu              sync.Mutex
	workers         map[string]*worker
	slots           chan struct{}
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	notify          chan struct{}
	feishu          *Feishu
}

func newApp(s *Store, c *ConfigFile, r Runner) *App {
	ctx, cancel := context.WithCancel(context.Background())
	return &App{hardwareAI: newHardwareAI(), hardwareAddress: c.get().Listen, discovery: newSSHDiscovery(), terminals: newTerminals(), hardware: newHardware(s), store: s, config: c, runner: r, workers: map[string]*worker{}, slots: make(chan struct{}, 2), ctx: ctx, cancel: cancel, notify: make(chan struct{}, 1)}
}
func (a *App) changed() {
	select {
	case a.notify <- struct{}{}:
	default:
	}
}
func (a *App) create(title, workspace, model string, environmentID ...string) (Task, error) {
	return a.createWithReasoning(title, workspace, model, "", environmentID...)
}
func (a *App) createWithReasoning(title, workspace, model, reasoning string, environmentID ...string) (Task, error) {
	return a.createWithExecution(title, workspace, model, "", reasoning, environmentID...)
}
func (a *App) createWithExecution(title, workspace, model, engine, reasoning string, environmentID ...string) (Task, error) {
	c := a.config.get()
	id := ""
	if len(environmentID) > 0 {
		id = environmentID[0]
	}
	env, err := c.environment(id)
	if err != nil {
		return Task{}, err
	}
	if engine == "" {
		engine = env.DefaultEngine
		if engine == "" {
			engine = "codex"
		}
	}
	if !validEngine(engine) {
		return Task{}, errors.New("AI 工具无效")
	}
	if !validEngineReasoning(engine, reasoning) {
		return Task{}, errors.New("此 AI 工具不支持所选推理强度")
	}
	workspace = strings.TrimSpace(workspace)
	valid := strings.HasPrefix(workspace, "/")
	if env.Type == "windows" {
		valid = filepath.IsAbs(workspace)
	}
	if !valid || strings.ContainsAny(workspace, "\x00\r\n") || len(workspace) > 4096 {
		return Task{}, errors.New("请填写所选环境的绝对路径：Windows 如 C:\\work，WSL / SSH 如 /home/dev/work")
	}
	title = strings.TrimSpace(title)
	if title == "" || len([]rune(title)) > 180 {
		return Task{}, errors.New("请填写 1–180 字的任务名称")
	}
	if len(model) > 120 || strings.ContainsAny(model, "\r\n") {
		return Task{}, errors.New("模型名称无效")
	}
	t := Task{Engine: engine, ReasoningEffort: reasoning, Environment: &env, ID: uid(), Title: title, Workspace: workspace, Model: model, Status: "idle", Created: now(), Updated: now()}
	tx, e := a.store.Begin()
	if e != nil {
		return Task{}, e
	}
	defer tx.Rollback()
	_, e = tx.Exec("INSERT INTO tasks(id,title,workspace,model,status,created,updated) VALUES(?,?,?,?,?,?,?)", t.ID, t.Title, t.Workspace, t.Model, t.Status, t.Created, t.Updated)
	if e != nil {
		return Task{}, e
	}
	raw, _ := json.Marshal(env)
	_, e = tx.Exec("INSERT INTO task_environments VALUES(?,?)", t.ID, string(raw))
	if e != nil {
		return Task{}, e
	}
	_, e = tx.Exec("INSERT INTO task_execution(task_id,reasoning_effort,engine) VALUES(?,?,?)", t.ID, t.ReasoningEffort, t.Engine)
	if e != nil {
		return Task{}, e
	}
	e = tx.Commit()
	a.changed()
	return t, e
}

type SubmitOptions struct {
	ModeID        string   `json:"mode_id"`
	AttachmentIDs []string `json:"attachment_ids"`
}

func (a *App) submit(id, input, kind, source string) (Run, error) {
	return a.submitWithOptions(id, input, kind, source, SubmitOptions{})
}
func (a *App) submitWithOptions(id, input, kind, source string, options SubmitOptions) (Run, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ctx.Err() != nil {
		return Run{}, errors.New("服务正在关闭")
	}
	if w := a.workers[id]; w != nil && w.stopping {
		return Run{}, errors.New("正在停止，请稍后再继续")
	}
	input = strings.TrimSpace(input)
	if input == "" || len(input) > 200000 {
		return Run{}, errors.New("消息不能为空，且不能超过 200 KB")
	}
	task, e := a.store.task(id)
	if e != nil {
		return Run{}, errors.New("任务不存在")
	}
	if task.Archived || task.Deleted {
		return Run{}, errors.New("任务已归档，请先在网页恢复任务")
	}
	mode, e := a.store.resolveMode(options.ModeID, task.Mode)
	if e != nil {
		return Run{}, e
	}
	attachments, e := a.store.messageAttachments(id, options.AttachmentIDs)
	if e != nil {
		return Run{}, e
	}
	var queued int
	_ = a.store.QueryRow("SELECT count(*) FROM runs WHERE task_id=? AND status='queued'", id).Scan(&queued)
	if queued >= 10 {
		return Run{}, errors.New("排队消息已达 10 条，请等待执行")
	}
	if kind != "knowledge" {
		kind = "chat"
	}
	r := Run{ID: uid(), TaskID: id, Input: input, Kind: kind, Source: source, Status: "queued", Created: now(), Mode: &mode, Attachments: attachments}
	tx, e := a.store.Begin()
	if e != nil {
		return r, e
	}
	defer tx.Rollback()
	_, e = tx.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES(?,?,?,?,?,?,?)", r.ID, id, input, kind, source, r.Status, r.Created)
	if e != nil {
		return r, e
	}
	_, e = tx.Exec("INSERT INTO events(task_id,run_id,kind,text,created) VALUES(?,?,?,?,?)", id, r.ID, "user", input, r.Created)
	if e != nil {
		return r, e
	}
	_, e = tx.Exec("UPDATE tasks SET updated=?,status=CASE WHEN status='running' THEN status ELSE 'queued' END WHERE id=?", now(), id)
	if e != nil {
		return r, e
	}
	modeJSON, _ := json.Marshal(mode)
	attachmentJSON, _ := json.Marshal(attachments)
	if _, e = tx.Exec("INSERT INTO run_options(run_id,mode,attachments) VALUES(?,?,?)", r.ID, string(modeJSON), string(attachmentJSON)); e != nil {
		return r, e
	}
	if _, e = tx.Exec("INSERT INTO task_options(task_id,mode) VALUES(?,?) ON CONFLICT(task_id) DO UPDATE SET mode=excluded.mode", id, string(modeJSON)); e != nil {
		return r, e
	}
	if _, e = tx.Exec("INSERT INTO run_metrics(run_id) VALUES(?)", r.ID); e != nil {
		return r, e
	}
	// Capture recipients in the same transaction as the run. Switching tasks later
	// must neither redirect its replies nor make its final result disappear.
	fc := a.config.get().Feishu
	if fc.Enabled && fc.AppID != "" && fc.Owner != "" {
		_, e = tx.Exec("INSERT INTO feishu_run_cards(id,run_id,chat_id,app_id,owner) SELECT lower(hex(randomblob(12))),?,chat_id,?,? FROM bindings WHERE task_id=?", r.ID, fc.AppID, fc.Owner, id)
		if e != nil {
			return r, e
		}
	}
	if e = tx.Commit(); e != nil {
		return r, e
	}
	if a.workers[id] == nil {
		ctx, cancel := context.WithCancel(a.ctx)
		w := &worker{cancel: cancel}
		a.workers[id] = w
		a.wg.Add(1)
		go a.work(ctx, id, w)
	}
	a.changed()
	return r, nil
}
func (a *App) work(ctx context.Context, id string, w *worker) {
	defer a.wg.Done()
	defer w.cancel()
	for {
		a.mu.Lock()
		var r Run
		e := a.store.QueryRow("SELECT id,input,kind FROM runs WHERE task_id=? AND status='queued' ORDER BY created,id LIMIT 1", id).Scan(&r.ID, &r.Input, &r.Kind)
		if e != nil || ctx.Err() != nil {
			delete(a.workers, id)
			a.mu.Unlock()
			return
		}
		_, _ = a.store.Exec("UPDATE runs SET status='running' WHERE id=?", r.ID)
		_, _ = a.store.Exec("UPDATE tasks SET status='running',updated=? WHERE id=?", now(), id)
		a.mu.Unlock()
		a.changed()
		select {
		case a.slots <- struct{}{}:
		case <-ctx.Done():
			a.finish(id, r, "", "", ctx.Err())
			continue
		}
		_, _ = a.store.Exec("INSERT INTO run_metrics(run_id,started) VALUES(?,?) ON CONFLICT(run_id) DO UPDATE SET started=excluded.started", r.ID, now())
		task, err := a.store.task(id)
		if err == nil {
			err = a.store.hydrateRun(&r)
			task.Mode = r.Mode
		}
		var session, result string
		if err == nil && task.Environment == nil {
			err = errors.New("任务缺少固定的执行环境，请检查迁移结果")
		}
		if err == nil {
			cfg := runtimeConfig(a.config.get(), *task.Environment)
			task.Files, err = a.stageAttachments(ctx, task, r.Attachments)
			var release = func() {}
			if err == nil && (task.Mode == nil || task.Mode.Permission != "read") {
				cfg.HardwareAI, release, err = a.prepareHardwareAI(ctx, task, r.ID, cfg)
			}
			if err == nil {
				session, result, err = a.runner.Run(ctx, cfg, task, executionInput(task, r.Input), func(kind, text string) {
					if kind == "usage" {
						_, _ = a.store.Exec("UPDATE run_metrics SET usage=? WHERE run_id=?", text, r.ID)
						a.changed()
						return
					}
					if kind == "session" {
						_, _ = a.store.Exec("UPDATE tasks SET session=? WHERE id=?", text, id)
						return
					}
					_ = a.store.event(id, r.ID, kind, text)
					a.changed()
				})
			}
			release()
		}
		<-a.slots
		a.finish(id, r, session, result, err)
	}
}
func (a *App) finish(id string, r Run, session, result string, err error) {
	status := "done"
	failure := ""
	if err != nil {
		status = "failed"
		failure = err.Error()
		if errors.Is(err, context.Canceled) {
			status = "interrupted"
			failure = "执行已停止"
		}
	}
	a.mu.Lock()
	tx, e := a.store.Begin()
	if e == nil {
		_, e = tx.Exec("UPDATE runs SET status=?,result=?,error=?,finished=? WHERE id=?", status, result, failure, now(), r.ID)
		if e == nil {
			_, e = tx.Exec("UPDATE tasks SET status=?,updated=?,session=CASE WHEN ?='' THEN session ELSE ? END WHERE id=?", status, now(), session, session, id)
		}
		if e == nil {
			e = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
	}
	a.mu.Unlock()
	if e != nil {
		_ = a.store.event(id, r.ID, "error", "保存运行结果失败："+e.Error())
	}
	_ = a.store.event(id, r.ID, "status", status+" "+failure)
	a.changed()
}
func (a *App) stop(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, e := a.store.task(id); e != nil {
		return e
	}
	_, e := a.store.Exec("UPDATE runs SET status='interrupted',error='已取消排队',finished=? WHERE task_id=? AND status='queued'", now(), id)
	if w := a.workers[id]; w != nil {
		w.stopping = true
		w.cancel()
	} else {
		_, _ = a.store.Exec("UPDATE tasks SET status='interrupted' WHERE id=?", id)
	}
	a.changed()
	return e
}
func (a *App) knowledge(id, source string) (Run, error) {
	items, e := a.store.knowledgeList(id)
	if e != nil {
		return Run{}, e
	}
	text := "请根据本任务已有对话整理可复用的任务知识，只返回 Markdown。包含问题、解决办法、实际验证结果和未解决事项；未验证的内容明确说明。不要调用工具，不修改项目文件。"
	if len(items) > 0 {
		var b strings.Builder
		b.WriteString(text)
		b.WriteString("\n\n已有知识，请合并保留有效内容：")
		for _, k := range items {
			if b.Len() > 12000 {
				b.WriteString("\n\n…（其余知识已省略，请只整理以上内容）")
				break
			}
			b.WriteString("\n\n### " + k.Title + "（" + knowledgeStateLabel(k.Status) + "）\n" + k.Content)
		}
		text = b.String()
	}
	return a.submit(id, text, "knowledge", source)
}
func (a *App) close() {
	a.discovery.close()
	a.terminals.close()
	a.hardware.close()
	a.cancel()
	if a.feishu != nil {
		a.feishu.shutdown()
	}
	a.wg.Wait()
}
func (a *App) bind(chat, id string) error {
	task, e := a.store.task(id)
	if e != nil {
		return fmt.Errorf("任务不存在：%w", e)
	}
	if task.Archived {
		return errors.New("任务已归档，请先在网页恢复任务")
	}
	_, e = a.store.Exec("INSERT INTO bindings VALUES(?,?) ON CONFLICT(chat_id) DO UPDATE SET task_id=excluded.task_id", chat, id)
	return e
}
func (a *App) bound(chat string) string {
	var id string
	err := a.store.QueryRow("SELECT task_id FROM bindings WHERE chat_id=?", chat).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	return id
}
