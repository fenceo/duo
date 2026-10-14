package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This executable is a synthetic ACP peer, not an installed CLI or a model.
func TestMimoProcessFixture(t *testing.T) {
	if os.Getenv("DUO_TEST_MIMO_ACP") != "1" {
		t.Skip("synthetic child only")
	}
	for _, arg := range os.Args {
		if arg == "export" {
			data, err := os.ReadFile("mimo-fixture-export.json")
			if err != nil {
				os.Exit(2)
			}
			os.Stdout.Write(data)
			os.Exit(0)
		}
	}
	var session, model, mode string
	var history []string
	path := "mimo-fixture-history.json"
	reply := func(id json.RawMessage, value any) {
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": value})
		os.Stdout.Write(append(raw, '\n'))
	}
	notify := func(update any) {
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": session, "update": update}})
		os.Stdout.Write(append(raw, '\n'))
	}
	finish := func(id json.RawMessage) {
		value, _ := json.Marshal(map[string]any{"session": session, "model": model, "mode": mode, "history": history, "pid": os.Getpid(), "permission": os.Getenv("MIMOCODE_PERMISSION")})
		provider, modelID, _ := strings.Cut(model, "/")
		input := history[len(history)-1]
		if len(history) > 1 && strings.HasPrefix(input, "decision:") {
			input = history[len(history)-2]
		}
		doc, _ := json.Marshal(map[string]any{"info": map[string]string{"id": session}, "messages": []any{
			map[string]any{"info": map[string]string{"id": "user-current", "role": "user"}, "parts": []any{map[string]string{"type": "text", "text": input}}},
			map[string]any{"info": map[string]string{"id": "assistant-current", "parentID": "user-current", "role": "assistant", "providerID": provider, "modelID": modelID}, "parts": []any{map[string]string{"type": "text", "text": string(value)}}},
		}})
		_ = os.WriteFile("mimo-fixture-export.json", doc, 0600)
		notify(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": string(value)}})
		reply(id, map[string]string{"stopReason": "end_turn"})
	}
	var pending json.RawMessage
	reader := bufio.NewScanner(os.Stdin)
	reader.Buffer(make([]byte, 4096), 2*1024*1024)
	for reader.Scan() {
		var rpc codexRPC
		if json.Unmarshal(reader.Bytes(), &rpc) != nil {
			os.Exit(3)
		}
		var p struct {
			SessionID string
			ConfigID  string
			Value     string
			ModeID    string
			Prompt    []struct{ Text string }
		}
		_ = json.Unmarshal(rpc.Params, &p)
		switch rpc.Method {
		case "initialize":
			reply(rpc.ID, map[string]any{"protocolVersion": 1, "agentInfo": map[string]string{"name": "OpenCode"}, "agentCapabilities": map[string]any{"loadSession": true, "promptCapabilities": map[string]bool{"image": true}}})
		case "session/new":
			session = "fixture-mimo-session"
			history = nil
			reply(rpc.ID, map[string]string{"sessionId": session})
		case "session/load":
			session = p.SessionID
			data, err := os.ReadFile(path)
			if err != nil || json.Unmarshal(data, &history) != nil {
				raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "error": map[string]any{"code": -32602, "message": "session missing"}})
				os.Stdout.Write(append(raw, '\n'))
			} else {
				reply(rpc.ID, map[string]any{})
			}
		case "session/set_config_option":
			model = p.Value
			reply(rpc.ID, map[string]any{})
		case "session/set_mode":
			mode = p.ModeID
			reply(rpc.ID, map[string]any{})
		case "session/prompt":
			input := ""
			for _, part := range p.Prompt {
				input += part.Text
			}
			history = append(history, input)
			data, _ := json.Marshal(history)
			_ = os.WriteFile(path, data, 0600)
			if input == "permission" {
				pending = rpc.ID
				notify(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call-1", "title": "synthetic shell", "rawInput": map[string]string{"command": "printf synthetic"}, "status": "pending"})
				raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "permission-1", "method": "session/request_permission", "params": map[string]any{"sessionId": session, "toolCall": map[string]string{"toolCallId": "call-1"}, "options": []any{map[string]string{"optionId": "once", "kind": "allow_once"}, map[string]string{"optionId": "reject", "kind": "reject_once"}}}})
				os.Stdout.Write(append(raw, '\n'))
			} else if input == "wait" {
				pending = rpc.ID
				notify(map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]string{"type": "text", "text": "waiting"}})
			} else {
				finish(rpc.ID)
			}
		case "session/cancel":
			if pending != nil {
				reply(pending, map[string]string{"stopReason": "cancelled"})
				pending = nil
			}
		case "":
			if string(rpc.ID) == `"permission-1"` {
				var value struct {
					Outcome struct{ Outcome, OptionID string }
				}
				_ = json.Unmarshal(rpc.Result, &value)
				history = append(history, "decision:"+value.Outcome.Outcome+":"+value.Outcome.OptionID)
				finish(pending)
				pending = nil
			}
		}
	}
	os.Exit(0)
}

func mimoFixture(t *testing.T) (Config, Task) {
	t.Helper()
	t.Setenv("DUO_TEST_MIMO_ACP", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Config{Mimo: exe}, Task{ID: "mimo-fixture", Engine: "mimo", Workspace: t.TempDir(), Model: "duo-mimo/model-one", Mode: &WorkMode{ID: "work", Permission: "workspace", Approval: "request", AllowNetwork: boolPtr(true)}}
}

func TestMimoNativeACPResumeAndPermissionSwitch(t *testing.T) {
	c, task := mimoFixture(t)
	var pid int
	for i, permission := range []string{"workspace", "full", "read"} {
		task.Mode = &WorkMode{Permission: permission, AllowNetwork: boolPtr(true)}
		task.Model = "duo-mimo/model-" + permission
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		session, result, err := runMimoACP(ctx, c, task, permission, func(string, string) {})
		cancel()
		var got struct {
			Model, Mode, Permission string
			PID                     int
			History                 []string
		}
		if err != nil || json.Unmarshal([]byte(result), &got) != nil || session == "" || (task.Session != "" && session != task.Session) || got.PID == pid || len(got.History) != i+1 || got.Model != task.Model {
			t.Fatalf("native history/model switch failed: %s %v", result, err)
		}
		wantMode := "build"
		if permission == "read" {
			wantMode = "plan"
		}
		if got.Mode != wantMode || (permission == "full" && !strings.Contains(got.Permission, `"*":"allow"`)) {
			t.Fatalf("wrong native policy: %s", result)
		}
		task.Session = session
		pid = got.PID
	}
}

func TestMimoACPApprovalMappingAndCancellation(t *testing.T) {
	c, task := mimoFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	called := false
	ctx = withCodexInteraction(ctx, func(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		called = true
		if method != mimoPermissionMethod || !strings.Contains(string(params), "printf synthetic") {
			t.Errorf("wrong request: %s %s", method, params)
		}
		return harnessAnswerPayload(params, CodexAnswer{Decision: "accept"})
	})
	session, result, err := runMimoACP(ctx, c, task, "permission", func(string, string) {})
	if err != nil || !called || !strings.Contains(result, "decision:selected:once") {
		t.Fatalf("native approval not mapped: %s %v", result, err)
	}
	task.Session = session
	waitCtx, stop := context.WithCancel(context.Background())
	waiting := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		_, _, err := runMimoACP(waitCtx, c, task, "wait", func(kind, value string) {
			if kind == "thought" && value == "waiting" {
				waiting <- struct{}{}
			}
		})
		done <- err
	}()
	select {
	case <-waiting:
	case <-time.After(20 * time.Second):
		stop()
		t.Fatal("prompt not started")
	}
	stop()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled prompt succeeded")
		}
	case <-time.After(12 * time.Second):
		t.Fatal("cancelled MiMo process did not stop")
	}
	if _, _, err = runMimoACP(ctx, c, task, "after cancel", func(string, string) {}); err != nil {
		t.Fatal("stopped session could not resume", err)
	}
}

func TestMimoMissingSessionDoesNotCreateReplacement(t *testing.T) {
	c, task := mimoFixture(t)
	task.Session = "missing-native-id"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	session, _, err := runMimoACP(ctx, c, task, "must not run", func(string, string) {})
	if err == nil || session != task.Session {
		t.Fatal("missing history was silently replaced", err)
	}
	if _, err = os.Stat(filepath.Join(task.Workspace, "mimo-fixture-history.json")); !os.IsNotExist(err) {
		t.Fatal("failed resume sent a prompt")
	}
}

func TestMimoModelParsingAndUnsupportedPromises(t *testing.T) {
	models := parseMimoModels("duo-mimo/model-a — window 1M, compacts at 950K\ninvalid diagnostic text\nduo-mimo/model-a\nrelay/org/model-b — window 128K, budget 100K, compacts at 90K\n")
	if len(models) != 2 || models[1].ID != "relay/org/model-b" {
		t.Fatal(models)
	}
	for _, task := range []Task{{NoTools: true}, {Mode: &WorkMode{Approval: "auto"}}, {Mode: &WorkMode{AllowNetwork: boolPtr(false)}}} {
		if _, _, err := mimoInvocation(task); err == nil {
			t.Fatal("unsupported guarantee accepted")
		}
	}
}

func TestMimoStandalonePermissionRequiresLiveSessionAndDetails(t *testing.T) {
	state := harnessACPTurn{session: "live-session"}
	raw := json.RawMessage(`{"sessionId":"live-session","toolCall":{"toolCallId":"permission-native","title":"external_directory","rawInput":{"filepath":"/synthetic/outside"}},"options":[{"optionId":"once","kind":"allow_once"},{"optionId":"reject","kind":"reject_once"}]}`)
	if _, _, err := state.mimoPermissionParams(raw); err != nil {
		t.Fatal("native standalone permission rejected", err)
	}
	for _, bad := range []string{strings.ReplaceAll(string(raw), "live-session", "another-session"), strings.ReplaceAll(string(raw), `{"filepath":"/synthetic/outside"}`, `{}`)} {
		if _, _, err := state.mimoPermissionParams(json.RawMessage(bad)); err == nil {
			t.Fatal("permission missing live ownership or details accepted")
		}
	}
}

func TestMimoWindowsModelsUseNativeEnvironmentInsteadOfUnixEnv(t *testing.T) {
	cmd := mimoModelsCommand(context.Background(), Environment{Type: "windows", Mimo: "C:/synthetic/mimo.exe", Workspaces: []string{"C:/synthetic/work"}}, map[string]string{"MIMOCODE_CONFIG": "C:/synthetic/profile with spaces/mimocode.json"})
	if len(cmd.Args) != 2 || cmd.Args[1] != "models" || cmd.Dir != "C:/synthetic/work" || !strings.Contains(strings.Join(cmd.Env, "\n"), "MIMOCODE_CONFIG=C:/synthetic/profile with spaces/mimocode.json") {
		t.Fatal("Windows model read lost its native program/environment")
	}
}
