package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type EngineSetupRequest struct {
	ID            string `json:"id"`
	Action        string `json:"action"`
	EnvironmentID string `json:"environment_id"`
	Engine        string `json:"engine"`
	Name          string `json:"name"`
	Login         string `json:"login"`
	APIKey        string `json:"api_key"`
	BaseURL       string `json:"base_url"`
	Model         string `json:"model"`
}
type EngineSetupJob struct {
	Action        string `json:"action"`
	Engine        string `json:"engine"`
	EnvironmentID string `json:"environment_id"`
	ID            string `json:"id"`
	State         string `json:"state"`
	Message       string `json:"message"`
	URL           string `json:"url,omitempty"`
	Code          string `json:"code,omitempty"`
	ProfileID     string `json:"profile_id,omitempty"`
	requestHash   string
	cancel        context.CancelFunc
}
type EngineSetup struct {
	mu      sync.Mutex
	jobs    map[string]*EngineSetupJob
	active  bool
	account func(context.Context, Environment, string, string, string, func(string, string)) error
	install func(context.Context, string, string, func(string)) (string, error)
}

func newEngineSetup() *EngineSetup {
	return &EngineSetup{jobs: map[string]*EngineSetupJob{}, account: setupCodexAccount, install: installManagedEngine}
}

func validateEngineSetup(r EngineSetupRequest, env Environment) error {
	if !safeWorkbenchID(r.ID) || len(r.ID) > 48 || env.Type != "windows" {
		return errors.New("自动安装和账号创建目前支持本机 Windows；其它环境请使用配置目录引用")
	}
	if r.Action == "install" {
		if _, ok := managedEnginePackages[r.Engine]; !ok {
			return errors.New("此引擎暂不支持自动安装")
		}
		return nil
	}
	if r.Action != "account" || r.Engine != "codex" || strings.TrimSpace(r.Name) == "" || len([]rune(r.Name)) > 60 {
		return errors.New("请填写 Codex 账号名称")
	}
	if r.Login != "chatgptDeviceCode" && r.Login != "apiKey" {
		return errors.New("请选择 ChatGPT 登录或 API key")
	}
	if r.Login == "apiKey" && (strings.TrimSpace(r.APIKey) == "" || len(r.APIKey) > 16000 || strings.ContainsAny(r.APIKey, "\r\n\x00")) {
		return errors.New("API key 无效")
	}
	if r.Login != "apiKey" && (r.APIKey != "" || r.BaseURL != "") {
		return errors.New("ChatGPT 登录不使用 API key 或自定义地址")
	}
	if len(r.Model) > 120 || strings.ContainsAny(r.Model, "\r\n\x00") {
		return errors.New("模型名称无效")
	}
	if r.BaseURL != "" {
		u, err := url.Parse(r.BaseURL)
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))) {
			return errors.New("API 地址需要 HTTPS；本机测试可使用回环 HTTP 地址")
		}
	}
	return nil
}
func (m *EngineSetup) snapshot(id string) (EngineSetupJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[id]
	if j == nil {
		return EngineSetupJob{}, false
	}
	return *j, true
}
func (m *EngineSetup) update(id, state, message, link, code string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[id]
	if j == nil {
		return
	}
	j.State, j.Message, j.URL, j.Code = state, message, link, code
}
func (a *App) startEngineSetup(r EngineSetupRequest) (EngineSetupJob, error) {
	env, err := a.config.get().environment(r.EnvironmentID)
	if err != nil {
		return EngineSetupJob{}, err
	}
	if err = validateEngineSetup(r, env); err != nil {
		return EngineSetupJob{}, err
	}
	raw, _ := json.Marshal(r)
	digest := hash(string(raw))
	m := a.engineSetup
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.updating.Load() || a.ctx.Err() != nil {
		return EngineSetupJob{}, errUpdateBusy
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old := m.jobs[r.ID]; old != nil {
		if old.requestHash != digest {
			return EngineSetupJob{}, errors.New("请求标识已用于其它内容")
		}
		return *old, nil
	}
	if m.active {
		return EngineSetupJob{}, errors.New("另一项安装或登录正在进行，请等待完成或取消")
	}
	if len(a.store.engineProfiles()) >= 50 && r.Action == "account" {
		return EngineSetupJob{}, errors.New("账号配置已达 50 个")
	}
	for id, j := range m.jobs {
		if len(m.jobs) >= 30 && j.State != "running" && j.State != "waiting" {
			delete(m.jobs, id)
		}
	}
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Minute)
	job := &EngineSetupJob{ID: r.ID, Action: r.Action, Engine: r.Engine, EnvironmentID: r.EnvironmentID, State: "running", Message: "正在准备…", requestHash: digest, cancel: cancel}
	m.jobs[r.ID] = job
	m.active = true
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer cancel()
		defer func() { m.mu.Lock(); m.active = false; m.mu.Unlock() }()
		var err error
		if r.Action == "install" {
			err = a.performEngineInstall(ctx, env, r)
		} else {
			err = a.performCodexAccount(ctx, env, r)
		}
		if err != nil {
			message := err.Error()
			if ctx.Err() != nil {
				message = "操作已取消或超时，可重新开始"
			}
			m.update(r.ID, "failed", message, "", "")
		}
		a.changed()
	}()
	return *job, nil
}

func (a *App) performCodexAccount(ctx context.Context, env Environment, r EngineSetupRequest) error {
	dir := filepath.Join(a.store.directory, "engine-accounts", r.ID)
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return errors.New("无法创建账号目录")
	}
	// A fresh, per-account native home: never overwrite or copy the global login.
	if err := os.Mkdir(dir, 0700); err != nil {
		return errors.New("账号目录已存在或无法创建，请重新开始")
	}
	config := "cli_auth_credentials_store = \"file\"\n"
	if r.Model != "" {
		config += "model = " + strconv.Quote(r.Model) + "\n"
	}
	if r.BaseURL != "" {
		config += "model_provider = \"duo_api\"\n[model_providers.duo_api]\nname = \"Duo API\"\nbase_url = " + strconv.Quote(strings.TrimRight(r.BaseURL, "/")) + "\nwire_api = \"responses\"\nrequires_openai_auth = true\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(config), 0600); err != nil {
		return errors.New("无法保存原生账号配置")
	}
	a.engineSetup.update(r.ID, "running", "正在连接 Codex 登录服务…", "", "")
	err := a.engineSetup.account(ctx, env, dir, r.Login, r.APIKey, func(link, code string) {
		a.engineSetup.update(r.ID, "waiting", "打开登录页面，输入下面的设备码；完成后会自动保存账号。", link, code)
	})
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	profile := EngineCredentialProfile{ID: "managed-" + r.ID, Name: strings.TrimSpace(r.Name), Engine: "codex", EnvironmentID: env.ID, Kind: "codex_home", Reference: dir, Created: now(), Updated: now()}
	a.mu.Lock()
	if err = validateEngineProfile(profile, a.config.get().Environments); err != nil {
		a.mu.Unlock()
		return err
	}
	profiles := a.store.engineProfiles()
	profiles = append(profiles, profile)
	err = a.store.saveEngineProfiles(profiles)
	a.mu.Unlock()
	if err != nil {
		return errors.New("登录已完成，但保存账号引用失败；原生配置已保留")
	}
	a.engineSetup.mu.Lock()
	a.engineSetup.jobs[r.ID].ProfileID = profile.ID
	a.engineSetup.mu.Unlock()
	a.engineSetup.update(r.ID, "done", "账号配置已保存。可设为新任务默认，或在现有任务中点击“切换 AI”。未发送模型请求。", "", "")
	return nil
}
func (a *App) performEngineInstall(ctx context.Context, env Environment, r EngineSetupRequest) error {
	path, err := a.engineSetup.install(ctx, filepath.Join(a.store.directory, "engine-tools", r.ID), r.Engine, func(message string) { a.engineSetup.update(r.ID, "running", message, "", "") })
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.config.get()
	found := false
	for i := range c.Environments {
		if c.Environments[i].ID == env.ID && c.Environments[i].Type == "windows" {
			found = true
			switch r.Engine {
			case "codex":
				c.Environments[i].Codex = path
			case "claude":
				c.Environments[i].Claude = path
			case "deepseek-harness":
				c.Environments[i].Harness = path
			}
		}
	}
	if !found {
		return errors.New("安装完成，但执行环境已更改，请重新检测工具")
	}
	if err = a.config.save(c); err != nil {
		return errors.New("安装完成，但保存工具路径失败，请重新检测工具")
	}
	a.engineSetup.update(r.ID, "done", fmt.Sprintf("%s 已安装并配置到该环境。接下来登录或配置 API，再选择云端模型。", r.Engine), "", "")
	return nil
}

func (s *Server) engineSetupRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/engine-setup", s.secure(func(w http.ResponseWriter, r *http.Request) {
		manager := s.app.engineSetup
		manager.mu.Lock()
		jobs := []EngineSetupJob{}
		for _, j := range manager.jobs {
			if j.State == "running" || j.State == "waiting" {
				jobs = append(jobs, *j)
			}
		}
		manager.mu.Unlock()
		jsonOut(w, 200, jobs)
	}))
	m.HandleFunc("POST /api/engine-setup", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v EngineSetupRequest
		if !body(w, r, &v) {
			return
		}
		job, err := s.app.startEngineSetup(v)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		jsonOut(w, 202, job)
	}))
	m.HandleFunc("GET /api/engine-setup/{id}", s.secure(func(w http.ResponseWriter, r *http.Request) {
		job, ok := s.app.engineSetup.snapshot(r.PathValue("id"))
		if !ok {
			fail(w, 404, "安装或登录记录已过期，请重新开始")
			return
		}
		jsonOut(w, 200, job)
	}))
	m.HandleFunc("DELETE /api/engine-setup/{id}", s.secure(func(w http.ResponseWriter, r *http.Request) {
		job, ok := s.app.engineSetup.snapshot(r.PathValue("id"))
		if ok {
			job.cancel()
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}))
}
