package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
)

// EngineDefinition describes an agent engine independently from the model and
// execution environment.  The registry is deliberately declarative: a future
// installer or adapter can add a native protocol without changing task data.
// Secrets are never part of this structure.
type EngineDefinition struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	Transport          string   `json:"transport"`
	Runnable           bool     `json:"runnable"`
	Targets            []string `json:"targets"`
	Capabilities       []string `json:"capabilities"`
	CredentialKinds    []string `json:"credential_kinds"`
	InstallDescription string   `json:"install_description,omitempty"`
	DocumentationURL   string   `json:"documentation_url,omitempty"`
}

// EngineCredentialProfile is a pointer to an environment-owned login/profile,
// not a secret. Reference is a path or a native profile name and is never
// returned with file contents. This lets Windows, WSL, and SSH keep credentials
// in their own native stores while Duo only switches the active reference.
type EngineCredentialProfile struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Engine        string `json:"engine"`
	EnvironmentID string `json:"environment_id"`
	Kind          string `json:"kind"`
	Reference     string `json:"reference"`
	Created       int64  `json:"created"`
	Updated       int64  `json:"updated"`
}

type EngineInstallStep struct {
	Description string   `json:"description"`
	Program     string   `json:"program,omitempty"`
	Arguments   []string `json:"arguments,omitempty"`
	Manual      bool     `json:"manual"`
}

type EngineInstallPlan struct {
	Engine        EngineDefinition    `json:"engine"`
	EnvironmentID string              `json:"environment_id"`
	Environment   string              `json:"environment"`
	Steps         []EngineInstallStep `json:"steps"`
	Message       string              `json:"message"`
	Executable    string              `json:"executable,omitempty"`
}

type EngineCatalog struct {
	Engines       []EngineDefinition        `json:"engines"`
	Profiles      []EngineCredentialProfile `json:"profiles"`
	ActiveProfile map[string]string         `json:"active_profile"`
}

const engineProfilesSetting = "engine_profiles_v1"

func builtinEngineDefinitions() []EngineDefinition {
	return []EngineDefinition{
		{
			ID: "codex", Name: "Codex", Description: "OpenAI Codex 原生 app-server",
			Transport: "codex_app_server", Runnable: true,
			Targets:            []string{"windows", "wsl", "ssh"},
			Capabilities:       []string{"stream", "resume", "approval", "interrupt", "image_input"},
			CredentialKinds:    []string{"native", "codex_home"},
			InstallDescription: "请在目标环境安装并登录 Codex CLI；Duo 不复制令牌。",
			DocumentationURL:   "https://developers.openai.com/docs/app-server",
		},
		{
			ID: "claude", Name: "Claude Code", Description: "Anthropic Claude Code CLI",
			Transport: "cli_stream_json", Runnable: true,
			Targets:            []string{"windows", "wsl", "ssh"},
			Capabilities:       []string{"stream", "resume", "mcp"},
			CredentialKinds:    []string{"native", "claude_home"},
			InstallDescription: "请在目标环境安装并登录 Claude Code；Duo 不复制令牌。",
			DocumentationURL:   "https://code.claude.com/docs/en/cli-usage",
		},
		{
			ID: "deepseek-harness", Name: "DeepSeek Harness", Description: "DeepSeek Harness 原生 SDK；同一运行时支持连续对话，停止或服务重启后不能恢复；暂不支持交互审批、图片和硬件工具",
			Transport: "sdk_jsonrpc", Runnable: true,
			Targets:            []string{"windows", "wsl", "ssh"},
			Capabilities:       []string{"stream", "live_session", "cancel", "sandbox"},
			CredentialKinds:    []string{"native", "dsh_home"},
			InstallDescription: "使用 dsh --profile sdk 的 JSON-RPC 接口；请在目标环境安装 Harness 并完成 provider 配置。Windows 默认使用 npm 的 dsh.cmd 入口。",
			DocumentationURL:   "https://github.com/deepseek-ai/deepseek-harness",
		},
		{
			ID: "kimi", Name: "Kimi Code", Description: "Moonshot Kimi Code CLI / ACP",
			Transport: "acp", Runnable: false,
			Targets:            []string{"windows", "wsl", "ssh"},
			Capabilities:       []string{"stream", "resume", "approval", "acp"},
			CredentialKinds:    []string{"native", "env_file"},
			InstallDescription: "先安装 Kimi Code CLI 并完成登录；ACP 适配器随后接入。",
			DocumentationURL:   "https://www.kimi.com/code/docs/en/kimi-code-cli/reference/kimi-command",
		},
		{
			ID: "mimo", Name: "MiMo Code", Description: "Xiaomi MiMo Code CLI / ACP",
			Transport: "acp", Runnable: false,
			Targets:            []string{"windows", "wsl", "ssh"},
			Capabilities:       []string{"stream", "resume", "approval", "acp"},
			CredentialKinds:    []string{"native", "env_file"},
			InstallDescription: "当前先以 CLI/ACP 作为接入面，桌面端不作为自动化协议依赖。",
			DocumentationURL:   "https://github.com/XiaomiMiMo/MiMo-Code",
		},
	}
}

func builtinEngine(id string) (EngineDefinition, bool) {
	for _, e := range builtinEngineDefinitions() {
		if e.ID == id {
			return e, true
		}
	}
	return EngineDefinition{}, false
}

func engineTargetSupported(e EngineDefinition, kind string) bool {
	for _, target := range e.Targets {
		if target == kind {
			return true
		}
	}
	return false
}

func validEngineCredentialKind(engine, kind string) bool {
	e, ok := builtinEngine(engine)
	if !ok {
		return false
	}
	for _, supported := range e.CredentialKinds {
		if supported == kind {
			return true
		}
	}
	return false
}

func (s *Store) engineProfiles() []EngineCredentialProfile {
	var profiles []EngineCredentialProfile
	if raw := s.setting(engineProfilesSetting); raw != "" {
		_ = json.Unmarshal([]byte(raw), &profiles)
	}
	if profiles == nil {
		profiles = []EngineCredentialProfile{}
	}
	return profiles
}

func (s *Store) saveEngineProfiles(profiles []EngineCredentialProfile) error {
	if profiles == nil {
		profiles = []EngineCredentialProfile{}
	}
	raw, err := json.Marshal(profiles)
	if err != nil {
		return err
	}
	return s.set(engineProfilesSetting, string(raw))
}

func engineProfileKey(environmentID, engine string) string {
	return "engine_active:" + environmentID + ":" + engine
}

func (s *Store) activeEngineProfile(environmentID, engine string) string {
	return s.setting(engineProfileKey(environmentID, engine))
}

func (s *Store) activateEngineProfile(environmentID, engine, profileID string) error {
	if profileID == "" {
		return s.set(engineProfileKey(environmentID, engine), "")
	}
	for _, profile := range s.engineProfiles() {
		if profile.ID == profileID && profile.EnvironmentID == environmentID && profile.Engine == engine {
			return s.set(engineProfileKey(environmentID, engine), profileID)
		}
	}
	return errors.New("账号/API 配置不存在或不属于此环境")
}

var errEngineProfileNotFound = errors.New("账号/API 配置不存在")

func (s *Store) removeEngineProfile(id string) error {
	profiles := s.engineProfiles()
	kept := make([]EngineCredentialProfile, 0, len(profiles))
	var removed *EngineCredentialProfile
	for _, profile := range profiles {
		if profile.ID == id {
			copy := profile
			removed = &copy
		} else {
			kept = append(kept, profile)
		}
	}
	if removed == nil {
		return errEngineProfileNotFound
	}
	raw, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", engineProfilesSetting, string(raw)); err != nil {
		return err
	}
	// Deleting an inactive profile must not switch a different active account.
	if _, err = tx.Exec("DELETE FROM settings WHERE key=? AND value=?", engineProfileKey(removed.EnvironmentID, removed.Engine), removed.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func validateEngineProfile(profile EngineCredentialProfile, environments []Environment) error {
	if !safeWorkbenchID(profile.ID) || strings.TrimSpace(profile.Name) == "" || len([]rune(profile.Name)) > 60 {
		return errors.New("账号/API 配置名称无效")
	}
	if !safeWorkbenchID(profile.EnvironmentID) {
		return errors.New("账号/API 配置的环境无效")
	}
	engine, ok := builtinEngine(profile.Engine)
	if !ok || !validEngineCredentialKind(profile.Engine, profile.Kind) {
		return errors.New("账号/API 配置的引擎或类型无效")
	}
	found := false
	var target Environment
	for _, env := range environments {
		if env.ID == profile.EnvironmentID {
			found = true
			target = env
			break
		}
	}
	if !found || !engineTargetSupported(engine, target.Type) {
		return errors.New("账号/API 配置的执行环境不支持此引擎")
	}
	profile.Reference = strings.TrimSpace(profile.Reference)
	if profile.Reference == "" || strings.ContainsAny(profile.Reference, "\x00\r\n") || len(profile.Reference) > 4096 {
		return errors.New("账号/API 配置需要填写外部配置引用")
	}
	if profile.Kind == "codex_home" || profile.Kind == "claude_home" || profile.Kind == "dsh_home" {
		if target.Type == "windows" {
			if !filepath.IsAbs(profile.Reference) {
				return errors.New("Windows 账号目录需要绝对路径")
			}
		} else if !strings.HasPrefix(profile.Reference, "/") {
			return errors.New("WSL/SSH 账号目录需要 Linux 绝对路径")
		}
	}
	return nil
}

func engineProfileEnv(profile EngineCredentialProfile) map[string]string {
	if profile.Reference == "" {
		return nil
	}
	switch profile.Kind {
	case "codex_home":
		return map[string]string{"CODEX_HOME": profile.Reference}
	case "claude_home":
		return map[string]string{"CLAUDE_CONFIG_DIR": profile.Reference}
	case "dsh_home":
		return map[string]string{"DSH_HOME": profile.Reference}
	default:
		// env_file is intentionally metadata-only for now. Reading arbitrary
		// secret files belongs in the target adapter, not the HTTP process.
		return nil
	}
}

func (a *App) activeEngineEnvironment(task Task) map[string]string {
	if task.Environment == nil {
		return nil
	}
	id := a.store.activeEngineProfile(task.Environment.ID, task.Engine)
	if id == "" {
		return nil
	}
	for _, profile := range a.store.engineProfiles() {
		if profile.ID == id && profile.Engine == task.Engine && profile.EnvironmentID == task.Environment.ID {
			return engineProfileEnv(profile)
		}
	}
	return nil
}

func engineInstallPlan(env Environment, engineID string) (EngineInstallPlan, error) {
	e, ok := builtinEngine(engineID)
	if !ok {
		return EngineInstallPlan{}, errors.New("AI 引擎不存在")
	}
	if !engineTargetSupported(e, env.Type) {
		return EngineInstallPlan{}, errors.New("该引擎不支持所选执行环境")
	}
	plan := EngineInstallPlan{Engine: e, EnvironmentID: env.ID, Environment: env.Name, Message: e.InstallDescription, Steps: []EngineInstallStep{}}
	if e.Runnable {
		plan.Steps = append(plan.Steps, EngineInstallStep{Description: "检查目标环境中的可执行文件与登录状态", Manual: true})
		if engineID == "codex" {
			plan.Executable = env.Codex
		} else if engineID == "claude" {
			plan.Executable = env.Claude
		} else if engineID == "deepseek-harness" {
			plan.Executable = env.Harness
		}
		return plan, nil
	}
	plan.Steps = append(plan.Steps, EngineInstallStep{Description: "在目标环境安装官方 CLI/运行时，并按官方文档登录", Manual: true})
	return plan, nil
}

func (s *Server) engineRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/engines", s.secure(func(w http.ResponseWriter, r *http.Request) {
		catalog := EngineCatalog{Engines: builtinEngineDefinitions(), Profiles: s.app.store.engineProfiles(), ActiveProfile: map[string]string{}}
		for _, env := range s.app.config.get().Environments {
			for _, engine := range catalog.Engines {
				if profile := s.app.store.activeEngineProfile(env.ID, engine.ID); profile != "" {
					catalog.ActiveProfile[env.ID+":"+engine.ID] = profile
				}
			}
		}
		jsonOut(w, http.StatusOK, catalog)
	}))
	m.HandleFunc("GET /api/environments/{id}/engines/{engine}/install-plan", s.secure(func(w http.ResponseWriter, r *http.Request) {
		env, err := s.app.config.get().environment(r.PathValue("id"))
		if err != nil {
			fail(w, http.StatusNotFound, err.Error())
			return
		}
		plan, err := engineInstallPlan(env, r.PathValue("engine"))
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonOut(w, http.StatusOK, plan)
	}))
	m.HandleFunc("PUT /api/engine-profiles", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var profile EngineCredentialProfile
		if !body(w, r, &profile) {
			return
		}
		profile.Name = strings.TrimSpace(profile.Name)
		profile.Engine = strings.TrimSpace(profile.Engine)
		profile.Kind = strings.TrimSpace(profile.Kind)
		profile.Reference = strings.TrimSpace(profile.Reference)
		if profile.Created == 0 {
			profile.Created = now()
		}
		profile.Updated = now()
		c := s.app.config.get()
		if err := validateEngineProfile(profile, c.Environments); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		s.app.mu.Lock()
		defer s.app.mu.Unlock()
		if s.app.updating.Load() {
			fail(w, http.StatusConflict, errUpdateBusy.Error())
			return
		}
		profiles := s.app.store.engineProfiles()
		for i := range profiles {
			if profiles[i].ID == profile.ID {
				profiles[i] = profile
				if err := s.app.store.saveEngineProfiles(profiles); err != nil {
					fail(w, http.StatusInternalServerError, err.Error())
					return
				}
				jsonOut(w, http.StatusOK, profile)
				return
			}
		}
		if len(profiles) >= 50 {
			fail(w, http.StatusBadRequest, "账号/API 配置最多 50 个")
			return
		}
		profiles = append(profiles, profile)
		if err := s.app.store.saveEngineProfiles(profiles); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonOut(w, http.StatusCreated, profile)
	}))
	m.HandleFunc("POST /api/engine-profiles/{id}/activate", s.secure(func(w http.ResponseWriter, r *http.Request) {
		s.app.mu.Lock()
		defer s.app.mu.Unlock()
		if s.app.updating.Load() {
			fail(w, http.StatusConflict, errUpdateBusy.Error())
			return
		}
		var profile *EngineCredentialProfile
		for _, candidate := range s.app.store.engineProfiles() {
			if candidate.ID == r.PathValue("id") {
				copy := candidate
				profile = &copy
				break
			}
		}
		if profile == nil {
			fail(w, http.StatusNotFound, "账号/API 配置不存在")
			return
		}
		if err := s.app.store.activateEngineProfile(profile.EnvironmentID, profile.Engine, profile.ID); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonOut(w, http.StatusOK, map[string]any{"ok": true, "profile": profile})
	}))
	m.HandleFunc("DELETE /api/engine-profiles/{id}", s.secure(func(w http.ResponseWriter, r *http.Request) {
		s.app.mu.Lock()
		defer s.app.mu.Unlock()
		if s.app.updating.Load() {
			fail(w, http.StatusConflict, errUpdateBusy.Error())
			return
		}
		err := s.app.store.removeEngineProfile(r.PathValue("id"))
		if errors.Is(err, errEngineProfileNotFound) {
			fail(w, http.StatusNotFound, "账号/API 配置不存在")
			return
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonOut(w, http.StatusOK, map[string]bool{"ok": true})
	}))
}

func engineRegistryIDs() []string {
	ids := make([]string, 0, len(builtinEngineDefinitions()))
	for _, e := range builtinEngineDefinitions() {
		ids = append(ids, e.ID)
	}
	sort.Strings(ids)
	return ids
}

func (e EngineInstallPlan) String() string {
	return fmt.Sprintf("%s/%s: %s", e.EnvironmentID, e.Engine.ID, e.Message)
}
