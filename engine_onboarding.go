package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type EngineSetupRequest struct {
	ExpectedPath  string            `json:"expected_path,omitempty"`
	ID            string            `json:"id"`
	Action        string            `json:"action"`
	EnvironmentID string            `json:"environment_id"`
	Engine        string            `json:"engine"`
	Name          string            `json:"name"`
	Login         string            `json:"login"`
	APIKey        string            `json:"api_key"`
	BaseURL       string            `json:"base_url"`
	Model         string            `json:"model"`
	APIProtocol   string            `json:"api_protocol,omitempty"`
	AuthMode      string            `json:"auth_mode,omitempty"`
	ClaudeRoles   *ClaudeRoleModels `json:"claude_roles,omitempty"`
}
type EngineSetupJob struct {
	Version       string `json:"version,omitempty"`
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
	updatingTarget     *Environment
	updatingEngine     string
	verifyVersion      func(context.Context, Environment, string) (string, error)
	mu                 sync.Mutex
	jobs               map[string]*EngineSetupJob
	active             bool
	account            func(context.Context, Environment, string, string, string, func(string, string)) error
	accountHome        func(context.Context, Environment, string, string, string, map[string][]byte) (string, error)
	install            func(context.Context, string, string, func(string)) (string, error)
	remoteInstall      func(context.Context, Environment, string, string, func(string)) (string, error)
	harnessRead        func(context.Context, Environment, string, string) (map[string][]byte, error)
	harnessWrite       func(context.Context, Environment, string, string, map[string][]byte, map[string][]byte) error
	harnessDiscover    func(context.Context, Environment, HarnessAPIRequest) HarnessDiscovery
	apiDirectory       func(context.Context, Environment) (string, error)
	claudeAPIDirectory func(context.Context, Environment) (string, error)
}

func newEngineSetup() *EngineSetup {
	return &EngineSetup{jobs: map[string]*EngineSetupJob{}, account: setupCodexAccount, accountHome: createManagedAccountHome, install: installManagedEngine, remoteInstall: installRemoteEngine, harnessRead: readNativeAccountFiles, harnessWrite: writeNativeAccountFiles, harnessDiscover: discoverHarnessModels, apiDirectory: mimoAPIDirectory, claudeAPIDirectory: claudeAPIDirectory, verifyVersion: func(ctx context.Context, env Environment, path string) (string, error) {
		return readEngineVersion(ctx, exactEngineProbe, env, path)
	}}
}

func validateEngineSetup(r EngineSetupRequest, env Environment) error {
	if err := validateClaudeRoles(r.Engine, r.ClaudeRoles); err != nil {
		return err
	}
	if !safeWorkbenchID(r.ID) || len(r.ID) > 48 {
		return errors.New("请求标识无效")
	}
	if r.Action == "install" || r.Action == "update" {
		if _, ok := managedEnginePackages[r.Engine]; !ok && r.Engine != "mimo" {
			return errors.New("此引擎暂不支持自动安装")
		}
		if env.Type != "windows" && env.Type != "wsl" && env.Type != "ssh" {
			return errors.New("目标环境类型不支持引擎安装")
		}
		if r.Action == "update" && (r.ExpectedPath == "" || r.ExpectedPath != environmentEnginePath(env, r.Engine)) {
			return errors.New("CLI 配置已变化或尚未安装，请重新检测后更新")
		}
		return nil
	}
	if env.Type != "windows" && env.Type != "wsl" && env.Type != "ssh" {
		return errors.New("目标环境类型不支持账号配置")
	}
	if r.Action != "account" || (r.Engine != "codex" && r.Engine != "claude" && r.Engine != "mimo") || strings.TrimSpace(r.Name) == "" || len([]rune(r.Name)) > 60 {
		return errors.New("请填写账号/API 配置名称")
	}
	if r.Engine == "claude" && r.Login != "apiKey" {
		return errors.New("Claude 订阅登录请引用已登录的配置目录；此向导用于 API 配置")
	}
	if r.Engine == "mimo" && (r.Login != "apiKey" || strings.TrimSpace(r.Model) == "" || strings.TrimSpace(r.BaseURL) == "") {
		return errors.New("MiMo 配置需要 API key、API 地址和模型 ID")
	}
	if r.Login != "chatgptDeviceCode" && r.Login != "apiKey" {
		return errors.New("请选择 ChatGPT 登录或 API key")
	}
	if r.Login == "apiKey" && (strings.TrimSpace(r.APIKey) == "" || len(r.APIKey) > 16000 || strings.ContainsAny(r.APIKey, "\r\n\x00")) {
		return errors.New("API key 无效")
	}
	if r.APIProtocol != "" && ((r.Engine == "claude" && r.APIProtocol != "anthropic-messages") || (r.Engine == "mimo" && r.APIProtocol != "anthropic-messages" && r.APIProtocol != "openai-chat")) {
		return errors.New("此引擎不支持所选 API 协议")
	}
	if r.AuthMode != "" && (r.Engine != "claude" || (r.AuthMode != "api-key" && r.AuthMode != "auth-token" && r.AuthMode != "auto")) {
		return errors.New("认证方式不适用于此引擎")
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
	env, err := a.config.get().environment(r.EnvironmentID)
	if err != nil {
		return EngineSetupJob{}, err
	}
	if err = validateEngineSetup(r, env); err != nil {
		return EngineSetupJob{}, err
	}
	if m.active {
		return EngineSetupJob{}, errors.New("另一项安装或登录正在进行，请等待完成或取消")
	}
	if r.Action == "update" {
		if err := a.engineTargetBusy(env, r.Engine); err != nil {
			return EngineSetupJob{}, err
		}
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
	if r.Action == "update" {
		m.updatingTarget = &env
		m.updatingEngine = r.Engine
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer cancel()
		defer func() { m.mu.Lock(); m.active = false; m.updatingTarget = nil; m.updatingEngine = ""; m.mu.Unlock() }()
		var err error
		if r.Action == "install" || r.Action == "update" {
			err = a.performEngineInstall(ctx, env, r)
		} else if r.Engine == "claude" {
			err = a.performClaudeAccount(ctx, env, r)
		} else if r.Engine == "mimo" {
			err = a.performMimoAccount(ctx, env, r)
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
	config := "cli_auth_credentials_store = \"file\"\n"
	if r.Model != "" {
		config += "model = " + strconv.Quote(r.Model) + "\n"
	}
	if r.BaseURL != "" {
		config += "model_provider = \"duo_api\"\n[model_providers.duo_api]\nname = \"Duo API\"\nbase_url = " + strconv.Quote(strings.TrimRight(r.BaseURL, "/")) + "\nwire_api = \"responses\"\nrequires_openai_auth = true\n"
	}
	dir, err := a.engineSetup.accountHome(ctx, env, a.store.directory, r.ID, "codex", map[string][]byte{"config.toml": []byte(config)})
	if err != nil {
		return err
	}
	a.engineSetup.update(r.ID, "running", "正在连接 Codex 登录服务…", "", "")
	err = a.engineSetup.account(ctx, env, dir, r.Login, r.APIKey, func(link, code string) {
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
	current, envErr := a.config.get().environment(env.ID)
	if envErr != nil || !sameAccountEnvironment(env, current) {
		a.mu.Unlock()
		return errors.New("账号配置已保留，但目标连接已更改，未保存旧目标的账号引用")
	}
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
	a.engineSetup.update(r.ID, "done", "账号已保存到所选环境的独立目录。在账号卡片点击“切换到环境”后，该环境的后续 CLI 执行使用此账号；不绑定任务或对话。未发送模型请求。", "", "")
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
	case "opencode":
		return "opencode", true
	default:
		return "", false
	}
}

// installRemoteEngine is deliberately a small allowlisted adapter. It runs
// the official npm package in the selected WSL/SSH environment and returns
// only the discovered executable path. Package-manager output never enters
// the task history or HTTP response.
func installRemoteEngine(ctx context.Context, env Environment, engine, installID string, progress func(string)) (string, error) {
	if engine == "mimo" {
		return installRemoteMimo(ctx, env, installID, progress)
	}
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
	progress("正在连接目标环境并检查 Node.js/npm；缺失时安装独立运行时…")
	if !safeWorkbenchID(installID) {
		return "", errors.New("安装目录标识无效")
	}
	script := remoteEngineInstallScript(pkg, binary, installID)
	// Install on the exact selected user: never retry writes as WSL's default user.
	cmd := commandWithContext(ctx, environmentProbeCommand(env, "sh", "-ls"))
	cmd.Stdin = strings.NewReader(script)
	stdout := &limitedBuffer{limit: 64 * 1024}
	cmd.Stdout = stdout
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	hideCommand(cmd)
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", remoteEngineInstallError(stdout.String())
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

// Official nodejs.org v24.19.0 SHASUMS256.txt. Never execute an unchecked archive.
const managedNodeLinuxX64SHA256 = "f625d97cd707df4ff96254916fbc5ff014f09c09effe5a1e0ca8f6d41a8789d4"
const managedNodeLinuxARM64SHA256 = "d28c8a5bf0a808f0ed434a1dce8c54ae98f0371c0bd86ac58abc613f73e6643f"

//go:embed remote_engine_install.sh
var remoteEngineInstaller string

func remoteEngineInstallScript(pkg, binary, id string) string {
	return strings.NewReplacer("{{ID}}", id, "{{PACKAGE}}", pkg, "{{BINARY}}", binary,
		"{{VERSION}}", managedNodeVersion, "{{X64_SHA}}", managedNodeLinuxX64SHA256,
		"{{ARM64_SHA}}", managedNodeLinuxARM64SHA256).Replace(remoteEngineInstaller)
}

func remoteEngineInstallError(output string) error {
	// Only fixed allowlisted codes are public. npm/SSH output can contain proxy
	// URLs or registry credentials, so never return their raw diagnostics.
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	code := ""
	for i, line := range lines {
		if line == "__DUO_ENGINE_ERROR__" && i+1 < len(lines) {
			code = lines[i+1]
			break
		}
	}
	messages := map[string]string{
		"directory":          "目标用户安装目录不可写，请检查该用户的主目录权限；不会使用 sudo 或切换用户安装",
		"runtime_platform":   "缺少可用的原生 Node.js/npm；自动下载运行时目前支持 Linux，请先在该目标环境安装 Node.js 22 或更新版本",
		"runtime_arch":       "目标 Linux 架构暂不支持自动下载 Node.js，请先安装该架构的 Node.js 22 或更新版本",
		"runtime_tools":      "目标环境缺少 curl、tar 或 sha256sum，请先安装这些基础工具后重试",
		"runtime_download":   "无法下载 Linux Node.js 运行时，请检查目标环境能否访问 nodejs.org，以及 DNS、代理和网络设置",
		"runtime_checksum":   "Linux Node.js 下载校验失败，未执行下载文件，请检查网络后重新安装",
		"runtime_unpack":     "Linux Node.js 运行时解压失败，请检查安装目录和磁盘空间",
		"runtime_version":    "目标环境的 Node.js 运行检查失败，需要可用的原生 Node.js 22 或更新版本",
		"package_permission": "npm 没有写入权限，请检查目标用户的主目录及 npm 缓存目录权限；不会使用 sudo 安装",
		"package_network":    "npm 无法连接官方仓库，请检查目标环境的 DNS、代理及 registry.npmjs.org 网络连接",
		"package_space":      "npm 安装时磁盘空间不足，请清理目标环境磁盘后重试",
		"package_install":    "官方 npm 包安装失败，尚未修改环境的工具配置；请检查目标环境网络、npm 和磁盘权限",
		"entry_missing":      "npm 安装完成，但安装包缺少可执行的 CLI 入口，尚未修改环境配置",
		"entry_start":        "CLI 已下载但版本检查失败，尚未修改环境配置，请检查目标系统兼容性",
	}
	if message, ok := messages[code]; ok {
		return errors.New(message)
	}
	return errors.New("无法完成目标环境安装，请检查 WSL 发行版/用户或 SSH 连接；安装输出未包含可确认的失败阶段")
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
	version := ""
	if r.Action == "update" {
		progress("正在验证新版 CLI；验证成功后才切换路径…")
		version, err = a.engineSetup.verifyVersion(ctx, env, path)
		if err != nil {
			return err
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	c := a.config.get()
	var expected []byte
	if r.Action == "update" {
		expected, _ = json.Marshal(c)
	}
	found := false
	for i := range c.Environments {
		if c.Environments[i].ID == env.ID {
			if !sameEngineTarget(c.Environments[i], env) || environmentEnginePath(c.Environments[i], r.Engine) != environmentEnginePath(env, r.Engine) {
				return errors.New("安装完成，但目标连接已修改，未保存旧目标的工具路径")
			}
			found = true
			if r.Action == "update" {
				previous := []string{r.ExpectedPath}
				if old, ok := c.Environments[i].ManagedTools[r.Engine]; ok && old.Path == r.ExpectedPath {
					previous = append(previous, old.PreviousPaths...)
				}
				if c.Environments[i].ManagedTools == nil {
					c.Environments[i].ManagedTools = map[string]ManagedEngineTool{}
				}
				c.Environments[i].ManagedTools[r.Engine] = ManagedEngineTool{Path: path, Version: version, PreviousPaths: previous}
			}
			switch r.Engine {
			case "codex":
				c.Environments[i].Codex = path
			case "claude":
				c.Environments[i].Claude = path
			case "deepseek-harness":
				c.Environments[i].Harness = path
			case "mimo":
				c.Environments[i].Mimo = path
			}
		}
	}
	if !found {
		return errors.New("安装完成，但执行环境已更改，请重新检测工具")
	}
	if err = a.config.saveChecked(c, expected); err != nil {
		return errors.New("安装完成，但保存工具路径失败，请重新检测工具")
	}
	if r.Action == "update" {
		a.engineSetup.mu.Lock()
		a.engineSetup.jobs[r.ID].Version = version
		a.engineSetup.mu.Unlock()
		a.engineSetup.update(r.ID, "done", fmt.Sprintf("%s CLI 已更新到 %s。该环境中使用原路径的任务下次执行会使用新版；账号、会话与原安装保留。", r.Engine, version), "", "")
		return nil
	}
	message := fmt.Sprintf("%s 已安装并配置到该环境。接下来登录或配置 API，再选择云端模型。", r.Engine)
	if r.Engine == "deepseek-harness" {
		message = "Harness CLI 已安装到该环境。点击 Harness 行的“配置 API”填写地址、Key 和模型；Duo 通过 ACP 调用。"
	}
	a.engineSetup.update(r.ID, "done", message, "", "")
	return nil
}

func (s *Server) engineSetupRoutes(m *http.ServeMux) {
	s.engineAPIModelsRoutes(m)
	s.engineAPIRoutes(m)
	s.harnessAPIRoutes(m)
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
