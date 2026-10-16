package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type EngineAPIRequest struct {
	Models      []ModelOption     `json:"-"`
	Target      string            `json:"target"`
	Engine      string            `json:"engine"`
	APIProtocol string            `json:"api_protocol"`
	BaseURL     string            `json:"base_url"`
	Model       string            `json:"model"`
	APIKey      string            `json:"api_key,omitempty"`
	AuthMode    string            `json:"auth_mode"`
	APIScope    string            `json:"api_scope,omitempty"`
	ClaudeRoles *ClaudeRoleModels `json:"claude_roles,omitempty"`
}
type EngineAPIView struct {
	EngineAPIRequest
	EnvironmentID    string        `json:"environment_id"`
	KeyConfigured    bool          `json:"key_configured"`
	Models           []ModelOption `json:"models"`
	Message          string        `json:"message,omitempty"`
	DiscoveryStatus  string        `json:"discovery_status,omitempty"`
	ConfigPath       string        `json:"mimo_config,omitempty"`
	ClaudeConfigPath string        `json:"claude_api_config,omitempty"`
}

func mimoAPIDirectory(ctx context.Context, env Environment) (string, error) {
	if env.Type == "windows" {
		return localAccountDirectory("", "mimo-api")
	}
	result, err := remoteNativeAccountFiles(ctx, env, nativeAccountRequest{Operation: "directory", Engine: "mimo-api"})
	if err != nil {
		return "", err
	}
	dir := string(result["directory"])
	if !strings.HasPrefix(dir, "/") || strings.ContainsAny(dir, "\r\n\x00") {
		return "", errors.New("MiMo 原生目录无效")
	}
	return dir, nil
}

func (a *App) engineAPITarget(env Environment, engine string) string {
	base := harnessAPITarget(env)
	if engine != "mimo" || env.MimoConfig != "" {
		return base
	}
	id := a.store.activeEngineProfile(env.ID, engine)
	for _, p := range a.store.engineProfiles() {
		if p.ID == id && p.Engine == engine && p.EnvironmentID == env.ID && p.Kind == "mimo_config" {
			return hash(base + "|" + p.ID + "|" + p.Reference)
		}
	}
	return base
}

func (a *App) engineAPILocation(ctx context.Context, env Environment, engine string) (string, string, error) {
	if engine == "claude" {
		if env.ClaudeAPIConfig != "" {
			return claudeAPIConfigLocation(env.ClaudeAPIConfig)
		}
		return "", "claude", nil
	}
	if engine == "opencode" {
		dir, err := a.engineSetup.apiDirectory(ctx, env)
		return dir, "opencode-api", err
	}
	if env.MimoConfig != "" {
		normalized := strings.ReplaceAll(env.MimoConfig, `\`, "/")
		pos := strings.LastIndex(normalized, "/")
		if pos < 1 {
			return "", "", errors.New("MiMo 配置路径无效")
		}
		kind := "mimo-api"
		switch normalized[pos+1:] {
		case "duo-api.json":
		case "mimocode.json":
			kind = "mimo"
		default:
			return "", "", errors.New("MiMo 配置文件名无效")
		}
		return env.MimoConfig[:pos], kind, nil
	}
	id := a.store.activeEngineProfile(env.ID, "mimo")
	for _, p := range a.store.engineProfiles() {
		if p.ID == id && p.Engine == "mimo" && p.EnvironmentID == env.ID && p.Kind == "mimo_config" {
			return p.Reference, "mimo", nil
		}
	}
	dir, err := a.engineSetup.apiDirectory(ctx, env)
	return dir, "mimo-api", err
}

func engineAPIConfigView(env Environment, engine, kind string, files map[string][]byte) (EngineAPIView, string, error) {
	v := EngineAPIView{EngineAPIRequest: EngineAPIRequest{Target: harnessAPITarget(env), Engine: engine, APIProtocol: "openai-chat", BaseURL: "https://api.xiaomimimo.com/v1", AuthMode: "auto"}, EnvironmentID: env.ID, Models: []ModelOption{}}
	key := ""
	if engine == "claude" {
		v.APIProtocol = "anthropic-messages"
		v.BaseURL = ""
		v.APIScope = "environment"
		name := "settings.json"
		if kind == "claude-api" {
			name = "duo-api.json"
			v.APIScope = "duo"
			v.ClaudeConfigPath = env.ClaudeAPIConfig
		}
		doc, err := accountJSON(files[name])
		if err != nil {
			return v, "", err
		}
		vars, _ := doc["env"].(map[string]any)
		if doc["env"] != nil && vars == nil {
			return v, "", errors.New("Claude env 结构无效")
		}
		v.BaseURL, _ = vars["ANTHROPIC_BASE_URL"].(string)
		v.Model, _ = vars["ANTHROPIC_MODEL"].(string)
		if v.Model == "" {
			v.Model, _ = doc["model"].(string)
		}
		v.ClaudeRoles = claudeRolesFromEnv(vars, v.Model)
		key, _ = vars["ANTHROPIC_AUTH_TOKEN"].(string)
		v.AuthMode = "auth-token"
		if key == "" {
			key, _ = vars["ANTHROPIC_API_KEY"].(string)
			v.AuthMode = "api-key"
		}
	} else {
		name := "duo-api.json"
		if kind == "mimo" {
			name = "mimocode.json"
		}
		if kind == "opencode-api" || engine == "opencode" {
			name = "opencode.json"
		}
		doc, err := accountJSON(files[name])
		if err != nil {
			return v, "", err
		}
		providerID := "duo-mimo"
		if engine == "opencode" {
			providerID = "duo-opencode"
			v.BaseURL = ""
			v.APIProtocol = "openai-chat"
		}
		providers, _ := doc["provider"].(map[string]any)
		if doc["provider"] != nil && providers == nil {
			return v, "", errors.New("provider 结构无效")
		}
		p, _ := providers[providerID].(map[string]any)
		options, _ := p["options"].(map[string]any)
		if p != nil {
			v.BaseURL, _ = options["baseURL"].(string)
			key, _ = options["apiKey"].(string)
			if p["npm"] == "@ai-sdk/anthropic" {
				v.APIProtocol = "anthropic-messages"
			}
			model, _ := doc["model"].(string)
			if engine == "opencode" {
				if models, ok := p["models"].(map[string]any); ok && v.Model == "" {
					for id := range models {
						v.Model = id
						break
					}
				}
			} else {
				v.Model = strings.TrimPrefix(model, "duo-mimo/")
			}
		}
	}
	v.KeyConfigured = key != ""
	if engine == "mimo" {
		v.ConfigPath = env.MimoConfig
	}
	if key != "" {
		for _, value := range []string{v.BaseURL, v.Model, v.APIProtocol, v.AuthMode} {
			if strings.Contains(value, key) {
				return v, "", errors.New("配置字段包含凭据，未返回")
			}
		}
		if v.ClaudeRoles != nil {
			for _, value := range v.ClaudeRoles.values() {
				if strings.Contains(value, key) {
					return v, "", errors.New("配置字段包含凭据，未返回")
				}
			}
		}
	}
	return v, key, nil
}

func engineAPISetup(v EngineAPIRequest) EngineSetupRequest {
	return EngineSetupRequest{ID: "direct-api", Action: "account", Name: "API", Engine: v.Engine, Login: "apiKey", APIKey: v.APIKey, BaseURL: v.BaseURL, Model: v.Model, APIProtocol: v.APIProtocol, AuthMode: v.AuthMode, ClaudeRoles: v.ClaudeRoles}
}
func resolveEngineAPIKey(v EngineAPIRequest, old EngineAPIView, key string) (string, error) {
	if v.APIKey != "" {
		return v.APIKey, nil
	}
	if key == "" {
		return "", errors.New("首次配置需要填写 API key")
	}
	if strings.TrimRight(v.BaseURL, "/") != strings.TrimRight(old.BaseURL, "/") || v.APIProtocol != old.APIProtocol || v.Engine == "claude" && v.AuthMode != old.AuthMode {
		return "", errors.New("地址、协议或认证方式改变时需要重新填写 Key")
	}
	return key, nil
}
func engineAPIDiscover(ctx context.Context, manager *EngineSetup, env Environment, v EngineAPIRequest) HarnessDiscovery {
	base := v.BaseURL
	if v.Engine == "claude" {
		if base == "" {
			base = "https://api.anthropic.com"
		}
		if !strings.HasSuffix(strings.TrimRight(base, "/"), "/v1") {
			base = strings.TrimRight(base, "/") + "/v1"
		}
	}
	out := manager.harnessDiscover(ctx, env, HarnessAPIRequest{API: v.APIProtocol, BaseURL: base, APIKey: v.APIKey, ModelListBearer: v.Engine == "claude" && claudeUsesToken(engineAPISetup(v))})
	models := []ModelOption{}
	excluded := 0
	for _, model := range out.Models {
		if v.Engine == "mimo" && mimoSpeechModel(model.ID) {
			excluded++
			continue
		}
		model.Engine = v.Engine
		models = append(models, model)
	}
	out.Models = models
	if excluded > 0 {
		out.Message += fmt.Sprintf("；其中 %d 个文本/会话模型、%d 个语音识别/合成模型；代码会话仅列出前者。", len(models), excluded)
	}
	return out
}
func mergeEngineAPI(files map[string][]byte, kind string, v EngineAPIRequest) (map[string][]byte, error) {
	if v.Engine == "claude" {
		if kind == "claude-api" {
			return map[string][]byte{"duo-api.json": claudeAPISettings(engineAPISetup(v))}, nil
		}
		return mergeAccountFiles("claude", map[string][]byte{"settings.json": claudeAPISettings(engineAPISetup(v))}, files)
	}
	name := "duo-api.json"
	if kind == "mimo" {
		name = "mimocode.json"
	}
	providerID := "duo-mimo"
	if v.Engine == "opencode" || kind == "opencode-api" {
		name = "opencode.json"
		providerID = "duo-opencode"
	}
	doc, err := accountJSON(files[name])
	if err != nil {
		return nil, err
	}
	var fresh map[string]any
	_ = json.Unmarshal(mimoAPIConfig(engineAPISetup(v)), &fresh)
	providers, _ := doc["provider"].(map[string]any)
	if doc["provider"] != nil && providers == nil {
		return nil, errors.New("原生 provider 结构无效")
	}
	if providers == nil {
		providers = map[string]any{}
	}
	providers[providerID] = fresh["provider"].(map[string]any)["duo-mimo"]
	entry := providers[providerID].(map[string]any)
	models, _ := entry["models"].(map[string]any)
	if models == nil {
		models = map[string]any{}
	}
	for _, model := range v.Models {
		if model.ID == "" {
			continue
		}
		if providerID == "duo-mimo" && mimoSpeechModel(model.ID) {
			continue
		}
		name := model.Name
		if name == "" {
			name = model.ID
		}
		models[model.ID] = mimoModelDefinition(model.ID, name, v.APIProtocol)
	}
	entry["models"] = models
	providers[providerID] = entry
	// Scope a fresh API configuration to its declared providers. Preserve
	// explicitly enabled native providers while making this API selectable.
	enabled, _ := doc["enabled_providers"].([]any)
	if doc["enabled_providers"] == nil {
		ids := []string{}
		for id := range providers {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			enabled = append(enabled, id)
		}
	} else {
		if enabled == nil {
			return nil, errors.New("原生 enabled_providers 结构无效")
		}
		found := false
		for _, id := range enabled {
			found = found || id == providerID
		}
		if !found {
			enabled = append(enabled, providerID)
		}
	}
	doc["enabled_providers"] = enabled
	doc["provider"] = providers
	doc["model"] = fresh["model"]
	data, err := json.MarshalIndent(doc, "", "  ")
	return map[string][]byte{name: data}, err
}

func (s *Server) engineAPIRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/environments/{id}/engines/{engine}/api", s.secure(func(w http.ResponseWriter, r *http.Request) {
		engine := r.PathValue("engine")
		if engine != "claude" && engine != "mimo" && engine != "opencode" {
			fail(w, 400, "此入口仅支持 Claude Code、MiMo Code 和 OpenCode")
			return
		}
		env, err := s.app.config.get().environment(r.PathValue("id"))
		if err != nil {
			fail(w, 404, "执行环境不存在")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		target := s.app.engineAPITarget(env, engine)
		dir, kind, err := s.app.engineAPILocation(ctx, env, engine)
		if err != nil {
			fail(w, 400, "无法读取目标配置目录")
			return
		}
		files, err := s.app.engineSetup.harnessRead(ctx, env, dir, kind)
		if err != nil {
			fail(w, 400, "无法读取目标环境配置，请检查连接、Python 3 和目录权限")
			return
		}
		v, _, err := engineAPIConfigView(env, engine, kind, files)
		if err != nil {
			fail(w, 400, "原生配置格式无效，未修改文件")
			return
		}
		current, e := s.app.config.get().environment(env.ID)
		if e != nil || s.app.engineAPITarget(current, engine) != target {
			fail(w, 409, "环境或当前配置已变化，请重新打开窗口")
			return
		}
		v.Target = target
		jsonOut(w, 200, v)
	}))
	for _, method := range []string{"POST", "PUT"} {
		pattern := method + " /api/environments/{id}/engines/{engine}/api"
		if method == "POST" {
			pattern += "/models"
		}
		m.HandleFunc(pattern, s.secure(func(w http.ResponseWriter, r *http.Request) { s.configureEngineAPI(w, r) }))
	}
}

func (s *Server) configureEngineAPI(w http.ResponseWriter, r *http.Request) {
	var v EngineAPIRequest
	if !body(w, r, &v) {
		return
	}
	v.Engine = r.PathValue("engine")
	if v.Engine != "claude" && v.Engine != "mimo" && v.Engine != "opencode" {
		fail(w, 400, "此入口仅支持 Claude Code、MiMo Code 和 OpenCode")
		return
	}
	if v.APIScope != "" && (v.Engine != "claude" || v.APIScope != "duo" && v.APIScope != "environment") {
		fail(w, 400, "API 生效范围无效")
		return
	}
	if err := validateClaudeRoles(v.Engine, v.ClaudeRoles); err != nil {
		fail(w, 400, err.Error())
		return
	}
	a := s.app
	save := r.Method == "PUT"
	a.mu.Lock()
	env, err := a.config.get().environment(r.PathValue("id"))
	if err != nil || v.Target != a.engineAPITarget(env, v.Engine) {
		a.mu.Unlock()
		fail(w, 409, "环境配置已变化，请重新打开窗口")
		return
	}
	if a.updating.Load() || a.ctx.Err() != nil {
		a.mu.Unlock()
		fail(w, 409, errUpdateBusy.Error())
		return
	}
	manager := a.engineSetup
	manager.mu.Lock()
	if manager.active {
		manager.mu.Unlock()
		a.mu.Unlock()
		fail(w, 409, "已有安装或配置正在进行")
		return
	}
	if save {
		if err = a.engineTargetBusy(env, v.Engine); err != nil {
			manager.mu.Unlock()
			a.mu.Unlock()
			fail(w, 409, "该环境的引擎仍有执行或排队任务，请先结束后再保存配置")
			return
		}
		manager.active = true
		manager.updatingTarget = &env
		manager.updatingEngine = v.Engine
		a.wg.Add(1)
	}
	manager.mu.Unlock()
	a.mu.Unlock()
	if save {
		defer a.wg.Done()
		defer func() {
			manager.mu.Lock()
			manager.active = false
			manager.updatingTarget = nil
			manager.updatingEngine = ""
			manager.mu.Unlock()
		}()
	}
	baseContext := r.Context()
	if save {
		baseContext = a.ctx
	}
	ctx, cancel := context.WithTimeout(baseContext, 50*time.Second)
	defer cancel()
	dir, kind, err := a.engineAPILocation(ctx, env, v.Engine)
	if err != nil {
		fail(w, 400, "目标配置目录不可用")
		return
	}
	old, err := manager.harnessRead(ctx, env, dir, kind)
	if err != nil {
		fail(w, 400, "目标原生配置不可读取")
		return
	}
	view, key, err := engineAPIConfigView(env, v.Engine, kind, old)
	if err != nil {
		fail(w, 400, "原生配置格式无效，未修改文件")
		return
	}
	v.APIKey, err = resolveEngineAPIKey(v, view, key)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	fields := []string{v.BaseURL, v.Model}
	if v.ClaudeRoles != nil {
		fields = append(fields, v.ClaudeRoles.values()...)
	}
	for _, value := range fields {
		if strings.Contains(value, v.APIKey) {
			fail(w, 400, "配置字段包含凭据，未保存")
			return
		}
	}
	if err = validateMimoAPI(v); err != nil {
		fail(w, 400, err.Error())
		return
	}
	check := v
	if !save {
		check.Model = "model-list-only"
	}
	if err = validateEngineSetup(engineAPISetup(check), env); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if !s.admitModelCatalog(ctx) {
		fail(w, 409, "已有模型读取或测试正在进行")
		return
	}
	discovery := engineAPIDiscover(ctx, manager, env, v)
	s.modelProbeMu.Unlock()
	current, e := a.config.get().environment(env.ID)
	if e != nil || a.engineAPITarget(current, v.Engine) != v.Target {
		fail(w, 409, "环境配置已变化，未保存")
		return
	}
	if !save {
		jsonOut(w, 200, discovery)
		return
	}
	if discovery.Status == "failed" {
		fail(w, 400, discovery.Message+"；配置未保存")
		return
	}
	if discovery.Status == "ready" {
		v.Models = discovery.Models
	}
	writeDir, writeKind, writeOld := dir, kind, old
	if v.Engine == "claude" {
		if v.APIScope == "" {
			v.APIScope = view.APIScope
		}
		if v.APIScope == "duo" {
			if kind != "claude-api" {
				writeDir, err = manager.claudeAPIDirectory(ctx, env)
				writeKind = "claude-api"
			}
		} else {
			writeDir, writeKind = "", "claude"
		}
		if err != nil {
			fail(w, 400, "Claude Duo 配置目录不可用")
			return
		}
		if writeDir != dir || writeKind != kind {
			writeOld, err = manager.harnessRead(ctx, env, writeDir, writeKind)
			if err != nil {
				fail(w, 400, "目标配置不可读取，未写入")
				return
			}
		}
	}
	next, err := mergeEngineAPI(writeOld, writeKind, v)
	if err != nil {
		fail(w, 400, "配置无法合并，原文件未更改")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.config.get()
	current, e = c.environment(env.ID)
	if e != nil || a.engineAPITarget(current, v.Engine) != v.Target {
		fail(w, 409, "环境配置已变化，未保存")
		return
	}
	if err = manager.harnessWrite(ctx, env, writeDir, writeKind, writeOld, next); err != nil {
		fail(w, 400, "配置写入失败，请检查权限或并发修改；备份保留在原目录")
		return
	}
	for i := range c.Environments {
		if c.Environments[i].ID == env.ID {
			if v.Engine == "claude" {
				c.Environments[i].ClaudeModel = v.Model
				c.Environments[i].ClaudeAPIConfig = ""
				if v.APIScope == "duo" {
					c.Environments[i].ClaudeAPIConfig = nativeAPIConfigPath(env, writeDir, "duo-api.json")
				}
			} else {
				name := "duo-api.json"
				if kind == "mimo" {
					name = "mimocode.json"
				}
				if env.Type == "windows" {
					c.Environments[i].MimoConfig = filepath.Join(dir, name)
				} else {
					c.Environments[i].MimoConfig = strings.TrimRight(dir, "/") + "/" + name
				}
				c.Environments[i].MimoModel = "duo-mimo/" + v.Model
			}
		}
	}
	if err = a.config.save(c); err != nil {
		rollback, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		if manager.harnessWrite(rollback, env, writeDir, writeKind, next, writeOld) != nil {
			fail(w, 500, "环境保存失败且文件回退未确认，请检查目录备份")
			return
		}
		fail(w, 500, "环境保存失败，原生配置已恢复")
		return
	}
	current, _ = c.environment(env.ID)
	out, _, _ := engineAPIConfigView(current, v.Engine, writeKind, next)
	out.Target = a.engineAPITarget(current, v.Engine)
	out.Models = discovery.Models
	out.DiscoveryStatus = discovery.Status
	out.Message = "配置已保存并生效，原文件已备份。" + discovery.Message + " 不修改任务记录或原生会话。"
	if v.Engine == "claude" && v.APIScope != "duo" {
		if err = a.store.useSyncedNativeAccount(env.ID, "claude", ""); err != nil {
			out.Message += " 账号来源标记更新失败，请重新打开核对。"
		}
	}
	if v.Engine == "claude" && v.APIScope == "duo" {
		out.Message = "已保存 Duo 专用 API 配置，仅 Duo 启动的 Claude 进程加载；原生登录、权限和历史目录不变。" + discovery.Message
	}
	a.changed()
	jsonOut(w, 200, out)
}
