package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"
)

// Native authentication only: no thread/start, turn/start or model request.
// Tokens remain in the dedicated CODEX_HOME and are never returned to the web.
func setupCodexAccount(ctx context.Context, env Environment, home, login, key string, waiting func(string, string)) error {
	c := runtimeConfig(Config{}, env)
	if c.Codex == "" {
		c.Codex = "codex"
	}
	cmd := command(c, c.Codex, "-c", `cli_auth_credentials_store="file"`, "app-server")
	cmd.Dir = home
	applyEngineEnv(cmd, map[string]string{"CODEX_HOME": home})
	hideCommand(cmd)
	input, err := cmd.StdinPipe()
	if err != nil {
		return errors.New("无法打开 Codex 登录输入")
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return errors.New("无法打开 Codex 登录输出")
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		input.Close()
		output.Close()
		return errors.New("无法启动 Codex，请先安装工具或检查该环境的路径")
	}
	messages := make(chan codexRPC, 8)
	readerDone := make(chan struct{})
	stop := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer close(messages)
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 65536), 1024*1024)
		for scanner.Scan() {
			var m codexRPC
			if json.Unmarshal(scanner.Bytes(), &m) != nil {
				return
			}
			select {
			case messages <- m:
			case <-stop:
				return
			}
		}
	}()
	finished := make(chan error, 1)
	go func() { <-readerDone; finished <- cmd.Wait() }()
	defer func() {
		input.Close()
		close(stop)
		select {
		case <-finished:
		case <-time.After(300 * time.Millisecond):
			_ = stopAppServerTree(c, cmd, 0)
			output.Close()
			select {
			case <-finished:
			case <-time.After(time.Second):
			}
		}
	}()
	write := func(v any) error {
		raw, _ := json.Marshal(v)
		result := make(chan error, 1)
		go func() { _, err := input.Write(append(raw, '\n')); result <- err }()
		select {
		case err := <-result:
			return err
		case <-ctx.Done():
			input.Close()
			return ctx.Err()
		}
	}
	if err = write(map[string]any{"id": "setup-init", "method": "initialize", "params": map[string]any{"clientInfo": map[string]string{"name": "duo-account-setup", "version": "1"}, "capabilities": map[string]bool{"experimentalApi": true}}}); err != nil {
		return errors.New("无法连接 Codex 登录服务")
	}
	loginID := ""
	started := false
	type completion struct {
		LoginID string `json:"loginId"`
		Success bool   `json:"success"`
	}
	var completed *completion
	for {
		if started && completed != nil && completed.LoginID == loginID {
			if completed.Success {
				return nil
			}
			return errors.New("Codex 登录未完成，请重新登录")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case m, ok := <-messages:
			if !ok {
				return errors.New("Codex 登录连接已结束，请重新开始")
			}
			if m.Method != "" {
				if len(m.ID) > 0 && string(m.ID) != "null" {
					return errors.New("登录过程中收到意外交互请求，未授权执行")
				}
				if m.Method == "account/login/completed" {
					var done completion
					if json.Unmarshal(m.Params, &done) == nil {
						completed = &done
					}
				}
				continue
			}
			var id string
			if json.Unmarshal(m.ID, &id) != nil {
				return errors.New("Codex 登录协议无效")
			}
			if m.Error != nil {
				return errors.New("Codex 登录请求失败，请检查工具版本、网络和账号设置")
			}
			switch id {
			case "setup-init":
				if err = write(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
					return err
				}
				params := map[string]string{"type": login}
				if login == "apiKey" {
					params["apiKey"] = key
				}
				if err = write(map[string]any{"id": "setup-login", "method": "account/login/start", "params": params}); err != nil {
					return errors.New("Codex 登录请求发送失败")
				}
				key = ""
			case "setup-login":
				var result struct {
					Type    string `json:"type"`
					LoginID string `json:"loginId"`
					URL     string `json:"verificationUrl"`
					Code    string `json:"userCode"`
				}
				if json.Unmarshal(m.Result, &result) != nil || result.Type != login {
					return errors.New("Codex 登录响应类型不匹配")
				}
				if login == "apiKey" {
					return nil
				}
				u, err := url.Parse(result.URL)
				if err != nil || u.Scheme != "https" || u.User != nil || !(u.Hostname() == "auth.openai.com" || u.Hostname() == "chatgpt.com") || result.LoginID == "" || result.Code == "" || len(result.Code) > 128 || strings.ContainsAny(result.Code, "\x00\r\n") {
					return errors.New("Codex 返回的设备登录信息无效")
				}
				loginID = result.LoginID
				started = true
				waiting(result.URL, result.Code)
			default:
				return errors.New("Codex 登录响应标识不匹配")
			}
		}
	}
}
