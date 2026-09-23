package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// This child is the test binary, never dsh: it cannot contact a model provider
// or read native Harness credentials. Its wire vocabulary matches 0.1.5-rc.2.
func runHarnessSDKFixture() {
	type request struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	fail := func(message string) {
		fmt.Fprintln(os.Stderr, "harness fixture: "+message)
		os.Exit(23)
	}
	decoder, encoder := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	write := func(value any) {
		if err := encoder.Encode(value); err != nil {
			fail(err.Error())
		}
	}
	reply := func(id json.RawMessage, result any) {
		write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	reject := func(id json.RawMessage, message string) {
		write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32603, "message": message}})
	}
	notify := func(method string, params any) {
		write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	}
	seq := 0
	event := func(session, kind string, data any) {
		seq++
		notify("session.event", map[string]any{"sessionId": session, "event": map[string]any{"type": kind, "seq": seq, "time": time.Now().UnixMilli(), "data": data}})
	}
	status := func(session, value string) {
		notify("session.status", map[string]any{"sessionId": session, "status": value})
	}
	cwd, _ := os.Getwd()
	patches := []map[string]any{}
	for i := 3; i < len(os.Args); i++ {
		if os.Args[i] != "--patch" || i+1 >= len(os.Args) {
			fail("unexpected argument; prompts and model options belong to JSON-RPC")
		}
		i++
		data, err := os.ReadFile(os.Args[i])
		if err != nil {
			fail("cannot read invocation policy patch: " + err.Error())
		}
		var patch []map[string]any
		if err := json.Unmarshal(data, &patch); err != nil {
			fail("policy patch must be a JSON patch-list: " + err.Error())
		}
		patches = append(patches, patch...)
	}
	if len(patches) == 0 {
		fail("missing explicit policy patch")
	}
	var initialized map[string]any
	turns := map[string]int{}
	scenario := os.Getenv("JIANZUO_TEST_HARNESS_SCENARIO")
	for {
		var req request
		if err := decoder.Decode(&req); err != nil {
			if err != io.EOF {
				fail(err.Error())
			}
			return
		}
		switch req.Method {
		case "initialize":
			var p map[string]any
			if err := json.Unmarshal(req.Params, &p); err != nil {
				reject(req.ID, "invalid initialize parameters")
				continue
			}
			if p["provider"] != "deepseek-official" {
				reject(req.ID, "no adapter registered for provider")
				continue
			}
			model, _ := p["model"].(string)
			workdir, _ := p["cwd"].(string)
			if model == "" || workdir == "" || filepath.Clean(workdir) != filepath.Clean(cwd) {
				reject(req.ID, "initialize requires an exact model and process cwd")
				continue
			}
			if effort, ok := p["reasoningEffort"]; ok && effort != "off" && effort != "low" && effort != "high" && effort != "max" {
				reject(req.ID, "unsupported reasoning effort")
				continue
			}
			initialized = p
			if scenario == "bad-handshake" {
				reply(req.ID, map[string]any{"serverInfo": map[string]any{"name": "unrelated-program", "version": "0.0.1"}})
				continue
			}
			reply(req.ID, map[string]any{"serverInfo": map[string]any{"name": "deepseek-harness-sdk-runtime", "version": "0.0.1"}})
		case "session/prompt":
			if initialized == nil {
				reject(req.ID, "SDK server is not initialized")
				continue
			}
			var p struct {
				SessionID     string `json:"sessionId"`
				ContentBlocks []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"contentBlocks"`
			}
			if err := json.Unmarshal(req.Params, &p); err != nil || p.SessionID == "" || len(p.ContentBlocks) != 1 || p.ContentBlocks[0].Type != "text" {
				reject(req.ID, "invalid session/prompt")
				continue
			}
			turns[p.SessionID]++
			turn := turns[p.SessionID]
			messageID := fmt.Sprintf("fixture-user-%d", turn)
			userMessage := map[string]any{"id": messageID, "role": "user", "source": map[string]any{"kind": "user"}, "content": p.ContentBlocks}
			// followup() commits the inbox splice before its RPC receipt returns.
			event(p.SessionID, "agent/inbox/spliced", map[string]any{"target": "next-turn", "start": 0, "inserted": []any{userMessage}})
			status(p.SessionID, "running")
			reply(req.ID, map[string]any{"messageId": messageID})
			event(p.SessionID, "turn/start", map[string]any{"turn": turn})
			event(p.SessionID, "user/message", userMessage)
			// Descendant and unrelated sessions must not overwrite the root result
			// or complete the root request, even when their idle arrives first.
			event("unrelated-session", "assistant/message", map[string]any{"message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "FOREIGN RESULT"}}}})
			event("unrelated-session", "turn/end", map[string]any{"reason": map[string]any{"kind": "completed"}})
			status("unrelated-session", "idle")
			if scenario == "wait" {
				event(p.SessionID, "tool/call", map[string]any{"callId": "fixture-wait", "name": "wait", "arguments": "{}"})
				continue
			}
			if scenario == "eof" {
				return
			}
			result, _ := json.Marshal(map[string]any{"input": p.ContentBlocks[0].Text, "cwd": cwd, "args": os.Args[1:], "initialize": initialized, "patches": patches, "pid": os.Getpid(), "turn": turn})
			event(p.SessionID, "tool/call", map[string]any{"callId": "fixture-read", "name": "read_file", "arguments": `{"path":"example.txt"}`})
			event(p.SessionID, "tool/result", map[string]any{"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool-result", "toolCallId": "fixture-read", "content": []any{map[string]any{"type": "text", "text": "fixture tool result"}}, "isError": false}}}})
			event(p.SessionID, "assistant/message", map[string]any{"turn": turn, "step": 1, "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "reasoning", "text": "fixture reasoning"}, map[string]any{"type": "text", "text": string(result)}}}, "usage": map[string]any{"inputTokens": 12, "outputTokens": 6}})
			reason := map[string]any{"kind": "completed"}
			if scenario == "error" {
				reason = map[string]any{"kind": "error", "error": map[string]any{"code": "FIXTURE_ERROR", "message": "fixture provider failure"}}
			} else if scenario == "max-tokens" {
				reason["kind"] = "max-tokens"
			}
			event(p.SessionID, "turn/end", map[string]any{"turn": turn, "reason": reason})
			status(p.SessionID, "idle")
		case "shutdown":
			reply(req.ID, map[string]any{})
			return
		default:
			reject(req.ID, "unsupported fixture SDK method")
		}
	}
}

type harnessFixtureResult struct {
	Input, Cwd string
	Args       []string
	Initialize map[string]any
	Patches    []map[string]any
	PID, Turn  int
}

func harnessFixtureSetup(t *testing.T, scenario string) (Config, Task) {
	t.Helper()
	closeHarnessRuntimes()
	t.Setenv("JIANZUO_TEST_HARNESS_SDK", "1")
	t.Setenv("JIANZUO_TEST_HARNESS_SCENARIO", scenario)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	// Stop children before TempDir's cleanup tries to remove their cwd.
	t.Cleanup(closeHarnessRuntimes)
	return Config{Harness: exe, HarnessProvider: "deepseek-official", HarnessModel: "deepseek-flash"}, Task{ID: "harness-fixture-" + filepath.Base(workspace), Engine: "deepseek-harness", Workspace: workspace, Model: "deepseek-flash", ReasoningEffort: "high", Mode: &WorkMode{Permission: "workspace", Approval: "never", AllowNetwork: boolPtr(true)}}
}

func harnessFixtureRun(t *testing.T, c Config, task Task, input string, emit func(string, string)) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return runHarnessSDK(ctx, c, task, input, emit)
}

func TestHarnessSDKRoundTripContinuesLiveSession(t *testing.T) {
	c, task := harnessFixtureSetup(t, "")
	input := "精确输入\n$() ${HOME} `quoted` ; & ' \" \\ end\n"
	kinds := map[string]int{}
	emit := func(kind, text string) {
		if strings.Contains(text, "FOREIGN RESULT") {
			t.Errorf("foreign session leaked into output: %s", text)
		}
		kinds[kind]++
	}
	session, result, err := harnessFixtureRun(t, c, task, input, emit)
	var first harnessFixtureResult
	decodeErr := json.Unmarshal([]byte(result), &first)
	if err != nil || decodeErr != nil || session == "" || first.Input != input || first.Turn != 1 || filepath.Clean(first.Cwd) != filepath.Clean(task.Workspace) {
		t.Fatalf("first round: session=%q result=%s err=%v decode=%v", session, result, err, decodeErr)
	}
	if first.Initialize["provider"] != "deepseek-official" || first.Initialize["model"] != "deepseek-flash" || first.Initialize["reasoningEffort"] != "high" {
		t.Fatalf("initialize lost selected model/effort: %#v", first.Initialize)
	}
	if kinds["assistant"] != 1 || kinds["tool"] < 2 {
		t.Fatalf("missing or duplicate streamed content: %#v", kinds)
	}
	for _, arg := range first.Args {
		if arg == input || arg == task.Model || strings.Contains(arg, "--json") || strings.Contains(arg, "--session-id") {
			t.Fatalf("RPC input leaked into argv: %#v", first.Args)
		}
	}
	task.Session = session
	secondInput := "第二轮保留特殊字符 $(Get-Item .)\n--profile web"
	continued, result, err := harnessFixtureRun(t, c, task, secondInput, emit)
	var second harnessFixtureResult
	decodeErr = json.Unmarshal([]byte(result), &second)
	if err != nil || decodeErr != nil || continued != session || second.Turn != 2 || second.PID != first.PID || second.Input != secondInput {
		t.Fatalf("continuation did not reuse exact live session: %s %s %v %v", continued, result, err, decodeErr)
	}
	closeHarnessRuntimes()
	if _, _, err := harnessFixtureRun(t, c, task, "after runtime closed", func(string, string) {}); err == nil {
		t.Fatal("a stopped native session must not silently restart without its history")
	}
}

func TestHarnessSDKFailureAndIncompleteTurn(t *testing.T) {
	for _, scenario := range []string{"error", "max-tokens", "eof"} {
		t.Run(scenario, func(t *testing.T) {
			c, task := harnessFixtureSetup(t, scenario)
			_, _, err := harnessFixtureRun(t, c, task, "exercise failure", func(string, string) {})
			if err == nil {
				t.Fatalf("%s was falsely reported as a successful turn", scenario)
			}
			if scenario == "error" && !strings.Contains(err.Error(), "fixture provider failure") {
				t.Fatalf("provider error detail was lost: %v", err)
			}
		})
	}
}

func TestHarnessSDKCancellationInvalidatesLiveSession(t *testing.T) {
	c, task := harnessFixtureSetup(t, "wait")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	started := time.Now()
	session, _, err := runHarnessSDK(ctx, c, task, "wait", func(kind, text string) {
		if kind == "tool" && strings.Contains(text, "wait") {
			cancel()
		}
	})
	if err == nil || time.Since(started) > 5*time.Second {
		t.Fatalf("cancel did not stop promptly: duration=%s err=%v", time.Since(started), err)
	}
	if session == "" {
		t.Fatal("accepted native session identity was lost on cancellation")
	}
	task.Session = session
	if _, _, err := harnessFixtureRun(t, c, task, "continue after stop", func(string, string) {}); err == nil {
		t.Fatal("cancelled SDK process cannot be resumed by creating a new process")
	}
}

func TestHarnessSDKRejectsUnknownSessionAndInvalidRoute(t *testing.T) {
	c, task := harnessFixtureSetup(t, "")
	task.Session = "previous-process-native-session"
	if _, _, err := harnessFixtureRun(t, c, task, "continue", func(string, string) {}); err == nil {
		t.Fatal("unknown persisted SDK session was silently started afresh")
	}
	task.Session = ""
	c.HarnessProvider = "missing-provider"
	if _, _, err := harnessFixtureRun(t, c, task, "must fail handshake", func(string, string) {}); err == nil {
		t.Fatal("unknown provider accepted")
	}
	c.HarnessProvider = "deepseek-official"
	task.ReasoningEffort = "unsupported-effort"
	if _, _, err := harnessFixtureRun(t, c, task, "must fail handshake", func(string, string) {}); err == nil {
		t.Fatal("unsupported reasoning effort accepted")
	}
}

func TestHarnessSDKPolicyPatchMatchesRequestedMode(t *testing.T) {
	for _, mode := range []struct{ permission, sandbox string }{{"read", "read-only"}, {"workspace", "workspace-write"}, {"full", "danger-full-access"}} {
		t.Run(mode.permission, func(t *testing.T) {
			c, task := harnessFixtureSetup(t, "")
			task.Mode.Permission = mode.permission
			_, result, err := harnessFixtureRun(t, c, task, "check policy", func(string, string) {})
			var actual harnessFixtureResult
			if decodeErr := json.Unmarshal([]byte(result), &actual); err != nil || decodeErr != nil {
				t.Fatalf("run=%v decode=%v result=%s", err, decodeErr, result)
			}
			rows := map[string]map[string]any{}
			for _, row := range actual.Patches {
				if id, ok := row["id"].(string); ok {
					rows[id] = row
				}
			}
			policy, _ := rows["sandbox-policy"]["config"].(map[string]any)
			approval, _ := rows["approval"]["config"].(map[string]any)
			if policy["mode"] != mode.sandbox || approval["policy"] != "never" || rows["permission"]["disabled"] != true {
				t.Fatalf("policy not pinned against native settings overrides: %#v", rows)
			}
		})
	}
}

func TestHarnessSDKRejectsUnsupportedGuarantees(t *testing.T) {
	for _, scenario := range []string{"offline", "automatic-approval", "bad-handshake"} {
		t.Run(scenario, func(t *testing.T) {
			c, task := harnessFixtureSetup(t, scenario)
			if scenario == "offline" {
				task.Mode.AllowNetwork = boolPtr(false)
			} else if scenario == "automatic-approval" {
				task.Mode.Approval = "auto"
			}
			if _, _, err := harnessFixtureRun(t, c, task, "must not run", func(string, string) {}); err == nil {
				t.Fatalf("unsupported guarantee/handshake %s accepted", scenario)
			}
		})
	}
}

func TestHarnessSDKLiveSessionCannotChangeExecutionIdentity(t *testing.T) {
	c, task := harnessFixtureSetup(t, "")
	session, _, err := harnessFixtureRun(t, c, task, "first", func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	task.Session = session
	task.Model = "different-model"
	if _, _, err = harnessFixtureRun(t, c, task, "new model", func(string, string) {}); err == nil {
		t.Fatal("changing model silently reused a worker initialized for another model")
	}
	task.Model = "deepseek-flash"
	task.Mode.Permission = "full"
	if _, _, err = harnessFixtureRun(t, c, task, "new permissions", func(string, string) {}); err == nil {
		t.Fatal("changing permissions silently reused the previous policy")
	}
}

func TestHarnessTurnReceiptAndSessionIsolation(t *testing.T) {
	s := harnessTurn{session: "root", receipt: "our-message"}
	var emitted []string
	consume := func(method string, params any) {
		raw, _ := json.Marshal(params)
		s.consume(codexRPC{Method: method, Params: raw}, func(kind, text string) { emitted = append(emitted, kind+":"+text) })
	}
	event := func(session, kind string, data any) {
		consume("session.event", map[string]any{"sessionId": session, "event": map[string]any{"type": kind, "data": data}})
	}
	status := func(session, status string) {
		consume("session.status", map[string]any{"sessionId": session, "status": status})
	}
	message := func(session, text string) {
		event(session, "assistant/message", map[string]any{"message": map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}})
	}
	status("root", "idle")
	event("root", "turn/end", map[string]any{"reason": map[string]any{"kind": "completed"}})
	event("root", "agent/inbox/spliced", map[string]any{"inserted": []any{map[string]any{"id": "other-message"}}})
	message("root", "stale root output")
	if s.active || s.ended || s.idle || len(emitted) != 0 {
		t.Fatalf("events before our receipt activated the turn: %#v %v", s, emitted)
	}
	event("root", "agent/inbox/spliced", map[string]any{"inserted": []any{map[string]any{"id": "our-message"}}})
	event("root", "turn/start", map[string]any{"turn": 2})
	message("child", "foreign output")
	event("child", "turn/end", map[string]any{"reason": map[string]any{"kind": "completed"}})
	status("child", "idle")
	message("root", "owned answer")
	event("root", "turn/end", map[string]any{"reason": map[string]any{"kind": "completed"}})
	if !s.active || !s.ended || s.idle || s.result != "owned answer" || len(emitted) != 1 {
		t.Fatalf("session filtering or completion boundary failed: %#v %v", s, emitted)
	}
	status("root", "idle")
	if !s.idle || s.failure != "" {
		t.Fatalf("owned completed turn did not settle: %#v", s)
	}
}

// Explicit opt-in: native handshakes send no session/prompt and use isolated
// homes with a dummy credential and a loopback-only endpoint. They never read
// the user's Harness home or incur a paid model call.
func TestHarnessSDKNativeHandshake(t *testing.T) {
	if os.Getenv("JIANZUO_TEST_HARNESS_NATIVE") != "1" {
		t.Skip("set JIANZUO_TEST_HARNESS_NATIVE=1 to validate installed SDK handshakes")
	}
	if runtime.GOOS != "windows" {
		t.Skip("this opt-in smoke covers the Windows host and its WSL installation")
	}
	t.Run("windows", func(t *testing.T) {
		workspace, home := t.TempDir(), t.TempDir()
		c := Config{Harness: "dsh.cmd", HarnessModel: "deepseek-flash", HarnessProvider: "deepseek-official", Workspaces: []string{workspace}, EngineEnv: map[string]string{"DSH_HOME": home, "DEEPSEEK_API_KEY": "jianzuo-no-network-smoke-dummy", "DEEPSEEK_BASE_URL": "http://127.0.0.1:1", "DSH_TELEMETRY_DISABLED": "1"}}
		message, err := checkHarness(c)
		if err != nil {
			t.Fatal(err)
		}
		t.Log(message)
	})
	t.Run("wsl", func(t *testing.T) {
		distro := os.Getenv("JIANZUO_TEST_HARNESS_WSL_DISTRO")
		if distro == "" {
			t.Skip("set JIANZUO_TEST_HARNESS_WSL_DISTRO to opt into one installed distro")
		}
		c := Config{Distro: distro, Harness: "dsh", HarnessModel: "deepseek-flash", HarnessProvider: "deepseek-official"}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		mktemp := commandWithContext(ctx, command(c, "mktemp", "-d", "/tmp/jianzuo-harness-native-XXXXXXXX"))
		data, err := mktemp.Output()
		if err != nil {
			t.Fatalf("create isolated WSL temp directory: %v", err)
		}
		root := strings.TrimSpace(string(data))
		// Only ever clean the exact mktemp-created leaf under the intended prefix.
		if !strings.HasPrefix(root, "/tmp/jianzuo-harness-native-") || strings.ContainsAny(strings.TrimPrefix(root, "/tmp/jianzuo-harness-native-"), "/\\\r\n") {
			t.Fatalf("unexpected WSL temp path %q", root)
		}
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanupCancel()
			cmd := commandWithContext(cleanupCtx, command(c, "rm", "-rf", "--", root))
			if err := cmd.Run(); err != nil {
				t.Errorf("clean isolated WSL test directory %s: %v", root, err)
			}
		})
		c.Workspaces = []string{root}
		c.EngineEnv = map[string]string{"DSH_HOME": root + "/home", "DEEPSEEK_API_KEY": "jianzuo-no-network-smoke-dummy", "DEEPSEEK_BASE_URL": "http://127.0.0.1:1", "DSH_TELEMETRY_DISABLED": "1"}
		message, err := checkHarness(c)
		if err != nil {
			t.Fatal(err)
		}
		t.Log(message)
	})
}

func TestHarnessSDKNativeBootDiagnostics(t *testing.T) {
	if os.Getenv("JIANZUO_TEST_HARNESS_DIAGNOSTICS") != "1" {
		t.Skip("isolated native boot diagnostic is opt-in")
	}
	args, err := harnessExecutable("dsh.cmd")
	if err != nil {
		t.Fatal(err)
	}
	for _, patched := range []bool{false, true} {
		t.Run(fmt.Sprint(patched), func(t *testing.T) {
			workspace, home := t.TempDir(), t.TempDir()
			argv := append(append([]string{}, args[1:]...), "--profile", "sdk")
			if patched {
				patch, _ := harnessPolicy(Task{Workspace: workspace})
				path := filepath.Join(t.TempDir(), "policy.json")
				if err := os.WriteFile(path, patch, 0600); err != nil {
					t.Fatal(err)
				}
				argv = append(argv, "--patch", path)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, args[0], argv...)
			cmd.Dir = workspace
			applyEngineEnv(cmd, map[string]string{"DSH_HOME": home, "DEEPSEEK_API_KEY": "jianzuo-no-network-smoke-dummy", "DEEPSEEK_BASE_URL": "http://127.0.0.1:1", "DSH_TELEMETRY_DISABLED": "1"})
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			_ = json.NewEncoder(stdin).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"cwd": workspace, "provider": "deepseek-official", "model": "deepseek-flash"}})
			time.Sleep(2 * time.Second)
			_ = json.NewEncoder(stdin).Encode(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "initialize", "params": map[string]any{"cwd": workspace, "provider": "deepseek-official", "model": "deepseek-flash"}})
			_ = json.NewEncoder(stdin).Encode(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "jianzuo/diagnostic-unknown-method", "params": map[string]any{}})
			_ = json.NewEncoder(stdin).Encode(map[string]any{"jsonrpc": "2.0", "id": 4, "method": "shutdown"})
			err = cmd.Wait()
			t.Logf("patched=%v exit=%v stdout=%s stderr=%s", patched, err, stdout.String(), stderr.String())
		})
	}
}
