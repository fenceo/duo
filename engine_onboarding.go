package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	mu            sync.Mutex
	jobs          map[string]*EngineSetupJob
	active        bool
	account       func(context.Context, Environment, string, string, string, func(string, string)) error
	install       func(context.Context, string, string, func(string)) (string, error)
	remoteInstall func(context.Context, Environment, string, string, func(string)) (string, error)
}

func newEngineSetup() *EngineSetup {
	return &EngineSetup{jobs: map[string]*EngineSetupJob{}, account: setupCodexAccount, install: installManagedEngine, remoteInstall: installRemoteEngine}
}

func validateEngineSetup(r EngineSetupRequest, env Environment) error {
	if !safeWorkbenchID(r.ID) || len(r.ID) > 48 {
		return errors.New("请求标识无效")
	}
	if r.Action == "install" {
		if _, ok := managedEnginePackages[r.Engine]; !ok {
			return errors.New("此引擎暂不支持自动安装")
		}
		if env.Type != "windows" && env.Type != "wsl" && env.Type != "ssh" {
			return errors.New("目标环境类型不支持引擎安装")
		}
		return nil
	}
	if env.Type != "windows" {
		return errors.New("Codex 账号登录向导目前需要本机 Windows；WSL/SSH 请先在目标环境登录，再添加配置目录引用")
	}
	if r.Action != "account" || (r.Engine != "codex" && r.Engine != "claude") || strings.TrimSpace(r.Name) == "" || len([]rune(r.Name)) > 60 {
		return errors.New("请填写 Codex 或 Claude 账号名称")
	}
	if r.Engine == "claude" && r.Login != "apiKey" {
		return errors.New("Claude 订阅登录请引用已登录的配置目录；此向导用于 API 配置")
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
		} else if r.Engine == "claude" {
			err = a.performClaudeAccount(ctx, env, r)
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

func remoteEngineBinary(engine string) (string, bool) {
	switch engine {
	case "codex":
		return "codex", true
	case "claude":
		return "claude", true
	case "deepseek-harness":
		return "dsh", true
	default:
		return "", false
	}
}

// installRemoteEngine is deliberately a small allowlisted adapter. It runs
// the official npm package in the selected WSL/SSH environment and returns
// only the discovered executable path. Package-manager output never enters
// the task history or HTTP response.
func installRemoteEngine(ctx context.Context, env Environment, engine, installID string, progress func(string)) (string, error) {
	pkg, ok := managedEnginePackages[engine]
	if !ok {
		return "", errors.New("此引擎暂不支持远程自动安装")
	}
	binary, ok := remoteEngineBinary(engine)
	if !ok {
		return "", errors.New("此引擎没有可验证的远程 CLI 入口")
	}
	if env.Type != "wsl" && env.Type != "ssh" {
		return "", errors.New("远程安装只适用于 WSL 或 SSH")
	}
	progress("正在连接目标环境并检查 npm…")
	if !safeWorkbenchID(installID) {
		return "", errors.New("安装目录标识无效")
	}
	script := remoteEngineInstallScript(pkg, binary, installID)
	// Install on the exact selected user: never retry writes as WSL's default user.
	cmd := commandWithContext(ctx, environmentProbeCommand(env, "sh", "-lc", script))
	stdout := &limitedBuffer{limit: 64 * 1024}
	cmd.Stdout = stdout
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	hideCommand(cmd)
	err := cmd.Run()
	if err != nil {
		return "", errors.New("目标环境安装失败，请检查连接、npm 和权限")
	}
	lines := strings.Split(strings.ReplaceAll(stdout.String(), "\r\n", "\n"), "\n")
	path := ""
	for i, line := range lines {
		if strings.TrimSpace(line) == "__DUO_ENGINE_PATH__" && i+1 < len(lines) {
			path = strings.TrimSpace(lines[i+1])
			break
		}
	}
	if path == "" || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\x00\r\n") {
		return "", errors.New("安装完成但未找到目标环境的 CLI 路径")
	}
	progress("目标环境已安装并找到 " + path)
	return path, nil
}

func remoteEngineInstallScript(pkg, binary, id string) string {
	return "set -eu\ncommand -v node >/dev/null 2>&1\ncommand -v npm >/dev/null 2>&1\n" +
		"prefix=\"$HOME/.local/share/duo/engine-tools/" + id + "\"\n" +
		"mkdir -p -- \"$prefix\"\n" +
		"npm install --global --prefix \"$prefix\" --registry=https://registry.npmjs.org --no-audit --no-fund " + posixQuote(pkg+"@latest") + " >/dev/null 2>&1\n" +
		"test -x \"$prefix/bin/" + binary + "\"\n" +
		"\"$prefix/bin/" + binary + "\" --version >/dev/null 2>&1\n" +
		"printf '__DUO_ENGINE_PATH__\\n%s\\n' \"$prefix/bin/" + binary + "\"\n"
}

func (a *App) performEngineInstall(ctx context.Context, env Environment, r EngineSetupRequest) error {
	progress := func(message string) { a.engineSetup.update(r.ID, "running", message, "", "") }
	path := ""
	var err error
	if env.Type == "windows" {
		path, err = a.engineSetup.install(ctx, filepath.Join(a.store.directory, "engine-tools", r.ID), r.Engine, progress)
	} else {
		path, err = a.engineSetup.remoteInstall(ctx, env, r.Engine, r.ID, progress)
	}
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
		if c.Environments[i].ID == env.ID {
			if c.Environments[i].Type != env.Type || c.Environments[i].Distro != env.Distro || c.Environments[i].User != env.User || c.Environments[i].Host != env.Host || c.Environments[i].Port != env.Port || c.Environments[i].Identity != env.Identity {
				return errors.New("安装完成，但目标连接已修改，未保存旧目标的工具路径")
			}
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
