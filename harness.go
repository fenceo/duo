package main

// The installed SDK has no load-session, interrupt or approval RPC. A native
// conversation therefore owns a live runtime; lost sessions fail explicitly.
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

const harnessLostSession = "Harness 运行会话已结束（停止任务、服务重启或空闲超过 30 分钟）。当前 SDK 不支持跨进程恢复，请新建空白会话；原聊天记录仍保留，但不会自动带入 AI 上下文。"
const harnessUnsupportedReasoningMessage = "此模型不支持所选推理强度，请选择工具默认后新建任务"

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
}

// Final overlay wins over native profile defaults. Approval "never" denies
// escalation, not approves everything. Presets must not override this boundary.
func harnessPolicy(t Task) ([]byte, error) {
	mode := "workspace-write"
	if t.Mode != nil {
		if t.Mode.Approval == "auto" {
			return nil, errors.New("Harness SDK 不支持原生自动风险评审")
		}
		if t.Mode.AllowNetwork != nil && !*t.Mode.AllowNetwork {
			return nil, errors.New("Harness SDK 暂不支持禁用网络保证，请选择允许联网的模式或使用 Codex")
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

// Read exactly one line so the SDK receives the remaining stdin intact. The
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
 p=subprocess.Popen([cfg['binary'],'--profile','sdk','--patch',path],start_new_session=True)
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
	node, err := exec.LookPath("node.exe")
	if err != nil {
		return nil, fmt.Errorf("Harness 需要 Node.js：%w", err)
	}
	return []string{node, entry}, nil
}

func harnessFingerprint(c Config, t Task) string {
	b, _ := json.Marshal([]any{c.Distro, c.User, c.SSHHost, c.SSHPort, c.SSHKey, c.Harness, c.HarnessProvider, c.HarnessModel, c.EngineEnv, t.Workspace, t.Model, t.ReasoningEffort, t.Mode})
	return string(b)
}

func startHarness(ctx context.Context, c Config, t Task, emit func(string, string)) (*harnessWorker, error) {
	patch, err := harnessPolicy(t)
	if err != nil {
		return nil, err
	}
	if !validEngineReasoning("deepseek-harness", t.ReasoningEffort) {
		return nil, errors.New("Harness 推理强度无效")
	}
	if c.HardwareAI != nil {
		return nil, errors.New("Harness 暂未接入简作硬件工具，请取消硬件授权或选择 Codex/Claude Code")
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
		cmd = exec.Command(args[0], append(args[1:], "--profile", "sdk", "--patch", f.Name())...)
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
	initctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if len(bootstrap) > 0 {
		if err = w.write(initctx, append(bootstrap, '\n')); err != nil {
			if stopErr := w.stop(); stopErr != nil {
				err = fmt.Errorf("%w；%v", err, stopErr)
			}
			return nil, err
		}
	}
	model := t.Model
	if model == "" {
		model = c.HarnessModel
	}
	if model == "" {
		model = "deepseek-flash"
	}
	provider := c.HarnessProvider
	if provider == "" {
		provider = "deepseek-official"
	}
	params := map[string]any{"cwd": t.Workspace, "provider": provider, "model": model}
	if t.ReasoningEffort != "" {
		params["reasoningEffort"] = t.ReasoningEffort
	}
	emit("progress", "正在连接 Harness SDK："+provider+" / "+model)
	res, err := w.request(initctx, "initialize", params)
	if err == nil {
		var info struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		}
		if json.Unmarshal(res, &info) != nil || info.ServerInfo.Name != "deepseek-harness-sdk-runtime" {
			err = errors.New("Harness SDK 握手不兼容")
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
			err = fmt.Errorf("Harness SDK 在 30 秒内未完成握手；请检查此环境的 dsh SDK 安装。Windows 无响应时可先选 WSL。%w", err)
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
		failure := fmt.Errorf("Harness SDK %s 读取失败：%w", stream, err)
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
	return fmt.Errorf("Harness SDK 意外退出：%v %s", w.exitErr, w.diagnostic)
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
			if string(msg.ID) != fmt.Sprint(id) {
				continue
			}
			if msg.Error != nil {
				if method == "initialize" && isHarnessUnsupportedReasoning(msg.Error.Message) {
					return nil, fmt.Errorf("Harness initialize [UNSUPPORTED_REASONING_EFFORT]：%s", harnessUnsupportedReasoningMessage)
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
					failure = fmt.Errorf("Harness 主进程已终止，但无法确认所有子进程已停止：%w", err)
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
		_ = w.stop()
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
	return w.stop()
}

func checkHarness(c Config) (string, error) {
	if len(c.Workspaces) == 0 {
		return "", errors.New("需要配置工作目录")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	w, err := startHarness(ctx, c, Task{Workspace: c.Workspaces[0], Engine: "deepseek-harness"}, func(string, string) {})
	if err != nil {
		return "", err
	}
	defer w.stop()
	shutdown, cancelShutdown := context.WithTimeout(ctx, 5*time.Second)
	defer cancelShutdown()
	if _, err = w.request(shutdown, "shutdown", nil); err != nil {
		return "", err
	}
	return "Harness SDK 握手成功（未发送模型请求）。这不代表 API 密钥、余额或模型调用已验证；停止/重启后需新建空白会话，不能恢复原生上下文。", nil
}

func runHarnessSDK(ctx context.Context, c Config, t Task, input string, emit func(string, string)) (session, result string, runErr error) {
	session = t.Session
	if _, err := harnessPolicy(t); err != nil {
		return session, "", err
	}
	if len(t.Files) > 0 || c.HardwareAI != nil {
		return session, "", errors.New("Harness 暂不支持简作附件或硬件授权，请使用文字任务或其它引擎")
	}
	harnessRuntimes.Lock()
	w := harnessRuntimes.workers[session]
	if session != "" && w == nil {
		harnessRuntimes.Unlock()
		return session, "", errors.New(harnessLostSession)
	}
	// Account activation applies to new conversations. A live SDK process
	// already owns its original credential environment; changing the active
	// profile must neither interrupt it nor silently replace its account.
	if w != nil {
		c.EngineEnv = w.c.EngineEnv
	}
	if w != nil && w.fingerprint != harnessFingerprint(c, t) {
		harnessRuntimes.Unlock()
		return session, "", errors.New("Harness 会话建立后不能更换模型、账号、工作目录或权限，请新建任务")
	}
	if w != nil && !w.lease.TryLock() {
		harnessRuntimes.Unlock()
		return session, "", errors.New("Harness 会话正忙，请等待当前轮结束")
	}
	if w == nil {
		if len(harnessRuntimes.workers)+harnessRuntimes.starting >= 16 {
			harnessRuntimes.Unlock()
			return "", "", errors.New("最多保留 16 个 Harness 运行会话，请等待空闲会话释放或重启服务")
		}
		harnessRuntimes.starting++
		harnessRuntimes.Unlock()
		var err error
		w, err = startHarness(ctx, c, t, emit)
		harnessRuntimes.Lock()
		harnessRuntimes.starting--
		if err != nil {
			harnessRuntimes.Unlock()
			return "", "", err
		}
		w.lease.Lock()
		session = "jianzuo-" + uid()
		harnessRuntimes.workers[session] = w
	}
	if w.idle != nil {
		w.idle.Stop()
	}
	w.generation++
	generation := w.generation
	harnessRuntimes.Unlock()
	defer w.lease.Unlock()
	defer func() {
		if runErr != nil || t.ID == "" {
			if err := w.stop(); err != nil {
				runErr = fmt.Errorf("%v；%w", runErr, err)
			}
			harnessRuntimes.Lock()
			delete(harnessRuntimes.workers, session)
			harnessRuntimes.Unlock()
			return
		}
		w.idle = time.AfterFunc(30*time.Minute, func() {
			harnessRuntimes.Lock()
			if harnessRuntimes.workers[session] != w || w.generation != generation || !w.lease.TryLock() {
				harnessRuntimes.Unlock()
				return
			}
			delete(harnessRuntimes.workers, session)
			harnessRuntimes.Unlock()
			_ = w.stop()
			w.lease.Unlock()
		})
	}()
	select {
	case <-w.done:
		return session, "", errors.New(harnessLostSession)
	default:
	}
	emit("session", session)
	id, err := w.send(ctx, "session/prompt", map[string]any{"sessionId": session, "contentBlocks": []any{map[string]any{"type": "text", "text": input}}})
	if err != nil {
		return session, "", err
	}
	state := harnessTurn{session: session}
	var pending []codexRPC
	receiptTimer := time.NewTimer(30 * time.Second)
	defer receiptTimer.Stop()
	receiptDeadline := receiptTimer.C
	for {
		select {
		case <-receiptDeadline:
			return session, "", errors.New("Harness 未在 30 秒内确认消息入队")
		case <-ctx.Done():
			return session, state.result, ctx.Err()
		case err := <-w.fault:
			return session, state.result, err
		case msg, ok := <-w.frames:
			if !ok {
				return session, state.result, w.failure()
			}
			if string(msg.ID) == fmt.Sprint(id) {
				if msg.Error != nil {
					return session, state.result, fmt.Errorf("Harness prompt：%s", msg.Error.Message)
				}
				var receipt struct {
					MessageID string `json:"messageId"`
				}
				_ = json.Unmarshal(msg.Result, &receipt)
				if receipt.MessageID == "" {
					return session, "", errors.New("Harness 未返回消息入队回执")
				}
				state.receipt = receipt.MessageID
				receiptTimer.Stop()
				receiptDeadline = nil
				for _, frame := range pending {
					state.consume(frame, emit)
				}
				pending = nil
			} else if state.receipt == "" {
				if len(pending) >= 4096 {
					return session, "", errors.New("Harness 入队回执前事件过多")
				}
				pending = append(pending, msg)
			} else {
				state.consume(msg, emit)
			}
			if state.receipt != "" && state.active && state.ended && state.idle {
				if state.failure != "" {
					return session, state.result, errors.New(state.failure)
				}
				if state.result == "" {
					return session, "", errors.New("Harness 未返回最终回复")
				}
				return session, state.result, nil
			}
		}
	}
}

type harnessTurn struct {
	session, receipt, result, failure string
	active, ended, idle               bool
	usage                             RunUsage
}

// Provider codes are identifiers, never a free-form field for credentials or
// diagnostics. Reject long strings and punctuation instead of echoing them.
func harnessErrorCode(code string) string {
	if len(code) == 0 || len(code) > 64 || code[0] < 'A' || code[0] > 'Z' {
		return ""
	}
	for _, r := range code {
		if r != '_' && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
			return ""
		}
	}
	return code
}

func (s *harnessTurn) consume(msg codexRPC, emit func(string, string)) {
	var p struct {
		Session string `json:"sessionId"`
		Status  string `json:"status"`
		Event   struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		} `json:"event"`
	}
	if json.Unmarshal(msg.Params, &p) != nil || p.Session != s.session {
		return
	}
	if msg.Method == "session.status" {
		if s.active {
			s.idle = p.Status == "idle"
		}
		return
	}
	if msg.Method != "session.event" {
		return
	}
	var d struct {
		ID       string `json:"id"`
		Inserted []struct {
			ID string `json:"id"`
		} `json:"inserted"`
		Name      string          `json:"name"`
		Arguments string          `json:"arguments"`
		Usage     json.RawMessage `json:"usage"`
		Message   struct {
			Content []struct {
				Type    string          `json:"type"`
				Text    string          `json:"text"`
				Content json.RawMessage `json:"content"`
				IsError bool            `json:"isError"`
			} `json:"content"`
		} `json:"message"`
		Reason struct {
			Kind  string `json:"kind"`
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"reason"`
	}
	if json.Unmarshal(p.Event.Data, &d) != nil {
		return
	}
	if p.Event.Type == "agent/inbox/spliced" {
		for _, m := range d.Inserted {
			if m.ID == s.receipt {
				s.active = true
			}
		}
	}
	if p.Event.Type == "user/message" && d.ID == s.receipt {
		s.active = true
	}
	if !s.active {
		return
	}
	switch p.Event.Type {
	case "turn/start":
		s.ended = false
		s.idle = false
	case "assistant/message":
		var parts []string
		for _, b := range d.Message.Content {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			} else if b.Type == "reasoning" {
				emit("progress", b.Text)
			}
		}
		if text := strings.Join(parts, "\n"); text != "" {
			s.result = text
			emit("assistant", text)
		}
		if usage := parseUsage("deepseek-harness", d.Usage); usage != nil {
			s.usage.Input += usage.Input
			s.usage.Output += usage.Output
			s.usage.Cached += usage.Cached
			s.usage.CacheWrite += usage.CacheWrite
			s.usage.Total += usage.Total
			encoded, _ := json.Marshal(s.usage)
			emit("usage", string(encoded))
		}
	case "tool/call":
		emit("tool", d.Name+"\n"+d.Arguments)
	case "tool/result":
		for _, b := range d.Message.Content {
			if b.Type == "tool-result" {
				text := string(b.Content)
				if len(text) > 24000 {
					text = text[:24000] + "…"
				}
				if b.IsError {
					text = "工具执行失败：" + text
				}
				emit("tool", text)
			}
		}
	case "turn/end":
		s.ended = true
		if d.Reason.Kind != "completed" {
			code := harnessErrorCode(d.Reason.Error.Code)
			if code == "MISSING_CREDENTIAL" {
				s.failure = "Harness 缺少凭据 [MISSING_CREDENTIAL]：请在此任务对应的执行环境中配置 Harness 原生凭据，再新建任务重试。"
				break
			}
			s.failure = "Harness 未完成本轮：" + d.Reason.Kind
			if code != "" {
				s.failure += " [" + code + "]"
			}
			if d.Reason.Error.Message != "" {
				s.failure += "：" + d.Reason.Error.Message
			}
		}
	}
}
