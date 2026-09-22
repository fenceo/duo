package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

type codexInteractionKey struct{}
type codexInteractionHandler func(context.Context, string, json.RawMessage) (json.RawMessage, error)

func withCodexInteraction(ctx context.Context, handler codexInteractionHandler) context.Context {
	return context.WithValue(ctx, codexInteractionKey{}, handler)
}

func handleCodexInteraction(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	handler, _ := ctx.Value(codexInteractionKey{}).(codexInteractionHandler)
	if handler == nil {
		return nil, errors.New("当前调用没有交互审批通道，操作未获批准")
	}
	return handler(ctx, method, params)
}

// Only live native requests are actionable. After disconnect/restart they must
// not be replayed: historical events are an audit trail, never an authorization.
type CodexPendingRequest struct {
	ID      string          `json:"id"`
	TaskID  string          `json:"task_id"`
	RunID   string          `json:"run_id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Created int64           `json:"created"`
}

type codexPending struct {
	CodexPendingRequest
	ctx    context.Context
	answer chan json.RawMessage
}

type CodexRequests struct {
	mu      sync.Mutex
	pending map[string]*codexPending
}

func newCodexRequests() *CodexRequests {
	return &CodexRequests{pending: make(map[string]*codexPending)}
}

func (c *CodexRequests) list(taskID string) []CodexPendingRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := []CodexPendingRequest{}
	for _, p := range c.pending {
		if p.TaskID == taskID && p.ctx.Err() == nil {
			result = append(result, p.CodexPendingRequest)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Created == result[j].Created {
			return result[i].ID < result[j].ID
		}
		return result[i].Created < result[j].Created
	})
	return result
}

func (a *App) requestCodexInteraction(ctx context.Context, taskID, runID, method string, params json.RawMessage) (json.RawMessage, error) {
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval", "item/tool/requestUserInput":
	case "mcpServer/elicitation/request":
		_ = a.store.event(taskID, runID, "progress", "此 MCP 表单尚未支持，已拒绝；未授予权限。")
		a.changed()
		return json.RawMessage(`{"action":"decline","content":null}`), nil
	default:
		return nil, fmt.Errorf("尚未支持的 Codex 交互：%s；未授予权限", method)
	}
	if len(params) > 256*1024 || !json.Valid(params) {
		return nil, errors.New("Codex 审批请求过大或格式无效")
	}
	var envelope struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		ItemID   string `json:"itemId"`
	}
	if json.Unmarshal(params, &envelope) != nil || envelope.ThreadID == "" || envelope.TurnID == "" || envelope.ItemID == "" {
		return nil, errors.New("Codex 审批请求缺少会话、轮次或操作标识")
	}
	p := &codexPending{CodexPendingRequest: CodexPendingRequest{ID: uid(), TaskID: taskID, RunID: runID, Method: method, Params: append(json.RawMessage(nil), params...), Created: now()}, ctx: ctx, answer: make(chan json.RawMessage, 1)}
	c := a.codexRequests
	c.mu.Lock()
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	if len(c.pending) >= 64 {
		c.mu.Unlock()
		return nil, errors.New("待处理的 Codex 请求过多")
	}
	c.pending[p.ID] = p
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, p.ID)
		c.mu.Unlock()
		a.changed()
	}()
	_ = a.store.event(taskID, runID, "progress", "Codex 等待网页确认 · "+method+" · "+p.ID)
	a.changed()
	select {
	case answer := <-p.answer:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return answer, nil
	case <-ctx.Done():
		_ = a.store.event(taskID, runID, "progress", "Codex 请求已失效 · "+p.ID)
		return nil, ctx.Err()
	}
}

type CodexAnswer struct {
	Decision string                         `json:"decision"`
	Answers  map[string]CodexQuestionAnswer `json:"answers,omitempty"`
}
type CodexQuestionAnswer struct {
	Answers []string `json:"answers"`
}

var errCodexRequestExpired = errors.New("此请求已处理或已失效，请刷新任务")

// Never forward browser-provided permissions, request IDs, policy amendments,
// or session scopes. A one-time approval grants at most the native request.
func codexAnswerPayload(p CodexPendingRequest, answer CodexAnswer) (json.RawMessage, error) {
	var result any
	switch p.Method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		if len(answer.Answers) != 0 || (answer.Decision != "accept" && answer.Decision != "decline" && answer.Decision != "cancel") {
			return nil, errors.New("审批决定无效")
		}
		var options struct {
			AvailableDecisions []json.RawMessage `json:"availableDecisions"`
		}
		_ = json.Unmarshal(p.Params, &options)
		if len(options.AvailableDecisions) > 0 {
			found := false
			for _, raw := range options.AvailableDecisions {
				var value string
				if json.Unmarshal(raw, &value) == nil && value == answer.Decision {
					found = true
				}
			}
			if !found {
				return nil, errors.New("此操作不支持所选审批决定")
			}
		}
		result = map[string]string{"decision": answer.Decision}
	case "item/permissions/requestApproval":
		if len(answer.Answers) != 0 || (answer.Decision != "accept" && answer.Decision != "decline") {
			return nil, errors.New("权限决定无效")
		}
		permissions := map[string]json.RawMessage{}
		if answer.Decision == "accept" {
			var request struct {
				Permissions map[string]json.RawMessage `json:"permissions"`
			}
			if json.Unmarshal(p.Params, &request) != nil || request.Permissions == nil {
				return nil, errors.New("原始权限请求无效")
			}
			for _, key := range []string{"network", "fileSystem"} {
				if raw, ok := request.Permissions[key]; ok && string(raw) != "null" {
					permissions[key] = raw
				}
			}
		}
		result = map[string]any{"permissions": permissions, "scope": "turn"}
	case "item/tool/requestUserInput":
		if answer.Decision != "" {
			return nil, errors.New("请回答问题，或停止当前执行")
		}
		var request struct {
			Questions []struct {
				ID string `json:"id"`
			} `json:"questions"`
		}
		if json.Unmarshal(p.Params, &request) != nil || len(request.Questions) == 0 || len(request.Questions) > 20 || len(answer.Answers) != len(request.Questions) {
			return nil, errors.New("请回答本次请求中的所有问题")
		}
		seen := map[string]bool{}
		for _, q := range request.Questions {
			if q.ID == "" || seen[q.ID] {
				return nil, errors.New("原始问题标识无效")
			}
			seen[q.ID] = true
			value, ok := answer.Answers[q.ID]
			if !ok || len(value.Answers) == 0 || len(value.Answers) > 10 {
				return nil, errors.New("问题回答无效")
			}
			for _, text := range value.Answers {
				if strings.TrimSpace(text) == "" || len(text) > 16000 {
					return nil, errors.New("回答不能为空或过长")
				}
			}
		}
		result = map[string]any{"answers": answer.Answers}
	default:
		return nil, errors.New("不支持的审批类型")
	}
	return json.Marshal(result)
}

func (a *App) answerCodexInteraction(taskID, id string, answer CodexAnswer) error {
	c := a.codexRequests
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.pending[id]
	if p == nil || p.TaskID != taskID || p.ctx.Err() != nil {
		return errCodexRequestExpired
	}
	response, err := codexAnswerPayload(p.CodexPendingRequest, answer)
	if err != nil {
		return err
	}
	// Persist the decision before releasing execution; never store secret answers.
	label := answer.Decision
	if p.Method == "item/tool/requestUserInput" {
		label = "已回答"
	}
	if err = a.store.event(taskID, p.RunID, "progress", "网页处理 Codex 请求 · "+p.ID+" · "+label); err != nil {
		return err
	}
	if p.ctx.Err() != nil {
		return errCodexRequestExpired
	}
	delete(c.pending, id)
	p.answer <- response
	a.changed()
	return nil
}

func (s *Server) codexApprovalRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/tasks/{id}/approvals/{request}", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var answer CodexAnswer
		if !body(w, r, &answer) {
			return
		}
		err := s.app.answerCodexInteraction(r.PathValue("id"), r.PathValue("request"), answer)
		if errors.Is(err, errCodexRequestExpired) {
			fail(w, 409, err.Error())
			return
		}
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}))
}
