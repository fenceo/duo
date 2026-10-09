package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// This is the current test executable, not Codex. It speaks real newline JSON
// RPC over subprocess pipes without loading credentials or requesting a model.
func runCodexAppServerFixture() {
	decoder, encoder := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	fail := func(message string) {
		fmt.Fprintln(os.Stderr, "fixture: "+message)
		os.Exit(23)
	}
	read := func(method string) codexRPC {
		var value codexRPC
		if err := decoder.Decode(&value); err != nil {
			fail("read " + method + ": " + err.Error())
		}
		if value.Method != method {
			fail("expected " + method + ", got " + value.Method)
		}
		return value
	}
	write := func(value any) {
		if err := encoder.Encode(value); err != nil {
			fail(err.Error())
		}
	}
	reply := func(message codexRPC, result any) { write(map[string]any{"id": message.ID, "result": result}) }
	notify := func(method string, params any) { write(map[string]any{"method": method, "params": params}) }
	initial := read("initialize")
	reply(initial, map[string]any{"userAgent": "jianzuo-fixture"})
	read("initialized")
	var thread codexRPC
	if err := decoder.Decode(&thread); err != nil || (thread.Method != "thread/start" && thread.Method != "thread/resume") {
		fail("thread start/resume missing")
	}
	var threadParams map[string]any
	_ = json.Unmarshal(thread.Params, &threadParams)
	threadID := "native-thread-new-123456789"
	if thread.Method == "thread/resume" {
		threadID, _ = threadParams["threadId"].(string)
		if threadID == "" {
			fail("resume did not use exact ID")
		}
	}
	scenario := os.Getenv("JIANZUO_TEST_CODEX_SCENARIO")
	if scenario == "active-writer" || scenario == "sandbox-setup-failure" {
		if thread.Method != "thread/resume" {
			fail("busy thread must not be replaced with thread/start")
		}
		message := "thread " + threadID + " already has an active writer"
		if scenario == "sandbox-setup-failure" {
			message = "windows sandbox failed: helper_unknown_error: setup refresh had errors"
		}
		write(map[string]any{"id": thread.ID, "error": map[string]any{"code": -32600, "message": message}})
		var next codexRPC
		if err := decoder.Decode(&next); err != io.EOF {
			fail("request sent after busy thread: " + next.Method)
		}
		return
	}
	if scenario == "wrong-thread" {
		threadID = "different-thread"
	}
	if scenario == "oversized-frame" {
		// Stream the oversized frame without allocating it all in the child.
		fmt.Fprint(os.Stdout, `{"id":"jianzuo-thread","result":{"padding":"`)
		chunk := strings.Repeat("x", 64*1024)
		for i := 0; i <= codexMaxProtocolFrameBytes/len(chunk); i++ {
			if _, err := io.WriteString(os.Stdout, chunk); err != nil {
				return
			}
		}
		fmt.Fprintln(os.Stdout, `"}}`)
		var next codexRPC
		if err := decoder.Decode(&next); err != io.EOF {
			fail("unexpected request after oversized frame")
		}
		return
	}
	threadResult := map[string]any{"id": threadID}
	if scenario == "large-history" {
		// An older server may still hydrate the response. It must not abort a
		// resumed task or replay old assistant messages into the new run.
		threadResult["turns"] = []any{map[string]any{"id": "old-turn", "items": []any{map[string]any{
			"id": "old-item", "type": "agentMessage", "text": strings.Repeat("h", 5*1024*1024),
		}}}}
	}
	reply(thread, map[string]any{"thread": threadResult})
	if scenario == "blocked-turn" {
		time.Sleep(15 * time.Second) // Deliberately never read the large turn/start.
		return
	}
	if scenario == "wrong-thread" || scenario == "cancel-before-turn" {
		var next codexRPC
		if err := decoder.Decode(&next); err != io.EOF {
			fail("unexpected request after rejected thread")
		}
		return
	}
	turn := read("turn/start")
	var turnParams map[string]any
	_ = json.Unmarshal(turn.Params, &turnParams)
	turnID := "turn-native-1"
	if scenario == "approval-before-turn-identity" {
		write(map[string]any{"id": 90, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": threadID, "turnId": "unconfirmed-turn"}})
		response := read("")
		if response.Error == nil {
			fail("unconfirmed turn approval was accepted")
		}
		return
	}
	reply(turn, map[string]any{"turn": map[string]any{"id": turnID, "status": "inProgress"}})
	notify("turn/started", map[string]any{"threadId": threadID, "turn": map[string]any{"id": turnID, "status": "inProgress"}})
	item := func(id, phase, text string) {
		notify("item/completed", map[string]any{"threadId": threadID, "turnId": turnID, "item": map[string]any{"id": id, "type": "agentMessage", "phase": phase, "text": text}})
	}
	complete := func(status string) {
		notify("turn/completed", map[string]any{"threadId": threadID, "turn": map[string]any{"id": turnID, "status": status}})
	}
	request := func(id any, method string, target string) {
		write(map[string]any{"id": id, "method": method, "params": map[string]any{"threadId": target, "turnId": turnID, "itemId": "tool-1", "command": "fixture command"}})
	}
	responses := []json.RawMessage{}
	switch scenario {
	case "async-question", "async-question-only":
		params := map[string]any{"threadId": threadID, "turnId": turnID, "item": map[string]any{"id": "async-question-1", "type": "agentMessage", "phase": "final_answer", "delivery": "async", "text": "选择云端或补充需求", "questions": []any{map[string]any{"title": "安装范围？", "options": []string{"云端", "本地"}}, map[string]any{"title": "补充说明", "options": nil}}}}
		notify("item/completed", params)
		notify("item/completed", params)
		if scenario == "async-question-only" {
			complete("completed")
			return
		}
	case "steer", "steer-question", "steer-reject", "steer-wrong-turn", "steer-no-ack":
		steer := read("turn/steer")
		var params struct {
			ThreadID       string `json:"threadId"`
			ExpectedTurnID string `json:"expectedTurnId"`
			Input          []struct {
				Text string `json:"text"`
			} `json:"input"`
		}
		if json.Unmarshal(steer.Params, &params) != nil || params.ThreadID != threadID || params.ExpectedTurnID != turnID || len(params.Input) != 1 {
			fail("steer payload or native identity mismatch")
		}
		if scenario == "steer-question" {
			if !strings.HasPrefix(params.Input[0].Text, "对 AI 提问的回答：") || !strings.Contains(params.Input[0].Text, "不下载本地模型") {
				fail("question answer not steered")
			}
		} else if params.Input[0].Text != "先检查失败测试，不要重构" {
			fail("steer text mismatch")
		}
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(steer.Params, &raw)
		if len(raw) != 3 {
			fail("steering must not override model, permissions or workspace")
		}
		switch scenario {
		case "steer", "steer-question":
			reply(steer, map[string]string{"turnId": turnID})
		case "steer-reject":
			write(map[string]any{"id": steer.ID, "error": map[string]any{"code": -32600, "message": "turn not active"}})
		case "steer-wrong-turn":
			reply(steer, map[string]string{"turnId": "other-turn"})
		}
	case "mcp-startup-error":
		notify("mcpServer/statusUpdated", map[string]any{"threadId": threadID, "name": "optional-fixture", "status": "failed", "error": "fixture MCP unavailable"})
		notify("mcpServer/statusUpdated", map[string]any{"threadId": nil, "name": "ready-fixture", "status": "ready", "error": nil})
	case "large-item":
		notify("item/completed", map[string]any{"threadId": threadID, "turnId": turnID, "item": map[string]any{
			"id": "large-tool", "type": "commandExecution", "command": "large fixture output", "status": "completed",
			"aggregatedOutput": "large-output-begin:" + strings.Repeat("x", 5*1024*1024) + ":large-output-end",
		}})
	case "approval":
		for index, method := range []string{"item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval", "item/tool/requestUserInput", "mcpServer/elicitation/request"} {
			if index == 1 {
				notify("item/started", map[string]any{"threadId": threadID, "turnId": turnID, "item": map[string]any{"id": "tool-1", "type": "fileChange", "changes": []any{map[string]any{"path": "review.txt", "diff": "+review this"}}}})
			}
			var id any = "approval-" + fmt.Sprint(index)
			if index == 0 {
				id = 41
			}
			request(id, method, threadID)
			response := read("")
			encodedID, _ := json.Marshal(id)
			if string(response.ID) != string(encodedID) || response.Error != nil || len(response.Result) == 0 {
				fail("approval response ID/result mismatch")
			}
			responses = append(responses, response.Result)
		}
	case "resolved", "turn-cancels-pending":
		request(42, "item/commandExecution/requestApproval", threadID)
		item("pending", "commentary", "fixture-request-open")
		if scenario == "resolved" {
			notify("serverRequest/resolved", map[string]any{"threadId": threadID, "requestId": 42})
		}
	case "reused-id":
		request(42, "item/commandExecution/requestApproval", threadID)
		item("pending-old", "commentary", "fixture-request-open")
		notify("serverRequest/resolved", map[string]any{"threadId": threadID, "requestId": 42})
		request(42, "item/commandExecution/requestApproval", threadID)
		response := read("")
		var answer struct {
			Decision string `json:"decision"`
		}
		_ = json.Unmarshal(response.Result, &answer)
		if response.Error != nil || answer.Decision != "decline" {
			fail("stale approval was applied to reused request ID")
		}
	case "cancel":
		item("ready", "commentary", "fixture-ready")
		interrupt := read("turn/interrupt")
		var params map[string]string
		_ = json.Unmarshal(interrupt.Params, &params)
		if params["threadId"] != threadID || params["turnId"] != turnID {
			fail("interrupt target mismatch")
		}
		reply(interrupt, map[string]any{})
		notify("item/completed", map[string]any{"threadId": threadID, "turnId": turnID, "item": map[string]any{"type": "commandExecution", "command": "native-interrupt-observed", "status": "completed"}})
		complete("interrupted")
		return
	case "unsupported":
		request("unsupported-1", "newUnsafeRequest", threadID)
		response := read("")
		if response.Error == nil || response.Error.Code != -32601 {
			fail("unsupported request was not denied")
		}
		return
	case "foreign":
		request("foreign-1", "item/commandExecution/requestApproval", "other-thread")
		response := read("")
		if response.Error == nil {
			fail("foreign request was not rejected")
		}
		notify("item/completed", map[string]any{"threadId": threadID, "turnId": "other-turn", "item": map[string]any{"id": "foreign-item", "type": "agentMessage", "text": "foreign output must not appear"}})
		notify("turn/completed", map[string]any{"threadId": threadID, "turn": map[string]any{"id": "other-turn", "status": "failed"}})
	case "disconnect":
		item("partial-final", "final_answer", "not a confirmed completed turn")
		return
	case "failed":
		notify("turn/completed", map[string]any{"threadId": threadID, "turn": map[string]any{"id": turnID, "status": "failed", "error": map[string]any{"message": "fixture denied"}}})
		return
	}
	cwd, _ := os.Getwd()
	result, _ := json.Marshal(map[string]any{"cwd": cwd, "args": os.Args[1:], "initialize": initial.Params, "method": thread.Method, "thread": threadParams, "turn": turnParams, "responses": responses})
	item("analysis", "commentary", "fixture progress")
	notify("item/completed", map[string]any{"threadId": threadID, "turnId": turnID, "item": map[string]any{"id": "cmd", "type": "commandExecution", "command": "echo fixture", "status": "completed", "aggregatedOutput": "tool output"}})
	notify("item/completed", map[string]any{"threadId": threadID, "turnId": turnID, "item": map[string]any{"id": "patch", "type": "fileChange", "changes": []any{map[string]any{"path": "demo.txt", "diff": "+safe"}}}})
	for _, n := range []int64{10, 10, 15} {
		notify("thread/tokenUsage/updated", map[string]any{"threadId": threadID, "turnId": turnID, "tokenUsage": map[string]any{"total": codexTokenUsage{Input: 100 + n, Output: 20 + n, Cached: 30}, "last": codexTokenUsage{Input: n, Output: n, Cached: 2}}})
	}
	item("final", "final_answer", string(result))
	item("final", "final_answer", string(result)) // Repeated notification must not duplicate assistant output.
	complete("completed")
}

func codexFixtureConfig(t *testing.T, scenario string) Config {
	t.Helper()
	t.Setenv("JIANZUO_TEST_CODEX_APP_SERVER", "1")
	t.Setenv("JIANZUO_TEST_CODEX_SCENARIO", scenario)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Config{Codex: exe, EngineEnv: map[string]string{"CODEX_HOME": t.TempDir()}}
}

func TestCodexAppServerOccupiedThreadPreservesSession(t *testing.T) {
	config := codexFixtureConfig(t, "active-writer")
	task := Task{Workspace: t.TempDir(), Session: "retained-occupied-thread"}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var mu sync.Mutex
	var newSession, output int
	session, result, err := runCodexAppServer(ctx, config, task, "continue", func(kind, value string) {
		mu.Lock()
		defer mu.Unlock()
		if kind == "session" {
			newSession++
		}
		if kind == "assistant" || kind == "tool" {
			output++
		}
	})
	if !errors.Is(err, errCodexSessionBusy) || !strings.Contains(err.Error(), "Codex 桌面版或终端会话") {
		t.Fatalf("missing actionable busy-thread error: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if session != task.Session || result != "" || newSession != 0 || output != 0 {
		t.Fatalf("occupied thread replaced or executed: session=%s new=%d output=%d", session, newSession, output)
	}
}

func TestCodexCleanupKeepsFailureAndRequiresExitConfirmation(t *testing.T) {
	primary := codexRequestError("jianzuo-thread", "thread original already has an active writer")
	stopErr := &codexLocalStopError{tree: errors.New("taskkill fixture failed"), direct: errors.New("TerminateProcess: Access is denied.")}
	for _, exited := range []bool{false, true} {
		err := codexCleanupError(primary, stopErr, exited)
		if !errors.Is(err, errCodexSessionBusy) || !strings.HasPrefix(err.Error(), errCodexSessionBusy.Error()) {
			t.Fatalf("cleanup masked initial failure: %v", err)
		}
		if exited {
			if !strings.Contains(err.Error(), "已确认本轮 Codex 主进程退出") || !strings.Contains(err.Error(), "无法确认全部子进程") || strings.Contains(err.Error(), "TerminateProcess") {
				t.Fatalf("incorrect confirmed-exit diagnosis: %v", err)
			}
		} else if !strings.Contains(err.Error(), "无法确认 Codex 进程已停止") {
			t.Fatalf("unconfirmed exit was hidden: %v", err)
		}
	}
	if err := codexCleanupError(nil, stopErr, false); !errors.Is(err, stopErr) {
		t.Fatal("cleanup-only failure lost", err)
	}
	if err := codexCleanupError(primary, nil, false); !errors.Is(err, primary) {
		t.Fatal("successful cleanup lost primary failure", err)
	}
	if err := codexRequestError("jianzuo-turn", "already has an active writer"); errors.Is(err, errCodexSessionBusy) {
		t.Fatal("unrelated RPC misclassified as a thread ownership failure")
	}
}

func TestCodexAppServerNativeRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name, session             string
		mode                      *WorkMode
		sandbox, policy, reviewer string
		network                   bool
	}{
		{"new", "", nil, "workspace-write", "on-request", "user", true},
		{"resume-offline", "019abc-full-native-thread-id", &WorkMode{Permission: "workspace", AllowNetwork: boolPtr(false)}, "workspace-write", "on-request", "user", false},
		{"auto", "", &WorkMode{Permission: "workspace", Approval: "auto"}, "workspace-write", "on-request", "auto_review", true},
		{"read", "", &WorkMode{Permission: "read"}, "read-only", "never", "user", false},
		{"full", "", &WorkMode{Permission: "full"}, "danger-full-access", "never", "user", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := codexFixtureConfig(t, "complete")
			workspace := t.TempDir()
			image := filepath.Join(workspace, "image literal $(not-shell).png")
			task := Task{Engine: "codex", Workspace: workspace, Session: tc.session, Mode: tc.mode, Model: "fixture-model", ReasoningEffort: "high", Files: []RuntimeAttachment{{Attachment: Attachment{Mime: "image/png", Name: "img.png"}, Path: image}}}
			var mu sync.Mutex
			events := map[string][]string{}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			session, result, err := (CodexRunner{}).Run(ctx, config, task, "literal $()\n你好", func(kind, value string) { mu.Lock(); defer mu.Unlock(); events[kind] = append(events[kind], value) })
			if err != nil {
				t.Fatal(err)
			}
			if session == "" || (tc.session != "" && session != tc.session) {
				t.Fatal(session)
			}
			var body struct {
				Cwd, Method  string
				Args         []string
				Thread, Turn map[string]json.RawMessage
				Initialize   json.RawMessage
			}
			if err := json.Unmarshal([]byte(result), &body); err != nil {
				t.Fatal(result, err)
			}
			if filepath.Clean(body.Cwd) != filepath.Clean(workspace) {
				t.Fatal(body.Cwd)
			}
			for key, want := range map[string]string{"approvalPolicy": tc.policy, "approvalsReviewer": tc.reviewer, "cwd": workspace, "model": "fixture-model"} {
				for _, params := range []map[string]json.RawMessage{body.Thread, body.Turn} {
					var got string
					_ = json.Unmarshal(params[key], &got)
					if got != want {
						t.Errorf("%s=%q want %q", key, got, want)
					}
				}
			}
			var sandbox string
			_ = json.Unmarshal(body.Thread["sandbox"], &sandbox)
			if sandbox != tc.sandbox {
				t.Fatal(sandbox)
			}
			var threadConfig map[string]bool
			_ = json.Unmarshal(body.Thread["config"], &threadConfig)
			if got, ok := threadConfig["sandbox_workspace_write.network_access"]; !ok || got != tc.network {
				t.Fatal(threadConfig)
			}
			var sandboxPolicy map[string]any
			_ = json.Unmarshal(body.Turn["sandboxPolicy"], &sandboxPolicy)
			if tc.sandbox != "danger-full-access" && sandboxPolicy["networkAccess"] != tc.network {
				t.Fatal(sandboxPolicy)
			}
			var inputs []map[string]any
			_ = json.Unmarshal(body.Turn["input"], &inputs)
			if len(inputs) != 2 || inputs[0]["text"] != "literal $()\n你好" || inputs[1]["type"] != "localImage" || inputs[1]["path"] != image {
				t.Fatal(inputs)
			}
			var effort string
			_ = json.Unmarshal(body.Turn["effort"], &effort)
			if effort != "high" {
				t.Fatal(effort)
			}
			if (tc.session == "") != (body.Method == "thread/start") {
				t.Fatal(body.Method)
			}
			if tc.session != "" && string(body.Thread["excludeTurns"]) != "true" {
				t.Error("resume must preserve the native thread without hydrating its entire history")
			}
			if tc.session == "" && body.Thread["excludeTurns"] != nil {
				t.Error("resume-only options must not be sent to thread/start")
			}
			args := strings.Join(body.Args, "|")
			if !strings.Contains(args, "features.default_mode_request_user_input=true") {
				t.Fatal("ordinary Codex turns must offer native user-input questions")
			}
			if got, want := strings.Contains(args, "features.prefer_mxc=true"), runtime.GOOS == "windows" && tc.sandbox != "danger-full-access"; got != want {
				t.Fatalf("MXC preference in native argv=%v, want %v: %s", got, want, args)
			}
			if !strings.Contains(args, "sandbox_workspace_write.network_access="+fmt.Sprint(tc.network)) || strings.Contains(args, "literal $()") || strings.Contains(args, "exec|") {
				t.Fatal(args)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(events["session"]) != 1 || len(events["assistant"]) != 1 || len(events["tool"]) < 2 || len(events["progress"]) < 2 {
				t.Fatal(events)
			}
			var usage RunUsage
			_ = json.Unmarshal([]byte(events["usage"][len(events["usage"])-1]), &usage)
			if usage.Input != 15 || usage.Output != 15 || usage.Cached != 2 || usage.Total != 30 {
				t.Fatal(usage)
			}
		})
	}
}

func TestCodexAppServerLargeMessages(t *testing.T) {
	for _, scenario := range []string{"large-history", "large-item"} {
		t.Run(scenario, func(t *testing.T) {
			config := codexFixtureConfig(t, scenario)
			task := Task{Workspace: t.TempDir(), Session: "existing-large-thread"}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var mu sync.Mutex
			assistantCount, largeTools := 0, 0
			session, result, err := runCodexAppServer(ctx, config, task, "continue", func(kind, value string) {
				mu.Lock()
				defer mu.Unlock()
				if kind == "assistant" {
					assistantCount++
				}
				if kind == "tool" && strings.Contains(value, "large-output-begin:") && strings.HasSuffix(value, ":large-output-end") {
					largeTools++
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if session != task.Session || !json.Valid([]byte(result)) {
				t.Fatalf("resume lost its original identity or result: %q", session)
			}
			mu.Lock()
			defer mu.Unlock()
			if assistantCount != 1 || (scenario == "large-item" && largeTools != 1) {
				t.Fatalf("assistant=%d large tool=%d", assistantCount, largeTools)
			}
		})
	}
}

func TestCodexAppServerOversizedFrame(t *testing.T) {
	config := codexFixtureConfig(t, "oversized-frame")
	task := Task{Workspace: t.TempDir(), Session: "retained-native-thread"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, result, err := runCodexAppServer(ctx, config, task, "continue", func(string, string) {})
	if err == nil || !strings.Contains(err.Error(), "单条消息超过 64 MiB") || strings.Contains(err.Error(), "xxxxx") {
		t.Fatalf("expected bounded, content-free size diagnostic, got %v", err)
	}
	if session != task.Session || result != "" {
		t.Fatalf("oversized frame replaced the session or produced a result: %q", session)
	}
}

func TestCodexAppServerOptionalMCPFailure(t *testing.T) {
	config := codexFixtureConfig(t, "mcp-startup-error")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var mu sync.Mutex
	warnings := 0
	_, result, err := runCodexAppServer(ctx, config, Task{Workspace: t.TempDir()}, "hello", func(kind, value string) {
		mu.Lock()
		defer mu.Unlock()
		if kind == "progress" && strings.Contains(value, "fixture MCP unavailable") {
			warnings++
		}
	})
	if err != nil || !json.Valid([]byte(result)) {
		t.Fatalf("optional MCP failure aborted the turn: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if warnings != 1 {
		t.Fatalf("expected one visible MCP warning, got %d", warnings)
	}
}

func TestCodexAppServerApprovalRoundTrip(t *testing.T) {
	config := codexFixtureConfig(t, "approval")
	var mu sync.Mutex
	methods := []string{}
	ctx := withCodexInteraction(context.Background(), func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		methods = append(methods, method)
		mu.Unlock()
		if method == "item/fileChange/requestApproval" && !strings.Contains(string(params), "+review this") {
			t.Error("file approval did not include its native item's diff", string(params))
		}
		switch method {
		case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
			return json.RawMessage(`{"decision":"accept"}`), nil
		case "item/permissions/requestApproval":
			return json.RawMessage(`{"permissions":{},"scope":"turn"}`), nil
		case "item/tool/requestUserInput":
			return json.RawMessage(`{"answers":{}}`), nil
		default:
			return json.RawMessage(`{"action":"decline"}`), nil
		}
	})
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	_, result, err := runCodexAppServer(ctx, config, Task{Workspace: t.TempDir()}, "input", func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	var body struct{ Responses []json.RawMessage }
	_ = json.Unmarshal([]byte(result), &body)
	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 5 || len(body.Responses) != 5 {
		t.Fatal(methods, result)
	}
}

func TestCodexAppServerResolvesPendingInteraction(t *testing.T) {
	for _, scenario := range []string{"resolved", "turn-cancels-pending"} {
		t.Run(scenario, func(t *testing.T) {
			config := codexFixtureConfig(t, scenario)
			opened, closed := make(chan struct{}), make(chan struct{})
			ctx := withCodexInteraction(context.Background(), func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
				close(opened)
				<-ctx.Done()
				close(closed)
				return nil, ctx.Err()
			})
			ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			_, _, err := runCodexAppServer(ctx, config, Task{Workspace: t.TempDir()}, "input", func(kind, text string) {
				if text == "fixture-request-open" {
					select {
					case <-opened:
					case <-time.After(time.Second):
						t.Error("interaction did not open")
					}
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("pending UI context remained live")
			}
		})
	}
}

func TestCodexAppServerNativeInterrupt(t *testing.T) {
	config := codexFixtureConfig(t, "cancel")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	observed := false
	_, _, err := runCodexAppServer(ctx, config, Task{Workspace: t.TempDir()}, "input", func(kind, text string) {
		if text == "fixture-ready" {
			cancel()
		}
		if strings.Contains(text, "native-interrupt-observed") {
			observed = true
		}
	})
	if !errors.Is(err, context.Canceled) || !observed {
		t.Fatalf("native interrupt=%v, err=%v", observed, err)
	}
}

func TestCodexAppServerFailsClosed(t *testing.T) {
	for _, scenario := range []string{"unsupported", "disconnect", "failed", "wrong-thread", "approval-before-turn-identity"} {
		t.Run(scenario, func(t *testing.T) {
			config := codexFixtureConfig(t, scenario)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			_, _, err := runCodexAppServer(ctx, config, Task{Workspace: t.TempDir(), Session: "exact-original-thread"}, "input", func(string, string) {})
			if err == nil {
				t.Fatal("unexpected success")
			}
			if errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("protocol fixture timed out", err)
			}
		})
	}
}

func TestCodexAppServerRequestIDReuse(t *testing.T) {
	config := codexFixtureConfig(t, "reused-id")
	opened, newOpened := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	calls := 0
	ctx := withCodexInteraction(context.Background(), func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			close(opened)
			<-ctx.Done()
			<-newOpened
			return json.RawMessage(`{"decision":"accept"}`), nil
		}
		close(newOpened)
		time.Sleep(100 * time.Millisecond)
		return json.RawMessage(`{"decision":"decline"}`), nil
	})
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	_, _, err := runCodexAppServer(ctx, config, Task{Workspace: t.TempDir()}, "input", func(_, text string) {
		if text == "fixture-request-open" {
			select {
			case <-opened:
			case <-ctx.Done():
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCodexAppServerCancelBlockedWrite(t *testing.T) {
	config := codexFixtureConfig(t, "blocked-turn")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, _, err := runCodexAppServer(ctx, config, Task{Workspace: t.TempDir()}, strings.Repeat("large prompt", 200000), func(string, string) {})
	if err == nil || time.Since(started) > 5*time.Second {
		t.Fatalf("cancel blocked pipe: %v after %v", err, time.Since(started))
	}
}

func TestCodexAppServerIgnoresForeignEvents(t *testing.T) {
	config := codexFixtureConfig(t, "foreign")
	ctx := withCodexInteraction(context.Background(), func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		t.Error("foreign approval reached user")
		return nil, errors.New("foreign")
	})
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	_, _, err := runCodexAppServer(ctx, config, Task{Workspace: t.TempDir()}, "input", func(_, text string) {
		if strings.Contains(text, "foreign output must not appear") {
			t.Error(text)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCodexAppServerCanceledBeforeTurn(t *testing.T) {
	config := codexFixtureConfig(t, "cancel-before-turn")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, _, err := runCodexAppServer(ctx, config, Task{Workspace: t.TempDir()}, "input", func(kind, _ string) {
		if kind == "session" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCodexAppServerTargetAndSecretIsolation(t *testing.T) {
	config := Config{Distro: "Ubuntu-22.04", User: "precise-user", Codex: "/home/precise-user/.local/bin/codex", HardwareAI: &HardwareRuntime{URL: "http://127.0.0.1:12345/mcp", Token: "fixture-secret-not-in-argv"}}
	cmd := codexAppServerCommand(config, Task{Workspace: "/work/path with literal $(x)"})
	args := strings.Join(cmd.Args, "|")
	for _, want := range []string{"-d|Ubuntu-22.04|-u|precise-user|--exec|python3|-u|-c", config.Codex, "/work/path with literal $(x)"} {
		if !strings.Contains(args, want) {
			t.Error(want, args)
		}
	}
	if strings.Contains(args, config.HardwareAI.Token) || strings.Contains(codexAppServerHardwareLauncher, "sys.stdin.read(") || strings.Contains(codexAppServerHardwareLauncher, "communicate(") {
		t.Fatal("secret exposed or stream consumed until EOF")
	}
	if !strings.Contains(codexAppServerHardwareLauncher, "os.read(0,1)") || !strings.Contains(codexAppServerHardwareLauncher, "start_new_session=True") {
		t.Fatal("unsafe hardware bridge")
	}
}
