package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Imports copy account material into a new managed home. They never change the
// source login, activate a profile, or execute configuration supplied in a file.
type AccountImportRequest struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Engine              string `json:"engine"`
	EnvironmentID       string `json:"environment_id"`
	Source              string `json:"source"`
	SourceEnvironmentID string `json:"source_environment_id"`
	JSON                string `json:"json"`
	ConfigTOML          string `json:"config_toml"`
}

func importString(v map[string]any, key string) string { s, _ := v[key].(string); return s }

func importedAccountFiles(engine string, source map[string][]byte) (map[string][]byte, error) {
	bad := errors.New("未识别到有效的原生账号信息；请使用 Codex auth.json、Claude .credentials.json 或含 API 凭据的 settings.json")
	if engine == "codex" {
		auth, err := accountJSON(source["auth.json"])
		if err != nil {
			return nil, bad
		}
		clean := map[string]any{}
		key := importString(auth, "OPENAI_API_KEY")
		tokens, _ := auth["tokens"].(map[string]any)
		if strings.TrimSpace(key) != "" {
			clean["OPENAI_API_KEY"] = key
			clean["auth_mode"] = "apikey"
		} else if strings.TrimSpace(importString(tokens, "access_token")) != "" {
			copied := map[string]any{}
			for _, k := range []string{"access_token", "refresh_token", "id_token", "account_id"} {
				if s := importString(tokens, k); s != "" {
					copied[k] = s
				}
			}
			clean["tokens"] = copied
			clean["auth_mode"] = "chatgpt"
			if value, ok := auth["last_refresh"].(string); ok {
				clean["last_refresh"] = value
			}
		} else {
			return nil, bad
		}
		from, err := accountTOML(source["config.toml"])
		if err != nil {
			return nil, err
		}
		if from["profile"] != nil {
			return nil, errors.New("此配置使用命名 profile，请先导出独立账号配置")
		}
		config := map[string]any{"cli_auth_credentials_store": "file"}
		for _, k := range []string{"model", "model_provider", "openai_base_url"} {
			if value, ok := from[k]; ok {
				if _, ok = value.(string); !ok {
					return nil, errors.New("Codex 账号路由格式无效")
				}
				config[k] = value
			}
		}
		provider := importString(config, "model_provider")
		if provider == "" {
			provider = "openai"
		}
		providers, _ := from["model_providers"].(map[string]any)
		selected, _ := providers[provider].(map[string]any)
		if selected == nil && provider != "openai" {
			return nil, errors.New("缺少所选 Codex 服务配置；请同时导入 config.toml")
		}
		if selected != nil {
			// External key helpers and environment-only auth are not portable logins.
			for _, k := range []string{"env_key", "env_http_headers", "experimental_bearer_token", "auth"} {
				if selected[k] != nil {
					return nil, errors.New("此服务使用外部凭据，请在账号管理中添加 API 配置")
				}
			}
			safe := map[string]any{}
			for _, k := range []string{"name", "base_url", "wire_api"} {
				if value, ok := selected[k]; ok {
					if _, ok = value.(string); !ok {
						return nil, bad
					}
					safe[k] = value
				}
			}
			if v, ok := selected["requires_openai_auth"]; ok {
				if _, ok = v.(bool); !ok {
					return nil, bad
				}
				safe["requires_openai_auth"] = v
			}
			if wire := importString(safe, "wire_api"); wire != "" && wire != "responses" {
				return nil, errors.New("Codex 导入仅支持 Responses 协议")
			}
			// auth.json is the single credential source in managed imports.
			safe["requires_openai_auth"] = true
			config["model_providers"] = map[string]any{provider: safe}
		}
		rawAuth, _ := json.MarshalIndent(clean, "", "  ")
		rawConfig, err := toml.Marshal(config)
		if err != nil {
			return nil, bad
		}
		return map[string][]byte{"auth.json": rawAuth, "config.toml": rawConfig}, nil
	}
	if engine != "claude" {
		return nil, bad
	}
	// Reuse route validation/reset semantics, then retain account fields only.
	merged, err := mergeAccountFiles("claude", source, map[string][]byte{})
	if err != nil {
		return nil, err
	}
	settings, _ := accountJSON(merged["settings.json"])
	safe := map[string]any{"env": settings["env"]}
	if v, ok := settings["model"]; ok {
		if _, ok = v.(string); !ok {
			return nil, bad
		}
		safe["model"] = v
	}
	rawSettings, _ := json.MarshalIndent(safe, "", "  ")
	files := map[string][]byte{"settings.json": rawSettings}
	if len(merged[".credentials.json"]) > 0 {
		auth, err := accountJSON(merged[".credentials.json"])
		if err != nil {
			return nil, bad
		}
		oauth, _ := auth["claudeAiOauth"].(map[string]any)
		if strings.TrimSpace(importString(oauth, "accessToken")) == "" {
			return nil, bad
		}
		clean := map[string]any{}
		for _, k := range []string{"accessToken", "refreshToken", "subscriptionType", "rateLimitTier"} {
			if s := importString(oauth, k); s != "" {
				clean[k] = s
			}
		}
		if v, ok := oauth["expiresAt"].(float64); ok {
			clean["expiresAt"] = v
		}
		if scopes, ok := oauth["scopes"].([]any); ok {
			for _, s := range scopes {
				if _, ok := s.(string); !ok {
					return nil, bad
				}
			}
			clean["scopes"] = scopes
		}
		files[".credentials.json"], _ = json.MarshalIndent(map[string]any{"claudeAiOauth": clean}, "", "  ")
	}
	return files, nil
}

func parseAccountImport(r AccountImportRequest) (map[string][]byte, error) {
	raw := []byte(strings.TrimPrefix(r.JSON, "\ufeff"))
	v, err := accountJSON(raw)
	if err != nil || len(v) == 0 {
		return nil, errors.New("请选择有效的账号 JSON 文件")
	}
	files := map[string][]byte{}
	if r.Engine == "codex" {
		files["auth.json"] = raw
		files["config.toml"] = []byte(strings.TrimPrefix(r.ConfigTOML, "\ufeff"))
	} else {
		if v["claudeAiOauth"] != nil {
			files[".credentials.json"] = raw
		} else if v["env"] != nil {
			files["settings.json"] = raw
		} else {
			return nil, errors.New("请选择 Claude .credentials.json 或包含 API 凭据的 settings.json")
		}
	}
	return importedAccountFiles(r.Engine, files)
}

func (a *App) importAccount(ctx context.Context, r AccountImportRequest) (EngineCredentialProfile, error) {
	empty := EngineCredentialProfile{}
	config := a.config.get()
	env, err := config.environment(r.EnvironmentID)
	if err != nil || env.Type != "windows" {
		return empty, errors.New("请选择保存账号的本机 Windows 环境")
	}
	if !safeWorkbenchID(r.ID) || len(r.ID) > 48 || strings.TrimSpace(r.Name) == "" || len([]rune(r.Name)) > 60 || (r.Engine != "codex" && r.Engine != "claude") {
		return empty, errors.New("账号名称、引擎或请求标识无效")
	}
	if len(r.JSON) > 256*1024 || len(r.ConfigTOML) > 256*1024 {
		return empty, errors.New("账号文件过大，每个文件最多 256 KB")
	}
	if r.Source != "json" && r.Source != "native" {
		return empty, errors.New("请选择 JSON 文件或环境已有账号")
	}
	encoded, _ := json.Marshal(r)
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	id := "imported-" + r.ID
	for _, p := range a.store.engineProfiles() {
		if p.ID == id {
			stored, e := os.ReadFile(filepath.Join(p.Reference, ".duo-import-request"))
			if e == nil && string(stored) == hash {
				return p, nil
			}
			return empty, errors.New("此导入标识已使用，请重新开始")
		}
	}
	var files map[string][]byte
	if r.Source == "json" {
		files, err = parseAccountImport(r)
	} else {
		var sourceEnv Environment
		sourceEnv, err = config.environment(r.SourceEnvironmentID)
		if err != nil {
			return empty, errors.New("来源环境不存在")
		}
		if r.JSON != "" || r.ConfigTOML != "" {
			return empty, errors.New("环境导入不使用上传文件")
		}
		files, err = readNativeAccountFiles(ctx, sourceEnv, "", r.Engine)
		if err == nil {
			files, err = importedAccountFiles(r.Engine, files)
		}
	}
	if err != nil {
		return empty, err
	}
	if ctx.Err() != nil {
		return empty, errors.New("读取账号已取消或超时")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	profiles := a.store.engineProfiles()
	for _, p := range profiles {
		if p.ID == id {
			return empty, errors.New("账号列表已更改，请刷新后重新导入")
		}
	}
	if len(profiles) >= 50 {
		return empty, errors.New("最多保存 50 个账号配置")
	}
	dir := filepath.Join(a.store.directory, "engine-accounts", id)
	if _, err = localAccountDirectory(dir, r.Engine); err != nil {
		return empty, err
	}
	if err = os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return empty, errors.New("无法创建账号目录")
	}
	if err = os.Mkdir(dir, 0700); err != nil {
		return empty, errors.New("账号目录已存在或无法创建，请重新开始导入")
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(dir)
		}
	}()
	files[".duo-import-request"] = []byte(hash)
	for name, data := range files {
		if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			return empty, errors.New("保存账号文件失败")
		}
	}
	profile := EngineCredentialProfile{ID: id, Name: strings.TrimSpace(r.Name), Engine: r.Engine, EnvironmentID: r.EnvironmentID, Kind: r.Engine + "_home", Reference: dir, Created: now(), Updated: now()}
	if err = validateEngineProfile(profile, a.config.get().Environments); err != nil {
		return empty, err
	}
	if err = a.store.saveEngineProfiles(append(profiles, profile)); err != nil {
		return empty, errors.New("保存账号引用失败")
	}
	committed = true
	return profile, nil
}

func (s *Server) accountImportRoutes(m *http.ServeMux) {
	s.managerAccountImportRoutes(m)
	m.HandleFunc("POST /api/account-import", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var req AccountImportRequest
		if !body(w, r, &req) {
			return
		}
		s.app.mu.Lock()
		if s.app.updating.Load() || s.app.ctx.Err() != nil {
			s.app.mu.Unlock()
			fail(w, 409, errUpdateBusy.Error())
			return
		}
		manager := s.app.engineSetup
		manager.mu.Lock()
		if manager.active {
			manager.mu.Unlock()
			s.app.mu.Unlock()
			fail(w, 409, "其他安装、登录或账号操作正在进行")
			return
		}
		manager.active = true
		manager.mu.Unlock()
		s.app.wg.Add(1)
		s.app.mu.Unlock()
		defer s.app.wg.Done()
		defer func() { manager.mu.Lock(); manager.active = false; manager.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		stop := context.AfterFunc(s.app.ctx, cancel)
		defer stop()
		profile, err := s.app.importAccount(ctx, req)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		jsonOut(w, 200, profile)
	}))
}
