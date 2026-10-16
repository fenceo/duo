package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// ApiCredential is a named provider key stored once in Duo and projected into
// each engine's native config when applied. Secrets never leave the local
// store; list APIs return only a fingerprint and key_configured flag.
type ApiCredential struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Protocol string `json:"api_protocol"`
	BaseURL  string `json:"base_url"`
	AuthMode string `json:"auth_mode"`
	Model    string `json:"model,omitempty"`
	Secret   string `json:"api_key,omitempty"`
	Created  int64  `json:"created"`
	Updated  int64  `json:"updated"`
}

type apiCredentialView struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Provider       string `json:"provider"`
	Protocol       string `json:"api_protocol"`
	BaseURL        string `json:"base_url"`
	AuthMode       string `json:"auth_mode"`
	Model          string `json:"model,omitempty"`
	KeyConfigured  bool   `json:"key_configured"`
	KeyFingerprint string `json:"key_fingerprint,omitempty"`
	Created        int64  `json:"created"`
	Updated        int64  `json:"updated"`
}

const apiCredentialsSetting = "api_credentials_v1"

func apiCredentialFingerprint(secret string) string {
	if secret == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:6])
}

func (c ApiCredential) view() apiCredentialView {
	return apiCredentialView{
		ID: c.ID, Name: c.Name, Provider: c.Provider, Protocol: c.Protocol,
		BaseURL: c.BaseURL, AuthMode: c.AuthMode, Model: c.Model,
		KeyConfigured: c.Secret != "", KeyFingerprint: apiCredentialFingerprint(c.Secret),
		Created: c.Created, Updated: c.Updated,
	}
}

func (s *Store) apiCredentials() []ApiCredential {
	var list []ApiCredential
	if raw := s.setting(apiCredentialsSetting); raw != "" {
		_ = json.Unmarshal([]byte(raw), &list)
	}
	if list == nil {
		list = []ApiCredential{}
	}
	return list
}

func (s *Store) saveApiCredentials(list []ApiCredential) error {
	if list == nil {
		list = []ApiCredential{}
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return s.set(apiCredentialsSetting, string(raw))
}

func (s *Store) apiCredential(id string) (ApiCredential, error) {
	for _, c := range s.apiCredentials() {
		if c.ID == id {
			return c, nil
		}
	}
	return ApiCredential{}, errors.New("API 凭据不存在")
}

func validateApiCredential(c ApiCredential) error {
	if !safeWorkbenchID(c.ID) || strings.TrimSpace(c.Name) == "" || len([]rune(c.Name)) > 60 {
		return errors.New("API 凭据名称无效")
	}
	c.BaseURL = strings.TrimSpace(c.BaseURL)
	if c.BaseURL != "" {
		if strings.ContainsAny(c.BaseURL, "\r\n\x00") || len(c.BaseURL) > 2048 {
			return errors.New("API 根地址无效")
		}
	}
	if c.Protocol != "openai-chat" && c.Protocol != "anthropic-messages" {
		return errors.New("接口协议无效")
	}
	if c.AuthMode != "api-key" && c.AuthMode != "auth-token" {
		return errors.New("认证方式无效")
	}
	if strings.TrimSpace(c.Secret) == "" || len(c.Secret) > 16000 || strings.ContainsAny(c.Secret, "\r\n\x00") {
		return errors.New("API key 无效")
	}
	return nil
}

func (s *Server) apiCredentialRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/api-credentials", s.secure(func(w http.ResponseWriter, r *http.Request) {
		views := []apiCredentialView{}
		for _, c := range s.app.store.apiCredentials() {
			views = append(views, c.view())
		}
		jsonOut(w, 200, map[string]any{"credentials": views, "presets": engineAPIPresetList()})
	}))
	m.HandleFunc("POST /api/api-credentials/{id}/apply", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Engine       string   `json:"engine"`
			Environments []string `json:"environments"`
			Protocol     string   `json:"api_protocol"`
			Model        string   `json:"model"`
		}
		if !body(w, r, &req) {
			return
		}
		cred, err := s.app.store.apiCredential(r.PathValue("id"))
		if err != nil {
			fail(w, 404, err.Error())
			return
		}
		if req.Engine != "claude" && req.Engine != "mimo" && req.Engine != "opencode" {
			fail(w, 400, "此凭据可下发到 Claude Code、MiMo Code 或 OpenCode")
			return
		}
		if len(req.Environments) == 0 {
			fail(w, 400, "请选择至少一个目标环境")
			return
		}
		protocol := req.Protocol
		if protocol == "" {
			protocol = cred.Protocol
		}
		model := req.Model
		if model == "" {
			model = cred.Model
		}
		v := EngineAPIRequest{
			Engine: req.Engine, APIProtocol: protocol, BaseURL: cred.BaseURL,
			Model: model, APIKey: cred.Secret, AuthMode: cred.AuthMode,
			Environments: req.Environments,
		}
		if req.Engine == "claude" && v.AuthMode == "" {
			v.AuthMode = "api-key"
		}
		s.applyApiCredential(w, r, v, cred)
	}))
	m.HandleFunc("PUT /api/api-credentials", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var c ApiCredential
		if !body(w, r, &c) {
			return
		}
		c.Name = strings.TrimSpace(c.Name)
		c.Provider = strings.TrimSpace(c.Provider)
		c.Model = strings.TrimSpace(c.Model)
		if c.Created == 0 {
			c.Created = now()
		}
		c.Updated = now()
		if c.ID == "" {
			c.ID = "cred-" + uid()
		}
		if err := validateApiCredential(c); err != nil {
			fail(w, 400, err.Error())
			return
		}
		s.app.mu.Lock()
		defer s.app.mu.Unlock()
		if s.app.updating.Load() {
			fail(w, 409, errUpdateBusy.Error())
			return
		}
		list := s.app.store.apiCredentials()
		found := false
		for i := range list {
			if list[i].ID == c.ID {
				if c.Secret == "" {
					c.Secret = list[i].Secret
				}
				list[i] = c
				found = true
				break
			}
		}
		if !found {
			list = append(list, c)
		}
		if err := s.app.store.saveApiCredentials(list); err != nil {
			fail(w, 500, "保存 API 凭据失败")
			return
		}
		jsonOut(w, 200, c.view())
	}))
	m.HandleFunc("DELETE /api/api-credentials/{id}", s.secure(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		s.app.mu.Lock()
		defer s.app.mu.Unlock()
		list := s.app.store.apiCredentials()
		kept := []ApiCredential{}
		found := false
		for _, c := range list {
			if c.ID == id {
				found = true
				continue
			}
			kept = append(kept, c)
		}
		if !found {
			fail(w, 404, "API 凭据不存在")
			return
		}
		if err := s.app.store.saveApiCredentials(kept); err != nil {
			fail(w, 500, "删除失败")
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}))
}

// applyApiCredential writes a stored key into each selected environment using
// that engine's native config format. Secrets stay server-side.
func (s *Server) applyApiCredential(w http.ResponseWriter, r *http.Request, v EngineAPIRequest, cred ApiCredential) {
	a := s.app
	a.mu.Lock()
	if a.updating.Load() {
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
	manager.active = true
	manager.updatingEngine = v.Engine
	a.wg.Add(1)
	manager.mu.Unlock()
	a.mu.Unlock()
	defer a.wg.Done()
	defer func() {
		manager.mu.Lock()
		manager.active = false
		manager.updatingTarget = nil
		manager.updatingEngine = ""
		manager.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	messages := []string{}
	saved := 0
	for _, id := range v.Environments {
		env, err := a.config.get().environment(id)
		if err != nil {
			messages = append(messages, id+"：环境不存在")
			continue
		}
		if err = a.engineTargetBusy(env, v.Engine); err != nil {
			messages = append(messages, env.Name+"：仍有执行或排队任务")
			continue
		}
		dir, kind, err := a.engineAPILocation(ctx, env, v.Engine)
		if err != nil {
			messages = append(messages, env.Name+"：配置目录不可用")
			continue
		}
		old, err := manager.harnessRead(ctx, env, dir, kind)
		if err != nil {
			messages = append(messages, env.Name+"：原配置不可读")
			continue
		}
		files, err := mergeEngineAPI(old, kind, v)
		if err != nil {
			messages = append(messages, env.Name+"：配置无法合并")
			continue
		}
		if err = manager.harnessWrite(ctx, env, dir, kind, old, files); err != nil {
			messages = append(messages, env.Name+"：写入失败")
			continue
		}
		saved++
		messages = append(messages, env.Name+"：已写入")
	}
	status := 200
	if saved == 0 {
		status = 400
	}
	jsonOut(w, status, map[string]any{
		"ok":         saved > 0,
		"applied":    saved,
		"message":    "凭据「" + cred.Name + "」下发到 " + v.Engine + "：" + strings.Join(messages, "；"),
		"credential": cred.view(),
	})
}
