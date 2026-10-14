package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// MiMo owns its SQLite conversation history. Each turn gets a fresh ACP
// process and resumes the same native session; changing API config never
// relocates its native data directory or replays Duo's display history.
var mimoWriters = struct {
	sync.Mutex
	workers map[string]*harnessWorker
}{workers: map[string]*harnessWorker{}}

func mimoInvocation(t Task) (map[string]string, string, error) {
	mode := "build"
	permission := map[string]any{}
	if t.Mode != nil {
		if t.Mode.Approval == "auto" || (t.Mode.AllowNetwork != nil && !*t.Mode.AllowNetwork) {
			return nil, "", errors.New("MiMo Code 不支持 Codex 自动风险评审或禁用网络保证")
		}
		switch t.Mode.Permission {
		case "", "workspace":
		case "read":
			mode = "plan"
		case "full":
			permission["*"] = "allow"
		default:
			return nil, "", errors.New("MiMo 权限模式无效")
		}
	}
	if t.NoTools {
		return nil, "", errors.New("MiMo 独立知识整理尚未接入，不会使用聊天工具权限整理知识")
	}
	raw, _ := json.Marshal(permission)
	return map[string]string{"MIMOCODE_PERMISSION": string(raw), "MIMOCODE_DISABLE_AUTOUPDATE": "1", "MIMOCODE_DISABLE_CLAUDE_CODE": "1"}, mode, nil
}

func startMimo(ctx context.Context, c Config, t Task, variables map[string]string) (*harnessWorker, error) {
	binary := c.Mimo
	if binary == "" {
		binary = "mimo"
	}
	var cmd *exec.Cmd
	if c.Distro != "" || c.SSHHost != "" {
		cmd = mimoTargetCommand(c, withEngineEnv([]string{"python3", "-c", claudeLauncher, t.Workspace, binary, "acp", "--hostname", "127.0.0.1", "--port", "0"}, variables)...)
	} else {
		cmd = exec.Command(binary, "acp", "--hostname", "127.0.0.1", "--port", "0")
		cmd.Dir = t.Workspace
		applyMimoEnv(cmd, variables)
	}
	if os.Getenv("DUO_TEST_MIMO_ACP") == "1" {
		cmd = exec.Command(binary, "-test.run=^TestMimoProcessFixture$")
		cmd.Dir = t.Workspace
		applyMimoEnv(cmd, variables)
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
	// Full access supplies native allow defaults; explicit native ask rules
	// still deserve a one-time decision instead of being silently cancelled.
	w := &harnessWorker{engine: "mimo", c: c, cmd: cmd, in: in, frames: make(chan codexRPC, 128), fault: make(chan error, 1), done: make(chan struct{}), halt: make(chan struct{}), interactive: true}
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
	data, err := w.request(ctx, "initialize", map[string]any{"protocolVersion": 1, "clientInfo": map[string]string{"name": "Duo", "version": version}, "clientCapabilities": map[string]any{"fs": map[string]bool{"readTextFile": false, "writeTextFile": false}, "terminal": false}})
	if err != nil {
		stopErr := w.stop()
		// stdout EOF may arrive before the stderr scanner sees the fatal
		// diagnostic. Recheck after confirmed stop, without echoing config.
		w.mu.Lock()
		if w.diagnosticErr != nil && ctx.Err() == nil {
			err = w.diagnosticErr
		}
		w.mu.Unlock()
		return w, errors.Join(err, stopErr)
	}
	var hello struct {
		ProtocolVersion   int
		AgentInfo         struct{ Name string }
		AgentCapabilities struct {
			LoadSession        bool
			PromptCapabilities struct{ Image bool }
		}
	}
	if json.Unmarshal(data, &hello) != nil || hello.ProtocolVersion != 1 || !hello.AgentCapabilities.LoadSession || (hello.AgentInfo.Name != "OpenCode" && hello.AgentInfo.Name != "MiMoCode" && hello.AgentInfo.Name != "MiMo Code") {
		return w, errors.Join(errors.New("MiMo CLI 未提供受支持的原生 ACP v1 会话接口，请安装当前官方 MiMo Code；旧 @xiaomi-mimo/cli 不兼容"), w.stop())
	}
	w.imageInput = hello.AgentCapabilities.PromptCapabilities.Image
	return w, nil
}

// WSL's default shell strips JSON quotes and expands shell syntax in env
// arguments. Execute native engine launchers directly, like Codex's ACP launcher;
// SSH already quotes each argument in sshCommand. Keep the selected user and
// minimal Windows host environment unchanged.
func mimoTargetCommand(c Config, args ...string) *exec.Cmd {
	if c.Distro == "" || c.SSHHost != "" {
		return command(c, args...)
	}
	base := []string{"-d", c.Distro}
	if c.User != "" {
		base = append(base, "-u", c.User)
	}
	return wslCommand(append(append(base, "--exec"), args...)...)
}

func applyMimoEnv(cmd *exec.Cmd, variables map[string]string) {
	cmd.Env = os.Environ()
	for key, value := range variables {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
}

func finishMimo(w *harnessWorker) error {
	_ = w.in.Close()
	select {
	case <-w.done:
		return nil
	case <-time.After(3 * time.Second):
		return w.stop()
	}
}

func runMimoACP(ctx context.Context, c Config, t Task, input string, emit func(string, string)) (session, result string, runErr error) {
	session = t.Session
	variables, mode, err := mimoInvocation(t)
	if err != nil {
		return session, "", err
	}
	if c.HardwareAI != nil {
		return session, "", errors.New("MiMo Code 的 Duo 桌面/硬件工具尚未接入，请先关闭此任务的工具授权")
	}
	if mimoSpeechModel(t.Model) {
		return session, "", errors.New("MiMo 语音识别/合成模型不能用于代码会话，请选择文本模型")
	}
	for k, v := range c.EngineEnv {
		variables[k] = v
	}
	// Invocation policy always wins over inherited environment settings.
	policy, _, _ := mimoInvocation(t)
	for k, v := range policy {
		variables[k] = v
	}
	if err = projectMimoSelectedModel(variables, t.Model); err != nil {
		return session, "", err
	}
	key := session
	if key == "" {
		key = "new:" + t.ID
	}
	mimoWriters.Lock()
	if old := mimoWriters.workers[key]; old != nil {
		select {
		case <-old.done:
			delete(mimoWriters.workers, key)
		default:
			mimoWriters.Unlock()
			return session, "", errors.New("MiMo 原生会话仍在运行或停止中，请稍后重试")
		}
	}
	// Reserve before starting; even a cold session may have only one writer.
	placeholder := &harnessWorker{done: make(chan struct{})}
	mimoWriters.workers[key] = placeholder
	mimoWriters.Unlock()
	defer func() {
		if runErr != nil {
			runErr = errors.New(strings.ReplaceAll(runErr.Error(), "Harness", "MiMo Code"))
		}
	}()
	setup, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	w, err := startMimo(setup, c, t, variables)
	if err != nil {
		mimoWriters.Lock()
		if w == nil {
			delete(mimoWriters.workers, key)
		} else {
			select {
			case <-w.done:
				delete(mimoWriters.workers, key)
			default:
				mimoWriters.workers[key] = w
			}
		}
		mimoWriters.Unlock()
		return session, "", err
	}
	mimoWriters.Lock()
	mimoWriters.workers[key] = w
	mimoWriters.Unlock()
	defer func() {
		if err := finishMimo(w); err != nil {
			runErr = errors.Join(runErr, err)
		}
		mimoWriters.Lock()
		defer mimoWriters.Unlock()
		select {
		case <-w.done:
			for _, id := range []string{key, session} {
				if mimoWriters.workers[id] == w {
					delete(mimoWriters.workers, id)
				}
			}
		default:
		}
	}()
	method := "session/new"
	params := map[string]any{"cwd": t.Workspace, "mcpServers": []any{}}
	if session != "" {
		method = "session/load"
		params["sessionId"] = session
		emit("progress", "正在恢复 MiMo 原生会话和上下文…")
	}
	data, err := w.request(setup, method, params)
	if err != nil {
		return session, "", fmt.Errorf("MiMo 原生会话创建/恢复失败，未清空历史或新建替代会话：%w", err)
	}
	if session == "" {
		var response struct{ SessionID string }
		if json.Unmarshal(data, &response) != nil || response.SessionID == "" {
			return "", "", errors.New("MiMo 未返回原生会话 ID")
		}
		session = response.SessionID
	}
	w.session = session
	mimoWriters.Lock()
	if owner := mimoWriters.workers[session]; owner != nil && owner != w {
		mimoWriters.Unlock()
		return session, "", errors.New("MiMo 返回已被其他执行占用的会话 ID")
	}
	mimoWriters.workers[session] = w
	mimoWriters.Unlock()
	emit("session", session)
	if t.Model != "" {
		if _, err = w.request(setup, "session/set_config_option", map[string]any{"sessionId": session, "configId": "model", "value": t.Model}); err != nil {
			return session, "", err
		}
	}
	if _, err = w.request(setup, "session/set_mode", map[string]any{"sessionId": session, "modeId": mode}); err != nil {
		return session, "", err
	}
	cancel()
	_, result, runErr = w.prompt(ctx, input, func(kind, value string) {
		if kind == "progress" {
			value = strings.ReplaceAll(value, "Harness", "MiMo Code")
		}
		emit(kind, value)
	}, t.Files...)
	if ctx.Err() != nil {
		return session, result, runErr
	}
	if err := finishMimo(w); err != nil {
		return session, result, errors.Join(runErr, err)
	}
	nativeResult, usage, nativeErr := mimoTranscript(ctx, c, t, variables, session, input)
	if nativeErr != nil {
		if errors.Is(nativeErr, errMimoTranscriptUnavailable) {
			emit("progress", nativeErr.Error()+"；已保留本轮流式记录。")
			return session, result, runErr
		}
		return session, result, nativeErr
	}
	if usage != nil {
		raw, _ := json.Marshal(usage)
		emit("usage", string(raw))
	}
	if nativeResult != "" {
		if nativeResult != result {
			emit("assistant", nativeResult)
		}
		if runErr != nil && !errors.Is(runErr, errACPEmptyReply) {
			return session, nativeResult, runErr
		}
		return session, nativeResult, nil
	}
	return session, result, runErr
}
