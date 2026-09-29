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
	lease        sync.Mutex
	c            Config
	cmd          *exec.Cmd
	in           io.WriteCloser
	frames       chan codexRPC
	fault        chan error
	done, halt   chan struct{}
	once         sync.Once
	pid          atomic.Int64
	mu           sync.Mutex
	diagnostic   string
	exitErr      error
	readErr      error
	stdoutClosed atomic.Bool
	fingerprint  string
	idle         *time.Timer
	nextID       int
	generation   int
	session      string
	selection    string
	retiring     bool
}

// Final overlay wins over native profile defaults. Approval "never" denies
// escalation, not approves everything. Presets must not override this boundary.
func harnessPolicy(t Task) ([]byte, error) {
	mode := "workspace-write"
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
	}
	return json.Marshal([]any{
		map[string]any{"id": "sandbox-policy", "config": map[string]any{"mode": mode, "workspaceRoot": t.Workspace}},
		map[string]any{"id": "approval", "config": map[string]any{"policy": "never"}},
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
try:
 with os.fdopen(fd,'w') as f: json.dump(cfg['patch'],f)
p=subprocess.Popen([cfg['binary'],'--profile','acp','--patch',path],start_new_session=True)
 print(json.dumps({'type':'jianzuo.process','pid':p.pid}),flush=True)
 sys.exit(p.wait())
finally:
 os.unlink(path)
`

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
	b, _ := json.Marshal([]any{c.Distro, c.User, c.SSHHost, c.SSHPort, c.SSHKey, c.Harness, c.EngineEnv, t.Workspace, json.RawMessage(policy)})
	return string(b)
}

func startHarness(ctx context.Context, c Config, t Task, emit func(string, string)) (*harnessWorker, error) {
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
		return nil, errors.New("Harness 暂未接入Duo硬件工具，请取消硬件授权或选择 Codex/Claude Code")
	}
	if len(t.Files) > 0 {
		return nil, errors.New("Harness 当前接入暂不支持附件，请使用文字任务或其它引擎")
	}
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
		bootstrap, _ = json.Marshal(map[string]any{"binary": binary, "cwd": t.Workspace, "patch": json.RawMessage(patch)})
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
	w := &harnessWorker{c: c, cmd: cmd, in: in, frames: make(chan codexRPC, 128), fault: make(chan error, 1), done: make(chan struct{}), halt: make(chan struct{}), fingerprint: harnessFingerprint(c, t)}
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
	initctx, cancel := context.WithTimeout(ctx, 60*time.Second)
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
				SessionCapabilities struct {
					Resume *json.RawMessage `json:"resume"`
					Close  *json.RawMessage `json:"close"`
				} `json:"sessionCapabilities"`
			} `json:"agentCapabilities"`
		}
		if json.Unmarshal(res, &info) != nil || info.ProtocolVersion != 1 || info.AgentInfo.Name != "deepseek-harness-acp" || info.AgentCapabilities.SessionCapabilities.Resume == nil || info.AgentCapabilities.SessionCapabilities.Close == nil {
			err = errors.New("Harness ACP 接口缺少会话恢复能力，请升级目标环境的 dsh")
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
			err = fmt.Errorf("Harness ACP 握手超时（%s / %s，最多等待 60 秒）；请检查此环境的 dsh --profile acp 启动、账号目录权限及安装状态：%w", provider, model, err)
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
				if msg.Error.Message == "Internal error" {
					var detail struct {
						Message string `json:"message"`
						Details string `json:"details"`
					}
					if json.Unmarshal(msg.Error.Data, &detail) == nil {
						if detail.Message != "" {
							msg.Error.Message += ": " + detail.Message
						} else if detail.Details != "" {
							msg.Error.Message += ": " + detail.Details
						}
					}
				}
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
