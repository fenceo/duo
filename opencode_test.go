package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This executable is a synthetic ACP peer for OpenCode, not an installed CLI
// and never a model. It records what Duo sent so the adapter contract can be
// asserted offline.
func TestOpenCodeProcessFixture(t *testing.T) {
	if os.Getenv("DUO_TEST_OPENCODE_ACP") != "1" {
		t.Skip("synthetic child only")
	}
	var session, model, cwd string
	var history []string
	mcp := ""
	historyPath := "opencode-fixture-history.json"
	reply := func(id json.RawMessage, value any) {
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": value})
		os.Stdout.Write(append(raw, '\n'))
	}
	notify := func(update any) {
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": session, "update": update}})
		os.Stdout.Write(append(raw, '\n'))
	}
	finish := func(id json.RawMessage) {
		value, _ := json.Marshal(map[string]any{"session": session, "model": model, "cwd": cwd, "history": history, "mcp": json.RawMessage(mcp)})
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
			SessionID  string
			ConfigID   string
			Value      string
			Cwd        string
			McpServers json.RawMessage `json:"mcpServers"`
			Prompt     []struct{ Text string }
		}
		_ = json.Unmarshal(rpc.Params, &p)
		switch rpc.Method {
		case "initialize":
			reply(rpc.ID, map[string]any{"protocolVersion": 1, "agentInfo": map[string]string{"name": "OpenCode"}, "agentCapabilities": map[string]any{
				"loadSession": true,
				"mcpCapabilities": map[string]bool{"http": os.Getenv("DUO_TEST_OPENCODE_NO_MCP") != "1"},
				"promptCapabilities": map[string]bool{"image": true},
				"sessionCapabilities": map[string]any{"resume": map[string]any{}, "close": map[string]any{}},
			}})
		case "session/new":
			session, cwd, history = "fixture-opencode-session", p.Cwd, nil
			mcp = string(p.McpServers)
			reply(rpc.ID, map[string]any{"sessionId": session, "configOptions": []any{map[string]any{
				"id": "model", "name": "Model", "category": "model", "type": "select", "currentValue": "fixture/model-one",
				"options": []any{map[string]string{"value": "fixture/model-one", "name": "Fixture One"}, map[string]string{"value": "fixture/model-two", "name": "Fixture Two"}},
			}}})
		case "session/load":
			session, mcp = p.SessionID, string(p.McpServers)
			data, err := os.ReadFile(historyPath)
			if err != nil || json.Unmarshal(data, &history) != nil {
				raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "error": map[string]any{"code": -32602, "message": "session missing"}})
				os.Stdout.Write(append(raw, '\n'))
			} else {
				reply(rpc.ID, map[string]any{})
			}
		case "session/set_config_option":
			model = p.Value
			reply(rpc.ID, map[string]any{})
		case "session/close":
			reply(rpc.ID, map[string]any{})
		case "session/prompt":
			input := ""
			for _, part := range p.Prompt {
				input += part.Text
			}
			history = append(history, input)
			data, _ := json.Marshal(history)
			_ = os.WriteFile(historyPath, data, 0600)
			if input == "permission" {
				pending = rpc.ID
				notify(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call-1", "title": "synthetic shell", "rawInput": map[string]string{"command": "printf synthetic"}, "status": "pending"})
				raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "permission-1", "method": "session/request_permission", "params": map[string]any{"sessionId": session, "toolCall": map[string]string{"toolCallId": "call-1"}, "options": []any{map[string]string{"optionId": "once", "kind": "allow_once"}, map[string]string{"optionId": "reject", "kind": "reject_once"}}}})
				os.Stdout.Write(append(raw, '\n'))
			} else {
				finish(rpc.ID)
			}
		case "session/cancel":
			if pending != nil {
				reply(pending, map[string]string{"stopReason": "cancelled"})
				pending = nil
			}
		case "":
			if string(rpc.ID) == `"permission-1"` && pending != nil {
				var value struct {
					Outcome struct{ Outcome, OptionID string }
				}
				_ = json.Unmarshal(rpc.Result, &value)
				history = append(history, "decision:"+value.Outcome.Outcome)
				finish(pending)
				pending = nil
			}
		}
	}
	os.Exit(0)
}

func opencodeFixture(t *testing.T) (Config, Task) {
	t.Helper()
	t.Setenv("DUO_TEST_OPENCODE_ACP", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Config{OpenCode: exe}, Task{ID: "opencode-fixture", Engine: "opencode", Workspace: t.TempDir(), Model: "fixture/model-one", Mode: &WorkMode{ID: "work", Permission: "workspace", Approval: "request", AllowNetwork: boolPtr(true)}}
}

type opencodeFixtureTurn struct {
	Session string   `json:"session"`
	Model   string   `json:"model"`
	Cwd     string   `json:"cwd"`
	History []string `json:"history"`
	MCP     []struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		URL     string `json:"url"`
		Headers []struct{ Name, Value string }
	} `json:"mcp"`
}

func TestOpenCodeNativeSessionResumeAndHardwareMCP(t *testing.T) {
	c, task := opencodeFixture(t)
	run := func(input string) opencodeFixtureTurn {
		t.Helper()
		session, result, err := runOpenCodeACP(context.Background(), c, task, input, func(string, string) {})
		if err != nil {
			t.Fatalf("turn %q failed: %v", input, err)
		}
		var got opencodeFixtureTurn
		if json.Unmarshal([]byte(result), &got) != nil {
			t.Fatalf("unexpected fixture result: %s", result)
		}
		if got.Session != "fixture-opencode-session" || session != got.Session || (task.Session != "" && session != task.Session) {
			t.Fatalf("session was not reused: %q / %q", session, got.Session)
		}
		task.Session = session
		return got
	}

	first := run("one")
	if first.Model != "fixture/model-one" || len(first.History) != 1 || len(first.MCP) != 0 {
		t.Fatalf("first turn did not select the requested model: %+v", first)
	}
	// Only session/new carries the workspace; a resumed session keeps its own.
	if first.Cwd != task.Workspace {
		t.Fatalf("workspace was not the ACP cwd: %q", first.Cwd)
	}

	ai, address := harnessMCPFixture(t)
	token, release := ai.issue(context.Background(), task.ID, "run-opencode", nil)
	defer release()
	c.HardwareAI = &HardwareRuntime{URL: address, Token: token}
	second := run("two")
	if second.Model != "fixture/model-one" || len(second.History) != 2 || len(second.MCP) != 1 {
		t.Fatalf("hardware grant did not reopen the native session with history: %+v", second)
	}
	mcp := second.MCP[0]
	if mcp.Type != "http" || mcp.Name != "duo_hardware" || mcp.URL != address || len(mcp.Headers) != 1 || mcp.Headers[0].Value != "Bearer "+token {
		t.Fatalf("hardware MCP declaration was not scoped to this run: %+v", mcp)
	}
}

func TestOpenCodeAdmissionAndErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		mode *WorkMode
		want bool
	}{
		{"read is never interactive", &WorkMode{Permission: "read", Approval: "never"}, false},
		{"workspace asks once", &WorkMode{Permission: "workspace", Approval: "request", AllowNetwork: boolPtr(true)}, true},
		{"full asks once", &WorkMode{Permission: "full", Approval: "never"}, true},
		{"default asks once", nil, true},
	} {
		got, err := opencodeInvocation(Task{Mode: test.mode})
		if err != nil || got != test.want {
			t.Fatalf("%s: interactive=%v err=%v", test.name, got, err)
		}
	}
	if _, err := opencodeInvocation(Task{NoTools: true}); err == nil || !strings.Contains(err.Error(), "知识整理") {
		t.Fatal("knowledge tasks must be refused until an isolated summary session exists")
	}
	if _, err := opencodeInvocation(Task{Mode: &WorkMode{Permission: "auto", Approval: "auto"}}); err == nil {
		t.Fatal("native auto risk review is not supported")
	}
	if _, err := opencodeInvocation(Task{Mode: &WorkMode{Permission: "workspace", Approval: "request", AllowNetwork: boolPtr(false)}}); err == nil {
		t.Fatal("network-disabled mode is not supported")
	}
	option := harnessConfigOption{ID: "model"}
	option.Options = append(option.Options, struct {
		Value string `json:"value"`
	}{Value: "fixture/model-one"})
	if err := opencodeModelSupported([]harnessConfigOption{option}, "fixture/unknown"); err == nil || !strings.Contains(err.Error(), "UNSUPPORTED_MODEL") {
		t.Fatal("a model outside the native catalogue must be refused")
	}
	if err := opencodeModelSupported(nil, "fixture/anything"); err != nil {
		t.Fatal("a catalogue that does not advertise models must not block selection")
	}
	if models := parseOpenCodeModels("opencode/big-pickle\nprovider/model with space\n\nbroken\n"); len(models) != 1 || models[0].ID != "opencode/big-pickle" || models[0].Engine != "opencode" {
		t.Fatalf("unexpected catalogue parse: %+v", models)
	}
}

func TestOpenCodeRefusesHTTPMCPIncapableCLI(t *testing.T) {
	c, task := opencodeFixture(t)
	t.Setenv("DUO_TEST_OPENCODE_NO_MCP", "1")
	ai, address := harnessMCPFixture(t)
	token, release := ai.issue(context.Background(), task.ID, "run-opencode", nil)
	defer release()
	c.HardwareAI = &HardwareRuntime{URL: address, Token: token}
	_, _, err := runOpenCodeACP(context.Background(), c, task, "one", func(string, string) {})
	if err == nil || !strings.Contains(err.Error(), "HTTP MCP") {
		t.Fatalf("granted tools must fail closed when the CLI cannot mount them: %v", err)
	}
}

func TestOpenCodePermissionUsesItsOwnApprovalMethod(t *testing.T) {
	c, task := opencodeFixture(t)
	seen := make(chan string, 1)
	ctx := withCodexInteraction(context.Background(), func(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		select {
		case seen <- method:
		default:
		}
		request, err := parseHarnessPermission(params)
		if err != nil {
			return nil, err
		}
		for _, option := range request.Options {
			if option.Kind == "allow_once" {
				return json.Marshal(map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": option.ID}})
			}
		}
		return nil, errors.New("missing allow_once")
	})
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, result, err := runOpenCodeACP(ctx, c, task, "permission", func(string, string) {})
	if err != nil {
		t.Fatalf("permission turn failed: %v", err)
	}
	if !strings.Contains(result, "decision:selected") {
		t.Fatalf("approval was not returned to the agent: %s", result)
	}
	select {
	case method := <-seen:
		if method != opencodePermissionMethod || !isACPToolPermission(method) {
			t.Fatalf("unexpected approval method %q", method)
		}
	default:
		t.Fatal("no approval was requested")
	}
}

func TestOpenCodeBinaryResolution(t *testing.T) {
	remote := opencodeBinary(Config{Distro: "Ubuntu-22.04"})
	if remote != "opencode" {
		t.Fatalf("remote targets must resolve through their own PATH: %q", remote)
	}
	if local := opencodeBinary(Config{}); local == "" || filepath.Base(local) == "" {
		t.Fatal("local targets must resolve an executable")
	}
}
