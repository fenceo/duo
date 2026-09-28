package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Run the installed Harness against a loopback-only synthetic provider in an
// empty DSH_HOME/workspace. No user credentials, project data or paid API calls.
func TestHarnessACPNativeLocalProvider(t *testing.T) {
	if os.Getenv("DUO_HARNESS_NATIVE_FIXTURE") != "1" {
		t.Skip("opt-in installed Harness with local synthetic provider")
	}
	closeHarnessRuntimes()
	var mu sync.Mutex
	var models []string
	cancelReached := make(chan struct{}, 1)
	const marker = "DUO_NATIVE_CONTEXT_7124"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "unexpected endpoint", 404)
			return
		}
		data, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		var request struct {
			Model    string          `json:"model"`
			Messages json.RawMessage `json:"messages"`
		}
		if json.Unmarshal(data, &request) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		mu.Lock()
		models = append(models, request.Model)
		mu.Unlock()
		var messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		_ = json.Unmarshal(request.Messages, &messages)
		if len(messages) > 0 && strings.Contains(string(messages[len(messages)-1].Content), "native cancellation probe") {
			cancelReached <- struct{}{}
			<-r.Context().Done()
			return
		}
		answer := "missing prior context"
		if strings.Contains(string(request.Messages), marker) {
			answer = "retained " + marker
		}
		w.Header().Set("Content-Type", "text/event-stream")
		payload, _ := json.Marshal(map[string]any{"id": "fixture", "object": "chat.completion.chunk", "created": 1, "model": request.Model, "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"role": "assistant", "content": answer}, "finish_reason": nil}}})
		fmt.Fprintf(w, "data: %s\n\n", payload)
		fmt.Fprint(w, "data: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"created\":1,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":5,\"total_tokens\":25}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	base := t.TempDir()
	home, workspace := filepath.Join(base, "home"), filepath.Join(base, "workspace")
	for _, path := range []string{home, workspace} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	settings := map[string]any{"llm-pi-ai": map[string]any{"providers": map[string]any{"duo-fixture": map[string]any{"api": "openai-completions", "apiKeyEnv": "DUO_FIXTURE_KEY", "baseURL": server.URL + "/v1", "models": []any{map[string]any{"id": "fixture-a", "name": "Fixture A"}, map[string]any{"id": "fixture-b", "name": "Fixture B"}}}}}}
	data, _ := json.Marshal(settings)
	if err := os.WriteFile(filepath.Join(home, "settings.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	c := Config{Harness: "dsh.cmd", HarnessProvider: "duo-fixture", HarnessModel: "fixture-a", EngineEnv: map[string]string{"DSH_HOME": home, "DUO_FIXTURE_KEY": "synthetic-no-real-key", "DSH_TELEMETRY_MODE": "DISABLED", "DSH_TELEMETRY_DISABLED": "1"}}
	task := Task{ID: "native-local-provider", Engine: "deepseek-harness", Workspace: workspace, Model: "fixture-a", Mode: &WorkMode{Permission: "read", Approval: "never", AllowNetwork: boolPtr(true)}}
	t.Cleanup(closeHarnessRuntimes)
	// A pre-upgrade SDK conversation must be adopted through native persistence.
	task.Session = createLegacyHarnessFixtureSession(t, c, task, marker)
	setupCtx, setupCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer setupCancel()
	initial, err := startHarness(setupCtx, c, task, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	initialSession, err := initial.prepareSession(setupCtx, c, task, func(string, string) {})
	if err != nil {
		initial.mu.Lock()
		diagnostic := initial.diagnostic
		initial.mu.Unlock()
		_ = initial.close()
		t.Fatalf("isolated native session creation: %v; diagnostic: %s", err, diagnostic)
	}
	if err := initial.close(); err != nil {
		t.Fatal(err)
	}
	task.Session = initialSession
	run := func(prompt string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		session, result, err := runHarnessACP(ctx, c, task, prompt, func(kind, text string) {
			if kind == "tool" {
				t.Errorf("unexpected tool from synthetic provider")
				cancel()
			}
		})
		if err != nil {
			t.Fatalf("isolated native local-provider turn: %v", err)
		}
		if !strings.Contains(result, marker) {
			t.Fatalf("native history was not restored: %q", result)
		}
		if task.Session != "" && session != task.Session {
			t.Fatal("native session changed")
		}
		task.Session = session
	}
	run("Reply with the marker from the previous SDK conversation.")
	task.Model = "fixture-b"
	run("Reply with the previous marker.")
	if err := closeIdleHarnessSession(task.Session); err != nil {
		t.Fatal(err)
	}
	run("After idle recycle, reply with the previous marker.")
	closeHarnessRuntimes()
	run("After service restart, reply with the previous marker.")
	cancelCtx, cancelPrompt := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelPrompt()
	go func() {
		select {
		case <-cancelReached:
			cancelPrompt()
		case <-cancelCtx.Done():
		}
	}()
	stoppedSession, _, stopErr := runHarnessACP(cancelCtx, c, task, "native cancellation probe", func(string, string) {})
	if stopErr == nil || stoppedSession != task.Session {
		t.Fatalf("native cancellation lost session: %v", stopErr)
	}
	run("After stopping the previous turn, reply with the original marker.")
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(models, ",") != "fixture-a,fixture-a,fixture-b,fixture-b,fixture-b,fixture-b,fixture-b" {
		t.Fatalf("incorrect native routes: %v", models)
	}
	t.Log("native SDK-to-ACP migration, model switch, idle recycle, restart and cancellation retained persisted history; 7 loopback requests, zero paid calls")
}

// This compatibility setup intentionally uses the old wire protocol. All later
// turns use production ACP code and must retain this SDK-created native ID.
func createLegacyHarnessFixtureSession(t *testing.T, c Config, task Task, marker string) string {
	t.Helper()
	args, err := harnessExecutable(c.Harness)
	if err != nil {
		t.Fatal(err)
	}
	patch, err := harnessPolicy(task)
	if err != nil {
		t.Fatal(err)
	}
	patchPath := filepath.Join(t.TempDir(), "legacy-policy.json")
	if err := os.WriteFile(patchPath, patch, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(args[0], append(args[1:], "--profile", "sdk", "--patch", patchPath)...)
	cmd.Dir = task.Workspace
	hideCommand(cmd)
	applyEngineEnv(cmd, c.EngineEnv)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	w := &harnessWorker{c: c, cmd: cmd, in: in, frames: make(chan codexRPC, 128), fault: make(chan error, 1), done: make(chan struct{}), halt: make(chan struct{})}
	defer w.stop()
	var readers sync.WaitGroup
	readers.Add(2)
	go func() { defer readers.Done(); w.scan(out, true) }()
	go func() { defer readers.Done(); w.scan(stderr, false) }()
	go func() {
		readers.Wait()
		err := cmd.Wait()
		w.mu.Lock()
		w.exitErr = err
		w.mu.Unlock()
		close(w.done)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := w.request(ctx, "initialize", map[string]any{"cwd": task.Workspace, "provider": c.HarnessProvider, "model": task.Model}); err != nil {
		t.Fatal(err)
	}
	session := "jianzuo-" + uid()
	if _, err := w.send(ctx, "session/prompt", map[string]any{"sessionId": session, "contentBlocks": []any{map[string]string{"type": "text", "text": "Remember " + marker}}}); err != nil {
		t.Fatal(err)
	}
	answered := false
loop:
	for {
		select {
		case <-ctx.Done():
			t.Fatal("legacy SDK fixture timed out")
		case frame, ok := <-w.frames:
			if !ok {
				t.Fatal(w.failure())
			}
			if frame.Error != nil {
				t.Fatal(frame.Error.Message)
			}
			var p struct {
				SessionID string `json:"sessionId"`
				Status    string `json:"status"`
				Event     struct {
					Type string          `json:"type"`
					Data json.RawMessage `json:"data"`
				} `json:"event"`
			}
			_ = json.Unmarshal(frame.Params, &p)
			if p.SessionID != session {
				continue
			}
			if p.Event.Type == "assistant/message" && strings.Contains(string(p.Event.Data), marker) {
				answered = true
			}
			if frame.Method == "session.status" && p.Status == "idle" && answered {
				break loop
			}
		}
	}
	if _, err := w.request(ctx, "shutdown", nil); err != nil {
		t.Fatal(err)
	}
	if err := w.close(); err != nil {
		t.Fatal(err)
	}
	return session
}
