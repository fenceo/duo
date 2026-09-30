package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Public account information is stored separately from routing and native
// credentials. JWT claims are display hints only, never proof of authentication.
type CodexAccountInfo struct {
	Email             string              `json:"email,omitempty"`
	DisplayName       string              `json:"display_name,omitempty"`
	AccountID         string              `json:"account_id,omitempty"`
	Plan              string              `json:"plan,omitempty"`
	AuthType          string              `json:"auth_type,omitempty"`
	Provider          string              `json:"provider,omitempty"`
	BaseURL           string              `json:"base_url,omitempty"`
	SubscriptionUntil string              `json:"subscription_until,omitempty"`
	State             string              `json:"state"`
	Message           string              `json:"message,omitempty"`
	IdentitySource    string              `json:"identity_source,omitempty"`
	IdentityUpdated   int64               `json:"identity_updated,omitempty"`
	QuotaUpdated      int64               `json:"quota_updated,omitempty"`
	Attempted         int64               `json:"attempted,omitempty"`
	Limits            []CodexAccountLimit `json:"limits,omitempty"`
}
type CodexAccountLimit struct {
	Name          string  `json:"name"`
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int64   `json:"window_minutes"`
	ResetsAt      int64   `json:"resets_at"`
}
type codexAccountCache struct {
	Signature string           `json:"signature"`
	Info      CodexAccountInfo `json:"info"`
}
type AccountOrganization struct {
	Note string   `json:"note"`
	Tags []string `json:"tags"`
}

func accountText(s string, limit int) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
	runes := []rune(s)
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}
func accountURL(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return accountText(u.String(), 500)
}
func accountClaims(token string) map[string]any {
	if len(token) > 256*1024 {
		return nil
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(raw, &claims) != nil {
		return nil
	}
	return claims
}
func codexInfoFromFiles(files map[string][]byte) CodexAccountInfo {
	out := CodexAccountInfo{State: "unknown", Message: "未读取到账号身份；可刷新账号或重新登录。"}
	auth, err := accountJSON(files["auth.json"])
	if err != nil {
		return out
	}
	config, _ := accountTOML(files["config.toml"])
	provider := importString(config, "model_provider")
	if provider == "" {
		provider = "openai"
	}
	out.Provider = accountText(provider, 100)
	out.BaseURL = accountURL(importString(config, "openai_base_url"))
	providers, _ := config["model_providers"].(map[string]any)
	selected, _ := providers[provider].(map[string]any)
	if name := importString(selected, "name"); name != "" {
		out.Provider = accountText(name, 100)
	}
	if base := accountURL(importString(selected, "base_url")); base != "" {
		out.BaseURL = base
	}
	if out.BaseURL == "" && provider == "openai" {
		out.BaseURL = "https://api.openai.com/v1"
	}
	if importString(auth, "OPENAI_API_KEY") != "" || importString(auth, "auth_mode") == "apikey" {
		out.AuthType = "apikey"
		out.State = "api"
		out.Message = "API / 中转站账号；Codex 不提供此类账号的余额和订阅额度。"
		out.IdentitySource = "local"
		out.IdentityUpdated = now()
		return out
	}
	tokens, _ := auth["tokens"].(map[string]any)
	if importString(tokens, "access_token") == "" {
		return out
	}
	out.AuthType = "chatgpt"
	out.State = "local"
	out.IdentitySource = "local"
	out.IdentityUpdated = now()
	out.BaseURL = ""
	out.Provider = "ChatGPT"
	// ID token identity wins over access-token fallback. The selected account_id
	// wins over claims so team/personal workspaces are not silently conflated.
	for _, key := range []string{"access_token", "id_token"} {
		claims := accountClaims(importString(tokens, key))
		profile, _ := claims["https://api.openai.com/profile"].(map[string]any)
		details, _ := claims["https://api.openai.com/auth"].(map[string]any)
		for _, v := range []string{importString(profile, "email"), importString(claims, "email")} {
			if v != "" {
				out.Email = accountText(v, 254)
			}
		}
		for _, v := range []string{importString(profile, "name"), importString(claims, "name")} {
			if v != "" {
				out.DisplayName = accountText(v, 120)
			}
		}
		if v := importString(details, "chatgpt_account_id"); v != "" {
			out.AccountID = accountText(v, 160)
		}
		if v := importString(details, "chatgpt_plan_type"); v != "" {
			out.Plan = accountText(v, 60)
		}
		if v := importString(details, "chatgpt_subscription_active_until"); v != "" {
			if date, e := time.Parse(time.RFC3339, v); e == nil {
				out.SubscriptionUntil = date.UTC().Format(time.RFC3339)
			}
		}
	}
	if v := importString(tokens, "account_id"); v != "" {
		out.AccountID = accountText(v, 160)
	}
	out.Message = "已读取本地身份；尚未在线查询额度。"
	if out.Email == "" && out.DisplayName == "" {
		out.Message = "本地登录未包含邮箱或名称；点击刷新账号读取 Codex 身份。"
	}
	return out
}
func codexAccountSignature(p EngineCredentialProfile, env Environment) string {
	raw, _ := json.Marshal([]any{p.ID, p.Created, p.Engine, p.Kind, p.Reference, env})
	return hash(string(raw))
}
func (s *Store) cachedCodexInfo(p EngineCredentialProfile, env Environment) CodexAccountInfo {
	var cached codexAccountCache
	if json.Unmarshal([]byte(s.setting("codex_account_info:"+p.ID)), &cached) == nil && cached.Signature == codexAccountSignature(p, env) {
		return cached.Info
	}
	return CodexAccountInfo{State: "unknown"}
}
func (s *Store) accountOrganization(id string) AccountOrganization {
	var value AccountOrganization
	_ = json.Unmarshal([]byte(s.setting("account_organization:"+id)), &value)
	return value
}
func sameCodexIdentity(a, b CodexAccountInfo) bool {
	return a.AuthType != "" && a.AuthType == b.AuthType && a.AccountID == b.AccountID && a.Email == b.Email && a.BaseURL == b.BaseURL
}
func inspectCodexAccount(ctx context.Context, env Environment, p EngineCredentialProfile, online bool, previous CodexAccountInfo) CodexAccountInfo {
	dir := ""
	if p.Kind == "codex_home" {
		dir = p.Reference
	}
	files, err := readNativeAccountFiles(ctx, env, dir, "codex")
	info := codexInfoFromFiles(files)
	if err != nil {
		info.State = "error"
		info.Message = "无法读取目标环境账号文件，请检查连接、目录权限或凭据存储方式。"
	}
	if !online {
		if sameCodexIdentity(info, previous) {
			info.Limits = previous.Limits
			info.QuotaUpdated = previous.QuotaUpdated
			info.Attempted = previous.Attempted
			if previous.QuotaUpdated > 0 {
				info.State = previous.State
				info.Message = previous.Message
			}
		}
		return info
	}
	info.Attempted = now()
	if info.AuthType == "apikey" {
		return info
	}
	updated, probeErr := nativeCodexAccountInfo(ctx, env, engineProfileEnv(p), info)
	if info.AuthType == "chatgpt" {
		// An external CLI may switch the same home while the request is running.
		// Never attach its quota to the identity captured before that switch.
		afterFiles, afterErr := readNativeAccountFiles(ctx, env, dir, "codex")
		after := codexInfoFromFiles(afterFiles)
		if afterErr != nil || !sameCodexIdentity(info, after) {
			after.State = "error"
			after.Attempted = info.Attempted
			after.Message = "查询期间原生账号发生变化或无法重新读取，请再次刷新。"
			return after
		}
	}
	if probeErr != nil {
		updated.State = "error"
		updated.Message = probeErr.Error()
	}
	if updated.QuotaUpdated == 0 && sameCodexIdentity(updated, previous) {
		updated.Limits = previous.Limits
		updated.QuotaUpdated = previous.QuotaUpdated
	}
	return updated
}

func (s *Server) codexAccountInfoRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/engine-profiles/{id}/account/refresh", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Online bool `json:"online"`
		}
		if !body(w, r, &req) {
			return
		}
		id := r.PathValue("id")
		s.app.mu.Lock()
		var profile EngineCredentialProfile
		for _, p := range s.app.store.engineProfiles() {
			if p.ID == id {
				profile = p
				break
			}
		}
		env, err := s.app.config.get().environment(profile.EnvironmentID)
		if profile.ID == "" || profile.Engine != "codex" || err != nil {
			s.app.mu.Unlock()
			fail(w, 404, "Codex 账号或执行环境不存在")
			return
		}
		if s.app.updating.Load() || s.app.ctx.Err() != nil {
			s.app.mu.Unlock()
			fail(w, 409, errUpdateBusy.Error())
			return
		}
		s.accountInfoMu.Lock()
		if s.accountInfoBusy == nil {
			s.accountInfoBusy = map[string]bool{}
		}
		key := env.ID + ":" + profile.Kind + ":" + profile.Reference
		if s.accountInfoBusy[key] || len(s.accountInfoBusy) >= 3 {
			s.accountInfoMu.Unlock()
			s.app.mu.Unlock()
			fail(w, 409, "账号信息正在刷新，请稍后重试")
			return
		}
		// Native refresh can renew tokens. Serialize it with account login,
		// imports and cross-environment sync so their file snapshots cannot race.
		manager := s.app.engineSetup
		manager.mu.Lock()
		if manager.active {
			manager.mu.Unlock()
			s.accountInfoMu.Unlock()
			s.app.mu.Unlock()
			fail(w, 409, "登录或账号同步正在进行，请稍后刷新")
			return
		}
		if req.Online {
			manager.active = true
		}
		manager.mu.Unlock()
		if req.Online {
			defer func() { manager.mu.Lock(); manager.active = false; manager.mu.Unlock() }()
		}
		s.accountInfoBusy[key] = true
		s.accountInfoMu.Unlock()
		s.app.wg.Add(1)
		s.app.mu.Unlock()
		defer s.app.wg.Done()
		defer func() { s.accountInfoMu.Lock(); delete(s.accountInfoBusy, key); s.accountInfoMu.Unlock() }()
		previous := s.app.store.cachedCodexInfo(profile, env)
		if req.Online && previous.Attempted > now()-15000 {
			jsonOut(w, 200, previous)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		stop := context.AfterFunc(s.app.ctx, cancel)
		defer stop()
		info := inspectCodexAccount(ctx, env, profile, req.Online, previous)
		if ctx.Err() != nil {
			fail(w, 408, "账号刷新超时或已取消，请检查目标环境连接")
			return
		}
		s.app.mu.Lock()
		defer s.app.mu.Unlock()
		currentEnv, e := s.app.config.get().environment(profile.EnvironmentID)
		valid := false
		for _, p := range s.app.store.engineProfiles() {
			if p.ID == id && e == nil && codexAccountSignature(p, currentEnv) == codexAccountSignature(profile, env) {
				valid = true
				break
			}
		}
		if !valid {
			fail(w, 409, "账号或环境已改变，请刷新列表")
			return
		}
		raw, _ := json.Marshal(codexAccountCache{Signature: codexAccountSignature(profile, env), Info: info})
		if s.app.store.set("codex_account_info:"+id, string(raw)) != nil {
			fail(w, 500, "保存账号信息失败")
			return
		}
		jsonOut(w, 200, info)
	}))
	m.HandleFunc("PATCH /api/engine-profiles/{id}/organization", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var req AccountOrganization
		if !body(w, r, &req) {
			return
		}
		if len([]rune(req.Note)) > 1000 || len(req.Tags) > 12 {
			fail(w, 400, "备注最多 1000 字，标签最多 12 个")
			return
		}
		req.Note = strings.TrimSpace(req.Note)
		tags := []string{}
		seen := map[string]bool{}
		for _, v := range req.Tags {
			v = accountText(v, 30)
			if v != "" && !seen[v] {
				tags = append(tags, v)
				seen[v] = true
			}
		}
		sort.Strings(tags)
		req.Tags = tags
		s.app.mu.Lock()
		defer s.app.mu.Unlock()
		if s.app.updating.Load() {
			fail(w, 409, errUpdateBusy.Error())
			return
		}
		found := false
		for _, p := range s.app.store.engineProfiles() {
			if p.ID == r.PathValue("id") {
				found = true
				break
			}
		}
		if !found {
			fail(w, 404, "账号已移除")
			return
		}
		raw, _ := json.Marshal(req)
		if s.app.store.set("account_organization:"+r.PathValue("id"), string(raw)) != nil {
			fail(w, 500, "保存备注标签失败")
			return
		}
		jsonOut(w, 200, req)
	}))
}

type codexQuotaWindow struct {
	Used    *float64 `json:"usedPercent"`
	Minutes int64    `json:"windowDurationMins"`
	Reset   int64    `json:"resetsAt"`
}
type codexQuotaBucket struct {
	Name      string            `json:"limitName"`
	Plan      string            `json:"planType"`
	Primary   *codexQuotaWindow `json:"primary"`
	Secondary *codexQuotaWindow `json:"secondary"`
}

func parseCodexQuota(raw json.RawMessage) ([]CodexAccountLimit, string, error) {
	var reply struct {
		RateLimits *codexQuotaBucket           `json:"rateLimits"`
		Buckets    map[string]codexQuotaBucket `json:"rateLimitsByLimitId"`
	}
	if json.Unmarshal(raw, &reply) != nil {
		return nil, "", errors.New("Codex 额度响应格式无效")
	}
	buckets := reply.Buckets
	if len(buckets) == 0 && reply.RateLimits != nil {
		buckets = map[string]codexQuotaBucket{"codex": *reply.RateLimits}
	}
	keys := []string{}
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 16 {
		keys = keys[:16]
	}
	limits := []CodexAccountLimit{}
	plan := ""
	for _, key := range keys {
		bucket := buckets[key]
		if bucket.Plan != "" && (plan == "" || key == "codex") {
			plan = accountText(bucket.Plan, 60)
		}
		name := accountText(bucket.Name, 100)
		if name == "" {
			name = accountText(key, 100)
		}
		for _, window := range []*codexQuotaWindow{bucket.Primary, bucket.Secondary} {
			if window == nil || window.Used == nil || *window.Used < 0 || *window.Used > 100 || window.Minutes <= 0 {
				continue
			}
			limits = append(limits, CodexAccountLimit{Name: name, UsedPercent: *window.Used, WindowMinutes: window.Minutes, ResetsAt: window.Reset})
		}
	}
	if len(limits) == 0 {
		return nil, plan, errors.New("服务未返回可显示的额度窗口")
	}
	return limits, plan, nil
}
func nativeCodexAccountInfo(ctx context.Context, env Environment, selected map[string]string, base CodexAccountInfo) (CodexAccountInfo, error) {
	out := base
	err := withCodexAccountRPC(ctx, env, selected, func(request func(string, string, any) (json.RawMessage, error)) error {
		raw, err := request("account-read", "account/read", map[string]bool{"refreshToken": true})
		if err != nil {
			return err
		}
		var reply struct {
			Account *struct {
				Type  string `json:"type"`
				Email string `json:"email"`
				Plan  string `json:"planType"`
			} `json:"account"`
			RequiresAuth bool `json:"requiresOpenaiAuth"`
		}
		if json.Unmarshal(raw, &reply) != nil {
			return errors.New("Codex 账号响应格式无效")
		}
		if reply.Account == nil {
			out.Email = ""
			out.DisplayName = ""
			out.AccountID = ""
			out.Plan = ""
			out.Limits = nil
			out.QuotaUpdated = 0
			out.State = "login_required"
			out.Message = "Codex 未返回已登录账号，请重新登录或检查服务配置。"
			return nil
		}
		if base.AuthType == "chatgpt" && (reply.Account.Type == "apiKey" || (base.Email != "" && reply.Account.Email != "" && !strings.EqualFold(base.Email, reply.Account.Email))) {
			return errors.New("Codex 返回的登录身份与所选账号不同，请检查该账号目录和目标环境配置。")
		}
		out.AuthType = accountText(reply.Account.Type, 50)
		out.IdentitySource = "codex"
		out.IdentityUpdated = now()
		if out.AuthType == "apiKey" {
			out.AuthType = "apikey"
			out.State = "api"
			out.Message = "API 账号不支持订阅额度查询。"
			out.Limits = nil
			out.QuotaUpdated = 0
			return nil
		}
		if reply.Account.Email != "" && out.Email != "" && reply.Account.Email != out.Email {
			out.DisplayName = ""
			out.AccountID = ""
			out.SubscriptionUntil = ""
			out.Limits = nil
			out.QuotaUpdated = 0
		}
		out.Email = accountText(reply.Account.Email, 254)
		out.Plan = accountText(reply.Account.Plan, 60)
		raw, err = request("account-limits", "account/rateLimits/read", map[string]any{})
		if err != nil {
			return err
		}
		limits, plan, err := parseCodexQuota(raw)
		if err != nil {
			return err
		}
		out.Limits = limits
		if plan != "" {
			out.Plan = plan
		}
		out.QuotaUpdated = now()
		out.State = "ready"
		out.Message = "账号与额度已刷新。"
		return nil
	})
	return out, err
}
