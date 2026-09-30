package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Isolated metadata connection; the callback may only read account information.
// No thread, turn, tool, login/logout or token export is exposed to the browser.
func withCodexAccountRPC(ctx context.Context, env Environment, selected map[string]string, visit func(func(string, string, any) (json.RawMessage, error)) error) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	env.Workspaces = nil
	cmd, config := codexCatalogCommand(env, selected)
	if selected["CODEX_HOME"] != "" {
		if env.Type == "windows" {
			base := cmd.Env
			if base == nil {
				base = os.Environ()
			}
			clean := []string{}
			for _, value := range base {
				key, _, _ := strings.Cut(value, "=")
				if !strings.EqualFold(key, "OPENAI_API_KEY") && !strings.EqualFold(key, "CODEX_API_KEY") && !strings.EqualFold(key, "OPENAI_BASE_URL") {
					clean = append(clean, value)
				}
			}
			cmd.Env = clean
		} else {
			args := []string{"env", "-u", "OPENAI_API_KEY", "-u", "CODEX_API_KEY", "-u", "OPENAI_BASE_URL"}
			args = append(args, withEngineEnv([]string{"python3", "-u", "-c", launcher, config.Codex, "app-server"}, selected)...)
			cmd = environmentProbeCommand(env, args...)
		}
	}
	hideCommand(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return errors.New("无法打开 Codex 账号信息输入")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return errors.New("无法打开 Codex 账号信息输出")
	}
	// Capture no auth/config diagnostics in API output.
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return errors.New("无法启动目标环境的 Codex，请检查 CLI 路径和工作目录")
	}
	reads := make(chan codexWireRead, 8)
	done, readerDone := make(chan struct{}), make(chan struct{})
	wait := make(chan error, 1)
	go func() {
		defer close(readerDone)
		defer close(reads)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 65536), 4*1024*1024)
		for scanner.Scan() {
			var item codexWireRead
			var envelope struct {
				Type string `json:"type"`
				PID  int    `json:"pid"`
			}
			if json.Unmarshal(scanner.Bytes(), &envelope) == nil && envelope.Type == "jianzuo.process" {
				item.PID = envelope.PID
			} else if json.Unmarshal(scanner.Bytes(), &item.Message) != nil {
				item.Err = errors.New("Codex 账号信息返回无效 JSON")
			}
			select {
			case reads <- item:
			case <-done:
				return
			}
		}
		if scanner.Err() != nil {
			select {
			case reads <- codexWireRead{Err: errors.New("Codex 账号信息响应读取失败或过大")}:
			case <-done:
			}
		}
	}()
	go func() { <-readerDone; wait <- cmd.Wait() }()
	pid := 0
	defer func() {
		_ = stdin.Close()
		// Drain PID notifications queued just before cancellation, so remote
		// cleanup never guesses a process ID or touches another user's process.
		drain := time.NewTimer(300 * time.Millisecond)
		defer drain.Stop()
		for {
			select {
			case item, ok := <-reads:
				if ok && item.PID > 1 {
					pid = item.PID
				}
				if !ok {
					reads = nil
				}
			case <-wait:
				close(done)
				return
			case <-drain.C:
				close(done)
				if err := stopAppServerTree(config, cmd, pid); err != nil {
					resultErr = errors.New("账号读取已结束，但未能确认目标 Codex 进程退出；请检查目标环境")
				}
				_ = stdout.Close()
				select {
				case <-wait:
				case <-time.After(time.Second):
					resultErr = errors.New("未能确认账号读取进程已退出")
				}
				return
			}
		}
	}()
	write := func(value any) error {
		raw, _ := json.Marshal(value)
		finished := make(chan error, 1)
		go func() { _, err := stdin.Write(append(raw, '\n')); finished <- err }()
		select {
		case err := <-finished:
			return err
		case <-ctx.Done():
			_ = stdin.Close()
			return ctx.Err()
		}
	}
	request := func(id, method string, params any) (json.RawMessage, error) {
		if err := write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
			return nil, err
		}
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case item, ok := <-reads:
				if !ok {
					return nil, errors.New("Codex 在返回账号信息之前退出；请检查登录和 CLI 配置")
				}
				if item.Err != nil {
					return nil, item.Err
				}
				if item.PID > 1 {
					pid = item.PID
					continue
				}
				message := item.Message
				if message.Method != "" {
					if len(message.ID) > 0 {
						return nil, errors.New("读取账号信息时收到意外的交互请求；未授权执行")
					}
					continue
				}
				var replyID string
				if json.Unmarshal(message.ID, &replyID) != nil || replyID != id {
					return nil, errors.New("Codex 账号信息响应 ID 不匹配")
				}
				if message.Error != nil {
					if message.Error.Code == -32601 {
						return nil, errors.New("此 Codex 版本不支持原生账号信息，请升级目标环境的 Codex CLI")
					}
					return nil, fmt.Errorf("Codex 账号信息请求失败（代码 %d）；请检查该环境的登录和 provider 配置", message.Error.Code)
				}
				return message.Result, nil
			}
		}
	}
	if _, err = request("duo-account-init", "initialize", map[string]any{"clientInfo": map[string]string{"name": "duo", "version": "1"}}); err != nil {
		return err
	}
	if err = write(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return err
	}

	return visit(func(id, method string, params any) (json.RawMessage, error) {
		if method != "account/read" && method != "account/rateLimits/read" {
			return nil, errors.New("账号查询不允许此操作")
		}
		return request(id, method, params)
	})
}
