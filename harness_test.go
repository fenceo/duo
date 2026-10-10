package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// This child is the test binary, never dsh: it cannot contact a model provider
// or read native Harness credentials. Its wire vocabulary matches 0.1.5-rc.2.
func runHarnessACPFixture() {
	type request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	decoder, encoder := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	write := func(v any) {
		if encoder.Encode(v) != nil {
			os.Exit(23)
		}
	}
	reply := func(id json.RawMessage, v any) { write(map[string]any{"jsonrpc": "2.0", "id": id, "result": v}) }
	reject := func(id json.RawMessage, message string) {
		write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32603, "message": message}})
	}
	notify := func(session, kind string, fields map[string]any) {
		fields["sessionUpdate"] = kind
		write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": session, "update": fields}})
	}
	cwd, _ := os.Getwd()
	var patches []map[string]any
	for i := 3; i < len(os.Args); i += 2 {
		if os.Args[i] != "--patch" || i+1 >= len(os.Args) {
			os.Exit(23)
		}
		data, err := os.ReadFile(os.Args[i+1])
		if err != nil || json.Unmarshal(data, &patches) != nil {
			os.Exit(23)
		}
	}
	route := map[string]any{}
	for _, row := range patches {
		if row["id"] == "acp" {
			route = row["config"].(map[string]any)
		}
	}
	scenario := os.Getenv("JIANZUO_TEST_HARNESS_SCENARIO")
	var session string
	var history []string
	var pending json.RawMessage
	coldProvider := scenario == "cold-provider"
	path := func() string { return filepath.Join(cwd, "fixture-"+session+".json") }
	save := func() {
		data, _ := json.Marshal(history)
		if os.WriteFile(path(), data, 0600) != nil {
			os.Exit(23)
		}
	}
	options := func() any {
		return map[string]any{"configOptions": []any{map[string]any{"id": "reasoning_effort", "options": []any{map[string]string{"value": "off"}, map[string]string{"value": "high"}, map[string]string{"value": "max"}}}}}
	}
	for {
		var req request
		if err := decoder.Decode(&req); err != nil {
			if err != io.EOF {
				os.Exit(23)
			}
			return
		}
		var p struct {
			SessionID string                `json:"sessionId"`
			Cwd       string                `json:"cwd"`
			ConfigID  string                `json:"configId"`
			Value     string                `json:"value"`
			Prompt    []harnessFixtureBlock `json:"prompt"`
		}
		_ = json.Unmarshal(req.Params, &p)
		switch req.Method {
		case "initialize":
			if route["provider"] != "deepseek-official" {
				reject(req.ID, "no adapter registered for provider")
				continue
			}
			name := "deepseek-harness-acp"
			if scenario == "bad-handshake" {
				name = "unrelated-program"
			}
			reply(req.ID, map[string]any{"protocolVersion": 1, "agentInfo": map[string]string{"name": name}, "agentCapabilities": map[string]any{"promptCapabilities": map[string]bool{"image": scenario == "attachments" && route["model"] == "fixture-vision"}, "sessionCapabilities": map[string]any{"resume": map[string]any{}, "close": map[string]any{}}}})
		case "session/new", "session/resume":
			if coldProvider {
				coldProvider = false
				reject(req.ID, "no adapter registered for provider")
				continue
			}
			if filepath.Clean(p.Cwd) != filepath.Clean(cwd) {
				reject(req.ID, "cwd mismatch")
				continue
			}
			if req.Method == "session/resume" {
				session = p.SessionID
				data, err := os.ReadFile(path())
				if err != nil || json.Unmarshal(data, &history) != nil {
					session = ""
					reject(req.ID, "session is not resumable")
					continue
				}
			} else {
				session = uid()
				history = nil
				save()
			}
			reply(req.ID, map[string]any{"sessionId": session})
		case "session/set_config_option":
			if p.SessionID != session {
				reject(req.ID, "wrong session")
				continue
			}
			if p.ConfigID == "model" {
				var selected []string
				if json.Unmarshal([]byte(p.Value), &selected) != nil || len(selected) != 2 {
					reject(req.ID, "bad model option")
					continue
				}
				route["provider"] = selected[0]
				route["model"] = selected[1]
				delete(route, "reasoningEffort")
			} else if p.ConfigID == "reasoning_effort" {
				route["reasoningEffort"] = p.Value
			}
			reply(req.ID, options())
		case "session/prompt":
			if session == "" || p.SessionID != session || len(p.Prompt) == 0 || p.Prompt[0].Type != "text" {
				reject(req.ID, "bad prompt")
				continue
			}
			input := p.Prompt[0].Text
			for _, block := range p.Prompt[1:] {
				switch block.Type {
				case "text":
					input += block.Text
				case "image":
					if scenario != "attachments" || route["model"] != "fixture-vision" || block.MimeType != "image/png" || block.Data == "" {
						os.Exit(23)
					}
					input += "[fixture native image]"
				default:
					os.Exit(23)
				}
			}
			history = append(history, input)
			save()
			notify("foreign-session", "agent_message_chunk", map[string]any{"content": map[string]string{"type": "text", "text": "FOREIGN RESULT"}})
			if scenario == "wait" && input == "wait" {
				pending = req.ID
				notify(session, "tool_call", map[string]any{"title": "wait"})
				continue
			}
			if scenario == "eof" {
				return
			}
			notify(session, "tool_call", map[string]any{"title": "read_file", "rawInput": map[string]string{"path": "example.txt"}})
			notify(session, "tool_call_update", map[string]any{"status": "completed", "content": []any{map[string]string{"text": "fixture tool result"}}})
			result, _ := json.Marshal(map[string]any{"input": input, "prompt": p.Prompt, "history": history, "cwd": cwd, "args": os.Args[1:], "initialize": route, "patches": patches, "pid": os.Getpid(), "turn": len(history)})
			notify(session, "agent_message_chunk", map[string]any{"content": map[string]string{"type": "text", "text": string(result)}})
			if scenario == "error" {
				reject(req.ID, "fixture provider failure")
				continue
			}
			reason := "end_turn"
			if scenario == "max-tokens" {
				reason = "max_tokens"
			}
			reply(req.ID, map[string]string{"stopReason": reason})
		case "session/cancel":
			if pending != nil {
				reply(pending, map[string]string{"stopReason": "cancelled"})
				pending = nil
			}
		case "session/close":
			if session != "" {
				save()
			}
			reply(req.ID, map[string]any{})
			session = ""
		default:
			reject(req.ID, "unsupported fixture ACP method")
		}
	}
}

type harnessFixtureResult struct {
	Input, Cwd string
	Args       []string
	Initialize map[string]any
	Patches    []map[string]any
	PID, Turn  int
	History    []string
	Prompt     []harnessFixtureBlock
}

type harnessFixtureBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
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
	return runHarnessACP(ctx, c, task, input, emit)
}

func TestHarnessACPRoundTripContinuesLiveSession(t *testing.T) {
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
	if resumed, result, err := harnessFixtureRun(t, c, task, "after runtime closed", func(string, string) {}); err != nil || resumed != session || !strings.Contains(result, "after runtime closed") {
		t.Fatalf("stopped session did not resume: %s %v", result, err)
	}
}

func TestHarnessACPFailureAndIncompleteTurn(t *testing.T) {
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

func TestHarnessACPCancellationInvalidatesLiveSession(t *testing.T) {
	c, task := harnessFixtureSetup(t, "wait")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	started := time.Now()
	session, _, err := runHarnessACP(ctx, c, task, "wait", func(kind, text string) {
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
	if resumed, result, err := harnessFixtureRun(t, c, task, "continue after stop", func(string, string) {}); err != nil || resumed != session || !strings.Contains(result, "wait") {
		t.Fatalf("cancelled session did not resume: %s %v", result, err)
	}
}

func TestHarnessACPRejectsUnknownSessionAndInvalidRoute(t *testing.T) {
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

func TestHarnessACPPolicyPatchMatchesRequestedMode(t *testing.T) {
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

func TestHarnessACPRejectsUnsupportedGuarantees(t *testing.T) {
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

func TestHarnessACPLiveSessionCannotChangeExecutionIdentity(t *testing.T) {
	c, task := harnessFixtureSetup(t, "")
	session, _, err := harnessFixtureRun(t, c, task, "first", func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	task.Session = session
	task.Model = "different-model"
	if resumed, result, err := harnessFixtureRun(t, c, task, "new model", func(string, string) {}); err != nil || resumed != session || !strings.Contains(result, "different-model") {
		t.Fatalf("model switch failed: %s %v", result, err)
	}
	task.Model = "deepseek-flash"
	task.Mode.Permission = "full"
	if _, _, err = harnessFixtureRun(t, c, task, "new permissions", func(string, string) {}); err == nil {
		t.Fatal("changing permissions silently reused the previous policy")
	}
}

// Explicit opt-in: native handshakes send no session/prompt and use isolated
// homes with a dummy credential and a loopback-only endpoint. They never read
// the user's Harness home or incur a paid model call.
func TestHarnessACPNativeHandshake(t *testing.T) {
	if os.Getenv("JIANZUO_TEST_HARNESS_NATIVE") != "1" {
		t.Skip("set JIANZUO_TEST_HARNESS_NATIVE=1 to validate installed ACP handshakes")
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
