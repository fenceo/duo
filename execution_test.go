package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A real child process exercises stdin, cwd, streaming, resume and cancellation
// without making a paid AI request or loading a user's CLI configuration.
func TestMain(m *testing.M) {
	if os.Getenv("JIANZUO_TEST_HARNESS_SDK") == "1" && len(os.Args) > 2 && os.Args[1] == "--profile" && os.Args[2] == "sdk" {
		runHarnessSDKFixture()
		os.Exit(0)
	}
	if os.Getenv("JIANZUO_TEST_CODEX_APP_SERVER") == "1" && len(os.Args) > 1 && os.Args[len(os.Args)-1] == "app-server" {
		runCodexAppServerFixture()
		os.Exit(0)
	}
	if os.Getenv("JIANZUO_TEST_CLAUDE") == "1" && len(os.Args) > 1 && os.Args[1] == "-p" {
		input, _ := io.ReadAll(os.Stdin)
		cwd, _ := os.Getwd()
		session := "fixture-session"
		for i, a := range os.Args {
			if a == "--resume" && i+1 < len(os.Args) {
				session = os.Args[i+1]
			}
		}
		enc := json.NewEncoder(os.Stdout)
		_ = enc.Encode(map[string]any{"type": "system", "subtype": "init", "session_id": session})
		if string(input) == "wait" {
			for {
				time.Sleep(time.Second)
			}
		}
		result, _ := json.Marshal(map[string]any{"input": string(input), "cwd": cwd, "args": os.Args[1:]})
		_ = enc.Encode(map[string]any{"type": "assistant", "session_id": session, "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": string(result)}}}})
		_ = enc.Encode(map[string]any{"type": "result", "subtype": "success", "session_id": session, "result": string(result), "is_error": false})
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestClaudeProcessRoundTripAndStop(t *testing.T) {
	t.Setenv("JIANZUO_TEST_CLAUDE", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	task := Task{Engine: "claude", Workspace: workspace, Model: "custom-model", ReasoningEffort: "high", Session: "exact-session"}
	messages := 0
	session, result, err := (CodexRunner{}).Run(context.Background(), Config{Claude: exe}, task, "精确要求\nkeep $() literal", func(kind, text string) {
		if kind == "assistant" {
			messages++
		}
	})
	var body struct {
		Input, Cwd string
		Args       []string
	}
	_ = json.Unmarshal([]byte(result), &body)
	if err != nil || session != "exact-session" || messages != 1 || body.Input != "精确要求\nkeep $() literal" || filepath.Clean(body.Cwd) != filepath.Clean(workspace) {
		t.Fatalf("round trip: %s %s %v %d", session, result, err, messages)
	}
	args := strings.Join(body.Args, "|")
	if !strings.Contains(args, "--effort|high") || !strings.Contains(args, "--resume|exact-session") || strings.Contains(args, "精确要求") || strings.Contains(args, "skip-permissions") {
		t.Fatal(args)
	}
	task.Session = ""
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := time.Now()
	_, _, err = (CodexRunner{}).Run(ctx, Config{Claude: exe}, task, "wait", func(kind, _ string) {
		if kind == "session" {
			cancel()
		}
	})
	// Use a fresh task session so init emits the session event and triggers Stop.
	if err == nil || time.Since(started) > 5*time.Second {
		t.Fatalf("stop: %v", err)
	}
}
func TestTaskEngineAndEffortPersistAndMigrate(t *testing.T) {
	f := &fakeRunner{}
	a := fixture(t, f)
	task, err := a.createWithExecution("Claude task", "/work", "sonnet", "claude", "high")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := a.store.task(task.ID)
	if err != nil || saved.Engine != "claude" || saved.ReasoningEffort != "high" {
		t.Fatal(saved, err)
	}
	all, err := a.store.tasks()
	if err != nil || len(all) != 1 || all[0].Engine != "claude" {
		t.Fatal(all, err)
	}
	for _, pair := range [][2]string{{"other", "high"}, {"claude", "ultra"}, {"codex", "oops"}} {
		if _, err := a.createWithExecution("bad", "/work", "", pair[0], pair[1]); err == nil {
			t.Fatal(pair)
		}
	}
	for _, source := range []string{"web", "feishu"} {
		run, err := a.submit(task.ID, "继续", "chat", source)
		if err != nil {
			t.Fatal(err)
		}
		waitUntil(t, func() bool {
			var status string
			_ = a.store.QueryRow("SELECT status FROM runs WHERE id=?", run.ID).Scan(&status)
			return status == "done"
		})
	}
	saved, _ = a.store.task(task.ID)
	if saved.Engine != "claude" || saved.ReasoningEffort != "high" || saved.Session != "test-session" {
		t.Fatal(saved)
	}
	if _, err := a.store.Exec("DELETE FROM task_execution WHERE task_id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	legacy, _ := a.store.task(task.ID)
	if legacy.Engine != "codex" || legacy.ReasoningEffort != "" {
		t.Fatal(legacy)
	}
}
func TestEngineCommandsAndModelMetadata(t *testing.T) {
	for _, session := range []string{"", "exact-native-session"} {
		task := Task{Workspace: "/tmp/中文 ' ; $()", Session: session, Model: "model", ReasoningEffort: "xhigh"}
		args := strings.Join(codexArgs(Config{}, task), "|")
		if !strings.Contains(args, `-c|model_reasoning_effort="xhigh"|exec`) {
			t.Fatal(args)
		}
		task.Engine = "claude"
		for _, env := range []Environment{{Type: "wsl", Distro: "Ubuntu", User: "dev", Claude: "/opt/claude"}, {Type: "ssh", Host: "board", User: "dev", Claude: "/opt/claude", Port: 2222}} {
			cmd, err := engineCommand(runtimeConfig(Config{}, env), task)
			if err != nil {
				t.Fatal(err)
			}
			args = strings.Join(cmd.Args, "|")
			if !strings.Contains(args, "/opt/claude") || !strings.Contains(args, "--effort") || strings.Contains(args, "--continue") {
				t.Fatal(args)
			}
			if session != "" && (!strings.Contains(args, "--resume") || !strings.Contains(args, session)) {
				t.Fatal(args)
			}
			if env.Type == "ssh" && !strings.Contains(args, "StrictHostKeyChecking=yes") {
				t.Fatal(args)
			}
		}
	}
	models, err := parseModels([]byte(`{"models":[{"slug":"a","supported_reasoning_levels":[{"effort":"low"},{"effort":"ultra"},{"effort":"bad"}],"default_reasoning_level":"low"},{"slug":"b","supported_reasoning_levels":[]},{"slug":"c"}]}`))
	if err != nil || len(models) != 3 || len(models[0].ReasoningLevels) != 2 || models[0].DefaultReasoning != "low" || models[1].ReasoningLevels == nil || models[2].ReasoningLevels != nil {
		t.Fatal(models, err)
	}
}
func TestCodexSandboxNetworkModes(t *testing.T) {
	networkArg := "sandbox_workspace_write.network_access=true"
	offline := false

	defaultArgs := strings.Join(codexArgs(Config{}, Task{Workspace: "/work"}), "|")
	if !strings.Contains(defaultArgs, `sandbox_mode="workspace-write"`) || !strings.Contains(defaultArgs, networkArg) {
		t.Fatal(defaultArgs)
	}

	workspaceArgs := strings.Join(codexArgs(Config{}, Task{Workspace: "/work", Mode: &WorkMode{Permission: "workspace", AllowNetwork: &offline}}), "|")
	if !strings.Contains(workspaceArgs, `sandbox_mode="workspace-write"`) || !strings.Contains(workspaceArgs, "sandbox_workspace_write.network_access=false") || strings.Contains(workspaceArgs, networkArg) {
		t.Fatal(workspaceArgs)
	}

	readArgs := strings.Join(codexArgs(Config{}, Task{Workspace: "/work", Mode: &WorkMode{Permission: "read"}}), "|")
	if !strings.Contains(readArgs, `sandbox_mode="read-only"`) || strings.Contains(readArgs, networkArg) {
		t.Fatal(readArgs)
	}

	fullArgs := strings.Join(codexArgs(Config{}, Task{Workspace: "/work", Mode: &WorkMode{Permission: "full"}}), "|")
	if !strings.Contains(fullArgs, `sandbox_mode="danger-full-access"`) || strings.Contains(fullArgs, networkArg) {
		t.Fatal(fullArgs)
	}

	claudeFullArgs := strings.Join(claudeArgs(Task{Mode: &WorkMode{Permission: "full"}}), "|")
	if !strings.Contains(claudeFullArgs, "--permission-mode|bypassPermissions") {
		t.Fatal(claudeFullArgs)
	}
}
func TestClaudeStreamErrorsAndSessionIsolation(t *testing.T) {
	emit := func(string, string) {}
	s := claudeStream{session: "root"}
	s.consume(`{"type":"assistant","parent_tool_use_id":"tool","session_id":"subagent","message":{"content":[{"type":"text","text":"child"}]}}`, emit)
	if s.session != "root" || s.lastAssistant != "" {
		t.Fatal(s)
	}
	s.consume(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"a"}},{"type":"text","text":"working"}]}}`, emit)
	s.consume(`{"type":"result","subtype":"success","session_id":"root","result":"done","permission_denials":[{"tool_name":"Bash"}]}`, emit)
	if !s.finished || s.failure == "" || !strings.Contains(s.failure, "Bash") {
		t.Fatal(s)
	}
	s = claudeStream{}
	s.consume(`{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["token expired"]}`, emit)
	if s.failure != "token expired" {
		t.Fatal(s)
	}
}
func TestTaskExecutionAPI(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	req := toolsClient(t, a)
	raw := req("/api/tasks", "POST", map[string]any{"title": "Claude API", "workspace": "/work", "engine": "claude", "reasoning_effort": "medium", "model": "sonnet"}, 201)
	var result struct{ Task Task }
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Task.Engine != "claude" || result.Task.ReasoningEffort != "medium" {
		t.Fatal(string(raw))
	}
	req("/api/tasks", "POST", map[string]any{"title": "bad", "workspace": "/work", "engine": "claude", "reasoning_effort": "ultra"}, 400)
	req(fmt.Sprintf("/api/environments/%s/models?engine=claude", a.config.get().DefaultEnvironment), "GET", nil, 200)
}

func TestTaskCreationRollsBackWhenModeWriteFails(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	if _, err := a.store.Exec("CREATE TRIGGER reject_task_mode BEFORE INSERT ON task_options BEGIN SELECT RAISE(ABORT,'reject mode'); END"); err != nil {
		t.Fatal(err)
	}
	req := toolsClient(t, a)
	c := a.config.get()
	req("/api/tasks", "POST", map[string]any{"title": "atomic", "workspace": c.Workspaces[0], "model": c.Model, "mode_id": "plan"}, 400)
	for _, table := range []string{"tasks", "task_environments", "task_execution", "task_options"} {
		var count int
		if err := a.store.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s kept %d rows after rollback", table, count)
		}
	}
}
