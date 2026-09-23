package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

type detectedTool struct {
	Path  string `json:"path"`
	State string `json:"state"`
	Label string `json:"label"`
}
type detectedEnvironment struct {
	Environment Environment  `json:"environment"`
	Codex       detectedTool `json:"codex"`
	Claude      detectedTool `json:"claude"`
	Message     string       `json:"message"`
}
type environmentDiscovery struct {
	Items   []detectedEnvironment `json:"items"`
	Message string                `json:"message"`
}
type environmentProbe func(context.Context, Environment, ...string) (string, error)

func environmentProbeVariants(env Environment) []Environment {
	variants := []Environment{env}
	if env.Type == "wsl" && strings.TrimSpace(env.User) != "" {
		fallback := env
		fallback.User = ""
		variants = append(variants, fallback)
	}
	return variants
}

// WSL user names are configuration hints, not a hard dependency. A distro can
// be imported under a different Linux user, so retry with its configured
// default user before reporting a connection failure.
func runEnvironmentCommand(ctx context.Context, env Environment, args ...string) ([]byte, []byte, Environment, error) {
	var lastStdout, lastStderr []byte
	lastEnv := env
	var lastErr error
	diagnostics := []string{}
	for _, variant := range environmentProbeVariants(env) {
		lastEnv = variant
		cmd := environmentProbeCommand(variant, args...)
		bounded := commandWithContext(ctx, cmd)
		bounded.WaitDelay = time.Second
		hideCommand(bounded)
		stdout := &cappedOutput{limit: 512 * 1024}
		stderr := &cappedOutput{limit: 4096}
		bounded.Stdout = stdout
		bounded.Stderr = stderr
		err := bounded.Run()
		lastStdout = stdout.Bytes()
		lastStderr = stderr.Bytes()
		if err == nil {
			return lastStdout, []byte(commandDiagnosticText(variant, lastStderr)), variant, nil
		}
		lastErr = err
		output := strings.TrimSpace(strings.Join([]string{
			commandDiagnosticText(variant, lastStdout),
			commandDiagnosticText(variant, lastStderr),
		}, "\n"))
		if text := strings.TrimSpace(output); text != "" {
			user := variant.User
			if user == "" {
				user = "发行版默认用户"
			}
			diagnostics = append(diagnostics, user+"： "+text)
		}
		if env.Type == "wsl" && strings.Contains(output, "Wsl/Service/E_ACCESSDENIED") {
			break
		}
		if ctx.Err() != nil {
			return lastStdout, []byte(strings.Join(diagnostics, "\n")), variant, ctx.Err()
		}
	}
	if len(diagnostics) > 0 {
		lastStderr = []byte(strings.Join(diagnostics, "\n"))
	} else {
		lastStderr = []byte(commandDiagnosticText(lastEnv, lastStderr))
	}
	if lastErr == nil {
		lastErr = errors.New("环境命令执行失败")
	}
	return lastStdout, []byte(friendlyEnvironmentDiagnostic(lastEnv, string(lastStderr))), lastEnv, lastErr
}

// Detection returns short states, never raw CLI auth output or account details.
func detectedAuth(path, output string, err error) detectedTool {
	if path == "" {
		return detectedTool{State: "missing", Label: "未发现安装"}
	}
	result := detectedTool{Path: path, State: "unknown", Label: "已找到 · 登录待检查"}
	text := strings.ToLower(output)
	if strings.Contains(text, "not logged") || strings.Contains(text, "not authenticated") || strings.Contains(text, "logged out") {
		result.State, result.Label = "login", "已安装 · 需要登录"
	} else if err == nil && (strings.Contains(text, "logged in") || strings.Contains(text, "authenticated") || strings.Contains(text, "auth method:") || strings.Contains(text, "auth token:")) {
		result.State, result.Label = "configured", "已安装 · 已有登录配置"
	}
	return result
}

func probeEnvironment(ctx context.Context, env Environment, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()
	stdout, stderr, _, err := runEnvironmentCommand(ctx, env, args...)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return string(append(stdout, stderr...)), err
}
func environmentProbeCommand(env Environment, args ...string) *exec.Cmd {
	if env.Type == "wsl" {
		base := []string{"-d", env.Distro}
		if env.User != "" {
			base = append(base, "-u", env.User)
		}
		// `wsl -- sh -c` invokes an extra login shell, expanding $@ and
		// $candidate before our script runs. --exec passes the script intact.
		return wslCommand(append(append(base, "--exec"), args...)...)
	}
	return command(runtimeConfig(Config{}, env), args...)
}

func commandDiagnosticText(env Environment, data []byte) string {
	if env.Type != "wsl" {
		return string(data)
	}
	return decodeWSLText(data)
}

func friendlyEnvironmentDiagnostic(env Environment, text string) string {
	text = strings.TrimSpace(text)
	if env.Type != "wsl" || text == "" {
		return text
	}
	if strings.Contains(text, "Wsl/Service/E_ACCESSDENIED") {
		return text + "；Windows 当前启动进程没有访问 WSL 服务的权限，请从开始菜单、资源管理器或安装版的启动入口打开 Duo"
	}
	return text
}

func decodeWSLText(data []byte) string {
	best := ""
	for offset := 0; offset+1 < len(data) && offset < 64; offset++ {
		units := make([]uint16, 0, (len(data)-offset)/2)
		for i := offset; i+1 < len(data); i += 2 {
			units = append(units, binary.LittleEndian.Uint16(data[i:i+2]))
		}
		text := string(utf16.Decode(units))
		if strings.Contains(text, "Wsl/") || strings.Contains(text, "WSL/") || strings.Contains(text, "Service/") {
			// wsl.exe can prefix UTF-16 diagnostics with a short binary
			// record. Prefer the first readable diagnostic marker.
			if index := strings.Index(text, "错误代码"); index >= 0 {
				return text[index:]
			}
			for _, marker := range []string{"Wsl/", "WSL/", "Service/"} {
				if index := strings.Index(text, marker); index >= 0 {
					text = text[index:]
					break
				}
			}
			if best == "" || len(text) < len(best) {
				best = text
			}
		}
	}
	if best != "" {
		return best
	}
	if len(data) > 1 && (data[1] == 0 || (data[0] == 0xff && data[1] == 0xfe)) {
		units := make([]uint16, 0, len(data)/2)
		for i := 0; i+1 < len(data); i += 2 {
			units = append(units, binary.LittleEndian.Uint16(data[i:i+2]))
		}
		return string(utf16.Decode(units))
	}
	return string(data)
}

func decodeWSLList(text string) []string {
	text = decodeWSLText([]byte(text))
	var names []string
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		name := strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if name == "" || strings.HasPrefix(strings.ToLower(name), "docker-desktop") || strings.ContainsRune(name, '\x00') || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
		if len(names) == 8 {
			break
		}
	}
	return names
}

const discoverWSLScript = `printf '__JIANZUO_ENV__\n%s\n%s\n' "$(id -un)" "$HOME"
find_cli() {
 for candidate in "$@"; do if [ -f "$candidate" ] && [ -x "$candidate" ]; then printf '%s\n' "$candidate"; return; fi; done
 printf '\n'
}
find_cli "$HOME/.codex/packages/standalone/current/bin/codex" "$HOME/.local/bin/codex" "$(command -v codex 2>/dev/null)" /usr/local/bin/codex
find_cli "$HOME/.local/bin/claude" "$(command -v claude 2>/dev/null)" /usr/local/bin/claude
proxy=0
for value in "${HTTPS_PROXY:-}" "${HTTP_PROXY:-}" "${ALL_PROXY:-}" "${https_proxy:-}" "${http_proxy:-}" "${all_proxy:-}"; do
 if [ -n "$value" ]; then proxy=1; break; fi
done
git_config=0
for file in "$HOME/.gitconfig" "${XDG_CONFIG_HOME:-$HOME/.config}/git/config" "${GIT_CONFIG_GLOBAL:-}"; do
 if [ -n "$file" ] && [ -f "$file" ]; then git_config=1; break; fi
done
ssh_agent=0
if [ -n "${SSH_AUTH_SOCK:-}" ] && [ -S "$SSH_AUTH_SOCK" ]; then ssh_agent=1; fi
shell=${SHELL:-/bin/sh}
shell_ready=0
if command -v "$shell" >/dev/null 2>&1; then shell_ready=1; fi
printf 'proxy=%s\ngit_config=%s\nssh_agent=%s\nshell=%s\n' "$proxy" "$git_config" "$ssh_agent" "$shell_ready"
`

func wslDiagnosticMessage(parts []string) string {
	state := func(index int, name, present, missing string) string {
		if len(parts) > index && parts[index] == name+"=1" {
			return present
		}
		return missing
	}
	return strings.Join([]string{
		state(4, "proxy", "已发现代理变量", "未设置代理变量"),
		state(5, "git_config", "Git 全局配置已发现", "未发现 Git 全局配置"),
		state(6, "ssh_agent", "SSH agent 可用", "未连接 SSH agent"),
		state(7, "shell", "用户 shell 可用", "用户 shell 不可用"),
	}, "；")
}

func discoverTool(ctx context.Context, probe environmentProbe, env Environment, path, engine string) detectedTool {
	if path == "" {
		return detectedAuth("", "", nil)
	}
	args := []string{path, "login", "status"}
	if engine == "claude" {
		args = []string{path, "auth", "status", "--text"}
	}
	out, err := probe(ctx, env, args...)
	return detectedAuth(path, out, err)
}
func discoverOne(ctx context.Context, probe environmentProbe, env Environment) detectedEnvironment {
	result := detectedEnvironment{Environment: env}
	codex, claude := env.Codex, env.Claude
	if env.Type == "wsl" {
		out, err := probe(ctx, env, "sh", "-c", discoverWSLScript)
		_, payload, ok := strings.Cut(out, "__JIANZUO_ENV__\n")
		parts := strings.Split(strings.ReplaceAll(payload, "\r\n", "\n"), "\n")
		if err != nil || !ok || len(parts) < 8 || !strings.HasPrefix(parts[1], "/") {
			result.Message = "未完成 WSL 检测。请确认所选发行版和用户可用；当前进程可能位于 Codex/受限沙箱，请从资源管理器、开始菜单或普通快捷方式启动Duo，再到高级设置手动配置。"
			result.Codex = detectedTool{State: "unknown", Label: "未完成检测"}
			result.Claude = result.Codex
			return result
		}
		env.User = parts[0]
		env.Workspaces = []string{parts[1]}
		codex, claude = parts[2], parts[3]
		result.Message = wslDiagnosticMessage(parts)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); result.Codex = discoverTool(ctx, probe, env, codex, "codex") }()
	go func() { defer wg.Done(); result.Claude = discoverTool(ctx, probe, env, claude, "claude") }()
	wg.Wait()
	env.Codex = codex
	env.Claude = claude
	env.DefaultEngine = "codex"
	if result.Claude.State == "configured" && result.Codex.State != "configured" || codex == "" && claude != "" {
		env.DefaultEngine = "claude"
	}
	if env.Codex == "" {
		env.Codex = "codex"
	}
	if env.Claude == "" {
		env.Claude = "claude"
	}
	result.Environment = env
	return result
}
func executableCandidate(paths ...string) string {
	for _, path := range paths {
		if path == "" {
			continue
		}
		found, err := exec.LookPath(path)
		if err == nil {
			return found
		}
	}
	return ""
}
func detectEnvironments(ctx context.Context) environmentDiscovery {
	ctx, cancel := context.WithTimeout(ctx, 24*time.Second)
	defer cancel()
	win := windowsEnvironment()
	home, _ := os.UserHomeDir()
	win.Codex = executableCandidate(win.Codex, filepath.Join(home, ".codex", "packages", "standalone", "current", "bin", "codex.exe"), "codex.exe")
	win.Claude = executableCandidate(claudeBinary("windows"), "claude.exe")
	work := filepath.Join(home, "Documents")
	if info, err := os.Stat(work); err != nil || !info.IsDir() {
		work = home
	}
	win.Workspaces = []string{work}
	envs := []Environment{win}
	result := environmentDiscovery{Message: "检测本机 Windows 和已安装的 WSL；登录状态来自本地工具，未调用模型。SSH 请单独添加。"}
	out, err := probeEnvironment(ctx, Environment{Type: "windows"}, "wsl.exe", "--list", "--quiet")
	if err == nil {
		for i, name := range decodeWSLList(out) {
			envs = append(envs, Environment{ID: fmt.Sprintf("detected-wsl-%d", i+1), Name: "WSL · " + name, Type: "wsl", Distro: name, Workspaces: []string{"/home"}})
		}
	}
	if err != nil {
		result.Message += " WSL 列表未能读取，请检查 WSL 安装状态或服务权限。"
	}
	result.Items = make([]detectedEnvironment, len(envs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 3)
	for i, env := range envs {
		wg.Add(1)
		go func(i int, env Environment) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			result.Items[i] = discoverOne(ctx, probeEnvironment, env)
		}(i, env)
	}
	wg.Wait()
	return result
}
func (s *Server) detectEnvironments(w http.ResponseWriter, r *http.Request) {
	if !s.environmentDetection.TryLock() {
		fail(w, 409, "环境检测正在进行，请稍后重试")
		return
	}
	defer s.environmentDetection.Unlock()
	jsonOut(w, 200, detectEnvironments(r.Context()))
}
func printDetectedEnvironments() error {
	return json.NewEncoder(os.Stdout).Encode(detectEnvironments(context.Background()))
}
