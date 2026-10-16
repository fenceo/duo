package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ZCode headless contract verified against zcode 0.16.9:
// `zcode --prompt <text> --json [--mode plan|build|edit|yolo] [--resume sess_...] [--cwd dir]`
// prints one JSON object with sessionId, response, usage and projection.
// Native login lives in the target environment (`zcode login`); Duo never stores keys.

type zcodePromptResult struct {
	SessionID string `json:"sessionId"`
	TraceID   string `json:"traceId"`
	TurnID    string `json:"turnId"`
	Response  string `json:"response"`
	Usage     struct {
		InputTokens  int `json:"inputTokens"`
		OutputTokens int `json:"outputTokens"`
		TotalTokens  int `json:"totalTokens"`
	} `json:"usage"`
	Projection struct {
		Status        string `json:"status"`
		ContextUsed   int    `json:"contextUsed"`
		ContextWindow int    `json:"contextWindow"`
	} `json:"projection"`
	EventCount int `json:"eventCount"`
}

func zcodeInvocation(t Task) error {
	if t.Mode != nil {
		if t.Mode.Approval == "auto" {
			return errors.New("ZCode 不支持 Codex 自动风险评审，请改用请求确认或直接拒绝")
		}
		if t.Mode.AllowNetwork != nil && !*t.Mode.AllowNetwork {
			return errors.New("ZCode 暂不支持禁用网络保证，请选择允许联网的模式")
		}
		switch t.Mode.Permission {
		case "", "workspace", "full", "read":
		default:
			return errors.New("ZCode 权限模式无效")
		}
	}
	if t.NoTools {
		return errors.New("ZCode 的独立知识整理尚未接入")
	}
	return nil
}

func zcodeMode(t Task) string {
	if t.Mode != nil {
		switch t.Mode.Permission {
		case "read":
			return "plan"
		case "full":
			return "yolo"
		}
	}
	return "build"
}

func zcodeBinary(c Config) string {
	if c.ZCode != "" {
		return c.ZCode
	}
	if c.Distro != "" || c.SSHHost != "" {
		return "zcode"
	}
	home, _ := os.UserHomeDir()
	for _, path := range []string{
		filepath.Join(home, "AppData", "Local", "Programs", "ZCode", "resources", "glm", "zcode.cjs"),
		filepath.Join(home, "AppData", "Roaming", "npm", "zcode.cmd"),
	} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return "zcode.exe"
}

func zcodeArgs(t Task, input string) []string {
	args := []string{"--prompt", input, "--json", "--mode", zcodeMode(t), "--cwd", t.Workspace, "--surface", "terminal"}
	if t.Session != "" {
		args = append(args, "--resume", t.Session)
	}
	return args
}

func parseZcodePromptResult(raw []byte) (zcodePromptResult, error) {
	raw = bytes.TrimSpace(raw)
	var parsed zcodePromptResult
	if err := json.Unmarshal(raw, &parsed); err == nil && parsed.SessionID != "" {
		return parsed, nil
	}
	// Packaging warnings may precede the JSON object on stdout.
	if idx := bytes.LastIndexByte(raw, '{'); idx > 0 {
		if err := json.Unmarshal(raw[idx:], &parsed); err == nil && parsed.SessionID != "" {
			return parsed, nil
		}
	}
	return zcodePromptResult{}, errors.New("ZCode 未返回可解析的 JSON 结果，请检查 CLI 版本与登录状态")
}

func runZCodePrompt(ctx context.Context, c Config, t Task, input string, emit func(string, string)) (session, result string, runErr error) {
	if err := zcodeInvocation(t); err != nil {
		return t.Session, "", err
	}
	binary := zcodeBinary(c)
	args := zcodeArgs(t, input)
	var cmd *exec.Cmd
	if strings.HasSuffix(binary, ".cjs") && c.Distro == "" && c.SSHHost == "" {
		cmd = exec.Command("node", append([]string{binary}, args...)...)
		cmd.Dir = t.Workspace
		applyNativeEnv(cmd, c.EngineEnv)
	} else if c.Distro != "" || c.SSHHost != "" {
		remote := append([]string{binary}, args...)
		cmd = nativeTargetCommand(c, withEngineEnv(append([]string{"python3", "-c", claudeLauncher, t.Workspace}, remote...), c.EngineEnv)...)
	} else {
		cmd = exec.Command(binary, args...)
		cmd.Dir = t.Workspace
		applyNativeEnv(cmd, c.EngineEnv)
	}
	if os.Getenv("DUO_TEST_ZCODE_PROMPT") == "1" {
		cmd = exec.Command(binary, "-test.run=^TestZCodePromptFixture$")
		cmd.Dir = t.Workspace
		applyNativeEnv(cmd, c.EngineEnv)
	}
	hideCommand(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	emit("progress", "正在连接 ZCode："+binary)
	if err := cmd.Run(); err != nil {
		diagnostic := strings.TrimSpace(stderr.String())
		if len(diagnostic) > 800 {
			diagnostic = diagnostic[:800]
		}
		return t.Session, "", fmt.Errorf("ZCode 执行失败：%v %s", err, diagnostic)
	}
	parsed, err := parseZcodePromptResult(stdout.Bytes())
	if err != nil {
		return t.Session, "", err
	}
	session = parsed.SessionID
	result = strings.TrimSpace(parsed.Response)
	if result == "" {
		return session, "", errors.New("ZCode 没有返回最终回复，请展开执行日志检查")
	}
	if parsed.Usage.TotalTokens > 0 {
		usage, _ := json.Marshal(map[string]any{"input": parsed.Usage.InputTokens, "output": parsed.Usage.OutputTokens, "total": parsed.Usage.TotalTokens})
		emit("usage", string(usage))
	}
	if parsed.Projection.ContextWindow > 0 {
		ctxRaw, _ := json.Marshal(RunContext{Used: int64(parsed.Projection.ContextUsed), Size: int64(parsed.Projection.ContextWindow)})
		emit("context", string(ctxRaw))
	}
	emit("assistant", result)
	return session, result, nil
}
