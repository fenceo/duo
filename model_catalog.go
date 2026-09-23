package main

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
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Catalog reads use the exact execution target and account. In particular, a
// missing WSL user or CODEX_HOME must never silently select a different user.
func runModelEnvironmentCommand(ctx context.Context, env Environment, args ...string) ([]byte, []byte, error) {
	cmd := commandWithContext(ctx, environmentProbeCommand(env, args...))
	cmd.WaitDelay = time.Second
	hideCommand(cmd)
	stdout, stderr := &cappedOutput{limit: 4 * 1024 * 1024}, &cappedOutput{limit: 4096}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func modelReadDiagnostic(ctx context.Context, diagnostic string) string {
	if ctx.Err() != nil {
		return "读取超时或已取消，请检查目标环境是否能正常启动"
	}
	// Do not return arbitrary process stderr: launch/config errors can contain
	// credentials. Recognized infrastructure codes are sufficient for diagnosis.
	for _, code := range []string{"Wsl/Service/E_ACCESSDENIED", "Wsl/Service/0x8007274c", "Permission denied", "No such file", "not found"} {
		if strings.Contains(diagnostic, code) {
			return code + "；请检查所选环境、用户和 CLI 路径"
		}
	}
	return "无法启动目标环境中的模型读取，请检查环境、用户和 CLI 路径"
}

// Only top-level string values are needed. Never use a regexp spanning TOML
// tables: [profiles.other].model is not the selected account's default model.
func codexTopLevelString(raw []byte, key string) string {
	if len(raw) > 1024*1024 {
		return ""
	}
	pattern := regexp.MustCompile(`^` + regexp.QuoteMeta(key) + `\s*=\s*("(?:[^"\\]|\\.)*"|'[^']*')\s*(?:#.*)?$`)
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			break
		}
		match := pattern.FindStringSubmatch(line)
		if len(match) == 0 {
			continue
		}
		value := match[1]
		if strings.HasPrefix(value, "'") {
			return value[1 : len(value)-1]
		}
		decoded, err := strconv.Unquote(value)
		if err == nil {
			return decoded
		}
	}
	return ""
}

func configuredCodexModel(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return codexTopLevelString(raw, "model")
}

func configuredEngineModels(env Environment, engine string) []ModelOption {
	out := []ModelOption{}
	for _, model := range env.Models {
		if model.Engine != "" && model.Engine != engine {
			continue
		}
		model.Engine, model.Origin = engine, "configured"
		out = append(out, model)
	}
	return out
}

func mergeEngineCatalog(list ModelList, env Environment, engine string, configuredDefault string) ModelList {
	for i := range list.Models {
		list.Models[i].Engine = engine
	}
	configured := configuredEngineModels(env, engine)
	if configuredDefault != "" {
		list.DefaultModel = configuredDefault
		configured = append([]ModelOption{{ID: configuredDefault, Engine: engine, Origin: "configured"}}, configured...)
	}
	list.Models = mergeConfiguredModels(list.Models, configured)
	return list
}

func codexCatalogCommand(env Environment, selected map[string]string) (*exec.Cmd, Config) {
	c := runtimeConfig(Config{}, env)
	c.EngineEnv = selected
	if c.Codex == "" {
		c.Codex = "codex"
	}
	args := []string{c.Codex}
	if len(env.Workspaces) > 0 {
		args = append(args, "-C", env.Workspaces[0])
	}
	args = append(args, "app-server")
	if env.Type == "windows" {
		cmd := command(c, args...)
		applyEngineEnv(cmd, selected)
		if len(env.Workspaces) > 0 {
			cmd.Dir = env.Workspaces[0]
		}
		return cmd, c
	}
	// Same process-group launcher as native task execution, without task,
	// hardware, approval or tool configuration and without creating a thread.
	args = append([]string{"python3", "-u", "-c", launcher}, args...)
	return environmentProbeCommand(env, withEngineEnv(args, selected)...), c
}

// https://learn.chatgpt.com/docs/app-server#list-models-modellist
// Only initialize / initialized / model/list / config/read are sent. No conversation, tool,
// turn or generation API is invoked by opening or refreshing a model picker.
func nativeCodexCatalog(ctx context.Context, env Environment, selected map[string]string) (out ModelList, resultErr error) {
	out = ModelList{Models: []ModelOption{}, Source: "Codex 原生模型列表", Status: "ready", Modified: now()}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	cmd, config := codexCatalogCommand(env, selected)
	hideCommand(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return out, errors.New("无法打开 Codex 模型目录输入")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return out, errors.New("无法打开 Codex 模型目录输出")
	}
	// Capture no auth/config diagnostics in API output.
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return out, errors.New("无法启动目标环境的 Codex，请检查 CLI 路径和工作目录")
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
				item.Err = errors.New("Codex 模型目录返回无效 JSON")
			}
			select {
			case reads <- item:
			case <-done:
				return
			}
		}
		if scanner.Err() != nil {
			select {
			case reads <- codexWireRead{Err: errors.New("Codex 模型目录响应读取失败或过大")}:
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
					resultErr = errors.New("模型读取已结束，但未能确认目标 Codex 进程退出；请检查目标环境")
				}
				_ = stdout.Close()
				select {
				case <-wait:
				case <-time.After(time.Second):
					resultErr = errors.New("未能确认模型读取进程已退出")
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
					return nil, errors.New("Codex 在返回模型列表之前退出；请检查登录和 CLI 配置")
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
						return nil, errors.New("读取模型列表时收到意外的交互请求；未授权执行")
					}
					continue
				}
				var replyID string
				if json.Unmarshal(message.ID, &replyID) != nil || replyID != id {
					return nil, errors.New("Codex 模型目录响应 ID 不匹配")
				}
				if message.Error != nil {
					if message.Error.Code == -32601 {
						return nil, errors.New("此 Codex 版本不支持原生模型列表，请升级目标环境的 Codex CLI")
					}
					return nil, fmt.Errorf("Codex 模型目录请求失败（代码 %d）；请检查该环境的登录和 provider 配置", message.Error.Code)
				}
				return message.Result, nil
			}
		}
	}
	if _, err = request("duo-model-init", "initialize", map[string]any{"clientInfo": map[string]string{"name": "duo", "version": "1"}}); err != nil {
		return out, err
	}
	if err = write(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return out, err
	}
	cursor, seenCursors := "", map[string]bool{}
	for page := 0; page < 20; page++ {
		params := map[string]any{"limit": 100, "includeHidden": false}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := request(fmt.Sprintf("duo-model-list-%d", page), "model/list", params)
		if err != nil {
			return out, err
		}
		var response struct {
			Data []struct {
				ID               string `json:"id"`
				Model            string `json:"model"`
				DisplayName      string `json:"displayName"`
				Hidden           bool   `json:"hidden"`
				IsDefault        bool   `json:"isDefault"`
				DefaultReasoning string `json:"defaultReasoningEffort"`
				Efforts          []struct {
					Effort string `json:"reasoningEffort"`
				} `json:"supportedReasoningEfforts"`
			} `json:"data"`
			NextCursor string `json:"nextCursor"`
		}
		if json.Unmarshal(raw, &response) != nil || response.Data == nil {
			return out, errors.New("Codex 原生模型列表格式无效")
		}
		for _, model := range response.Data {
			if model.Hidden {
				continue
			}
			id := model.Model
			if id == "" {
				id = model.ID
			}
			if id == "" || len(id) > 200 || strings.ContainsAny(id, "\x00\r\n") {
				continue
			}
			option := ModelOption{ID: id, Name: model.DisplayName, Origin: "native", Engine: "codex", DefaultReasoning: model.DefaultReasoning}
			for _, effort := range model.Efforts {
				if effort.Effort != "" && validReasoning(effort.Effort) {
					option.ReasoningLevels = append(option.ReasoningLevels, effort.Effort)
				}
			}
			out.Models = append(out.Models, option)
			if model.IsDefault {
				out.DefaultModel = id
			}
		}
		cursor = response.NextCursor
		if cursor == "" {
			out.Models = mergeConfiguredModels(out.Models, nil)
			params := map[string]any{"includeLayers": false}
			if len(env.Workspaces) > 0 {
				params["cwd"] = env.Workspaces[0]
			}
			configRaw, configErr := request("duo-model-config", "config/read", params)
			if configErr != nil {
				if ctx.Err() != nil {
					return out, ctx.Err()
				}
				out.Message = "已读取原生目录，但未能读取当前默认模型配置。"
				return out, nil
			}
			// Decode only this whitelist. Provider auth/header/config values
			// never enter ModelList, errors, logs or persistent storage.
			var configured struct {
				Config struct {
					Model string `json:"model"`
				} `json:"config"`
			}
			if json.Unmarshal(configRaw, &configured) == nil && validCatalogModelID(configured.Config.Model) {
				out.DefaultModel = configured.Config.Model
				out.Models = mergeConfiguredModels(out.Models, []ModelOption{{ID: configured.Config.Model, Origin: "configured", Engine: "codex"}})
			}
			return out, nil
		}
		if seenCursors[cursor] {
			return out, errors.New("Codex 模型目录分页重复")
		}
		seenCursors[cursor] = true
	}
	return out, errors.New("Codex 模型目录页数超出限制")
}

func modelsForEnvironment(ctx context.Context, env Environment, profileEnv ...map[string]string) (ModelList, error) {
	var selected map[string]string
	if len(profileEnv) > 0 {
		selected = profileEnv[0]
	}
	nativeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	list, nativeErr := nativeCodexCatalog(nativeCtx, env, selected)
	cancel()
	if ctx.Err() != nil {
		return ModelList{}, ctx.Err()
	}
	if nativeErr == nil && len(list.Models) > 0 {
		list = mergeEngineCatalog(list, env, "codex", env.Model)
		list.Message = strings.TrimSpace(list.Message + " 来自当前环境和账号的 Codex 原生目录；目录条目不代表已完成实际调用测试。")
		return list, nil
	}
	cacheCtx, cancelCache := context.WithTimeout(ctx, 5*time.Second)
	cached, cacheErr := cachedModelsForEnvironment(cacheCtx, env, selected)
	cancelCache()
	if ctx.Err() != nil {
		return ModelList{}, ctx.Err()
	}
	for i := range cached.Models {
		cached.Models[i].Origin = "cache"
	}
	defaultModel := env.Model
	if defaultModel == "" {
		defaultModel = cached.DefaultModel
	}
	cached = mergeEngineCatalog(cached, env, "codex", defaultModel)
	cached.Status = "fallback"
	cached.Message = "原生目录未返回模型；以下为该账号缓存或显式配置，尚未验证可用性。"
	if nativeErr != nil {
		if errors.Is(nativeErr, context.DeadlineExceeded) {
			cached.Message = "Codex 原生模型目录读取超时；请检查该环境的 CLI、网络和登录。"
		} else {
			cached.Message = nativeErr.Error()
		}
	}
	if len(cached.Models) == 0 {
		cached.Status = "empty"
		if cacheErr != nil {
			cached.Message += " 同时未能读取该账号的本地模型配置。"
		}
	} else {
		cached.Message += " 当前显示缓存或已配置模型；未验证实际调用。"
	}
	if cached.Source == "" {
		cached.Source = "该环境的已配置模型"
	}
	return cached, nil
}

func validCatalogModelID(id string) bool {
	return strings.TrimSpace(id) != "" && len(id) <= 200 && !strings.ContainsAny(id, "\x00\r\n")
}

// A context switch aborts the old fetch before opening a new one. Give the
// old metadata process a short chance to exit, without letting a probe or
// update keep a new picker request queued indefinitely.
func (s *Server) admitModelCatalog(ctx context.Context) bool {
	timer, tick := time.NewTimer(3*time.Second), time.NewTicker(50*time.Millisecond)
	defer timer.Stop()
	defer tick.Stop()
	for {
		if ctx.Err() != nil || s.app.updating.Load() {
			return false
		}
		if s.modelProbeMu.TryLock() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return false
		case <-tick.C:
		}
	}
}

// Kept separate from native metadata so a failed/old CLI can still present an
// explicitly configured model without pretending it came from model/list.
func localEngineConfigRoot(selected map[string]string, key, defaultName string) string {
	if root := selected[key]; root != "" {
		return root
	}
	if root := os.Getenv(key); root != "" {
		return root
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, defaultName)
}
