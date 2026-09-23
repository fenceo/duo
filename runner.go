package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Runner interface {
	Run(context.Context, Config, Task, string, func(string, string)) (string, string, error)
}
type CodexRunner struct{}

// The WSL launcher owns a distinct process group so Stop also stops child tools.
const launcher = `import sys,subprocess,json,os
p=subprocess.Popen(sys.argv[1:],start_new_session=True)
print(json.dumps({"type":"jianzuo.process","pid":p.pid}),flush=True)
sys.exit(p.wait())
`

// Codex tools can create their own process groups. Include all descendants.
const stopTree = `import os,sys,signal,pathlib
root=int(sys.argv[1]); children={root}
if root<=1: sys.exit(1)
try: os.kill(root,signal.SIGSTOP)
except ProcessLookupError: pass
for _ in range(3):
 parents={}
 for p in pathlib.Path('/proc').iterdir():
  if not p.name.isdigit(): continue
  try: parents[int(p.name)]=int((p/'stat').read_text().rsplit(')',1)[1].split()[1])
  except (OSError,ValueError,IndexError): pass
 while True:
  expanded=children|{pid for pid,parent in parents.items() if parent in children}
  if expanded==children: break
  children=expanded
 for pid in children:
  try: os.kill(pid,signal.SIGSTOP)
  except ProcessLookupError: pass
for pid in sorted(children,reverse=True):
 try: os.kill(pid,signal.SIGKILL)
 except ProcessLookupError: pass
`

// Legacy exec helper retained for compatibility tests. Production Codex runs
// use app-server; this one-way transport cannot service interactive approvals.
func codexArgs(c Config, t Task) []string {
	sandbox := "workspace-write"
	network := true
	if t.Mode != nil {
		switch t.Mode.Permission {
		case "read":
			sandbox = "read-only"
			network = false
		case "full":
			sandbox = "danger-full-access"
			network = false
		default:
			if t.Mode.AllowNetwork != nil {
				network = *t.Mode.AllowNetwork
			}
		}
	}
	args := []string{"-a", "never", "-C", t.Workspace, "-c", "sandbox_mode=" + strconv.Quote(sandbox)}
	args = append(args, "-c", "sandbox_workspace_write.network_access="+strconv.FormatBool(network))
	if h := c.HardwareAI; h != nil {
		args = append(args, "-c", "mcp_servers.jianzuo_hardware.url="+strconv.Quote(h.URL), "-c", `mcp_servers.jianzuo_hardware.bearer_token_env_var="JIANZUO_HARDWARE_TOKEN"`, "-c", `mcp_servers.jianzuo_hardware.required=true`, "-c", `mcp_servers.jianzuo_hardware.default_tools_approval_mode="approve"`, "-c", `mcp_servers.jianzuo_hardware.tool_timeout_sec=95`)
	}
	if t.ReasoningEffort != "" {
		args = append(args, "-c", "model_reasoning_effort="+strconv.Quote(t.ReasoningEffort))
	}
	args = append(args, "exec")
	if t.Session != "" {
		args = append(args, "resume")
	}
	args = append(args, "--json", "--skip-git-repo-check")
	if t.Model != "" {
		args = append(args, "-m", t.Model)
	}
	for _, f := range t.Files {
		if isImageAttachment(f.Attachment) {
			args = append(args, "--image", f.Path)
		}
	}
	if t.Session != "" {
		args = append(args, t.Session)
	}
	return append(args, "-")
}
func command(c Config, args ...string) *exec.Cmd {
	if c.SSHHost != "" {
		return sshCommand(c, args...)
	}
	if c.Distro != "" {
		base := []string{"-d", c.Distro}
		if c.User != "" {
			base = append(base, "-u", c.User)
		}
		return wslCommand(append(base, append([]string{"--"}, args...)...)...)
	}
	return exec.Command(args[0], args[1:]...)
}

// WSL should use the selected Linux user's own home, Git, SSH agent, proxy and
// login configuration. Passing the caller's full Windows environment can leak
// restricted-tool variables and prepend Windows shims to the Linux PATH.
func wslHostEnvironment() []string {
	env := []string{}
	for _, name := range []string{"SystemRoot", "WINDIR"} {
		if value := os.Getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}
	return env
}

func wslExecutable() string {
	if path, err := exec.LookPath("wsl.exe"); err == nil {
		return path
	}
	if root := os.Getenv("SystemRoot"); root != "" {
		candidate := filepath.Join(root, "System32", "wsl.exe")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "wsl.exe"
}

func wslCommand(args ...string) *exec.Cmd {
	cmd := exec.Command(wslExecutable(), args...)
	cmd.Env = wslHostEnvironment()
	return cmd
}

func engineEnvPairs(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key, value := range env {
		if key != "" && value != "" {
			keys = append(keys, key+"="+value)
		}
	}
	sort.Strings(keys)
	return keys
}

func applyEngineEnv(cmd *exec.Cmd, env map[string]string) {
	if cmd == nil || len(env) == 0 {
		return
	}
	base := cmd.Env
	if base == nil {
		base = os.Environ()
	}
	cmd.Env = append(append([]string{}, base...), engineEnvPairs(env)...)
}

// Linux targets are launched without a shell. Prefixing with env keeps profile
// values out of the engine argv while working for both WSL and SSH.
func withEngineEnv(args []string, env map[string]string) []string {
	pairs := engineEnvPairs(env)
	if len(pairs) == 0 {
		return args
	}
	out := make([]string, 0, len(args)+len(pairs)+1)
	out = append(out, "env")
	out = append(out, pairs...)
	out = append(out, args...)
	return out
}

func commandWithContext(ctx context.Context, base *exec.Cmd) *exec.Cmd {
	cmd := exec.CommandContext(ctx, base.Path, base.Args[1:]...)
	cmd.Env = base.Env
	cmd.Dir = base.Dir
	return cmd
}

func (CodexRunner) Run(ctx context.Context, c Config, t Task, input string, emit func(string, string)) (string, string, error) {
	if ctx.Err() != nil {
		return t.Session, "", ctx.Err()
	}
	if t.Engine == "" || t.Engine == "codex" {
		return runCodexAppServer(ctx, c, t, input, emit)
	}
	if t.Engine == "deepseek-harness" {
		return runHarnessSDK(ctx, c, t, input, emit)
	}
	cmd, commandErr := engineCommand(c, t)
	if commandErr != nil {
		return t.Session, "", commandErr
	}
	input = runnerInput(t, input)
	cmd.Stdin = strings.NewReader(input)
	if c.HardwareAI != nil {
		if c.Distro != "" || c.SSHHost != "" {
			payload, _ := json.Marshal(map[string]any{"token": c.HardwareAI.Token, "url": c.HardwareAI.URL, "fallback_urls": c.HardwareAI.FallbackURLs})
			cmd.Stdin = strings.NewReader(string(payload) + "\n" + input)
		} else {
			if err := probeHardwareMCP(ctx, c.HardwareAI); err != nil {
				return t.Session, "", err
			}
			cmd.Env = append(os.Environ(), "JIANZUO_HARDWARE_TOKEN="+c.HardwareAI.Token)
			applyEngineEnv(cmd, c.EngineEnv)
		}
		emit("progress", "已为本轮配置任务授权的硬件工具")
	}
	hideCommand(cmd)
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		return "", "", e
	}
	stderr, e := cmd.StderrPipe()
	if e != nil {
		return "", "", e
	}
	if e = cmd.Start(); e != nil {
		return "", "", e
	}
	emit("progress", "正在启动 "+engineName(t.Engine)+"…")
	var mu sync.Mutex
	pid := 0
	session := t.Session
	result := ""
	failure := ""
	claude := claudeStream{session: t.Session}
	diagnostic := ""
	stopFailure := ""
	cleanupDone := make(chan struct{})
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer close(cleanupDone)
		select {
		case <-ctx.Done():
			select {
			case <-done:
				return
			default:
			}
			mu.Lock()
			group := pid
			mu.Unlock()
			for attempt := 0; (c.Distro != "" || c.SSHHost != "") && group == 0 && attempt < 40; attempt++ {
				time.Sleep(50 * time.Millisecond)
				mu.Lock()
				group = pid
				mu.Unlock()
			}
			if (c.Distro != "" || c.SSHHost != "") && group > 1 {
				kill := command(c, "python3", "-c", stopTree, strconv.Itoa(group))
				killCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				bounded := commandWithContext(killCtx, kill)
				hideCommand(bounded)
				if err := bounded.Run(); err != nil && c.SSHHost != "" {
					mu.Lock()
					stopFailure = fmt.Sprintf("SSH 已断开，但未能确认远端进程 %d 已停止，请检查远端主机：%v", group, err)
					mu.Unlock()
				}
				cancel()
			}
			if c.SSHHost != "" && group == 0 {
				mu.Lock()
				stopFailure = "SSH 已断开，未取得远端进程号，无法确认远端停止状态"
				mu.Unlock()
			}
			if c.Distro == "" && c.SSHHost == "" {
				kill := exec.Command("taskkill.exe", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
				hideCommand(kill)
				_ = kill.Run()
			}
			_ = cmd.Process.Kill()
		case <-done:
		}
	}()
	var wg sync.WaitGroup
	scan := func(r io.Reader, output bool) {
		defer wg.Done()
		s := bufio.NewScanner(r)
		s.Buffer(make([]byte, 65536), 4*1024*1024)
		for s.Scan() {
			line := s.Text()
			if !output {
				if len(line) > 0 {
					mu.Lock()
					diagnostic = line
					mu.Unlock()
					emit("log", line)
				}
				continue
			}
			var hardwareStatus struct {
				Type string `json:"type"`
				URL  string `json:"url"`
			}
			if json.Unmarshal([]byte(line), &hardwareStatus) == nil && hardwareStatus.Type == "jianzuo.hardware" {
				emit("progress", "已连接硬件服务："+hardwareStatus.URL)
				continue
			}
			if t.Engine == "claude" {
				var header struct {
					Type string `json:"type"`
					PID  int    `json:"pid"`
				}
				if json.Unmarshal([]byte(line), &header) == nil && header.Type == "jianzuo.process" {
					mu.Lock()
					pid = header.PID
					mu.Unlock()
				} else {
					claude.consume(line, emit)
				}
				continue
			}
			var v struct {
				Type    string          `json:"type"`
				PID     int             `json:"pid"`
				Thread  string          `json:"thread_id"`
				Usage   json.RawMessage `json:"usage"`
				Message string          `json:"message"`
				Error   struct {
					Message string `json:"message"`
				} `json:"error"`
				Item struct {
					Type    string `json:"type"`
					Text    string `json:"text"`
					Command string `json:"command"`
					Output  string `json:"aggregated_output"`
					Status  string `json:"status"`
					Changes []struct {
						Path string `json:"path"`
					} `json:"changes"`
				} `json:"item"`
			}
			if json.Unmarshal([]byte(line), &v) != nil {
				emit("log", line)
				continue
			}
			switch v.Type {
			case "jianzuo.process":
				mu.Lock()
				pid = v.PID
				mu.Unlock()
			case "turn.completed":
				emitUsage("codex", v.Usage, emit)
			case "thread.started":
				session = v.Thread
				emit("session", session)
			case "item.started", "item.updated", "item.completed":
				switch v.Item.Type {
				case "agent_message":
					if v.Type == "item.completed" {
						result = v.Item.Text
						emit("assistant", v.Item.Text)
					}
				case "command_execution":
					emit("tool", v.Item.Command+"\n"+v.Item.Status+"\n"+v.Item.Output)
				case "file_change":
					for _, change := range v.Item.Changes {
						emit("tool", "文件修改："+change.Path)
					}
				default:
					if v.Item.Text != "" {
						emit("progress", v.Item.Text)
					}
				}
			case "turn.failed":
				failure = v.Error.Message
				emit("error", failure)
			case "error":
				emit("error", v.Message)
			}
		}
		if err := s.Err(); err != nil {
			mu.Lock()
			diagnostic = err.Error()
			mu.Unlock()
		}
	}
	wg.Add(2)
	go scan(stdout, true)
	go scan(stderr, false)
	wg.Wait()
	e = cmd.Wait()
	if t.Engine == "claude" {
		session = claude.session
		result = claude.result
		failure = claude.failure
		if !claude.finished && failure == "" {
			failure = "Claude Code 未返回结束事件，请展开执行日志检查。 " + diagnostic
		}
	}
	if ctx.Err() != nil {
		<-cleanupDone
		mu.Lock()
		failureToStop := stopFailure
		mu.Unlock()
		if failureToStop != "" {
			return session, result, fmt.Errorf("%s", failureToStop)
		}
		return session, result, ctx.Err()
	}
	if failure != "" {
		return session, result, fmt.Errorf("%s", failure)
	}
	if e != nil {
		return session, result, fmt.Errorf("%s 执行失败：%v %s", engineName(t.Engine), e, diagnostic)
	}
	if result == "" {
		return session, "", fmt.Errorf("%s 没有返回最终回复，请展开执行日志检查", engineName(t.Engine))
	}
	return session, result, nil
}
func engineCheckCommand(c Config, args ...string) *exec.Cmd {
	if c.Distro != "" || c.SSHHost != "" {
		return command(c, withEngineEnv(args, c.EngineEnv)...)
	}
	cmd := command(c, args...)
	applyEngineEnv(cmd, c.EngineEnv)
	return cmd
}

func checkEnvironment(c Config) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := engineCheckCommand(c, c.Codex, "login", "status")
	bounded := commandWithContext(ctx, cmd)
	hideCommand(bounded)
	b, e := bounded.CombinedOutput()
	return string(b), e
}
