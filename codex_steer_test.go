package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCodexSteerNativeProtocol(t *testing.T) {
	for _, scenario := range []string{"steer", "steer-reject", "steer-wrong-turn", "steer-no-ack"} {
		t.Run(scenario, func(t *testing.T) {
			config := codexFixtureConfig(t, scenario)
			var records []string
			control := newCodexTurnControl(func(text string) error { records = append(records, text); return nil })
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			ctx = context.WithValue(ctx, codexSteerKey{}, control)
			finished := make(chan error, 1)
			go func() {
				_, _, err := runCodexAppServer(ctx, config, Task{Engine: "codex", Workspace: t.TempDir()}, "fixture initial", func(string, string) {})
				finished <- err
			}()
			waitUntil(t, func() bool { ready, _ := control.state(); return ready })
			r, err := control.begin("fixture-once", "先检查失败测试，不要重构")
			if err != nil {
				t.Fatal(err)
			}
			duplicate, err := control.begin("fixture-once", r.Text)
			if err != nil || duplicate != r {
				t.Fatal("HTTP retry did not reuse original request", err)
			}
			select {
			case <-r.Done:
			case <-time.After(5 * time.Second):
				t.Fatal("steer not resolved")
			}
			if (r.Err == nil) != (scenario == "steer") {
				t.Fatal(scenario, r.Err)
			}
			if err := <-finished; err != nil {
				t.Fatal("steer rejection must not fail the original turn", err)
			}
			if len(records) != map[bool]int{true: 1, false: 0}[scenario == "steer"] {
				t.Fatal("only native acknowledgement can record a delivered user message", records)
			}
			if _, err := control.begin("late", "late"); !errors.Is(err, errLiveTurnChanged) {
				t.Fatal(err)
			}
		})
	}
}

func TestCodexSteerHTTPGuardsAndIdempotency(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	control := newCodexTurnControl(nil)
	control.setReady()
	a.mu.Lock()
	a.workers[task.ID] = &worker{runID: "active-run", runKind: "chat", steer: control}
	a.mu.Unlock()
	t.Cleanup(func() { a.mu.Lock(); delete(a.workers, task.ID); a.mu.Unlock(); control.close() })
	request := toolsClient(t, a)
	request("/api/tasks/"+task.ID+"/steer", "POST", map[string]any{"content": "safe", "expected_run_id": "old", "request_id": "a"}, 409)
	request("/api/tasks/"+task.ID+"/steer", "POST", map[string]any{"content": " ", "expected_run_id": "active-run", "request_id": "a"}, 400)
	served := make(chan struct{})
	go func() { r := <-control.requests; control.resolve(r, nil, ""); close(served) }()
	body := map[string]any{"content": "safe", "expected_run_id": "active-run", "request_id": "once"}
	for i := 0; i < 2; i++ {
		request("/api/tasks/"+task.ID+"/steer", "POST", body, 200)
	}
	<-served
	select {
	case <-control.requests:
		t.Fatal("duplicate native input")
	default:
	}
	body["content"] = "changed"
	request("/api/tasks/"+task.ID+"/steer", "POST", body, 400)
}

type codexSteerBridgeRunner struct {
	config    Config
	workspace string
}

func (f codexSteerBridgeRunner) Run(ctx context.Context, _ Config, task Task, input string, emit func(string, string)) (string, string, error) {
	task.Workspace = f.workspace
	return runCodexAppServer(ctx, f.config, task, input, emit)
}

func TestCodexSteerAppBridgeRecordsAcknowledgedInput(t *testing.T) {
	a := fixture(t, codexSteerBridgeRunner{config: codexFixtureConfig(t, "steer"), workspace: t.TempDir()})
	task := taskFor(t, a)
	first, err := a.submit(task.ID, "first", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { state := a.liveInteraction(task.ID); return state != nil && state.CanSteer })
	request := toolsClient(t, a)
	request("/api/tasks/"+task.ID+"/steer", "POST", map[string]any{"content": "先检查失败测试，不要重构", "expected_run_id": first.ID, "request_id": "bridge-once"}, 200)
	waitUntil(t, func() bool { runs, _ := a.store.runs(task.ID); return len(runs) == 1 && runs[0].Status == "done" })
	events, err := a.store.events(task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Kind == "user" && event.Text == "先检查失败测试，不要重构" {
			count++
			if event.RunID != first.ID {
				t.Fatal("steer recorded in a different run")
			}
		}
	}
	if count != 1 {
		t.Fatal("acknowledged steer must be stored once", count)
	}
}

type interruptFollowupRunner struct {
	mu        sync.Mutex
	inputs    []string
	sessions  []string
	stopped   chan struct{}
	release   chan struct{}
	stopError error
}

func (f *interruptFollowupRunner) Run(ctx context.Context, _ Config, task Task, input string, emit func(string, string)) (string, string, error) {
	f.mu.Lock()
	index := len(f.inputs)
	f.inputs = append(f.inputs, input)
	f.sessions = append(f.sessions, task.Session)
	f.mu.Unlock()
	if index == 0 {
		<-ctx.Done()
		close(f.stopped)
		<-f.release
		if f.stopError != nil {
			return "same-native-session", "", f.stopError
		}
		return "same-native-session", "", ctx.Err()
	}
	emit("assistant", "fixture complete")
	return "same-native-session", "fixture complete", nil
}

func TestInterruptFollowupWaitsForShutdownAndPreservesQueue(t *testing.T) {
	f := &interruptFollowupRunner{stopped: make(chan struct{}), release: make(chan struct{})}
	a := fixture(t, f)
	task := taskFor(t, a)
	first, err := a.submit(task.ID, "first", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.inputs) == 1 })
	a.submit(task.ID, "queued earlier", "chat", "web")
	options := SubmitOptions{Delivery: "interrupt", ExpectedRunID: first.ID}
	options.ExpectedRunID = "stale"
	if _, err = a.submitWithOptions(task.ID, "wrong", "chat", "web", options); !errors.Is(err, errLiveTurnChanged) {
		t.Fatal(err)
	}
	options.ExpectedRunID = first.ID
	if _, err = a.submitWithOptions(task.ID, "priority correction", "chat", "web", options); err != nil {
		t.Fatal(err)
	}
	<-f.stopped
	if _, err = a.submitWithOptions(task.ID, "duplicate", "chat", "web", options); !errors.Is(err, errLiveTurnChanged) {
		t.Fatal(err)
	}
	f.mu.Lock()
	count := len(f.inputs)
	f.mu.Unlock()
	if count != 1 {
		t.Fatal("follow-up started before old process exited")
	}
	close(f.release)
	waitUntil(t, func() bool {
		runs, _ := a.store.runs(task.ID)
		return len(runs) == 3 && runs[1].Status == "done" && runs[2].Status == "done"
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.inputs) != 3 || f.inputs[1] != "priority correction" || f.inputs[2] != "queued earlier" {
		t.Fatal(f.inputs)
	}
	if f.sessions[1] != "same-native-session" {
		t.Fatal("interrupted native session not resumed", f.sessions)
	}
}

func TestInterruptFollowupDoesNotRunAfterUnconfirmedShutdown(t *testing.T) {
	f := &interruptFollowupRunner{stopped: make(chan struct{}), release: make(chan struct{}), stopError: errors.New("unconfirmed process cleanup")}
	a := fixture(t, f)
	task := taskFor(t, a)
	first, _ := a.submit(task.ID, "first", "chat", "web")
	waitUntil(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.inputs) == 1 })
	if _, err := a.submitWithOptions(task.ID, "must not start", "chat", "web", SubmitOptions{Delivery: "interrupt", ExpectedRunID: first.ID}); err != nil {
		t.Fatal(err)
	}
	<-f.stopped
	close(f.release)
	waitUntil(t, func() bool {
		runs, _ := a.store.runs(task.ID)
		return len(runs) == 2 && runs[1].Status == "interrupted" && strings.Contains(runs[1].Error, "未能确认")
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.inputs) != 1 {
		t.Fatal(f.inputs)
	}
}
