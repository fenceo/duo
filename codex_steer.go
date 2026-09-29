package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

var errLiveTurnChanged = errors.New("当前执行已变化或正在停止，请刷新后重试；内容未自动排队")

type codexSteerKey struct{}

type codexSteerRequest struct {
	ID      string
	Text    string
	Done    chan struct{}
	Err     error
	Warning string
}

// One run owns this channel. Only the app-server event loop writes the native
// connection. Request IDs let an HTTP retry observe its original result.
type codexTurnControl struct {
	mu       sync.Mutex
	ready    bool
	closed   bool
	active   *codexSteerRequest
	requests chan *codexSteerRequest
	attempts map[string]*codexSteerRequest
	record   func(string) error
}

func newCodexTurnControl(record func(string) error) *codexTurnControl {
	return &codexTurnControl{requests: make(chan *codexSteerRequest, 1), attempts: map[string]*codexSteerRequest{}, record: record}
}

func (c *codexTurnControl) setReady() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.ready = true
	}
}

func (c *codexTurnControl) state() (bool, bool) {
	if c == nil {
		return false, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ready && !c.closed && c.active == nil, c.active != nil
}

func (c *codexTurnControl) begin(id, text string) (*codexSteerRequest, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if old := c.attempts[id]; old != nil {
		if old.Text != text {
			return nil, errors.New("同一发送标识不能用于不同内容")
		}
		return old, nil
	}
	if !c.ready || c.closed || c.active != nil {
		return nil, errLiveTurnChanged
	}
	if len(c.attempts) >= 128 {
		return nil, errors.New("本轮引导次数已达上限，请使用排队发送")
	}
	r := &codexSteerRequest{ID: id, Text: text, Done: make(chan struct{})}
	c.attempts[id], c.active = r, r
	c.requests <- r
	return r, nil
}

func (c *codexTurnControl) resolve(r *codexSteerRequest, err error, warning string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-r.Done:
		return
	default:
	}
	r.Err, r.Warning = err, warning
	if c.active == r {
		c.active = nil
	}
	close(r.Done)
}

func (c *codexTurnControl) close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.closed = true
	active := c.active
	c.mu.Unlock()
	if active != nil {
		c.resolve(active, errors.New("执行连接已结束，未能确认引导结果；请检查对话记录，不要直接重复发送"), "")
	}
}

type LiveInteraction struct {
	RunID        string `json:"run_id"`
	CanSteer     bool   `json:"can_steer"`
	CanInterrupt bool   `json:"can_interrupt"`
	Steering     bool   `json:"steering"`
	Interrupting bool   `json:"interrupting"`
}

func (a *App) liveInteraction(id string) *LiveInteraction {
	a.mu.Lock()
	defer a.mu.Unlock()
	w := a.workers[id]
	if w == nil || w.runID == "" || w.runKind != "chat" {
		return nil
	}
	ready, steering := w.steer.state()
	return &LiveInteraction{RunID: w.runID, CanSteer: ready && !w.stopping && !w.interrupting,
		CanInterrupt: w.runCancel != nil && !w.stopping && !w.interrupting,
		Steering:     steering, Interrupting: w.interrupting || w.stopping}
}

func (a *App) steer(ctx context.Context, id, runID, requestID, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 200000 {
		return "", errors.New("引导内容不能为空，且不能超过 200 KB")
	}
	if requestID == "" || len(requestID) > 80 || strings.ContainsAny(requestID, "\r\n\x00") {
		return "", errors.New("发送标识无效")
	}
	a.mu.Lock()
	w := a.workers[id]
	if a.updating.Load() || a.ctx.Err() != nil || w == nil || w.stopping || w.interrupting || w.runID != runID || w.runKind != "chat" || w.steer == nil {
		a.mu.Unlock()
		return "", errLiveTurnChanged
	}
	r, err := w.steer.begin(requestID, text)
	a.mu.Unlock()
	if err != nil {
		return "", err
	}
	a.changed()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case <-r.Done:
		a.changed()
		return r.Warning, r.Err
	case <-timer.C:
		return "", fmt.Errorf("Codex 仍未确认引导结果，内容已保留；请查看记录后重试同一内容")
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (s *Server) codexSteerRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/tasks/{id}/steer", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Content   string `json:"content"`
			RunID     string `json:"expected_run_id"`
			RequestID string `json:"request_id"`
		}
		if !body(w, r, &v) {
			return
		}
		warning, err := s.app.steer(r.Context(), r.PathValue("id"), v.RunID, v.RequestID, v.Content)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, errLiveTurnChanged) {
				status = http.StatusConflict
			}
			fail(w, status, err.Error())
			return
		}
		jsonOut(w, 200, map[string]any{"accepted": true, "warning": warning})
	}))
}
