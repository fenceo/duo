package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func validEngine(engine string) bool {
	return engine == "codex" || engine == "claude" || engine == "deepseek-harness"
}
func engineName(engine string) string {
	if engine == "claude" {
		return "Claude Code"
	}
	if engine == "deepseek-harness" {
		return "DeepSeek Harness"
	}
	return "Codex"
}
func validEngineReasoning(engine, effort string) bool {
	if engine == "codex" {
		return validReasoning(effort)
	}
	if engine == "deepseek-harness" {
		switch effort {
		case "", "off", "low", "high", "max":
			return true
		}
		return false
	}
	switch effort {
	case "", "low", "medium", "high", "xhigh", "max":
		return true
	}
	return false
}
func claudeBinary(kind string) string {
	if kind != "windows" {
		return "claude"
	}
	home, _ := os.UserHomeDir()
	for _, path := range []string{filepath.Join(home, ".local", "bin", "claude.exe"), filepath.Join(home, "AppData", "Local", "Microsoft", "WinGet", "Links", "claude.exe")} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return "claude.exe"
}
func claudeArgs(t Task) []string {
	// Headless runs keep normal tool permissions. acceptEdits allows workspace
	// edits; commands needing authorization must already be allowed by the user.
	permission := "acceptEdits"
	if t.Mode != nil {
		switch t.Mode.Permission {
		case "read":
			permission = "plan"
		case "full":
			permission = "bypassPermissions"
		}
	}
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", permission}
	if permission == "plan" {
		args = append(args, "--disallowedTools", "Write,Edit,NotebookEdit")
	}
	if len(t.Files) > 0 {
		args = append(args, "--input-format", "stream-json")
	}
	if t.Model != "" {
		args = append(args, "--model", t.Model)
	}
	if t.ReasoningEffort != "" {
		args = append(args, "--effort", t.ReasoningEffort)
	}
	if t.Session != "" {
		args = append(args, "--resume", t.Session)
	}
	return args
}

const claudeLauncher = `import sys,subprocess,json,os
os.chdir(sys.argv[1])
p=subprocess.Popen(sys.argv[2:],start_new_session=True)
print(json.dumps({"type":"jianzuo.process","pid":p.pid}),flush=True)
sys.exit(p.wait())
`

func engineCommand(c Config, t Task) (*exec.Cmd, error) {
	engine := t.Engine
	if engine == "" {
		engine = "codex"
	}
	if !validEngine(engine) || !validEngineReasoning(engine, t.ReasoningEffort) {
		return nil, errors.New("任务的 AI 工具或推理强度无效")
	}
	if engine == "deepseek-harness" {
		return nil, errors.New("DeepSeek Harness 必须通过 SDK 运行")
	}
	args := codexArgs(c, t)
	binary := c.Codex
	launch := launcher
	if engine == "claude" {
		args = claudeArgs(t)
		binary = c.Claude
		if binary == "" {
			kind := "windows"
			if c.Distro != "" || c.SSHHost != "" {
				kind = "linux"
			}
			binary = claudeBinary(kind)
		}
		launch = claudeLauncher
	}
	if c.Distro != "" || c.SSHHost != "" {
		if c.HardwareAI != nil {
			if engine == "claude" {
				args = append(args, claudeHardwareArgs(c.HardwareAI)...)
			}
			base := []string{"python3", "-u", "-c", hardwareLauncher, t.Workspace, binary}
			return command(c, withEngineEnv(append(base, args...), c.EngineEnv)...), nil
		}
		base := []string{"python3", "-u", "-c", launch}
		if engine == "claude" {
			base = append(base, t.Workspace)
		}
		return command(c, withEngineEnv(append(append(base, binary), args...), c.EngineEnv)...), nil
	}
	if engine == "claude" && c.HardwareAI != nil {
		args = append(args, claudeHardwareArgs(c.HardwareAI)...)
	}
	cmd := command(c, append([]string{binary}, args...)...)
	applyEngineEnv(cmd, c.EngineEnv)
	if engine == "claude" {
		cmd.Dir = t.Workspace
	}
	return cmd, nil
}
func modelsForEngine(ctx context.Context, env Environment, engine string) (ModelList, error) {
	if engine == "" || engine == "codex" {
		return modelsForEnvironment(ctx, env)
	}
	if engine == "deepseek-harness" {
		models := mergeConfiguredModels([]ModelOption{{ID: "deepseek-flash", Name: "DeepSeek Flash"}}, env.Models)
		for i := range models {
			// Harness has its own effort vocabulary; do not use Codex's levels.
			models[i].ReasoningLevels = []string{"off", "low", "high", "max"}
			if !validEngineReasoning(engine, models[i].DefaultReasoning) {
				models[i].DefaultReasoning = ""
			}
		}
		return ModelList{Source: "DeepSeek Harness SDK 模型", Message: "模型 ID 通过 SDK 传给此环境配置的 provider；可添加自定义模型 ID，实际可用性以 provider 为准。", Models: models}, nil
	}
	if engine != "claude" {
		return ModelList{}, errors.New("AI 工具无效")
	}
	levels := []string{"low", "medium", "high", "xhigh", "max"}
	return ModelList{Source: "Claude Code 模型别名", Message: "使用此环境的 Claude 登录与服务配置；模型可用性以该账号为准。", Models: []ModelOption{
		{ID: "sonnet", Name: "Sonnet", ReasoningLevels: levels}, {ID: "opus", Name: "Opus", ReasoningLevels: levels},
		{ID: "haiku", Name: "Haiku", ReasoningLevels: []string{}}, {ID: "fable", Name: "Fable", ReasoningLevels: levels},
	}}, nil
}
func checkEngine(c Config, engine string) (string, error) {
	if engine == "codex" {
		return checkEnvironment(c)
	}
	if engine == "deepseek-harness" {
		return checkHarness(c)
	}
	if engine != "claude" {
		return "", errors.New("AI 工具无效")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := engineCheckCommand(c, c.Claude, "auth", "status", "--text")
	bounded := commandWithContext(ctx, cmd)
	hideCommand(bounded)
	b, err := bounded.CombinedOutput()
	if err != nil && len(b) == 0 {
		return err.Error(), err
	}
	return string(b), err
}

// Only stdout's reader owns this state. A result event is required, so a
// truncated stream or a denied tool call cannot be reported as success.
type claudeStream struct {
	session, result, failure, lastAssistant string
	finished                                bool
}

func (s *claudeStream) consume(line string, emit func(string, string)) {
	var v struct {
		Type        string          `json:"type"`
		Subtype     string          `json:"subtype"`
		Usage       json.RawMessage `json:"usage"`
		Session     string          `json:"session_id"`
		Parent      string          `json:"parent_tool_use_id"`
		Result      string          `json:"result"`
		IsError     bool            `json:"is_error"`
		Errors      []string        `json:"errors"`
		Attempt     int             `json:"attempt"`
		ErrorStatus int             `json:"error_status"`
		MCPServers  []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"mcp_servers"`
		PermissionDenials []struct {
			Tool string `json:"tool_name"`
		} `json:"permission_denials"`
		Message struct {
			Content []struct {
				Type    string          `json:"type"`
				Text    string          `json:"text"`
				Name    string          `json:"name"`
				Input   json.RawMessage `json:"input"`
				IsError bool            `json:"is_error"`
				Content json.RawMessage `json:"content"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &v); err != nil {
		emit("log", line)
		return
	}
	if v.Parent != "" {
		return
	} // A subagent session must never replace the task session.
	if v.Session != "" && s.session != v.Session {
		s.session = v.Session
		emit("session", s.session)
	}
	switch v.Type {
	case "system":
		if v.Subtype == "init" {
			for _, server := range v.MCPServers {
				if server.Name == "jianzuo_hardware" {
					if server.Status == "connected" {
						emit("progress", "Claude 已连接任务硬件工具")
					} else {
						emit("error", "Claude 未连接任务硬件工具，请检查执行环境到简作的网络和 CLI 配置")
					}
				}
			}
		}
		if v.Subtype == "api_retry" {
			message := fmt.Sprintf("Claude 服务请求重试 %d（HTTP %d）", v.Attempt, v.ErrorStatus)
			if v.ErrorStatus == 401 || v.ErrorStatus == 403 {
				message += "：认证或访问权限被拒绝，请检查该环境的 Claude 账号/网关配置"
			}
			emit("progress", message)
		}
	case "assistant":
		texts := []string{}
		for _, b := range v.Message.Content {
			switch b.Type {
			case "text":
				texts = append(texts, b.Text)
			case "tool_use":
				emit("tool", b.Name+"\n"+string(b.Input))
			}
		}
		if text := strings.Join(texts, "\n"); text != "" {
			s.lastAssistant = text
			emit("assistant", text)
		}
	case "user":
		for _, b := range v.Message.Content {
			if b.Type == "tool_result" {
				content := string(b.Content)
				var text string
				if json.Unmarshal(b.Content, &text) == nil {
					content = text
				}
				if len(content) > 24000 {
					content = content[:24000] + "\n…"
				}
				if b.IsError {
					content = "工具执行失败：" + content
				}
				emit("tool", content)
			}
		}
	case "result":
		emitUsage("claude", v.Usage, emit)
		s.finished = true
		s.result = v.Result
		if s.result != "" && s.result != s.lastAssistant {
			emit("assistant", s.result)
		}
		if v.IsError || strings.HasPrefix(v.Subtype, "error") {
			s.failure = strings.Join(v.Errors, "\n")
			if s.failure == "" {
				s.failure = v.Result
			}
			if s.failure == "" {
				s.failure = "Claude Code 返回错误：" + v.Subtype
			}
		}
		if len(v.PermissionDenials) > 0 {
			names := []string{}
			for _, p := range v.PermissionDenials {
				names = append(names, p.Tool)
			}
			s.failure = fmt.Sprintf("Claude 工具尚未授权：%s。请在该任务终端中运行 claude 配置权限，再回到任务继续。", strings.Join(names, ", "))
		}
		if s.failure != "" {
			emit("error", s.failure)
		}
	}
}
