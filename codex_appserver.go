package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Each run owns one stdio app-server. Threads remain in the user's native Codex
// store; only the selected thread ID is resumed, never the most recent thread.
// Protocol fields are based on the locally generated Codex 0.153.4 v2 schema.
func codexAppServerArgs(c Config, t Task) []string {
	sandbox, approval, reviewer, network := codexPermissionSettings(t)
	args := []string{"-C", t.Workspace,
		"-c", "sandbox_mode=" + strconv.Quote(sandbox),
		"-c", "approval_policy=" + strconv.Quote(approval),
		"-c", "approvals_reviewer=" + strconv.Quote(reviewer),
		"-c", "sandbox_workspace_write.network_access=" + strconv.FormatBool(network)}
	if h := c.HardwareAI; h != nil {
		args = append(args, "-c", "mcp_servers.jianzuo_hardware.url="+strconv.Quote(h.URL),
			"-c", `mcp_servers.jianzuo_hardware.bearer_token_env_var="JIANZUO_HARDWARE_TOKEN"`,
			"-c", `mcp_servers.jianzuo_hardware.required=true`,
			"-c", `mcp_servers.jianzuo_hardware.default_tools_approval_mode="approve"`,
			"-c", `mcp_servers.jianzuo_hardware.tool_timeout_sec=95`)
	}
	return append(args, "app-server")
}

// Read exactly one bootstrap line without buffered read-ahead. The app-server
// then inherits the live stdin pipe, including JSON-RPC already queued in it.
// Unlike hardwareLauncher, this must never wait for stdin EOF before starting.
const codexAppServerHardwareLauncher = `import sys,subprocess,json,os,urllib.request
line=bytearray()
while True:
 b=os.read(0,1)
 if not b: raise RuntimeError('missing hardware bootstrap')
 if b==b'\n': break
 line.extend(b)
 if len(line)>65536: raise RuntimeError('hardware bootstrap too large')
payload=json.loads(line)
print(json.dumps({'type':'jianzuo.process','pid':os.getpid()}),flush=True)
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*args,**kwargs): return None
client=urllib.request.build_opener(urllib.request.ProxyHandler({}),NoRedirect())
message={'jsonrpc':'2.0','id':'probe','method':'initialize','params':{'protocolVersion':'2025-06-18','capabilities':{},'clientInfo':{'name':'jianzuo-probe','version':'1'}}}
selected=None;failures=[]
for address in [payload['url']]+(payload.get('fallback_urls') or []):
 try:
  request=urllib.request.Request(address,data=json.dumps(message).encode(),headers={'Content-Type':'application/json','Accept':'application/json, text/event-stream','Authorization':'Bearer '+payload['token']})
  with client.open(request,timeout=4) as response: result=json.load(response)
  if result.get('result',{}).get('serverInfo',{}).get('name')!='jianzuo-hardware': raise ValueError('not a Jianzuo hardware endpoint')
  selected=address;break
 except Exception as error: failures.append(address+': '+str(error))
if selected is None:
 print('Hardware MCP connection failed. Tried addresses: '+'; '.join(failures),file=sys.stderr)
 sys.exit(1)
args=sys.argv[2:]
for i,arg in enumerate(args):
 if arg.startswith('mcp_servers.jianzuo_hardware.url='): args[i]='mcp_servers.jianzuo_hardware.url='+json.dumps(selected)
print(json.dumps({'type':'jianzuo.hardware','url':selected}),flush=True)
env=os.environ.copy();env['JIANZUO_HARDWARE_TOKEN']=payload['token']
os.chdir(sys.argv[1])
p=subprocess.Popen(args,start_new_session=True,env=env)
print(json.dumps({'type':'jianzuo.process','pid':p.pid}),flush=True)
sys.exit(p.wait())
`

func codexAppServerCommand(c Config, t Task) *exec.Cmd {
	args := codexAppServerArgs(c, t)
	if c.Distro == "" && c.SSHHost == "" {
		cmd := command(c, append([]string{c.Codex}, args...)...)
		cmd.Dir = t.Workspace
		if c.HardwareAI != nil {
			cmd.Env = append(os.Environ(), "JIANZUO_HARDWARE_TOKEN="+c.HardwareAI.Token)
		}
		applyEngineEnv(cmd, c.EngineEnv)
		return cmd
	}
	base := []string{"python3", "-u", "-c", launcher, c.Codex}
	if c.HardwareAI != nil {
		base = []string{"python3", "-u", "-c", codexAppServerHardwareLauncher, t.Workspace, c.Codex}
	}
	base = append(base, args...)
	if c.SSHHost != "" {
		return command(c, withEngineEnv(base, c.EngineEnv)...)
	}
	// --exec avoids an extra shell interpreting script text or workspace names.
	wslArgs := []string{"-d", c.Distro}
	if c.User != "" {
		wslArgs = append(wslArgs, "-u", c.User)
	}
	return wslCommand(append(append(wslArgs, "--exec"), withEngineEnv(base, c.EngineEnv)...)...)
}

func codexSandboxPolicy(sandbox, workspace string, network bool) map[string]any {
	switch sandbox {
	case "read-only":
		return map[string]any{"type": "readOnly", "networkAccess": network}
	case "danger-full-access":
		return map[string]any{"type": "dangerFullAccess"}
	default:
		return map[string]any{"type": "workspaceWrite", "writableRoots": []string{workspace}, "networkAccess": network, "excludeTmpdirEnvVar": false, "excludeSlashTmp": false}
	}
}

type codexRPC struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type codexWireRead struct {
	Message codexRPC
	PID     int
	URL     string
	Err     error
}

type codexInteractionResult struct {
	Key        string
	Generation uint64
	Result     json.RawMessage
	Err        error
}

type codexPendingRequest struct {
	ID         json.RawMessage
	Generation uint64
	Cancel     context.CancelFunc
}

func codexSupportedInteraction(method string) bool {
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval", "item/tool/requestUserInput", "mcpServer/elicitation/request":
		return true
	}
	return false
}

// stopAppServerTree is a last-resort cleanup after native turn/interrupt. A
// failed remote cleanup is observable rather than reported as a confirmed stop.
func stopAppServerTree(c Config, cmd *exec.Cmd, pid int) error {
	if c.Distro != "" || c.SSHHost != "" {
		if pid <= 1 {
			_ = cmd.Process.Kill()
			return errors.New("未取得执行端进程号，无法确认 Codex 子进程已停止")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		kill := commandWithContext(ctx, command(c, "python3", "-c", stopTree, strconv.Itoa(pid)))
		hideCommand(kill)
		err := kill.Run()
		_ = cmd.Process.Kill()
		if err != nil {
			return fmt.Errorf("无法确认执行端 Codex 进程 %d 已停止：%w", pid, err)
		}
		return nil
	}
	kill := exec.Command("taskkill.exe", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
	hideCommand(kill)
	err := kill.Run()
	if err != nil {
		if direct := cmd.Process.Kill(); direct != nil && !errors.Is(direct, os.ErrProcessDone) {
			return fmt.Errorf("无法确认 Codex 进程已停止：%w", direct)
		}
	}
	return nil
}

func runCodexAppServer(ctx context.Context, c Config, t Task, input string, emit func(string, string)) (session string, result string, runErr error) {
	session = t.Session
	if ctx.Err() != nil {
		return session, "", ctx.Err()
	}
	if c.HardwareAI != nil && c.Distro == "" && c.SSHHost == "" {
		if err := probeHardwareMCP(ctx, c.HardwareAI); err != nil {
			return session, "", err
		}
	}
	redact := func(s string) string {
		if c.HardwareAI != nil && c.HardwareAI.Token != "" {
			s = strings.ReplaceAll(s, c.HardwareAI.Token, "[REDACTED]")
		}
		return s
	}
	cmd := codexAppServerCommand(c, t)
	hideCommand(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return session, "", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return session, "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return session, "", err
	}
	if err = cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return session, "", err
	}
	emit("progress", "正在连接 Codex 原生会话…")
	done := make(chan struct{})
	reads := make(chan codexWireRead, 32)
	stderrDone := make(chan struct{})
	stdoutDone := make(chan struct{})
	wait := make(chan error, 1)
	var diagnosticMu sync.Mutex
	diagnostic := ""
	go func() {
		defer close(stderrDone)
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 65536), 4*1024*1024)
		for scanner.Scan() {
			line := redact(scanner.Text())
			diagnosticMu.Lock()
			diagnostic = line
			diagnosticMu.Unlock()
			emit("log", line)
		}
	}()
	go func() {
		defer close(stdoutDone)
		defer close(reads)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 65536), 4*1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			var envelope struct {
				Type string `json:"type"`
				PID  int    `json:"pid"`
				URL  string `json:"url"`
			}
			var item codexWireRead
			if json.Unmarshal(line, &envelope) == nil && envelope.Type == "jianzuo.process" {
				item.PID = envelope.PID
			} else if envelope.Type == "jianzuo.hardware" {
				item.URL = envelope.URL
			} else if parseErr := json.Unmarshal(line, &item.Message); parseErr != nil {
				item.Err = errors.New("Codex app-server 返回了无效 JSON，协议已停止")
			}
			select {
			case reads <- item:
			case <-done:
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case reads <- codexWireRead{Err: fmt.Errorf("Codex 协议读取失败：%w", err)}:
			case <-done:
			}
		}
	}()
	go func() {
		<-stdoutDone
		<-stderrDone
		wait <- cmd.Wait()
	}()
	pid := 0
	pending := map[string]codexPendingRequest{}
	cancelRequests := func() {
		for key, request := range pending {
			request.Cancel()
			delete(pending, key)
		}
	}
	defer func() {
		cancelRequests()
		close(done)
		_ = stdin.Close()
		select {
		case <-wait:
			return
		case <-time.After(time.Second):
		}
		if stopErr := stopAppServerTree(c, cmd, pid); stopErr != nil {
			emit("error", stopErr.Error())
			runErr = stopErr
		}
		_ = stdout.Close()
		_ = stderr.Close()
		select {
		case <-wait:
		case <-time.After(time.Second):
			if runErr == nil {
				runErr = errors.New("Codex 会话已结束，但未能确认执行进程退出")
			}
		}
	}()
	// A wedged server must not trap cancellation in a full stdin pipe. All
	// writes are serialized by this event loop; on timeout/cancel the pipe is
	// closed permanently and cleanup stops the process tree.
	writeContext := func(writeCtx context.Context, value any) error {
		payload, err := json.Marshal(value)
		if err != nil {
			return err
		}
		written := make(chan error, 1)
		go func() { _, err := stdin.Write(append(payload, '\n')); written <- err }()
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		select {
		case err := <-written:
			return err
		case <-writeCtx.Done():
			_ = stdin.Close()
			return writeCtx.Err()
		case <-timer.C:
			_ = stdin.Close()
			return errors.New("Codex 协议写入超时，已停止执行")
		}
	}
	write := func(value any) error { return writeContext(ctx, value) }
	request := func(id, method string, params any) error {
		if method == "turn/interrupt" {
			return writeContext(context.Background(), map[string]any{"id": id, "method": method, "params": params})
		}
		return write(map[string]any{"id": id, "method": method, "params": params})
	}
	if c.HardwareAI != nil && (c.Distro != "" || c.SSHHost != "") {
		if err = write(map[string]any{"token": c.HardwareAI.Token, "url": c.HardwareAI.URL, "fallback_urls": c.HardwareAI.FallbackURLs}); err != nil {
			return session, "", err
		}
	}
	if err = request("jianzuo-init", "initialize", map[string]any{"clientInfo": map[string]string{"name": "jianzuo", "version": "1"}, "capabilities": map[string]any{"experimentalApi": true}}); err != nil {
		return session, "", err
	}
	sandbox, approval, reviewer, network := codexPermissionSettings(t)
	config := map[string]any{"sandbox_workspace_write.network_access": network}
	threadParams := map[string]any{"cwd": t.Workspace, "approvalPolicy": approval, "approvalsReviewer": reviewer, "sandbox": sandbox, "config": config}
	if t.Model != "" {
		threadParams["model"] = t.Model
	}
	inputs := []any{map[string]any{"type": "text", "text": input, "text_elements": []any{}}}
	for _, f := range t.Files {
		if isImageAttachment(f.Attachment) {
			inputs = append(inputs, map[string]any{"type": "localImage", "path": f.Path})
		}
	}
	interactionResults := make(chan codexInteractionResult, 64)
	var requestGeneration uint64
	turnID := ""
	stopping := false
	interruptSent := false
	ctxDone := ctx.Done()
	stageTimer := time.NewTimer(30 * time.Second)
	defer stageTimer.Stop()
	stageTimeout := stageTimer.C
	var stopTimeout <-chan time.Time
	var stopTimer *time.Timer
	defer func() {
		if stopTimer != nil {
			stopTimer.Stop()
		}
	}()
	sendInterrupt := func() error {
		if stopping && !interruptSent && session != "" && turnID != "" {
			interruptSent = true
			return request("jianzuo-interrupt", "turn/interrupt", map[string]string{"threadId": session, "turnId": turnID})
		}
		return nil
	}
	seenItems := map[string]bool{}
	fileChanges := map[string]json.RawMessage{}
	fileChangesBytes := 0
	var priorUsage *codexTokenUsage
	var usage RunUsage
	for {
		select {
		case <-ctxDone:
			stopping = true
			ctxDone = nil
			cancelRequests()
			stopTimer = time.NewTimer(4 * time.Second)
			stopTimeout = stopTimer.C
			if err = sendInterrupt(); err != nil {
				return session, result, ctx.Err()
			}
		case <-stopTimeout:
			return session, result, ctx.Err()
		case <-stageTimeout:
			return session, result, errors.New("Codex 原生协议初始化超时，请检查 CLI 版本、执行环境及 MCP 配置")
		case answer := <-interactionResults:
			p, ok := pending[answer.Key]
			if !ok || p.Generation != answer.Generation {
				continue // Resolved/cancelled by the server while the UI was open.
			}
			p.Cancel()
			delete(pending, answer.Key)
			if stopping || ctx.Err() != nil {
				continue
			}
			if answer.Err != nil {
				err = write(map[string]any{"id": p.ID, "error": map[string]any{"code": -32000, "message": "简作未授权此请求：" + redact(answer.Err.Error())}})
			} else if !json.Valid(answer.Result) {
				return session, result, errors.New("无效的 Codex 审批响应，已停止执行")
			} else {
				err = write(map[string]any{"id": p.ID, "result": answer.Result})
			}
			if err != nil {
				return session, result, err
			}
		case read, ok := <-reads:
			if !ok {
				diagnosticMu.Lock()
				last := diagnostic
				diagnosticMu.Unlock()
				if ctx.Err() != nil {
					return session, result, ctx.Err()
				}
				return session, result, fmt.Errorf("Codex app-server 在 turn/completed 前断开：%s", last)
			}
			if read.Err != nil {
				return session, result, read.Err
			}
			if read.PID > 1 {
				pid = read.PID
				continue
			}
			if read.URL != "" {
				emit("progress", "已连接硬件服务："+redact(read.URL))
				continue
			}
			m := read.Message
			if m.Method == "" {
				var id string
				_ = json.Unmarshal(m.ID, &id)
				if m.Error != nil {
					return session, result, fmt.Errorf("Codex 原生请求 %s 失败：%s", id, redact(m.Error.Message))
				}
				if stopping || ctx.Err() != nil {
					if id != "jianzuo-turn" {
						continue
					}
				}
				switch id {
				case "jianzuo-init":
					if err = write(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
						return session, result, err
					}
					method := "thread/start"
					if session != "" {
						method = "thread/resume"
						threadParams["threadId"] = session
					}
					err = request("jianzuo-thread", method, threadParams)
				case "jianzuo-thread":
					var v struct {
						Thread struct {
							ID string `json:"id"`
						} `json:"thread"`
					}
					if json.Unmarshal(m.Result, &v) != nil || v.Thread.ID == "" {
						return session, result, errors.New("Codex 未返回有效的原生线程 ID")
					}
					if session != "" && session != v.Thread.ID {
						return session, result, errors.New("Codex 恢复的线程与任务记录不一致，已停止")
					}
					session = v.Thread.ID
					emit("session", session)
					if ctx.Err() != nil {
						return session, result, ctx.Err()
					}
					params := map[string]any{"threadId": session, "cwd": t.Workspace, "input": inputs, "approvalPolicy": approval, "approvalsReviewer": reviewer, "sandboxPolicy": codexSandboxPolicy(sandbox, t.Workspace, network)}
					if t.Model != "" {
						params["model"] = t.Model
					}
					if t.ReasoningEffort != "" {
						params["effort"] = t.ReasoningEffort
					}
					err = request("jianzuo-turn", "turn/start", params)
				case "jianzuo-turn":
					var v struct {
						Turn struct {
							ID string `json:"id"`
						} `json:"turn"`
					}
					if json.Unmarshal(m.Result, &v) != nil || v.Turn.ID == "" {
						return session, result, errors.New("Codex 未返回有效的轮次 ID")
					}
					if turnID != "" && turnID != v.Turn.ID {
						return session, result, errors.New("Codex 轮次 ID 不一致")
					}
					turnID = v.Turn.ID
					stageTimer.Stop()
					stageTimeout = nil
					err = sendInterrupt()
				}
				if err != nil {
					return session, result, err
				}
				continue
			}
			var p struct {
				ThreadID  string          `json:"threadId"`
				TurnID    string          `json:"turnId"`
				ItemID    string          `json:"itemId"`
				RequestID json.RawMessage `json:"requestId"`
				Turn      struct {
					ID     string `json:"id"`
					Status string `json:"status"`
					Error  *struct {
						Message string `json:"message"`
					} `json:"error"`
				} `json:"turn"`
				Item struct {
					ID      string `json:"id"`
					Type    string `json:"type"`
					Text    string `json:"text"`
					Phase   string `json:"phase"`
					Command string `json:"command"`
					Status  string `json:"status"`
					Output  string `json:"aggregatedOutput"`
					Changes []struct {
						Path string `json:"path"`
						Diff string `json:"diff"`
					} `json:"changes"`
				} `json:"item"`
				Message string `json:"message"`
				Summary string `json:"summary"`
				Error   struct {
					Message string `json:"message"`
				} `json:"error"`
				TokenUsage struct {
					Total codexTokenUsage `json:"total"`
					Last  codexTokenUsage `json:"last"`
				} `json:"tokenUsage"`
			}
			if json.Unmarshal(m.Params, &p) != nil {
				return session, result, errors.New("Codex 返回无效事件参数")
			}
			foreign := (p.ThreadID != "" && p.ThreadID != session) || (p.TurnID != "" && turnID != "" && p.TurnID != turnID)
			if (m.Method == "turn/started" || m.Method == "turn/completed") && turnID != "" && p.Turn.ID != "" && p.Turn.ID != turnID {
				foreign = true
			}
			if len(m.ID) > 0 && string(m.ID) != "null" {
				if foreign || !codexSupportedInteraction(m.Method) || stopping || len(pending) >= 64 || turnID == "" || session == "" || p.ThreadID == "" || (p.TurnID == "" && m.Method != "mcpServer/elicitation/request") {
					_ = writeContext(context.Background(), map[string]any{"id": m.ID, "error": map[string]any{"code": -32601, "message": "简作不支持或不能授权此请求"}})
					if !foreign && !stopping {
						return session, result, fmt.Errorf("Codex 请求 %s 不受当前适配器支持，已停止", m.Method)
					}
					continue
				}
				key := string(m.ID)
				if _, exists := pending[key]; exists {
					return session, result, errors.New("Codex 重复使用了尚未完成的请求 ID")
				}
				child, cancel := context.WithCancel(ctx)
				requestGeneration++
				pending[key] = codexPendingRequest{ID: append(json.RawMessage{}, m.ID...), Generation: requestGeneration, Cancel: cancel}
				// Native file approvals refer to an item, while its diff arrived in
				// item/started. Add display-only context to the local UI request;
				// the native response and its authorization decision stay unchanged.
				if m.Method == "item/fileChange/requestApproval" && fileChanges[p.ItemID] != nil {
					var display map[string]json.RawMessage
					if json.Unmarshal(m.Params, &display) == nil {
						display["changes"] = fileChanges[p.ItemID]
						if enriched, marshalErr := json.Marshal(display); marshalErr == nil && len(enriched) <= 256*1024 {
							m.Params = enriched
						}
					}
				}
				go func(child context.Context, message codexRPC, key string, generation uint64) {
					response, handleErr := handleCodexInteraction(child, message.Method, message.Params)
					select {
					case interactionResults <- codexInteractionResult{Key: key, Generation: generation, Result: response, Err: handleErr}:
					case <-done:
					}
				}(child, m, key, requestGeneration)
				continue
			}
			if foreign {
				continue
			}
			// Task-scoped events must carry the native identity. Only global
			// warning/config events are allowed without a thread/turn envelope.
			switch m.Method {
			case "serverRequest/resolved", "turn/started", "turn/completed":
				if session == "" || p.ThreadID != session {
					continue
				}
			case "item/started", "item/completed", "thread/tokenUsage/updated":
				if session == "" || turnID == "" || p.ThreadID != session || p.TurnID != turnID {
					continue
				}
			}
			switch m.Method {
			case "serverRequest/resolved":
				key := string(p.RequestID)
				if request, ok := pending[key]; ok {
					request.Cancel()
					delete(pending, key)
				}
			case "turn/started":
				if turnID == "" {
					turnID = p.Turn.ID
				}
				if err = sendInterrupt(); err != nil {
					return session, result, err
				}
			case "item/started", "item/completed":
				item := p.Item
				switch item.Type {
				case "agentMessage":
					if m.Method == "item/completed" && !seenItems[item.ID] {
						seenItems[item.ID] = true
						if item.Phase == "commentary" {
							emit("progress", item.Text)
						} else {
							result = item.Text
							emit("assistant", item.Text)
						}
					}
				case "commandExecution":
					emit("tool", redact(item.Command+"\n"+item.Status+"\n"+item.Output))
				case "fileChange":
					if item.ID != "" && len(item.Changes) != 0 {
						changes, _ := json.Marshal(item.Changes)
						updatedBytes := fileChangesBytes - len(fileChanges[item.ID]) + len(changes)
						if len(changes) <= 128*1024 && updatedBytes <= 4*1024*1024 {
							fileChanges[item.ID] = changes
							fileChangesBytes = updatedBytes
						}
					}
					for _, change := range item.Changes {
						emit("tool", "文件修改："+change.Path+"\n"+change.Diff)
					}
				default:
					if item.Text != "" {
						emit("progress", item.Text)
					}
				}
			case "thread/tokenUsage/updated":
				current := p.TokenUsage.Total
				delta := p.TokenUsage.Last
				if priorUsage != nil && current.Input >= priorUsage.Input && current.Output >= priorUsage.Output {
					delta = codexTokenUsage{Input: current.Input - priorUsage.Input, Output: current.Output - priorUsage.Output, Cached: current.Cached - priorUsage.Cached, CacheWrite: current.CacheWrite - priorUsage.CacheWrite}
				}
				priorUsage = &current
				if delta.Input >= 0 && delta.Output >= 0 && delta.Cached >= 0 && delta.CacheWrite >= 0 {
					usage.Input += delta.Input
					usage.Output += delta.Output
					usage.Cached += delta.Cached
					usage.CacheWrite += delta.CacheWrite
					usage.Total = usage.Input + usage.Output
					payload, _ := json.Marshal(usage)
					emit("usage", string(payload))
				}
			case "warning", "configWarning":
				emit("progress", redact(strings.TrimSpace(p.Summary+" "+p.Message)))
			case "error":
				emit("error", redact(p.Error.Message))
			case "turn/completed":
				if turnID == "" || p.Turn.ID != turnID {
					return session, result, errors.New("Codex 结束事件的轮次 ID 不一致")
				}
				cancelRequests()
				if stopping || ctx.Err() != nil || p.Turn.Status == "interrupted" {
					return session, result, context.Canceled
				}
				if p.Turn.Status != "completed" {
					message := "Codex 原生轮次失败"
					if p.Turn.Error != nil && p.Turn.Error.Message != "" {
						message += "：" + redact(p.Turn.Error.Message)
					}
					return session, result, errors.New(message)
				}
				if result == "" {
					return session, result, errors.New("Codex 原生轮次结束但没有最终回复")
				}
				return session, result, nil
			}
		}
	}
}

type codexTokenUsage struct {
	Input      int64 `json:"inputTokens"`
	Output     int64 `json:"outputTokens"`
	Cached     int64 `json:"cachedInputTokens"`
	CacheWrite int64 `json:"cacheWriteInputTokens"`
}
