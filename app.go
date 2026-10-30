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
	"sync/atomic"
	"time"
)

type worker struct {
	cancel       context.CancelFunc
	stopping     bool
	runID        string
	runKind      string
	runCancel    context.CancelFunc
	steer        *codexTurnControl
	nextRunID    string
	interrupting bool
}
type App struct {
	desktop             *DesktopControl
	engineSetup         *EngineSetup
	vaultMu             sync.Mutex
	knowledgeOrganizeMu sync.Mutex
	vaultReport         VaultReport
	vaultCacheRoot      string
	vaultCache          map[string]vaultCachedFile
	codexRequests       *CodexRequests
	hardwareAI          *HardwareAI
	hardwareAddress     string
	discovery           *SSHDiscovery
	hardware            *Hardware
	terminals           *Terminals
	store               *Store
	config              *ConfigFile
	runner              Runner
	mu                  sync.Mutex
	codexSyncMu         sync.Mutex
	workers             map[string]*worker
	slots               chan struct{}
	ctx                 context.Context
	cancel              context.CancelFunc
	wg                  sync.WaitGroup
	notify              chan struct{}
	feishu              *Feishu
	updating            atomic.Bool
}

func newApp(s *Store, c *ConfigFile, r Runner) *App {
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{codexRequests: newCodexRequests(), hardwareAI: newHardwareAI(), hardwareAddress: c.get().Listen, discovery: newSSHDiscovery(), terminals: newTerminals(), hardware: newHardware(s), store: s, config: c, runner: r, workers: map[string]*worker{}, slots: make(chan struct{}, 2), ctx: ctx, cancel: cancel, notify: make(chan struct{}, 1)}
	a.engineSetup = newEngineSetup()
	a.desktop = newDesktopControl()
	a.wg.Add(1)
	go a.vaultLoop()
	return a
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
	return a.createWithExecutionAndMode(title, workspace, model, engine, reasoning, nil, environmentID...)
}
func (a *App) createWithExecutionAndMode(title, workspace, model, engine, reasoning string, mode *WorkMode, environmentID ...string) (Task, error) {
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
	if engine != "codex" && mode != nil && mode.Approval == "auto" {
		return Task{}, errors.New("原生自动风险评审目前仅支持 Codex，请为当前引擎选择其它工作模式")
	}
	if mode != nil && (mode.ID == "harness:read" || mode.ID == harnessWorkspaceMode) && engine != "deepseek-harness" {
		return Task{}, errors.New("Harness 权限模式仅适用于 Harness")
	}
	if mode != nil && mode.ID == "mimo:plan" && engine != "mimo" {
		return Task{}, errors.New("MiMo 计划模式仅适用于 MiMo Code")
	}
	if engine == "mimo" {
		if _, _, err := mimoInvocation(Task{Mode: mode}); err != nil {
			return Task{}, err
		}
	}
	if engine == "opencode" {
		if _, err := opencodeInvocation(Task{Mode: mode}); err != nil {
			return Task{}, err
		}
	}
	if engine == "deepseek-harness" {
		if _, err := harnessPolicy(Task{Mode: mode, Workspace: workspace}); err != nil {
			return Task{}, err
		}
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
	t.Binding, err = a.store.defaultEngineBinding(t)
	if err != nil {
		return Task{}, err
	}
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
	if mode != nil {
		selected := *mode
		raw, _ := json.Marshal(selected)
		if _, e = tx.Exec("INSERT INTO task_options(task_id,mode) VALUES(?,?)", t.ID, string(raw)); e != nil {
			return Task{}, e
		}
		t.Mode = &selected
	}
	if e = saveEngineBinding(tx, t.ID, t.Binding); e != nil {
		return Task{}, e
	}
	if e = tx.Commit(); e != nil {
		return Task{}, e
	}
	a.changed()
	return t, nil
}

type SubmitOptions struct {
	QuestionID        string   `json:"-"`
	QuestionAnswers   []string `json:"-"`
	QuestionImmediate bool     `json:"-"`
	ModeID            string   `json:"mode_id"`
	AttachmentIDs     []string `json:"attachment_ids"`
	Delivery          string   `json:"delivery,omitempty"`
	ExpectedRunID     string   `json:"expected_run_id,omitempty"`
}

func (a *App) submit(id, input, kind, source string) (Run, error) {
	return a.submitWithOptions(id, input, kind, source, SubmitOptions{})
}
func (a *App) submitWithOptions(id, input, kind, source string, options SubmitOptions) (Run, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.updating.Load() {
		return Run{}, errUpdateBusy
	}
	if a.ctx.Err() != nil {
		return Run{}, errors.New("服务正在关闭")
	}
	if w := a.workers[id]; w != nil && w.stopping {
		return Run{}, errors.New("正在停止，请稍后再继续")
	}
	if options.Delivery != "" && options.Delivery != "queue" && options.Delivery != "interrupt" {
		return Run{}, errors.New("发送方式无效")
	}
	interrupt := options.Delivery == "interrupt"
	if interrupt {
		w := a.workers[id]
		if kind != "chat" || w == nil || w.runKind != "chat" || w.interrupting || w.runCancel == nil || options.ExpectedRunID == "" || w.runID != options.ExpectedRunID {
			return Run{}, errLiveTurnChanged
		}
	}
	if options.QuestionID != "" {
		var previous string
		var err error
		input, previous, err = a.store.questionAnswer(id, options.QuestionID, options.QuestionAnswers)
		if err != nil {
			return Run{}, err
		}
		if previous != "" {
			return Run{ID: previous, TaskID: id}, nil
		}
		if options.QuestionImmediate {
			var state string
			if err := a.store.QueryRow("SELECT status FROM async_questions WHERE task_id=? AND id=?", id, options.QuestionID).Scan(&state); err != nil {
				return Run{}, err
			}
			if state == "steering" {
				return Run{}, errors.New("上次答案接收结果未确认，未自动排队")
			}
			if w := a.workers[id]; w != nil && w.runID != "" {
				return Run{}, errLiveTurnChanged
			}
		}
	}
	input = strings.TrimSpace(input)
	if input == "" || len(input) > 200000 {
		return Run{}, errors.New("消息不能为空，且不能超过 200 KB")
	}
	task, e := a.store.task(id)
	if e != nil {
		return Run{}, errors.New("任务不存在")
	}
	if a.engineUpdateBlocks(task) {
		return Run{}, errors.New("该环境正在更新 CLI，请等待完成或取消后再提交任务")
	}
	if task.Archived || task.Deleted {
		return Run{}, errors.New("任务已归档，请先在网页恢复任务")
	}
	if task.Binding == nil {
		task.Binding, e = a.store.defaultEngineBinding(task)
		if e != nil {
			return Run{}, e
		}
	}
	mode, e := a.store.resolveMode(options.ModeID, task.Mode)
	if e != nil {
		return Run{}, e
	}
	if kind == "knowledge" {
		mode = knowledgeMode(task.Engine)
	}
	if task.Engine != "codex" && mode.Approval == "auto" {
		return Run{}, errors.New("原生自动风险评审目前仅支持 Codex，请为当前引擎选择其它工作模式")
	}
	if (mode.ID == "harness:read" || mode.ID == harnessWorkspaceMode) && task.Engine != "deepseek-harness" {
		return Run{}, errors.New("Harness 权限模式仅适用于 Harness")
	}
	if mode.ID == "mimo:plan" && task.Engine != "mimo" {
		return Run{}, errors.New("MiMo 计划模式仅适用于 MiMo Code")
	}
	if task.Engine == "mimo" {
		if _, _, err := mimoInvocation(Task{Mode: &mode}); err != nil {
			return Run{}, err
		}
		if kind == "knowledge" {
			return Run{}, errors.New("MiMo 独立知识整理尚未接入")
		}
	}
	if task.Engine == "opencode" {
		if _, err := opencodeInvocation(Task{Mode: &mode}); err != nil {
			return Run{}, err
		}
		if kind == "knowledge" {
			return Run{}, errors.New("OpenCode 独立知识整理尚未接入")
		}
	}
	if task.Engine == "zcode" {
		if err := zcodeInvocation(Task{Mode: &mode}); err != nil {
			return Run{}, err
		}
		if kind == "knowledge" {
			return Run{}, errors.New("ZCode 独立知识整理尚未接入")
		}
	}
	if task.Engine == "deepseek-harness" {
		if _, err := harnessPolicy(Task{Mode: &mode, Workspace: task.Workspace}); err != nil {
			return Run{}, err
		}
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
	if options.QuestionID != "" {
		changed, err := tx.Exec("UPDATE async_questions SET status='answered',answer=?,answer_run=? WHERE id=? AND task_id=? AND status='pending'", input, r.ID, options.QuestionID, id)
		if err != nil {
			return r, err
		}
		count, _ := changed.RowsAffected()
		if count != 1 {
			return r, errAsyncQuestionExpired
		}
	}
	if e = saveEngineBinding(tx, id, task.Binding); e != nil {
		return r, e
	}
	if e = saveRunExecution(tx, r.ID, task); e != nil {
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
	if kind != "knowledge" {
		if _, e = tx.Exec("INSERT INTO task_options(task_id,mode) VALUES(?,?) ON CONFLICT(task_id) DO UPDATE SET mode=excluded.mode", id, string(modeJSON)); e != nil {
			return r, e
		}
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
	if interrupt {
		// Save the replacement before cancelling. The same worker waits for
		// native shutdown and persists its session before starting this run.
		w := a.workers[id]
		w.nextRunID = r.ID
		w.interrupting = true
		w.runCancel()
	}
	if options.QuestionImmediate && a.workers[id] != nil {
		a.workers[id].nextRunID = r.ID
	}
	if a.workers[id] == nil {
		ctx, cancel := context.WithCancel(a.ctx)
		w := &worker{cancel: cancel}
		if options.QuestionImmediate {
			w.nextRunID = r.ID
		}
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
		e := a.store.QueryRow("SELECT id,input,kind,source FROM runs WHERE task_id=? AND status='queued' ORDER BY CASE WHEN id=? THEN 0 ELSE 1 END,created,rowid LIMIT 1", id, w.nextRunID).Scan(&r.ID, &r.Input, &r.Kind, &r.Source)
		r.TaskID = id
		if e != nil || ctx.Err() != nil {
			delete(a.workers, id)
			a.mu.Unlock()
			return
		}
		_, _ = a.store.Exec("UPDATE runs SET status='running' WHERE id=?", r.ID)
		_, _ = a.store.Exec("UPDATE tasks SET status='running',updated=? WHERE id=?", now(), id)
		runCtx, cancelRun := context.WithCancel(ctx)
		w.runID, w.runKind, w.runCancel = r.ID, r.Kind, cancelRun
		w.nextRunID, w.interrupting = "", false
		a.mu.Unlock()
		a.changed()
		select {
		case a.slots <- struct{}{}:
		case <-runCtx.Done():
			a.clearRunControl(id, w)
			a.finish(id, r, "", "", runCtx.Err())
			cancelRun()
			continue
		}
		_, _ = a.store.Exec("INSERT INTO run_metrics(run_id,started) VALUES(?,?) ON CONFLICT(run_id) DO UPDATE SET started=excluded.started", r.ID, now())
		a.mu.Lock()
		task, err := a.store.task(id)
		if err == nil {
			err = a.store.applyRunExecution(r.ID, &task)
		}
		a.mu.Unlock()
		if err == nil {
			err = a.store.hydrateRun(&r)
			task.Mode = r.Mode
			// Summaries must not inherit a full-access or hardware-enabled mode.
			if r.Kind == "knowledge" {
				task.Mode = modePtr(knowledgeMode(task.Engine))
			}
		}
		var session, result string
		var worklogRecall *workspaceWorklogRecall
		if err == nil && task.Environment == nil {
			err = errors.New("任务缺少固定的执行环境，请检查迁移结果")
		}
		cleanupAttachments := func() {}
		handoffInput := ""
		if err == nil {
			cfg := runtimeConfig(a.config.get(), *task.Environment)
			cfg.EngineEnv = a.activeEngineEnvironment(task)
			var stagedCleanup func()
			task.Files, stagedCleanup, err = a.stageAttachments(runCtx, task, r.Attachments)
			if err == nil {
				cleanupAttachments = stagedCleanup
			}
			if err == nil && r.Kind == "chat" {
				var historyFiles []RuntimeAttachment
				var historyCleanup func()
				handoffInput, historyFiles, historyCleanup, err = a.stageContinuation(runCtx, task)
				if err == nil {
					task.Files = append(task.Files, historyFiles...)
					attachmentCleanup := cleanupAttachments
					cleanupAttachments = func() { historyCleanup(); attachmentCleanup() }
				}
			}
			var release = func() {}
			if err == nil && (task.Mode == nil || task.Mode.Permission != "read") {
				cfg.HardwareAI, release, err = a.prepareHardwareAI(runCtx, task, r.ID, cfg)
			}
			if err == nil {
				input := r.Input
				if r.Kind == "knowledge" && (task.Engine == "deepseek-harness" || r.Source == knowledgeExtractionSource) {
					if r.Source == knowledgeExtractionSource {
						input = knowledgeExtractionPrompt(r.ID)
					}
					if r.Source == knowledgeExtractionSource {
						input, err = a.knowledgeSynthesisInput(runCtx, task, r.ID, input)
					} else {
						input, err = a.store.harnessKnowledgeInput(id, input)
					}
					if task.Engine != "deepseek-harness" {
						input = strings.ReplaceAll(input, "本次独立会话的全部工具操作被拒绝。", "本次使用独立只读会话。请仅根据提供的文本整理，不调用工具。")
					}
					// Never resume or persist the source task's durable session.
					task.ID, task.Session, task.Files, task.NoTools = "", "", nil, true
					cfg.HardwareAI = nil
				}
				if handoffInput != "" {
					input = handoffInput + "\n\n本轮用户要求：\n" + input
				}
				if r.Kind == "chat" {
					// Name a deliberate tool-free turn before the model runs.
					if handoffInput == "" {
						lookupCtx, lookupCancel := context.WithTimeout(runCtx, 5*time.Second)
						lookup, lookupIDs, lookupErr := a.store.knowledgeLookupContext(lookupCtx, task, r.Input)
						lookupCancel()
						if lookupErr == nil && lookup != "" {
							input = lookup + input
							_ = a.store.event(id, r.ID, "progress", "已检索相关知识（来源 ID）："+strings.Join(lookupIDs, "、"))
						}
					}
					if note := a.hardwareToolOmission(task); note != "" {
						_ = a.store.event(id, r.ID, "progress", note)
					}
					// A brief recovery already carries the reviewed local recall.
					// Do not silently add a second set of knowledge to that scope.
					if handoffInput == "" || task.Binding == nil || task.Binding.HistoryMode != "brief" {
						worklogCtx, worklogCancel := context.WithTimeout(runCtx, 8*time.Second)
						var worklogErr error
						worklogRecall, worklogErr = a.prepareWorkspaceWorklogContext(worklogCtx, task)
						worklogCancel()
						if worklogErr != nil {
							_ = a.store.event(id, r.ID, "progress", "本任务 Markdown 记录读取失败，本轮继续使用已取得的上下文。")
						} else if worklogRecall != nil {
							input = worklogRecall.Text + input
							_ = a.store.event(id, r.ID, "progress", "已读取本任务 Markdown 记录（来源 ID）："+worklogRecall.ID)
						}
						contextText, ids, recallErr := a.store.automaticKnowledgeContext(runCtx, task)
						if recallErr != nil {
							_ = a.store.event(id, r.ID, "progress", "自动恢复任务知识失败，本轮继续使用原始要求。")
						} else if contextText != "" {
							input = contextText + input
							_ = a.store.event(id, r.ID, "progress", "已自动恢复当前任务知识（来源 ID）："+strings.Join(ids, "、"))
						}
						recallCtx, recallCancel := context.WithTimeout(runCtx, 8*time.Second)
						workspaceText, workspaceIDs, workspaceErr := a.workspaceMemoryContext(recallCtx, task)
						recallCancel()
						if workspaceErr != nil {
							_ = a.store.event(id, r.ID, "progress", "工作区交接资料读取失败，本轮继续使用已取得的上下文。")
						} else if workspaceText != "" {
							input = workspaceText + input
							_ = a.store.event(id, r.ID, "progress", "已恢复同工作区交接（来源 ID）："+strings.Join(workspaceIDs, "、"))
						}
						consensusText, consensusIDs, consensusErr := a.store.curatedContext(runCtx, task)
						if consensusErr != nil {
							_ = a.store.event(id, r.ID, "progress", "平台共识读取失败，本轮继续使用已取得的上下文。")
						} else if consensusText != "" {
							input = consensusText + input
							_ = a.store.event(id, r.ID, "progress", "已恢复已确认平台共识（来源 ID）："+strings.Join(consensusIDs, "、"))
						}
					}
					if automatic, configErr := automaticKnowledgeConfig(a.store); configErr == nil && automatic.Capture {
						input = memoryPrompt(r.ID) + input
					}
				}
				if task.Engine == "codex" && r.Kind == "chat" {
					control := newCodexTurnControl(func(text string) error {
						err := a.store.event(id, r.ID, "user", text)
						a.changed()
						return err
					})
					a.mu.Lock()
					w.steer = control
					a.mu.Unlock()
					runCtx = context.WithValue(runCtx, codexSteerKey{}, control)
				}
				runCtx = withCodexInteraction(runCtx, func(requestCtx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
					return a.requestCodexInteraction(requestCtx, id, r.ID, method, params)
				})
				lastVisibleAssistant := ""
				if err == nil {
					stopCheckpoint := a.startWorkspaceExecutionCheckpoint(task, r)
					session, result, err = a.runner.Run(runCtx, cfg, task, executionInput(task, input), func(kind, text string) {
						if kind == "context" {
							var value RunContext
							if json.Unmarshal([]byte(text), &value) == nil && value.valid() {
								_, _ = a.store.Exec("INSERT INTO run_context VALUES(?,?) ON CONFLICT(run_id) DO UPDATE SET value=excluded.value", r.ID, text)
								a.changed()
							}
							return
						}
						if kind == "output_image_reset" {
							_, _ = a.store.Exec("UPDATE output_images SET source='process' WHERE run_id=? AND task_id=? AND source='assistant'", r.ID, id)
							a.changed()
							return
						}
						if kind == "output_image" {
							if imageErr := a.store.saveOutputImage(id, r.ID, text); imageErr != nil {
								_ = a.store.event(id, r.ID, "progress", "输出图片未保存："+imageErr.Error())
							}
							a.changed()
							return
						}
						if kind == "async_question" {
							if saveErr := a.store.saveAsyncQuestion(id, r.ID, text); saveErr != nil {
								_ = a.store.event(id, r.ID, "error", "问题卡片保存失败："+saveErr.Error())
							}
							a.changed()
							return
						}
						if kind == "usage" {
							_, _ = a.store.Exec("UPDATE run_metrics SET usage=? WHERE run_id=?", text, r.ID)
							a.changed()
							return
						}
						if kind == "session" {
							if !task.NoTools {
								_, _ = a.store.Exec("UPDATE tasks SET session=? WHERE id=?", text, id)
							}
							return
						}
						if kind == "assistant" && (r.Kind == "chat" || r.Source == knowledgeExtractionSource) {
							visible, hidden := visibleMemoryStream(text, r.ID)
							if hidden && (visible == "" || visible == lastVisibleAssistant) {
								return
							}
							text = visible
							lastVisibleAssistant = text
						}
						_ = a.store.event(id, r.ID, kind, text)
						a.changed()
					})
					stopCheckpoint()
				}
				if err == nil && r.Kind == "chat" && worklogRecall != nil {
					ackSession := session
					if ackSession == "" {
						// Some runners report durable identity only through events.
						if current, e := a.store.task(id); e == nil {
							ackSession = current.Session
						}
					}
					ackCtx, ackCancel := context.WithTimeout(context.Background(), 5*time.Second)
					ackErr := a.acknowledgeWorkspaceWorklogContext(ackCtx, task, worklogRecall, ackSession)
					ackCancel()
					if ackErr != nil {
						_ = a.store.event(id, r.ID, "progress", "本轮已读取任务文档，但交接进度未保存；下轮会重试读取。")
					}
				}
				if task.NoTools {
					session = ""
				}
			}
			release()
		}
		cleanupAttachments()
		cancelRun()
		// A failed cleanup must never start another process against the same
		// workspace while the old one may still be alive.
		a.mu.Lock()
		if w.interrupting && err != nil && !errors.Is(err, context.Canceled) {
			_, _ = a.store.Exec("UPDATE runs SET status='interrupted',error='未能确认前一轮停止，请检查后重新发送',finished=? WHERE task_id=? AND status='queued'", now(), id)
			w.stopping = true
			w.cancel()
		}
		a.mu.Unlock()
		a.clearRunControl(id, w)
		<-a.slots
		a.finish(id, r, session, result, err)
	}
}

func (a *App) clearRunControl(id string, w *worker) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.steer.close()
	w.steer, w.runCancel = nil, nil
	w.runID, w.runKind = "", ""
}
func (a *App) finish(id string, r Run, session, result string, err error) {
	var memory *taskMemory
	synthesisValid := r.Source != knowledgeExtractionSource || validKnowledgeSynthesisResponse(result, r.ID)
	if r.Source == knowledgeExtractionSource {
		// The temporary extraction identity must never replace the chat session,
		// including runners that report a session only in their return value.
		session = ""
	}
	if r.Kind == "chat" || r.Source == knowledgeExtractionSource {
		result, memory = splitTaskMemory(result, r.ID)
	}
	if !synthesisValid {
		memory = nil
	}
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
	var captured Knowledge
	var captureErr error
	tx, e := a.store.Begin()
	if e == nil {
		var previous string
		e = tx.QueryRow("SELECT status FROM runs WHERE id=? AND task_id=?", r.ID, id).Scan(&previous)
		if e == nil {
			_, e = tx.Exec("UPDATE runs SET status=?,result=?,error=?,finished=? WHERE id=?", status, result, failure, now(), r.ID)
		}
		if e == nil {
			_, e = tx.Exec("UPDATE tasks SET status=?,updated=?,session=CASE WHEN ?='' THEN session ELSE ? END WHERE id=?", status, now(), session, session, id)
		}
		if e == nil && status == "done" && (previous == "running" || previous == "queued") {
			if r.Source == knowledgeExtractionSource {
				captureErr = captureExtractedMemory(tx, id, r.ID, memory)
			} else {
				captured, captureErr = captureAutomaticKnowledge(tx, id, r.ID)
			}
		}
		// A failed/stopped chat can still report completed substeps and useful
		// blockers. Preserve validated task-state patches independently of the
		// done-only long-term knowledge capture/extraction path.
		if e == nil && r.Kind == "chat" && r.Source != knowledgeExtractionSource && (previous == "running" || previous == "queued") {
			memoryErr := captureTaskMemory(tx, id, r.ID, memory)
			if captureErr == nil {
				captureErr = memoryErr
			}
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
	} else if captureErr != nil {
		_ = a.store.event(id, r.ID, "progress", "本轮结果已保存，但自动记录知识失败；可在对话底部手动保存。")
	} else if captured.ID != "" {
		_ = a.store.event(id, r.ID, "progress", "已自动记录到任务资料，来源 ID："+captured.ID)
	}
	_ = a.store.event(id, r.ID, "status", status+" "+failure)
	a.changed()
	if e == nil {
		automatic, automaticErr := automaticKnowledgeConfig(a.store)
		if status == "done" && captureErr == nil && (r.Source == knowledgeExtractionSource || automaticErr == nil && automatic.Capture && automatic.Organize && a.store.knowledgeExtractionEnabled()) {
			extractCtx, extractCancel := context.WithTimeout(context.Background(), 8*time.Second)
			a.knowledgeOrganizeMu.Lock()
			report, extractErr := a.store.extractTaskKnowledge(extractCtx, id, r.ID, nil)
			a.knowledgeOrganizeMu.Unlock()
			extractCancel()
			if extractErr != nil {
				_ = a.store.event(id, r.ID, "progress", "运行结果已保存，分层知识提取未完成；可在知识库中重试。")
			} else if report.Created > 0 {
				_ = a.store.event(id, r.ID, "progress", fmt.Sprintf("已提取 %d 条分层知识候选，仍需核对依据和适用条件。", report.Created))
			} else if report.Revisions > 0 {
				_ = a.store.event(id, r.ID, "progress", fmt.Sprintf("已记录 %d 条同主题待合并修订，原知识正文保持不变。", report.Revisions))
			} else if r.Source == knowledgeExtractionSource {
				_ = a.store.event(id, r.ID, "progress", fmt.Sprintf("本次未新增分层知识候选（重复 %d 条）；可查看整理结果及未提取原因。", report.Duplicates))
			}
			if r.Source == knowledgeExtractionSource && extractErr == nil {
				expected := 0
				if memory != nil {
					expected = len(memory.KnowledgeCandidates)
					if expected == 0 {
						expected = len(memory.Insights)
					}
				}
				if captureErr == nil && report.Skipped == 0 && report.Created+report.Revisions+report.Duplicates >= expected {
					ackCtx, ackCancel := context.WithTimeout(context.Background(), 5*time.Second)
					ackErr := a.store.acknowledgeKnowledgeSynthesis(ackCtx, id, r.ID, memory)
					ackCancel()
					if ackErr != nil {
						_ = a.store.event(id, r.ID, "progress", "整理结果已保留，但来源进度未推进；请检查有效候选或重新整理。")
					} else {
						_ = a.store.event(id, r.ID, "progress", "本批整理进度已保存；再次使用 AI 整理会继续尚未处理的历史。")
					}
				}
				for _, warning := range report.Warnings {
					_ = a.store.event(id, r.ID, "progress", warning)
				}
			}
		}
		// Export after committing and releasing the app lock. A failed mirror
		// never turns a completed model run into a failure.
		memoryBase := a.ctx
		if memoryBase == nil {
			memoryBase = context.Background()
		}
		memoryCtx, cancel := context.WithTimeout(memoryBase, 8*time.Second)
		report, syncErr := a.syncWorkspaceMemory(memoryCtx, id)
		cancel()
		if syncErr != nil || report.Error != "" {
			_ = a.store.event(id, r.ID, "progress", "运行结果已保存，工作区过程同步未完成；可在本任务知识中重试。")
		} else if len(report.Conflicts) > 0 || len(report.Warnings) > 0 {
			_ = a.store.event(id, r.ID, "progress", "工作区过程已同步，存在待检查项目；请在本任务知识中查看。")
		}
		a.changed()
	}
}
func (a *App) stop(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	task, e := a.store.task(id)
	if e != nil {
		return e
	}
	_, e = a.store.Exec("UPDATE runs SET status='interrupted',error='已取消排队',finished=? WHERE task_id=? AND status='queued'", now(), id)
	if w := a.workers[id]; w != nil {
		w.stopping = true
		w.cancel()
	} else {
		if task.Engine == "deepseek-harness" {
			if err := closeIdleHarnessSession(task.Session); err != nil {
				return err
			}
		}
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
			section, cut := clipContinuation("### "+k.Title+"（"+knowledgeStateLabel(k.Status)+"）\n"+k.Content, min(2400, max(0, (12000-b.Len())/4-30)))
			b.WriteString("\n\n" + section)
			if cut {
				b.WriteString("\n（该条知识仅纳入部分内容）")
			}
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
	closeHarnessRuntimes()
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
