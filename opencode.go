package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// OpenCode owns its native session store. Every Duo turn talks to a fresh
// `opencode acp` process over stdio and resumes the same native session id, so
// restarting Duo or switching models never replays or loses the conversation.
var opencodeWriters = struct {
	sync.Mutex
	workers map[string]*harnessWorker
}{workers: map[string]*harnessWorker{}}

// OpenCode exposes no selectable ACP modes, so Duo maps its permission presets
// onto the ACP approval channel: a read-only task answers every native request
// with a cancellation, while other presets surface one-time user decisions.
func opencodeInvocation(t Task) (bool, error) {
	if t.Mode != nil {
		if t.Mode.Approval == "auto" {
			return false, errors.New("OpenCode 不支持 Codex 自动风险评审，请改用请求确认或直接拒绝")
		}
		if t.Mode.AllowNetwork != nil && !*t.Mode.AllowNetwork {
			return false, errors.New("OpenCode 暂不支持禁用网络保证，请选择允许联网的模式或使用 Codex")
		}
		switch t.Mode.Permission {
		case "", "workspace", "full", "read":
		default:
			return false, errors.New("OpenCode 权限模式无效")
		}
	}
	if t.NoTools {
		return false, errors.New("OpenCode 的独立知识整理尚未接入，不会使用聊天工具权限整理知识")
	}
	// Hardware tools are attached by session/new, never by a launch-time
	// environment injection, so they do not change the approval channel.
	return t.Mode == nil || t.Mode.Permission != "read", nil
}

// Resolve the native binary without shell evaluation. A managed install always
// wins; otherwise the standard npm global layout on this host is preferred over
// PATH, because Go cannot execute npm's .cmd shim directly.
func opencodeBinary(c Config) string {
	if c.Distro != "" || c.SSHHost != "" {
		return "opencode"
	}
	home, _ := os.UserHomeDir()
	for _, path := range []string{
		filepath.Join(home, "AppData", "Roaming", "npm", "node_modules", "opencode-ai", "bin", "opencode.exe"),
		filepath.Join(home, ".opencode", "bin", "opencode.exe"),
	} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return "opencode.exe"
}

func startOpenCode(ctx context.Context, c Config, t Task, interactive bool) (*harnessWorker, error) {
	binary := c.OpenCode
	if binary == "" {
		binary = opencodeBinary(c)
	}
	var cmd *exec.Cmd
	if c.Distro != "" || c.SSHHost != "" {
		cmd = nativeTargetCommand(c, withEngineEnv([]string{"python3", "-c", claudeLauncher, t.Workspace, binary, "acp"}, c.EngineEnv)...)
	} else {
		cmd = exec.Command(binary, "acp")
		cmd.Dir = t.Workspace
		applyNativeEnv(cmd, c.EngineEnv)
	}
	if os.Getenv("DUO_TEST_OPENCODE_ACP") == "1" {
		cmd = exec.Command(binary, "-test.run=^TestOpenCodeProcessFixture$")
		cmd.Dir = t.Workspace
		applyNativeEnv(cmd, c.EngineEnv)
	}
	hideCommand(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		return nil, err
	}
	errout, err := cmd.StderrPipe()
	if err != nil {
		in.Close()
		out.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		in.Close()
		out.Close()
		errout.Close()
		return nil, err
	}
	w := &harnessWorker{engine: "opencode", c: c, cmd: cmd, in: in, frames: make(chan codexRPC, 128), fault: make(chan error, 1), done: make(chan struct{}), halt: make(chan struct{}), interactive: interactive}
	var scans sync.WaitGroup
	scans.Add(2)
	go func() { defer scans.Done(); w.scan(out, true) }()
	go func() { defer scans.Done(); w.scan(errout, false) }()
	go func() {
		scans.Wait()
		err := cmd.Wait()
		w.mu.Lock()
		w.exitErr = err
		w.mu.Unlock()
		close(w.done)
	}()
	data, err := w.request(ctx, "initialize", map[string]any{"protocolVersion": 1, "clientInfo": map[string]any{"name": "Duo", "version": version}, "clientCapabilities": map[string]any{"fs": map[string]bool{"readTextFile": false, "writeTextFile": false}, "terminal": false}})
	if err != nil {
		stopErr := w.stop()
		w.mu.Lock()
		if w.diagnosticErr != nil && ctx.Err() == nil {
			err = w.diagnosticErr
		}
		w.mu.Unlock()
		return w, errors.Join(err, stopErr)
	}
	var hello struct {
		ProtocolVersion   int `json:"protocolVersion"`
		AgentInfo         struct{ Name string } `json:"agentInfo"`
		AgentCapabilities struct {
			LoadSession        bool `json:"loadSession"`
			MCPCapabilities    struct{ HTTP bool } `json:"mcpCapabilities"`
			PromptCapabilities struct{ Image bool } `json:"promptCapabilities"`
			SessionCapabilities struct {
				Resume *json.RawMessage `json:"resume"`
				Close  *json.RawMessage `json:"close"`
			} `json:"sessionCapabilities"`
		} `json:"agentCapabilities"`
	}
	if json.Unmarshal(data, &hello) != nil || hello.ProtocolVersion != 1 || hello.AgentInfo.Name != "OpenCode" || (!hello.AgentCapabilities.LoadSession && hello.AgentCapabilities.SessionCapabilities.Resume == nil) {
		return w, errors.Join(errors.New("OpenCode CLI 未提供受支持的 ACP v1 会话恢复接口，请升级目标环境的 opencode"), w.stop())
	}
	if c.HardwareAI != nil && !hello.AgentCapabilities.MCPCapabilities.HTTP {
		return w, errors.Join(errors.New("OpenCode ACP 不支持 HTTP MCP，请升级目标环境的 opencode 后再使用 Duo 桌面/硬件工具（未发送模型请求）"), w.stop())
	}
	w.imageInput = hello.AgentCapabilities.PromptCapabilities.Image
	return w, nil
}

func runOpenCodeACP(ctx context.Context, c Config, t Task, input string, emit func(string, string)) (session, result string, runErr error) {
	session = t.Session
	interactive, err := opencodeInvocation(t)
	if err != nil {
		return session, "", err
	}
	if c.HardwareAI != nil {
		emit("progress", "正在从执行环境连接 Duo 桌面/硬件工具…")
	}
	hardware, err := resolveHardwareRuntime(ctx, c, "OpenCode")
	if err != nil {
		return session, "", err
	}
	c.HardwareAI = hardware
	key := session
	if key == "" {
		key = "new:" + t.ID
	}
	opencodeWriters.Lock()
	if old := opencodeWriters.workers[key]; old != nil {
		select {
		case <-old.done:
			delete(opencodeWriters.workers, key)
		default:
			opencodeWriters.Unlock()
			return session, "", errors.New("OpenCode 原生会话仍在运行或停止中，请稍后重试")
		}
	}
	// Reserve before starting; even a cold session may have only one writer.
	placeholder := &harnessWorker{done: make(chan struct{})}
	opencodeWriters.workers[key] = placeholder
	opencodeWriters.Unlock()
	defer func() {
		if runErr != nil {
			runErr = errors.New(strings.ReplaceAll(runErr.Error(), "Harness", "OpenCode"))
		}
	}()
	setup, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	w, err := startOpenCode(setup, c, t, interactive)
	if err != nil {
		opencodeWriters.Lock()
		if w == nil {
			delete(opencodeWriters.workers, key)
		} else {
			select {
			case <-w.done:
				delete(opencodeWriters.workers, key)
			default:
				opencodeWriters.workers[key] = w
			}
		}
		opencodeWriters.Unlock()
		return session, "", err
	}
	opencodeWriters.Lock()
	opencodeWriters.workers[key] = w
	opencodeWriters.Unlock()
	defer func() {
		if err := finishNativeACP(w); err != nil {
			runErr = errors.Join(runErr, err)
		}
		opencodeWriters.Lock()
		defer opencodeWriters.Unlock()
		select {
		case <-w.done:
			for _, id := range []string{key, session} {
				if opencodeWriters.workers[id] == w {
					delete(opencodeWriters.workers, id)
				}
			}
		default:
		}
	}()
	method := "session/new"
	params := map[string]any{"cwd": t.Workspace, "mcpServers": harnessMCPServers(hardware)}
	if session != "" {
		method = "session/load"
		params["sessionId"] = session
		emit("progress", "正在恢复 OpenCode 原生会话和上下文…")
	}
	data, err := w.request(setup, method, params)
	if err != nil {
		return session, "", fmt.Errorf("OpenCode 原生会话创建/恢复失败，未清空历史或新建替代会话：%w", err)
	}
	if session == "" {
		var response struct {
			SessionID     string               `json:"sessionId"`
			ConfigOptions []harnessConfigOption `json:"configOptions"`
		}
		if json.Unmarshal(data, &response) != nil || response.SessionID == "" {
			return "", "", errors.New("OpenCode 未返回原生会话 ID")
		}
		session = response.SessionID
		if t.Model != "" {
			if err = opencodeModelSupported(response.ConfigOptions, t.Model); err != nil {
				return "", "", err
			}
		}
	}
	w.session = session
	opencodeWriters.Lock()
	if owner := opencodeWriters.workers[session]; owner != nil && owner != w {
		opencodeWriters.Unlock()
		return session, "", errors.New("OpenCode 返回已被其他执行占用的会话 ID")
	}
	opencodeWriters.workers[session] = w
	opencodeWriters.Unlock()
	emit("session", session)
	if t.Model != "" {
		if _, err = w.request(setup, "session/set_config_option", map[string]any{"sessionId": session, "configId": "model", "value": t.Model}); err != nil {
			return session, "", err
		}
	}
	cancel()
	_, result, runErr = w.prompt(ctx, input, func(kind, value string) {
		if kind == "progress" {
			value = strings.ReplaceAll(value, "Harness", "OpenCode")
		}
		emit(kind, value)
	}, t.Files...)
	return session, result, runErr
}

// The native model catalogue is authoritative for this session: only a value it
// advertises may be selected, so a typo never silently runs another model.
func opencodeModelSupported(options []harnessConfigOption, model string) error {
	for _, option := range options {
		if option.ID != "model" {
			continue
		}
		for _, value := range option.Options {
			if value.Value == model {
				return nil
			}
		}
		return fmt.Errorf("OpenCode [UNSUPPORTED_MODEL]：%s 不在本环境 OpenCode 的模型目录中，请重读模型列表后再选择", model)
	}
	return nil
}

var opencodeModelIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:@+\-]+/[A-Za-z0-9_./:@+\-]+$`)

func parseOpenCodeModels(stdout string) []ModelOption {
	models := []ModelOption{}
	seen := map[string]bool{}
	if len(stdout) > 2*1024*1024 {
		return models
	}
	for _, line := range strings.Split(strings.ReplaceAll(stdout, "\r\n", "\n"), "\n") {
		id := strings.TrimSpace(line)
		if len(id) <= 200 && opencodeModelIDPattern.MatchString(id) && !seen[id] {
			seen[id] = true
			models = append(models, ModelOption{ID: id, Engine: "opencode", Origin: "native"})
		}
	}
	return models
}

func opencodeModelsCommand(ctx context.Context, env Environment, selected map[string]string) *exec.Cmd {
	path := env.OpenCode
	if path == "" {
		path = opencodeBinary(Config{})
	}
	workspace := ""
	if len(env.Workspaces) > 0 {
		workspace = env.Workspaces[0]
	}
	if env.Type == "windows" {
		cmd := exec.CommandContext(ctx, path, "models")
		cmd.Dir = workspace
		applyNativeEnv(cmd, selected)
		return cmd
	}
	args := withEngineEnv([]string{path, "models"}, selected)
	quoted := []string{}
	for _, arg := range args {
		quoted = append(quoted, posixQuote(arg))
	}
	script := "exec " + strings.Join(quoted, " ")
	if workspace != "" {
		script = "cd -- " + posixQuote(workspace) + " && " + script
	}
	return commandWithContext(ctx, environmentProbeCommand(env, "sh", "-lc", script))
}

func configuredOpenCodeCatalog(ctx context.Context, env Environment, selected map[string]string) (ModelList, error) {
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := opencodeModelsCommand(bounded, env, selected)
	stdout, stderr := &cappedOutput{limit: 2 * 1024 * 1024}, &cappedOutput{limit: 4096}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = time.Second
	hideCommand(cmd)
	err := cmd.Run()
	if ctx.Err() != nil {
		return ModelList{}, ctx.Err()
	}
	list := ModelList{Models: []ModelOption{}, Source: "OpenCode 原生 CLI 模型目录", Status: "empty"}
	if err != nil {
		list.Message = "无法读取目标环境的 OpenCode CLI 模型目录，请先安装 opencode 并在该环境执行 opencode auth login。"
	} else {
		list.Models = parseOpenCodeModels(string(stdout.Bytes()))
		if len(list.Models) > 0 {
			list.Status = "ready"
		}
	}
	list = mergeEngineCatalog(list, env, "opencode", "")
	if len(list.Models) > 0 && list.Status == "empty" {
		list.Status = "fallback"
	}
	list.Message += "目录来自原生 CLI 和显式配置；列出模型不代表已验证推理请求。"
	return list, nil
}
