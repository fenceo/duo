package main

// ACP owns durable native sessions. Processes are only a cache: a cold session
// is resumed from Harness persistence, never reconstructed from Duo's UI log.
import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const harnessUnsupportedReasoningMessage = "此模型不支持所选推理强度，请选择工具默认后重试"

func isHarnessUnsupportedReasoning(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "unsupported_reasoning_effort") || strings.Contains(message, "does not support reasoning effort") || strings.Contains(message, harnessUnsupportedReasoningMessage)
}

var harnessRuntimes = struct {
	sync.Mutex
	workers  map[string]*harnessWorker
	starting int
}{workers: map[string]*harnessWorker{}}

type harnessWorker struct {
	engine        string
	lease         sync.Mutex
	c             Config
	cmd           *exec.Cmd
	in            io.WriteCloser
	frames        chan codexRPC
	fault         chan error
	done, halt    chan struct{}
	once          sync.Once
	pid           atomic.Int64
	mu            sync.Mutex
	diagnostic    string
	diagnosticErr error
	exitErr       error
	readErr       error
	stdoutClosed  atomic.Bool
	fingerprint   string
	identity      string
	idle          *time.Timer
	nextID        int
	generation    int
	session       string
	selection     string
	retiring      bool
	interactive   bool
	imageInput    bool
	guardReady    atomic.Bool
}

// Final overlay wins over native profile defaults. Approval "never" denies
// escalation, not approves everything. Presets must not override this boundary.
func harnessPolicy(t Task) ([]byte, error) {
	mode, approval := "workspace-write", "never"
	if t.Mode != nil {
		if t.Mode.Approval == "auto" {
			return nil, errors.New("Harness ACP 不支持原生自动风险评审")
		}
		if t.Mode.AllowNetwork != nil && !*t.Mode.AllowNetwork {
			return nil, errors.New("Harness ACP 暂不支持禁用网络保证，请选择允许联网的模式或使用 Codex")
		}
		switch t.Mode.Permission {
		case "read":
			mode = "read-only"
		case "full":
			mode = "danger-full-access"
		case "", "workspace":
		default:
			return nil, errors.New("Harness 权限模式无效")
		}
		if harnessInteractiveMode(t.Mode) {
			approval = "ask"
		}
	}
	return json.Marshal([]any{
		map[string]any{"id": "sandbox-policy", "config": map[string]any{"mode": mode, "workspaceRoot": t.Workspace}},
		map[string]any{"id": "approval", "config": map[string]any{"policy": approval}},
		map[string]any{"id": "permission", "disabled": true},
		map[string]any{"id": "session-log-deepseek", "config": map[string]any{"enabled": false}},
	})
}

// Read exactly one line so ACP receives the remaining stdin intact. The
// wrapper owns and removes its private policy file after its child exits.
const harnessLauncher = `import sys,os,json,tempfile,subprocess,shutil
line=bytearray()
while True:
 b=os.read(0,1)
 if not b: raise RuntimeError('missing Harness bootstrap')
 if b==b'\n': break
 line.extend(b)
cfg=json.loads(line)
os.chdir(cfg['cwd'])
if cfg['binary']=='dsh' and shutil.which('dsh') is None:
 candidate=os.path.join(os.path.expanduser('~'),'.local','bin','dsh')
 if os.path.isfile(candidate):
  cfg['binary']=candidate
if os.path.isabs(cfg['binary']):
 os.environ['PATH']=os.path.dirname(cfg['binary'])+os.pathsep+os.environ.get('PATH','')
fd,path=tempfile.mkstemp(prefix='jianzuo-harness-',suffix='.json')
guard_path=None
try:
 if cfg.get('guard'):
  guard_fd,guard_path=tempfile.mkstemp(prefix='duo-knowledge-',suffix='.mjs')
  with os.fdopen(guard_fd,'w') as f: f.write(cfg['guard'])
  cfg['patch'].append({'insert':[{'id':'duo-knowledge-guard','name':guard_path}]})
 with os.fdopen(fd,'w') as f: json.dump(cfg['patch'],f)
 p=subprocess.Popen([cfg['binary'],'--profile','acp','--patch',path],start_new_session=True)
 print(json.dumps({'type':'jianzuo.process','pid':p.pid}),flush=True)
 sys.exit(p.wait())
finally:
 os.unlink(path)
 if guard_path: os.unlink(guard_path)
`

// The official tools.guard extension denies even custom scoped tools. This
// temporary plugin belongs only to the independent summary worker.
const harnessKnowledgeGuard = `export const name='duo-knowledge-guard';
export const inject=['tools'];
export function apply(ctx){ctx.tools.guard(()=> 'Duo knowledge summary forbids every tool operation');process.stdout.write(JSON.stringify({type:'duo.knowledge.guard.ready'})+'\n');}`

// Resolve npm's Windows shim without shell-evaluating any user-controlled text.
func harnessExecutable(binary string) ([]string, error) {
	if binary == "" {
		binary = "dsh.cmd"
	}
	p, err := exec.LookPath(binary)
	if err != nil && binary == "dsh.exe" {
		p, err = exec.LookPath("dsh.cmd")
	}
	if err != nil {
		return nil, fmt.Errorf("找不到 Harness 程序 %s，请先在此环境安装 dsh：%w", binary, err)
	}
	if !strings.EqualFold(filepath.Ext(p), ".cmd") && !strings.EqualFold(filepath.Ext(p), ".bat") {
		return []string{p}, nil
	}
	entry := filepath.Join(filepath.Dir(p), "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	if _, err := os.Stat(entry); err != nil {
		return nil, errors.New("无法解析此 Harness npm 启动器，请使用标准 @deepseek-ai/dsh 安装或原生可执行文件")
	}
	node := filepath.Join(filepath.Dir(p), "node.exe")
	if info, localErr := os.Stat(node); localErr != nil || info.IsDir() {
		node, err = exec.LookPath("node.exe")
	}
	if err != nil {
		return nil, fmt.Errorf("Harness 需要 Node.js：%w", err)
	}
	return []string{node, entry}, nil
}

func harnessFingerprint(c Config, t Task) string {
	policy, _ := harnessPolicy(t)
	b, _ := json.Marshal([]any{c.Distro, c.User, c.SSHHost, c.SSHPort, c.SSHKey, c.Harness, c.EngineEnv, t.Workspace, json.RawMessage(policy), t.NoTools})
	return string(b)
}

// Policy is a per-turn choice. Everything identifying the native log and its
// execution boundary, including the independent knowledge guard, stays pinned.
func harnessIdentity(c Config, t Task) string {
	b, _ := json.Marshal([]any{c.Distro, c.User, c.SSHHost, c.SSHPort, c.SSHKey, c.Harness, c.EngineEnv, t.Workspace, t.NoTools})
	return string(b)
}

func startHarness(ctx context.Context, c Config, t Task, emit func(string, string)) (*harnessWorker, error) {
	if t.NoTools {
		c.HardwareAI = nil
	}
	provider, model, err := harnessRouteForConfig(c, t.Model)
	if err != nil {
		return nil, err
	}
	patch, err := harnessPolicy(t)
	if err != nil {
		return nil, err
	}
	var rows []any
	if err := json.Unmarshal(patch, &rows); err != nil {
		return nil, err
	}
	rows = append(rows, map[string]any{"id": "acp", "config": map[string]any{"provider": provider, "model": model}})
	patch, err = json.Marshal(rows)
	if err != nil {
		return nil, err
	}
	if !validEngineReasoning("deepseek-harness", t.ReasoningEffort) {
		return nil, errors.New("Harness 推理强度无效")
	}
	if c.HardwareAI != nil {
		emit("progress", "正在从执行环境连接 Duo 桌面/硬件工具…")
	}
	hardware, err := resolveHarnessHardware(ctx, c)
	if err != nil {
		return nil, err
	}
	c.HardwareAI = hardware
	env := map[string]string{}
	for k, v := range c.EngineEnv {
		env[k] = v
	}
	env["DSH_TELEMETRY_MODE"] = "DISABLED"
	env["DSH_TELEMETRY_DISABLED"] = "1"
	var cmd *exec.Cmd
	cleanup := func() {}
	var bootstrap []byte
	if c.Distro != "" || c.SSHHost != "" {
		binary := c.Harness
		if binary == "" {
			binary = "dsh"
		}
		cmd = command(c, withEngineEnv([]string{"python3", "-u", "-c", harnessLauncher}, env)...)
		guard := ""
		if t.NoTools {
			guard = harnessKnowledgeGuard
		}
		bootstrap, _ = json.Marshal(map[string]any{"binary": binary, "cwd": t.Workspace, "patch": json.RawMessage(patch), "guard": guard})
	} else {
		args, err := harnessExecutable(c.Harness)
		if err != nil {
			return nil, err
		}
		f, err := os.CreateTemp("", "jianzuo-harness-*.json")
		if err != nil {
			return nil, err
		}
		cleanup = func() { _ = os.Remove(f.Name()) }
		if t.NoTools {
			guard, guardErr := os.CreateTemp("", "duo-knowledge-*.mjs")
			if guardErr != nil {
				f.Close()
				cleanup()
				return nil, guardErr
			}
			_, guardErr = guard.WriteString(harnessKnowledgeGuard)
			closeErr := guard.Close()
			policyCleanup := cleanup
			cleanup = func() { policyCleanup(); _ = os.Remove(guard.Name()) }
			if guardErr != nil || closeErr != nil {
				f.Close()
				cleanup()
				return nil, errors.Join(guardErr, closeErr)
			}
			rows = append(rows, map[string]any{"insert": []any{map[string]any{"id": "duo-knowledge-guard", "name": "file:///" + filepath.ToSlash(guard.Name())}}})
			patch, err = json.Marshal(rows)
			if err != nil {
				f.Close()
				cleanup()
				return nil, err
			}
		}
		if _, err = f.Write(patch); err != nil {
			f.Close()
			cleanup()
			return nil, err
		}
		if err = f.Close(); err != nil {
			cleanup()
			return nil, err
		}
		cmd = exec.Command(args[0], append(args[1:], "--profile", "acp", "--patch", f.Name())...)
		cmd.Dir = t.Workspace
		applyEngineEnv(cmd, env)
	}
	hideCommand(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		cleanup()
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		cleanup()
		return nil, err
	}
	errout, err := cmd.StderrPipe()
	if err != nil {
		in.Close()
		out.Close()
		cleanup()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		in.Close()
		out.Close()
		errout.Close()
		cleanup()
		return nil, err
	}
	w := &harnessWorker{c: c, cmd: cmd, in: in, frames: make(chan codexRPC, 128), fault: make(chan error, 1), done: make(chan struct{}), halt: make(chan struct{}), fingerprint: harnessFingerprint(c, t), identity: harnessIdentity(c, t), interactive: harnessInteractiveMode(t.Mode)}
	var scans sync.WaitGroup
	scans.Add(2)
	go func() {
		defer scans.Done()
		w.scan(out, true)
	}()
	go func() {
		defer scans.Done()
		w.scan(errout, false)
	}()
	go func() {
		scans.Wait()
		err := cmd.Wait()
		w.mu.Lock()
		w.exitErr = err
		w.mu.Unlock()
		close(w.done)
		cleanup()
	}()
	stageTimeout := modelProbeStageTimeout(ctx, 60*time.Second)
	initctx, cancel := context.WithTimeout(ctx, stageTimeout)
	defer cancel()
	if len(bootstrap) > 0 {
		if err = w.write(initctx, append(bootstrap, '\n')); err != nil {
			if stopErr := w.stop(); stopErr != nil {
				err = fmt.Errorf("%w；%v", err, stopErr)
			}
			return nil, err
		}
	}
	params := map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}, "clientInfo": map[string]any{"name": "duo", "version": version}}
	emit("progress", "正在连接 Harness："+provider+" / "+model)
	res, err := w.request(initctx, "initialize", params)
	if err == nil {
		var info struct {
			ProtocolVersion int `json:"protocolVersion"`
			AgentInfo       struct {
				Name string `json:"name"`
			} `json:"agentInfo"`
			AgentCapabilities struct {
				MCPCapabilities struct {
					HTTP bool `json:"http"`
				} `json:"mcpCapabilities"`
				PromptCapabilities struct {
					Image bool `json:"image"`
				} `json:"promptCapabilities"`
				SessionCapabilities struct {
					Resume *json.RawMessage `json:"resume"`
					Close  *json.RawMessage `json:"close"`
				} `json:"sessionCapabilities"`
			} `json:"agentCapabilities"`
		}
		if json.Unmarshal(res, &info) != nil || info.ProtocolVersion != 1 || info.AgentInfo.Name != "deepseek-harness-acp" || info.AgentCapabilities.SessionCapabilities.Resume == nil || info.AgentCapabilities.SessionCapabilities.Close == nil {
			err = errors.New("Harness ACP 接口缺少会话恢复能力，请升级目标环境的 dsh")
		}
		w.imageInput = info.AgentCapabilities.PromptCapabilities.Image
		if err == nil && c.HardwareAI != nil && !info.AgentCapabilities.MCPCapabilities.HTTP {
			err = errors.New("Harness ACP 不支持 HTTP MCP，请升级此环境的 dsh 后再使用桌面/硬件工具（未发送模型请求）")
		}
	}
	if err == nil && t.NoTools {
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for !w.guardReady.Load() && err == nil {
			select {
			case <-initctx.Done():
				err = initctx.Err()
			case <-w.done:
				err = w.failure()
			case <-deadline.C:
				err = errors.New("Harness 未确认知识整理工具禁用策略已生效，请升级目标环境的 dsh（未发送模型请求）")
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	if err != nil {
		w.mu.Lock()
		diagnostic := w.diagnostic
		w.mu.Unlock()
		if diagnostic != "" {
			err = fmt.Errorf("%w：%s", err, diagnostic)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			// Keep the effective timeout visible. Latency probes may
			// intentionally extend startup beyond the normal 60-second budget.
			// If the child already exited, prefer its exit/diagnostic details over
			// the generic timeout so startup failures remain actionable.
			childExited := false
			select {
			case <-w.done:
				childExited = true
			default:
			}
			if childExited {
				err = fmt.Errorf("Harness ACP 启动失败（%s / %s）：%w", provider, model, w.failure())
			} else {
				err = fmt.Errorf("Harness ACP 握手超时（%s / %s，最多等待 %s）；请检查此环境的 dsh --profile acp 启动、账号目录权限及安装状态：%w", provider, model, stageTimeout.Round(time.Second), err)
			}
		}
		stopErr := w.stop()
		if stopErr != nil {
			err = fmt.Errorf("%v；%w", err, stopErr)
		}
		return nil, err
	}
	return w, nil
}

// stdout is the sole frame producer and closes its channel on EOF or a scan
// error. Consumers can drain complete frames without waiting for stderr or for
// a still-live process that is blocked on an unread oversized output line.
func (w *harnessWorker) scan(r io.Reader, stdout bool) {
	if stdout {
		defer func() {
			w.stdoutClosed.Store(true)
			close(w.frames)
		}()
	}
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 65536), 4*1024*1024)
	for s.Scan() {
		line := s.Text()
		if stdout {
			var h struct {
				Type string `json:"type"`
				PID  int    `json:"pid"`
			}
			if json.Unmarshal([]byte(line), &h) == nil && h.Type == "jianzuo.process" {
				w.pid.Store(int64(h.PID))
				continue
			}
			if h.Type == "duo.knowledge.guard.ready" {
				w.guardReady.Store(true)
				continue
			}
			var frame codexRPC
			if json.Unmarshal([]byte(line), &frame) == nil && (frame.Method != "" || len(frame.ID) > 0) {
				select {
				case w.frames <- frame:
				case <-w.halt:
					return
				}
				continue
			}
		}
		w.mu.Lock()
		if w.engine == "mimo" && strings.Contains(line, "ConfigJsonError") {
			// MiMo's fatal JSON report can contain the entire configuration.
			// Preserve a safe cause across the final "--- End ---" banner.
			w.diagnosticErr = errors.New("MiMo Code [CONFIG_PARSE_ERROR]：原生配置 JSON 解析失败，请检查目标环境配置或升级 Duo；原有会话已保留")
		}
		w.diagnostic = line
		w.mu.Unlock()
	}
	if err := s.Err(); err != nil {
		stream := "stdout"
		if !stdout {
			stream = "stderr"
		}
		failure := fmt.Errorf("Harness ACP %s 读取失败：%w", stream, err)
		w.mu.Lock()
		w.readErr = failure
		w.mu.Unlock()
		if !stdout {
			select {
			case w.fault <- failure:
			default:
			}
		}
	}
}

// StdinPipe and io.Pipe writes are unblocked by Close. Await the writer after
// closing so a cancelled write cannot outlive its invocation or race a reuse.
func (w *harnessWorker) write(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	written := make(chan error, 1)
	go func() {
		n, err := w.in.Write(data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
		written <- err
	}()
	select {
	case err := <-written:
		return err
	case <-ctx.Done():
		_ = w.in.Close()
		<-written
		return ctx.Err()
	case err := <-w.fault:
		_ = w.in.Close()
		<-written
		return err
	}
}

func (w *harnessWorker) send(ctx context.Context, method string, params any) (int, error) {
	w.nextID++
	data, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": w.nextID, "method": method, "params": params})
	if err != nil {
		return w.nextID, err
	}
	return w.nextID, w.write(ctx, append(data, '\n'))
}
func (w *harnessWorker) failure() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.readErr != nil {
		return w.readErr
	}
	if w.diagnosticErr != nil {
		return w.diagnosticErr
	}
	return fmt.Errorf("Harness ACP 意外退出：%v %s", w.exitErr, w.diagnostic)
}
func (w *harnessWorker) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id, err := w.send(ctx, method, params)
	if err != nil {
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err := <-w.fault:
			return nil, err
		case msg, ok := <-w.frames:
			if !ok {
				return nil, w.failure()
			}
			if msg.Method != "" && len(msg.ID) > 0 {
				if err := w.rejectClientRequest(ctx, msg); err != nil {
					return nil, err
				}
				continue
			}
			if string(msg.ID) != fmt.Sprint(id) {
				continue
			}
			if msg.Error != nil {
				msg.Error.Message = harnessRPCMessage(msg.Error.Message, msg.Error.Data)
				if isHarnessUnsupportedReasoning(msg.Error.Message) {
					return nil, fmt.Errorf("Harness %s [UNSUPPORTED_REASONING_EFFORT]：%s", method, harnessUnsupportedReasoningMessage)
				}
				return nil, fmt.Errorf("Harness %s：%s", method, msg.Error.Message)
			}
			return msg.Result, nil
		}
	}
}
func (w *harnessWorker) stop() error {
	var failure error
	w.once.Do(func() {
		close(w.halt)
		select {
		case <-w.done:
			return
		default:
		}
		if w.c.Distro == "" && w.c.SSHHost == "" {
			kill := exec.Command("taskkill.exe", "/PID", strconv.Itoa(w.cmd.Process.Pid), "/T", "/F")
			hideCommand(kill)
			if err := kill.Run(); err != nil {
				select {
				case <-w.done:
				default:
					_ = w.cmd.Process.Kill()
					// Process.Kill is asynchronous on Windows. Give the reader
					// goroutine a moment to observe the exit before reporting that
					// the process tree could not be confirmed.
					select {
					case <-w.done:
					case <-time.After(500 * time.Millisecond):
						failure = fmt.Errorf("Harness 主进程已终止，但无法确认所有子进程已停止：%w", err)
					}
				}
			}
		} else {
			failure = stopAppServerTree(w.c, w.cmd, int(w.pid.Load()))
		}
		_ = w.in.Close()
		select {
		case <-w.done:
		case <-time.After(5 * time.Second):
			if failure == nil {
				failure = errors.New("无法确认 Harness 进程已停止")
			}
		}
	})
	if failure != nil {
		return errors.New(strings.ReplaceAll(failure.Error(), "Codex", "Harness"))
	}
	return nil
}

func closeHarnessRuntimes() {
	harnessRuntimes.Lock()
	workers := harnessRuntimes.workers
	harnessRuntimes.workers = map[string]*harnessWorker{}
	harnessRuntimes.Unlock()
	for _, w := range workers {
		w.lease.Lock()
		if w.idle != nil {
			w.idle.Stop()
		}
		_ = w.close()
		w.lease.Unlock()
	}
}

func closeIdleHarnessSession(session string) error {
	harnessRuntimes.Lock()
	w := harnessRuntimes.workers[session]
	if w == nil {
		harnessRuntimes.Unlock()
		return nil
	}
	if !w.lease.TryLock() {
		harnessRuntimes.Unlock()
		return errors.New("Harness 当前轮仍在结束，请稍后重试")
	}
	delete(harnessRuntimes.workers, session)
	harnessRuntimes.Unlock()
	defer w.lease.Unlock()
	if w.idle != nil {
		w.idle.Stop()
	}
	return w.close()
}

func checkHarness(c Config) (string, error) {
	if len(c.Workspaces) == 0 {
		return "", errors.New("需要配置工作目录")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	w, err := startHarness(ctx, c, Task{Workspace: c.Workspaces[0], Engine: "deepseek-harness"}, func(string, string) {})
	if err != nil {
		return "", err
	}
	if err = w.close(); err != nil {
		return "", err
	}
	return "Harness ACP 握手成功，支持原生会话恢复和模型切换（未发送模型请求，尚未验证 API 密钥、余额或实际回复）。", nil
}
