package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

type modelProbeOptions struct {
	mode    string
	seconds int
}

func normalizeModelProbeOptions(r ModelProbeRequest) (modelProbeOptions, error) {
	if r.Mode == "" || r.Mode == "availability" {
		if r.TimeoutSeconds != nil {
			return modelProbeOptions{}, errors.New("可用性测试固定为 25 秒；自定义超时仅用于首次应答延时测试")
		}
		return modelProbeOptions{}, nil
	}
	if r.Mode != "latency" {
		return modelProbeOptions{}, errors.New("模型测试模式无效")
	}
	if len(r.Models) != 1 || strings.TrimSpace(r.Models[0]) == "" {
		return modelProbeOptions{}, errors.New("首次应答延时测试每次只能选择一个明确的模型")
	}
	if r.Engine == "mimo" {
		return modelProbeOptions{}, errors.New("MiMo API 当前使用非流式测试，无法观测首段回复；请使用可用性测试")
	}
	seconds := 180
	if r.TimeoutSeconds != nil {
		seconds = *r.TimeoutSeconds
	}
	if seconds < 60 || seconds > 600 {
		return modelProbeOptions{}, errors.New("首次应答延时测试的超时须为 60 到 600 秒")
	}
	return modelProbeOptions{mode: "latency", seconds: seconds}, nil
}

func (o modelProbeOptions) timeout() time.Duration {
	if o.mode == "latency" {
		return time.Duration(o.seconds) * time.Second
	}
	return modelProbeTimeout
}

func (o modelProbeOptions) publicTimeout() int {
	if o.mode == "latency" {
		return o.seconds
	}
	return 0
}

type modelProbeOptionsKey struct{}
type modelProbeTextKey struct{}
type modelProbeFirstKey struct{}

func modelProbeOptionsFromContext(ctx context.Context) modelProbeOptions {
	o, _ := ctx.Value(modelProbeOptionsKey{}).(modelProbeOptions)
	return o
}

func modelProbeTextObserver(ctx context.Context) func(string, string) {
	f, _ := ctx.Value(modelProbeTextKey{}).(func(string, string))
	return f
}

func modelProbeFirstCallback(ctx context.Context) func(int64, string) {
	f, _ := ctx.Value(modelProbeFirstKey{}).(func(int64, string))
	return f
}

// Only an explicitly selected latency probe extends startup waits. Normal
// conversations and availability tests retain every existing stage limit.
func modelProbeStageTimeout(ctx context.Context, normal time.Duration) time.Duration {
	if o := modelProbeOptionsFromContext(ctx); o.mode == "latency" {
		return o.timeout()
	}
	return normal
}

func codexModelProbeObserver(ctx context.Context) func(string, json.RawMessage, string, string) {
	observe := modelProbeTextObserver(ctx)
	if observe == nil {
		return nil
	}
	excluded := map[string]bool{}
	return func(method string, raw json.RawMessage, session, turn string) {
		if method != "item/started" && method != "item/completed" && method != "item/agentMessage/delta" {
			return
		}
		var p struct {
			ThreadID string                                           `json:"threadId"`
			TurnID   string                                           `json:"turnId"`
			ItemID   string                                           `json:"itemId"`
			Delta    string                                           `json:"delta"`
			Item     struct{ ID, Type, Phase, Delivery, Text string } `json:"item"`
		}
		if json.Unmarshal(raw, &p) != nil || session == "" || turn == "" || p.ThreadID != session || p.TurnID != turn {
			return
		}
		if method == "item/agentMessage/delta" {
			if p.ItemID != "" && !excluded[p.ItemID] {
				observe(p.Delta, "stream_text")
			}
			return
		}
		if p.Item.Type != "agentMessage" {
			return
		}
		skip := p.Item.Phase == "commentary" || p.Item.Delivery == "async"
		if method == "item/started" {
			if skip {
				excluded[p.Item.ID] = true
			}
			return
		}
		if !skip && !excluded[p.Item.ID] {
			observe(p.Item.Text, "completed_text")
		}
		delete(excluded, p.Item.ID)
	}
}

type modelProbeTiming struct {
	mu      sync.Mutex
	started time.Time
	first   *int64
	source  string
	notify  func(int64, string)
}

func newModelProbeTiming(started time.Time, notify func(int64, string)) *modelProbeTiming {
	return &modelProbeTiming{started: started, notify: notify}
}

// Text stays inside the process. Public events carry only elapsed time and
// provenance, and a failure after this observation cannot erase the timing.
func (t *modelProbeTiming) observe(text, source string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	t.mu.Lock()
	if t.first != nil {
		t.mu.Unlock()
		return
	}
	elapsed := max(int64(0), time.Since(t.started).Milliseconds())
	t.first, t.source = &elapsed, source
	notify := t.notify
	t.mu.Unlock()
	if notify != nil {
		notify(elapsed, source)
	}
}

func (t *modelProbeTiming) snapshot() (*int64, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.first == nil {
		return nil, ""
	}
	value := *t.first
	return &value, t.source
}

// Only this goroutine writes public stream events. A disconnected client
// cancels the probe, then waits for the runner's existing cleanup before the
// shared model admission lock can be released.
func invokeModelProbe(parent context.Context, plan modelProbePlan, model string, run func(context.Context, Config, string, string, string) ModelProbeResult, emit func(ModelProbeEvent) error) (ModelProbeResult, bool) {
	if plan.options.mode != "latency" {
		return run(parent, plan.config, plan.engine, plan.workspace, model), true
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	ctx = context.WithValue(ctx, modelProbeOptionsKey{}, plan.options)
	first := make(chan ModelProbeEvent, 1)
	ctx = context.WithValue(ctx, modelProbeFirstKey{}, func(ms int64, source string) {
		select {
		case first <- ModelProbeEvent{Type: "model_response", Model: model, FirstResponseMS: &ms, TimingSource: source, TimeoutSeconds: plan.options.seconds}:
		default:
		}
	})
	done := make(chan ModelProbeResult, 1)
	go func() { done <- run(ctx, plan.config, plan.engine, plan.workspace, model) }()
	started := time.Now()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	connected := true
	ctxDone := ctx.Done()
	send := func(event ModelProbeEvent) {
		if connected && emit != nil && emit(event) != nil {
			connected = false
			cancel()
		}
	}
	for {
		select {
		case <-ctxDone:
			connected = false
			ctxDone = nil
		case event := <-first:
			send(event)
		case <-ticker.C:
			send(ModelProbeEvent{Type: "heartbeat", Model: model, ElapsedMS: time.Since(started).Milliseconds(), TimeoutSeconds: plan.options.seconds})
		case result := <-done:
			// Completion may race with the buffered first-text observation.
			select {
			case event := <-first:
				send(event)
			default:
			}
			return result, connected
		}
	}
}
