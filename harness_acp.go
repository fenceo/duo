package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Only native Harness may run tools, under the invocation policy. Duo does not
// advertise client filesystem/terminal capabilities. Permission requests outside
// an explicitly interactive prompt are always cancelled.
func (w *harnessWorker) rejectClientRequest(ctx context.Context, msg codexRPC) error {
	response := map[string]any{"jsonrpc": "2.0", "id": msg.ID}
	if msg.Method == "session/request_permission" {
		response["result"] = map[string]any{"outcome": map[string]string{"outcome": "cancelled"}}
	} else {
		response["error"] = map[string]any{"code": -32601, "message": "Duo does not provide this ACP client capability"}
	}
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return w.write(ctx, append(data, '\n'))
}

// Close flushes the native log before releasing its process. A bounded forced
// stop remains the fallback for a crashed or unresponsive runtime.
func (w *harnessWorker) close() error {
	select {
	case <-w.done:
		return nil
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var closeErr error
	if w.session != "" && !w.stdoutClosed.Load() {
		_, closeErr = w.request(ctx, "session/close", map[string]any{"sessionId": w.session})
	}
	_ = w.in.Close()
	select {
	case <-w.done:
		return closeErr
	case <-ctx.Done():
		if err := w.stop(); err != nil {
			return err
		}
		return closeErr
	}
}

type harnessConfigOption struct {
	ID           string `json:"id"`
	CurrentValue string `json:"currentValue"`
	Options      []struct {
		Value string `json:"value"`
	} `json:"options"`
}

func (w *harnessWorker) prepareSession(ctx context.Context, c Config, t Task, emit func(string, string)) (string, error) {
	params := map[string]any{"cwd": t.Workspace, "mcpServers": harnessMCPServers(w.c.HardwareAI)}
	method := "session/new"
	if t.Session != "" {
		method = "session/resume"
		params["sessionId"] = t.Session
		emit("progress", "正在恢复 Harness 原生会话和上下文…")
	}
	// In dsh 0.1.5 the ACP transport can accept initialize before the settings
	// plugin has mounted its model adapters. Retry only this explicit startup
	// condition, before any prompt exists; never replay a model request.
	readyDeadline := time.Now().Add(5 * time.Second)
	var data json.RawMessage
	var err error
	for {
		data, err = w.request(ctx, method, params)
		if err == nil || !strings.Contains(err.Error(), "no adapter registered for provider") || time.Now().After(readyDeadline) {
			break
		}
		select {
		case <-ctx.Done():
			return t.Session, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	if err != nil {
		if t.Session != "" {
			return t.Session, fmt.Errorf("Harness 原生会话恢复失败；请确认执行环境和账号目录与原任务一致、原生会话文件仍存在。聊天记录已保留，未自动新建空白会话：%w", err)
		}
		return "", err
	}
	var response struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return t.Session, errors.New("Harness 会话响应无效")
	}
	w.session = t.Session
	if w.session == "" {
		w.session = response.SessionID
	}
	if w.session == "" {
		return "", errors.New("Harness 未返回原生会话 ID")
	}
	// Publish the durable ID before configuration or a prompt can fail, so a
	// retry always addresses the same conversation, including pre-ACP sessions.
	emit("session", w.session)
	return w.session, w.configure(ctx, c, t)
}

func (w *harnessWorker) configure(ctx context.Context, c Config, t Task) error {
	provider, model, err := harnessRouteForConfig(c, t.Model)
	if err != nil {
		return err
	}
	if !validEngineReasoning("deepseek-harness", t.ReasoningEffort) {
		return errors.New("Harness 推理强度无效")
	}
	route, _ := json.Marshal([]string{provider, model})
	selection := string(route) + "\n" + t.ReasoningEffort
	if w.selection == selection {
		return nil
	}
	// ACP's model selector resets reasoning to the provider default. Reapply it
	// even when only clearing an effort override, instead of guessing that default.
	data, err := w.request(ctx, "session/set_config_option", map[string]any{"sessionId": w.session, "configId": "model", "value": string(route)})
	if err != nil {
		return err
	}
	if t.ReasoningEffort != "" {
		var response struct {
			ConfigOptions []harnessConfigOption `json:"configOptions"`
		}
		if json.Unmarshal(data, &response) != nil {
			return errors.New("Harness 模型配置响应无效")
		}
		supported := false
		for _, option := range response.ConfigOptions {
			if option.ID != "reasoning_effort" {
				continue
			}
			for _, value := range option.Options {
				if value.Value == t.ReasoningEffort {
					supported = true
				}
			}
		}
		if !supported {
			return fmt.Errorf("Harness [UNSUPPORTED_REASONING_EFFORT]：%s", harnessUnsupportedReasoningMessage)
		}
		if _, err := w.request(ctx, "session/set_config_option", map[string]any{"sessionId": w.session, "configId": "reasoning_effort", "value": t.ReasoningEffort}); err != nil {
			return err
		}
	}
	w.selection = selection
	return nil
}

func runHarnessACP(ctx context.Context, c Config, t Task, input string, emit func(string, string)) (session, result string, runErr error) {
	session = t.Session
	if _, err := harnessPolicy(t); err != nil {
		return session, "", err
	}
	reserved := false
retryWorker:
	harnessRuntimes.Lock()
	w := harnessRuntimes.workers[session]
	if w != nil && w.retiring {
		harnessRuntimes.Unlock()
		select {
		case <-ctx.Done():
			return session, "", ctx.Err()
		case <-w.done:
		}
		harnessRuntimes.Lock()
		if harnessRuntimes.workers[session] == w {
			delete(harnessRuntimes.workers, session)
		}
		harnessRuntimes.Unlock()
		goto retryWorker
	}
	if w != nil {
		c.EngineEnv = w.c.EngineEnv
		if w.fingerprint != harnessFingerprint(c, t) {
			harnessRuntimes.Unlock()
			return session, "", errors.New("Harness 运行会话不能更换工作目录、可执行程序或权限，请新建任务")
		}
		if !w.lease.TryLock() {
			harnessRuntimes.Unlock()
			return session, "", errors.New("Harness 会话正忙，请等待当前轮结束")
		}
		if w.stdoutClosed.Load() {
			delete(harnessRuntimes.workers, session)
			harnessRuntimes.Unlock()
			_ = w.stop()
			w.lease.Unlock()
			w = nil
			harnessRuntimes.Lock()
		}
		// Image admission is fixed by initialize against the startup route. A
		// text-only connection must be reopened after selecting a vision model
		// or editing its native capability declaration (even with the same ID);
		// retain the old profile and durable ID, and close before resuming.
		// MCP credentials are scoped to a single run. An idle worker without
		// tools must also reopen to attach newly granted tools on cold resume.
		if w != nil && (c.HardwareAI != nil || w.c.HardwareAI != nil || (harnessHasImages(t.Files) && !w.imageInput)) {
			w.retiring = true
			if w.idle != nil {
				w.idle.Stop()
			}
			harnessRuntimes.Unlock()
			err := w.close()
			harnessRuntimes.Lock()
			if harnessRuntimes.workers[session] == w {
				delete(harnessRuntimes.workers, session)
			}
			w.lease.Unlock()
			if err != nil {
				harnessRuntimes.Unlock()
				return session, "", err
			}
			w = nil
		}
	}
	if w == nil {
		if len(harnessRuntimes.workers)+harnessRuntimes.starting >= 16 {
			harnessRuntimes.Unlock()
			return session, "", errors.New("Harness 运行会话已达 16 个，请先停止一个空闲任务后重试")
		}
		harnessRuntimes.starting++
		harnessRuntimes.Unlock()
		var err error
		w, err = startHarness(ctx, c, t, emit)
		harnessRuntimes.Lock()
		if err != nil {
			harnessRuntimes.starting--
			harnessRuntimes.Unlock()
			return session, "", err
		}
		reserved = true
		w.lease.Lock()
	}
	if w.idle != nil {
		w.idle.Stop()
	}
	w.generation++
	generation := w.generation
	harnessRuntimes.Unlock()
	defer w.lease.Unlock()
	defer func() {
		if reserved {
			harnessRuntimes.Lock()
			harnessRuntimes.starting--
			harnessRuntimes.Unlock()
		}
		if runErr != nil || t.ID == "" || w.c.HardwareAI != nil {
			harnessRuntimes.Lock()
			w.retiring = true
			harnessRuntimes.Unlock()
			if err := w.close(); err != nil {
				runErr = errors.Join(runErr, err)
			}
			harnessRuntimes.Lock()
			if harnessRuntimes.workers[session] == w {
				delete(harnessRuntimes.workers, session)
			}
			harnessRuntimes.Unlock()
			return
		}
		harnessRuntimes.Lock()
		w.idle = time.AfterFunc(30*time.Minute, func() {
			harnessRuntimes.Lock()
			if harnessRuntimes.workers[session] != w || w.generation != generation || !w.lease.TryLock() {
				harnessRuntimes.Unlock()
				return
			}
			// Keep ownership registered until close completes. A concurrent turn
			// must not open the same durable log while its old writer is flushing.
			w.retiring = true
			harnessRuntimes.Unlock()
			_ = w.close()
			harnessRuntimes.Lock()
			if harnessRuntimes.workers[session] == w {
				delete(harnessRuntimes.workers, session)
			}
			harnessRuntimes.Unlock()
			w.lease.Unlock()
		})
		harnessRuntimes.Unlock()
	}()
	if _, err := harnessPromptContent(input, t.Files, w.imageInput); err != nil {
		return session, "", err
	}
	setup, cancel := context.WithTimeout(ctx, 60*time.Second)
	if w.session == "" {
		session, runErr = w.prepareSession(setup, c, t, emit)
	} else {
		runErr = w.configure(setup, c, t)
	}
	cancel()
	if runErr != nil {
		return session, "", runErr
	}
	harnessRuntimes.Lock()
	if reserved {
		harnessRuntimes.starting--
		reserved = false
	}
	harnessRuntimes.workers[session] = w
	harnessRuntimes.Unlock()
	emit("session", session)
	return w.prompt(ctx, input, emit, t.Files...)
}

func (w *harnessWorker) prompt(ctx context.Context, input string, emit func(string, string), files ...RuntimeAttachment) (string, string, error) {
	ctx, cancelTurn := context.WithCancel(ctx)
	defer cancelTurn()
	content, err := harnessPromptContent(input, files, w.imageInput)
	if err != nil {
		return w.session, "", err
	}
	id, err := w.send(ctx, "session/prompt", map[string]any{"sessionId": w.session, "prompt": content})
	if err != nil {
		return w.session, "", err
	}
	state := harnessACPTurn{session: w.session}
	type pendingPermission struct {
		callID string
		cancel context.CancelFunc
	}
	pending := map[string]pendingPermission{}
	replies := make(chan harnessPermissionReply, 8)
	defer func() {
		for _, request := range pending {
			request.cancel()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			w.cancelPrompt(id)
			return w.session, state.result, ctx.Err()
		case err := <-w.fault:
			return w.session, state.result, err
		case reply := <-replies:
			key := string(reply.id)
			request, ok := pending[key]
			if !ok {
				continue
			}
			request.cancel()
			delete(pending, key)
			if _, live := state.tools[reply.callID]; !live {
				reply.result = json.RawMessage(`{"outcome":{"outcome":"cancelled"}}`)
			}
			data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": reply.id, "result": reply.result})
			if err := w.write(ctx, append(data, '\n')); err != nil {
				return w.session, state.result, err
			}
		case msg, ok := <-w.frames:
			if !ok {
				return w.session, state.result, w.failure()
			}
			if msg.Method != "" && len(msg.ID) > 0 {
				if w.interactive && msg.Method == "session/request_permission" {
					params, callID, err := state.permissionParams(msg.Params)
					key := string(msg.ID)
					_, duplicate := pending[key]
					if duplicate {
						return w.session, state.result, errors.New("Harness 重复发送了待处理审批标识")
					}
					busyCall := false
					for _, request := range pending {
						busyCall = busyCall || request.callID == callID
					}
					if err == nil && len(pending) < 8 && !busyCall {
						requestCtx, cancel := context.WithCancel(ctx)
						pending[key] = pendingPermission{callID, cancel}
						go func(msg codexRPC, callID string, params json.RawMessage) {
							reply := requestHarnessPermission(requestCtx, msg.ID, callID, params)
							select {
							case replies <- reply:
							case <-ctx.Done():
							}
						}(msg, callID, params)
						continue
					}
					emit("progress", "Harness 审批请求缺少有效的本轮操作详情或请求过多，已取消；未授予权限。")
				}
				if err := w.rejectClientRequest(ctx, msg); err != nil {
					return w.session, state.result, err
				}
				continue
			}
			if msg.Method != "" {
				state.consume(msg, emit)
				for _, request := range pending {
					if _, live := state.tools[request.callID]; !live {
						request.cancel()
					}
				}
				continue
			}
			if string(msg.ID) != fmt.Sprint(id) {
				continue
			}
			if msg.Error != nil {
				return w.session, state.result, harnessACPError(harnessRPCMessage(msg.Error.Message, msg.Error.Data))
			}
			var response struct {
				StopReason string `json:"stopReason"`
			}
			if json.Unmarshal(msg.Result, &response) != nil || response.StopReason != "end_turn" {
				return w.session, state.result, fmt.Errorf("Harness 本轮未正常完成：%s", response.StopReason)
			}
			if strings.TrimSpace(state.result) == "" {
				return w.session, "", errors.New("Harness 未返回最终回复")
			}
			return w.session, state.result, nil
		}
	}
}

func (w *harnessWorker) cancelPrompt(id int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]string{"sessionId": w.session}})
	if w.write(ctx, append(data, '\n')) != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.fault:
			return
		case msg, ok := <-w.frames:
			if !ok {
				return
			}
			if msg.Method != "" && len(msg.ID) > 0 {
				_ = w.rejectClientRequest(ctx, msg)
				continue
			}
			if msg.Method == "" && string(msg.ID) == fmt.Sprint(id) {
				return
			}
		}
	}
}

func harnessRPCMessage(message string, data json.RawMessage) string {
	var detail struct {
		Message string `json:"message"`
		Details string `json:"details"`
	}
	if json.Unmarshal(data, &detail) == nil {
		if detail.Message != "" {
			message += ": " + detail.Message
		} else if detail.Details != "" {
			message += ": " + detail.Details
		}
	}
	return message
}

func harnessACPError(message string) error {
	if strings.Contains(message, "MISSING_CREDENTIAL") || strings.Contains(strings.ToLower(message), "missing credential") {
		return errors.New("Harness 缺少凭据 [MISSING_CREDENTIAL]：请在此任务对应的执行环境中配置 Harness 原生凭据，再重试")
	}
	if isHarnessUnsupportedReasoning(message) {
		return errors.New(harnessUnsupportedReasoningMessage)
	}
	if strings.Contains(message, "does not declare image input") || strings.Contains(message, "inline image prompts were not advertised") {
		return errors.New(harnessUnsupportedImageMessage)
	}
	return errors.New("Harness 本轮失败：" + message)
}

type harnessACPTurn struct {
	session, result string
	afterTool       bool
	tools           map[string]harnessToolCall
}

func (s *harnessACPTurn) consume(msg codexRPC, emit func(string, string)) {
	if msg.Method != "session/update" {
		return
	}
	var p struct {
		SessionID string `json:"sessionId"`
		Update    struct {
			Kind     string          `json:"sessionUpdate"`
			Content  json.RawMessage `json:"content"`
			Title    string          `json:"title"`
			RawInput json.RawMessage `json:"rawInput"`
			Status   string          `json:"status"`
			CallID   string          `json:"toolCallId"`
		} `json:"update"`
	}
	if json.Unmarshal(msg.Params, &p) != nil || p.SessionID != s.session {
		return
	}
	u := p.Update
	switch u.Kind {
	case "agent_message_chunk", "agent_thought_chunk":
		var block struct{ Type, Text string }
		if json.Unmarshal(u.Content, &block) != nil || block.Type != "text" || block.Text == "" {
			return
		}
		if u.Kind == "agent_thought_chunk" {
			emit("progress", block.Text)
			return
		}
		if s.afterTool {
			s.result = ""
			s.afterTool = false
		}
		s.result += block.Text
		emit("assistant", s.result)
	case "tool_call":
		if u.CallID != "" && u.Title != "" && len(u.RawInput) > 0 && len(u.RawInput) < 240*1024 && len(s.tools) < 128 {
			if s.tools == nil {
				s.tools = map[string]harnessToolCall{}
			}
			// A duplicate ID cannot replace the operation awaiting user consent.
			if _, exists := s.tools[u.CallID]; !exists {
				s.tools[u.CallID] = harnessToolCall{ID: u.CallID, Title: u.Title, RawInput: append(json.RawMessage(nil), u.RawInput...)}
			}
		}
		s.afterTool = true
		emit("tool", u.Title+"\n"+string(u.RawInput))
	case "tool_call_update":
		if u.Status == "completed" || u.Status == "failed" {
			delete(s.tools, u.CallID)
		}
		text := string(u.Content)
		if len(text) > 24000 {
			text = text[:24000] + "…"
		}
		if u.Status == "failed" {
			text = "工具执行失败：" + text
		}
		if text != "" {
			emit("tool", text)
		}
	}
}
