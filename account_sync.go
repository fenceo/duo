package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Only login/routing fields are transferred. Target MCP servers, permissions,
// hooks, workspace trust and other machine-specific preferences are retained.
var claudeAccountEnvKeys = []string{
	"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
	"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_MODEL",
	"ANTHROPIC_CUSTOM_HEADERS",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL",
}

func accountJSON(raw []byte) (map[string]any, error) {
	v := map[string]any{}
	if len(raw) > 0 && (json.Unmarshal(raw, &v) != nil || v == nil) {
		return nil, errors.New("账号 JSON 配置无效")
	}
	return v, nil
}

func accountTOML(raw []byte) (map[string]any, error) {
	v := map[string]any{}
	if len(raw) > 0 && toml.Unmarshal(raw, &v) != nil {
		return nil, errors.New("Codex TOML 配置无效")
	}
	return v, nil
}

func mergeAccountFiles(engine string, source, target map[string][]byte) (map[string][]byte, error) {
	if engine == "codex" {
		auth, err := accountJSON(source["auth.json"])
		if err != nil || len(auth) == 0 {
			return nil, errors.New("源账号没有文件形式的 Codex 登录信息；请使用 file 凭据存储后重新登录")
		}
		from, err := accountTOML(source["config.toml"])
		if err != nil {
			return nil, err
		}
		to, err := accountTOML(target["config.toml"])
		if err != nil {
			return nil, err
		}
		// Named profiles can override routing. Require users to pick a native
		// account home with its routing at the top level before projecting it.
		if from["profile"] != nil {
			return nil, errors.New("源 Codex 配置选择了命名 profile，请先将账号路由保存为独立配置目录")
		}
		delete(to, "profile")
		for _, key := range []string{"model", "model_provider", "openai_base_url"} {
			delete(to, key)
			if value, ok := from[key]; ok {
				to[key] = value
			}
		}
		provider, _ := from["model_provider"].(string)
		if provider != "" && provider != "openai" {
			providers, _ := from["model_providers"].(map[string]any)
			selected, ok := providers[provider].(map[string]any)
			if !ok {
				return nil, errors.New("源 Codex 自定义服务配置缺失")
			}
			// Env/keychain-only credentials cannot be portably transferred.
			if selected["env_key"] != nil {
				return nil, errors.New("源 Codex 服务使用环境变量密钥，请通过账号中心添加 API 配置后同步")
			}
			all, _ := to["model_providers"].(map[string]any)
			if all == nil {
				all = map[string]any{}
			}
			all[provider] = selected
			to["model_providers"] = all
		} else {
			// Do not leave a target override of the built-in OpenAI provider.
			all, _ := to["model_providers"].(map[string]any)
			if all == nil {
				all = map[string]any{}
			}
			delete(all, "openai")
			if sourceAll, ok := from["model_providers"].(map[string]any); ok && sourceAll["openai"] != nil {
				selected, _ := sourceAll["openai"].(map[string]any)
				if selected["env_key"] != nil {
					return nil, errors.New("源 Codex 服务使用环境变量密钥，请通过账号中心添加 API 配置后同步")
				}
				all["openai"] = sourceAll["openai"]
			}
			to["model_providers"] = all
		}
		to["cli_auth_credentials_store"] = "file"
		config, err := toml.Marshal(to)
		if err != nil {
			return nil, errors.New("无法合并 Codex 服务配置")
		}
		return map[string][]byte{"auth.json": source["auth.json"], "config.toml": config}, nil
	}
	if engine != "claude" {
		return nil, errors.New("此引擎暂不支持账号同步")
	}
	from, err := accountJSON(source["settings.json"])
	if err != nil {
		return nil, err
	}
	to, err := accountJSON(target["settings.json"])
	if err != nil {
		return nil, err
	}
	fromEnv, _ := from["env"].(map[string]any)
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "ANTHROPIC_PROFILE"} {
		if value, ok := fromEnv[key].(string); ok && value != "" && value != "0" {
			return nil, errors.New("源 Claude 配置使用云厂商或命名身份，请在目标环境配置；此同步支持订阅登录及 API/中转站")
		}
	}
	toEnv, _ := to["env"].(map[string]any)
	if toEnv == nil {
		toEnv = map[string]any{}
	}
	credential := false
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if value, ok := fromEnv[key].(string); ok && strings.TrimSpace(value) != "" {
			credential = true
		}
	}
	auth := source[".credentials.json"]
	if credential {
		// A relay/API profile must not retain the previous subscription login.
		auth = nil
	} else {
		v, err := accountJSON(auth)
		if err != nil || v["claudeAiOauth"] == nil {
			return nil, errors.New("源 Claude 配置没有可同步的订阅登录或 API 凭据；Console/WIF 登录需在目标环境单独配置")
		}
	}
	for _, key := range claudeAccountEnvKeys {
		// Empty values also override stale values inherited from the shell.
		toEnv[key] = ""
		if value, ok := fromEnv[key]; ok {
			if _, valid := value.(string); !valid {
				return nil, errors.New("Claude 账号环境变量必须为文本")
			}
			toEnv[key] = value
		}
	}
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "ANTHROPIC_PROFILE"} {
		toEnv[key] = ""
	}
	delete(to, "apiKeyHelper")
	delete(to, "model")
	if value, ok := from["model"]; ok {
		to["model"] = value
	}
	to["env"] = toEnv
	settings, err := json.MarshalIndent(to, "", "  ")
	if err != nil {
		return nil, errors.New("无法合并 Claude 服务配置")
	}
	return map[string][]byte{".credentials.json": auth, "settings.json": settings}, nil
}

func (s *Server) accountSyncRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/account-sync", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var req CodexSyncRequest
		if !body(w, r, &req) {
			return
		}
		if len(req.EnvironmentIDs) == 0 || len(req.EnvironmentIDs) > 30 {
			fail(w, 400, "请选择目标环境")
			return
		}
		var source EngineCredentialProfile
		for _, p := range s.app.store.engineProfiles() {
			if p.ID == req.SourceProfileID {
				source = p
				break
			}
		}
		if (source.Engine != "codex" || source.Kind != "codex_home") && (source.Engine != "claude" || source.Kind != "claude_home") {
			fail(w, 400, "请选择 Codex 或 Claude 的配置目录账号")
			return
		}
		config := s.app.config.get()
		sourceEnv, err := config.environment(source.EnvironmentID)
		if err != nil {
			fail(w, 400, "源环境不存在")
			return
		}
		var targets []Environment
		seen := map[string]bool{}
		for _, id := range req.EnvironmentIDs {
			if seen[id] {
				continue
			}
			seen[id] = true
			env, err := config.environment(id)
			if err != nil || (env.Type != "windows" && env.Type != "wsl" && env.Type != "ssh") {
				fail(w, 400, "目标环境不存在或不支持账号同步")
				return
			}
			targets = append(targets, env)
		}
		s.app.mu.Lock()
		if s.app.updating.Load() {
			s.app.mu.Unlock()
			fail(w, 409, errUpdateBusy.Error())
			return
		}
		s.app.engineSetup.mu.Lock()
		if s.app.engineSetup.active {
			s.app.engineSetup.mu.Unlock()
			s.app.mu.Unlock()
			fail(w, 409, "其他安装、登录或账号同步正在进行")
			return
		}
		s.app.engineSetup.active = true
		s.app.engineSetup.mu.Unlock()
		s.app.wg.Add(1)
		s.app.mu.Unlock()
		defer s.app.wg.Done()
		defer func() { s.app.engineSetup.mu.Lock(); s.app.engineSetup.active = false; s.app.engineSetup.mu.Unlock() }()
		s.app.codexSyncMu.Lock()
		defer s.app.codexSyncMu.Unlock()
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		stop := context.AfterFunc(s.app.ctx, cancel)
		defer stop()
		from, err := readNativeAccountFiles(ctx, sourceEnv, source.Reference, source.Engine)
		if err == nil {
			_, err = mergeAccountFiles(source.Engine, from, map[string][]byte{})
		}
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		results := []CodexSyncTargetResult{}
		for _, env := range targets {
			s.app.mu.Lock()
			targetCtx, targetCancel := context.WithTimeout(ctx, 20*time.Second)
			err := s.app.environmentAccountIdle(env.ID, source.Engine)
			var old map[string][]byte
			if err == nil {
				old, err = readNativeAccountFiles(targetCtx, env, "", source.Engine)
			}
			if err == nil {
				var next map[string][]byte
				next, err = mergeAccountFiles(source.Engine, from, old)
				if err == nil {
					err = writeNativeAccountFiles(targetCtx, env, "", source.Engine, old, next)
				}
			}
			targetCancel()
			if err == nil {
				err = s.app.store.useSyncedNativeAccount(env.ID, source.Engine)
			}
			s.app.mu.Unlock()
			result := CodexSyncTargetResult{EnvironmentID: env.ID, State: "done", Message: "账号及服务配置已同步，原文件已备份；跟随环境的 Duo 任务将在下一轮使用此账号。外部 CLI 请重新打开"}
			if err != nil {
				result.State, result.Message = "failed", err.Error()
			}
			results = append(results, result)
		}
		jsonOut(w, 200, CodexSyncResponse{SourceProfileID: source.ID, Results: results})
	}))
}
