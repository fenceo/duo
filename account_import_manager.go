package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/pelletier/go-toml/v2"
)

const managerImportLimit = 1024 * 1024

type ManagerImportRequest struct {
	ID            string `json:"id"`
	Engine        string `json:"engine"`
	EnvironmentID string `json:"environment_id"`
	JSON          string `json:"json"`
	Indexes       []int  `json:"indexes"`
}

type ManagerImportItem struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Valid bool   `json:"valid"`
	Error string `json:"error,omitempty"`
	files map[string][]byte
}

func managerImportName(v map[string]any, index int) string {
	for _, k := range []string{"account_name", "name", "email"} {
		s := strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, importString(v, k)))
		if s != "" {
			r := []rune(s)
			if len(r) > 60 {
				r = r[:60]
			}
			return string(r)
		}
	}
	return fmt.Sprintf("导入账号 %d", index+1)
}

// Only known credential schemas are interpreted; exported paths, commands,
// notes and manager settings are never used. Errors must not echo input values.
func managerImportFiles(engine string, v map[string]any) (map[string][]byte, string, error) {
	bad := errors.New("未识别到所选引擎的账号格式")
	for _, k := range []string{"auth_mode", "type", "OPENAI_API_KEY", "openai_api_key", "api_key", "api_base_url", "api_provider_mode", "api_provider_id", "api_wire_api", "api_instance_access_mode", "api_startup_model", "api_key_field", "access_token", "refresh_token", "id_token", "account_id"} {
		if value := v[k]; value != nil {
			if _, ok := value.(string); !ok {
				return nil, "", bad
			}
		}
	}
	mode := importString(v, "auth_mode")
	marshal := func(value any) []byte { raw, _ := json.Marshal(value); return raw }
	validateAPI := func(key, base, model string) error {
		return validateEngineSetup(EngineSetupRequest{ID: "import", Action: "account", Engine: engine, Name: "import", Login: "apiKey", APIKey: key, BaseURL: base, Model: model}, Environment{Type: "windows"})
	}
	if engine == "codex" {
		if v["agent_identity"] != nil || mode == "agentIdentity" || v["personal_access_token"] != nil {
			return nil, "", errors.New("暂不支持 Agent Identity 或 Personal Access Token 账号")
		}
		if mode != "" && mode != "apikey" && mode != "chatgpt" {
			return nil, "", bad
		}
		if kind := importString(v, "type"); kind != "" && kind != "codex" {
			return nil, "", bad
		}
		key := importString(v, "OPENAI_API_KEY")
		if other := importString(v, "openai_api_key"); other != "" {
			if key != "" && key != other {
				return nil, "", errors.New("API 凭据字段冲突")
			}
			key = other
		}
		if key != "" || mode == "apikey" {
			base, model := importString(v, "api_base_url"), importString(v, "api_startup_model")
			if err := validateAPI(key, base, model); err != nil {
				return nil, "", err
			}
			if mode == "chatgpt" {
				return nil, "", bad
			}
			providerMode, providerID := importString(v, "api_provider_mode"), importString(v, "api_provider_id")
			if providerMode != "" && providerMode != "custom" && providerMode != "openai_builtin" {
				return nil, "", errors.New("不支持此 API 服务配置")
			}
			if base == "" && (providerMode == "custom" || (providerID != "" && providerID != "openai")) {
				return nil, "", errors.New("自定义 API 账号缺少服务地址")
			}
			if wire := importString(v, "api_wire_api"); wire != "" && wire != "responses" {
				return nil, "", errors.New("Codex 导入仅支持 Responses 协议")
			}
			if route := importString(v, "api_instance_access_mode"); route != "" && route != "direct" {
				return nil, "", errors.New("此账号依赖管理器网关或 CDP，请导出直接连接配置")
			}
			if mappings := v["api_model_mappings"]; mappings != nil {
				rows, ok := mappings.([]any)
				if !ok || len(rows) > 0 {
					return nil, "", errors.New("此账号依赖管理器模型映射，请先转为直接连接配置")
				}
			}
			config := map[string]any{}
			if model != "" {
				config["model"] = model
			}
			if base != "" {
				config["model_provider"] = "duo_import"
				config["model_providers"] = map[string]any{"duo_import": map[string]any{"name": "Imported API", "base_url": base, "wire_api": "responses", "requires_openai_auth": true}}
			}
			rawConfig, _ := toml.Marshal(config)
			files, err := importedAccountFiles(engine, map[string][]byte{"auth.json": marshal(map[string]any{"OPENAI_API_KEY": key}), "config.toml": rawConfig})
			return files, "API / 中转站", err
		}
		tokens, _ := v["tokens"].(map[string]any)
		if tokens == nil {
			tokens = map[string]any{}
			for _, k := range []string{"access_token", "refresh_token", "id_token", "account_id"} {
				tokens[k] = v[k]
			}
		}
		for _, k := range []string{"access_token", "refresh_token", "id_token", "account_id"} {
			if value := tokens[k]; value != nil {
				if _, ok := value.(string); !ok {
					return nil, "", bad
				}
			}
		}
		if importString(tokens, "access_token") == "" {
			return nil, "", bad
		}
		if importString(tokens, "refresh_token") == "" && importString(tokens, "id_token") == "" {
			return nil, "", errors.New("仅有 access_token 的账号暂不支持；请导出完整订阅登录信息")
		}
		if importString(tokens, "account_id") == "" {
			tokens["account_id"] = v["account_id"]
		}
		files, err := importedAccountFiles(engine, map[string][]byte{"auth.json": marshal(map[string]any{"tokens": tokens, "last_refresh": v["last_refresh"]})})
		return files, "订阅登录", err
	}
	if mode == "desktop_oauth" || mode == "desktop_o_auth" || mode == "desktop_gateway" {
		return nil, "", errors.New("Claude 桌面专用账号不能作为 CLI 账号导入")
	}
	if mode != "" && mode != "oauth" && mode != "o_auth" && mode != "setup_token" && mode != "api_key" {
		return nil, "", bad
	}
	raw, _ := v["claude_credentials_raw"].(map[string]any)
	if raw == nil {
		raw = v
	}
	if mode == "api_key" || importString(v, "api_key") != "" {
		if mode != "" && mode != "api_key" {
			return nil, "", errors.New("账号登录类型与 API 凭据冲突")
		}
		key, base := importString(v, "api_key"), importString(v, "api_base_url")
		if providerID := importString(v, "api_provider_id"); base == "" && providerID != "" && providerID != "anthropic" {
			return nil, "", errors.New("自定义 API 账号缺少服务地址")
		}
		if err := validateAPI(key, base, ""); err != nil {
			return nil, "", err
		}
		field := strings.ToUpper(importString(v, "api_key_field"))
		if field == "" {
			field = "ANTHROPIC_AUTH_TOKEN"
			u, _ := url.Parse(base)
			if base == "" || strings.EqualFold(u.Hostname(), "api.anthropic.com") || strings.EqualFold(u.Hostname(), "api.claude.com") {
				field = "ANTHROPIC_API_KEY"
			}
		}
		if field != "ANTHROPIC_API_KEY" && field != "ANTHROPIC_AUTH_TOKEN" {
			return nil, "", errors.New("Claude API 凭据字段不受支持")
		}
		env := map[string]any{field: key, "ANTHROPIC_BASE_URL": base}
		if extra := v["api_extra_env"]; extra != nil {
			values, ok := extra.(map[string]any)
			if !ok {
				return nil, "", bad
			}
			for _, k := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "ANTHROPIC_PROFILE"} {
				if value := values[k]; value != nil && value != "" && value != "0" {
					return nil, "", errors.New("云厂商或命名身份需要在目标环境单独配置")
				}
			}
			for _, k := range claudeAccountEnvKeys[4:] {
				if value := values[k]; value != nil {
					if _, ok := value.(string); !ok {
						return nil, "", bad
					}
					env[k] = value
				}
			}
			// Conflicting routes must not silently turn into a different account.
			for _, k := range claudeAccountEnvKeys[:4] {
				if value := values[k]; value != nil && value != "" && value != env[k] {
					return nil, "", errors.New("API 地址或凭据与附加环境变量冲突")
				}
			}
		}
		files, err := importedAccountFiles(engine, map[string][]byte{"settings.json": marshal(map[string]any{"env": env})})
		return files, "API / 中转站", err
	}
	if raw["claudeAiOauth"] == nil {
		return nil, "", bad
	}
	files, err := importedAccountFiles(engine, map[string][]byte{".credentials.json": marshal(raw)})
	return files, "订阅 / Setup Token", err
}

func parseManagerImport(engine, raw string) ([]ManagerImportItem, error) {
	if engine != "codex" && engine != "claude" {
		return nil, errors.New("请选择 Codex 或 Claude Code")
	}
	if len(raw) > managerImportLimit {
		return nil, errors.New("管理器导出文件最多 1 MB")
	}
	var decoded any
	if json.Unmarshal([]byte(strings.TrimPrefix(raw, "\ufeff")), &decoded) != nil {
		return nil, errors.New("请选择有效的管理器导出 JSON")
	}
	var records []any
	switch value := decoded.(type) {
	case []any:
		records = value
	case map[string]any:
		if wrapped, ok := value["accounts"]; ok {
			if value["type"] != nil {
				return nil, errors.New("请在管理器中选择 Cockpit Tools 格式导出")
			}
			records, _ = wrapped.([]any)
		} else {
			records = []any{value}
		}
	}
	if len(records) == 0 || len(records) > 50 {
		return nil, errors.New("每次请选择包含 1 至 50 个账号的 JSON")
	}
	items := make([]ManagerImportItem, 0, len(records))
	for i, record := range records {
		v, _ := record.(map[string]any)
		item := ManagerImportItem{Index: i, Name: managerImportName(v, i)}
		var err error
		item.files, item.Kind, err = managerImportFiles(engine, v)
		item.Valid = err == nil
		if err != nil {
			item.Error = err.Error()
		}
		items = append(items, item)
	}
	return items, nil
}

func (a *App) importManagerAccounts(ctx context.Context, r ManagerImportRequest) ([]EngineCredentialProfile, error) {
	if !safeWorkbenchID(r.ID) || len(r.ID) > 48 {
		return nil, errors.New("导入请求标识无效")
	}
	items, err := parseManagerImport(r.Engine, r.JSON)
	if err != nil {
		return nil, err
	}
	if len(r.Indexes) == 0 || len(r.Indexes) > len(items) {
		return nil, errors.New("请勾选要导入的账号")
	}
	r.Indexes = append([]int(nil), r.Indexes...)
	sort.Ints(r.Indexes)
	for i, index := range r.Indexes {
		if index < 0 || index >= len(items) || !items[index].Valid || (i > 0 && index == r.Indexes[i-1]) {
			return nil, errors.New("所选账号无效，请重新解析并选择")
		}
	}
	encoded, _ := json.Marshal(r)
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	a.mu.Lock()
	defer a.mu.Unlock()
	config := a.config.get()
	env, err := config.environment(r.EnvironmentID)
	if err != nil || env.Type != "windows" {
		return nil, errors.New("请选择保存账号的本机 Windows 环境")
	}
	profiles := a.store.engineProfiles()
	prefix := "batch-" + r.ID + "-"
	existing := map[string]EngineCredentialProfile{}
	for _, p := range profiles {
		if strings.HasPrefix(p.ID, prefix) {
			stored, e := os.ReadFile(filepath.Join(p.Reference, ".duo-import-request"))
			if e != nil || string(stored) != hash {
				return nil, errors.New("此导入标识已使用，请重新解析文件")
			}
			existing[p.ID] = p
		}
	}
	result := make([]EngineCredentialProfile, 0, len(r.Indexes))
	if len(existing) > 0 {
		for _, index := range r.Indexes {
			p, ok := existing[fmt.Sprintf("%s%d", prefix, index)]
			if !ok {
				return nil, errors.New("导入记录已更改，请重新解析文件")
			}
			result = append(result, p)
		}
		return result, nil
	}
	if len(profiles)+len(r.Indexes) > 50 {
		return nil, errors.New("账号总数不能超过 50 个，请减少勾选")
	}
	created := []string{}
	committed := false
	defer func() {
		if !committed {
			for _, dir := range created {
				_ = os.RemoveAll(dir)
			}
		}
	}()
	for _, index := range r.Indexes {
		if ctx.Err() != nil {
			return nil, errors.New("导入已取消或超时")
		}
		id := fmt.Sprintf("%s%d", prefix, index)
		dir := filepath.Join(a.store.directory, "engine-accounts", id)
		if _, err = localAccountDirectory(dir, r.Engine); err != nil {
			return nil, err
		}
		p := EngineCredentialProfile{ID: id, Name: items[index].Name, Engine: r.Engine, EnvironmentID: r.EnvironmentID, Kind: r.Engine + "_home", Reference: dir, Created: now(), Updated: now()}
		if err = validateEngineProfile(p, config.Environments); err != nil {
			return nil, err
		}
		if err = os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
			return nil, errors.New("无法创建账号目录")
		}
		if err = os.Mkdir(dir, 0700); err != nil {
			return nil, errors.New("账号目录已存在或无法创建，请重新解析文件")
		}
		// Only directories successfully created by this request are rolled back.
		created = append(created, dir)
		files := items[index].files
		files[".duo-import-request"] = []byte(hash)
		for name, data := range files {
			if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
				return nil, errors.New("保存账号文件失败")
			}
		}
		result = append(result, p)
	}
	if ctx.Err() != nil {
		return nil, errors.New("导入已取消或超时")
	}
	if err = a.store.saveEngineProfiles(append(profiles, result...)); err != nil {
		return nil, errors.New("保存账号引用失败")
	}
	committed = true
	return result, nil
}

func managerImportBody(w http.ResponseWriter, r *http.Request, req *ManagerImportRequest) bool {
	// JSON string escaping may expand the 1 MB file; retain the file limit too.
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*managerImportLimit))
	var extra any
	if d.Decode(req) != nil || d.Decode(&extra) != io.EOF {
		fail(w, 400, "导入请求无效或过大")
		return false
	}
	return true
}

func (s *Server) managerAccountImportRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/account-import/preview", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var req ManagerImportRequest
		if !managerImportBody(w, r, &req) {
			return
		}
		items, err := parseManagerImport(req.Engine, req.JSON)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		jsonOut(w, 200, items)
	}))
	m.HandleFunc("POST /api/account-import/batch", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var req ManagerImportRequest
		if !managerImportBody(w, r, &req) {
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
		profiles, err := s.app.importManagerAccounts(ctx, req)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		jsonOut(w, 200, profiles)
	}))
}
