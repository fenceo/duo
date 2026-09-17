package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
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
	base := environmentProbeCommand(env, args...)
	cmd := exec.CommandContext(ctx, base.Path, base.Args[1:]...)
	cmd.WaitDelay = time.Second
	hideCommand(cmd)
	data, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return string(data), err
}
func environmentProbeCommand(env Environment, args ...string) *exec.Cmd {
	if env.Type == "wsl" {
		base := []string{"-d", env.Distro}
		if env.User != "" {
			base = append(base, "-u", env.User)
		}
		// `wsl -- sh -c` invokes an extra login shell, expanding $@ and
		// $candidate before our script runs. --exec passes the script intact.
		return exec.Command("wsl.exe", append(append(base, "--exec"), args...)...)
	}
	return command(runtimeConfig(Config{}, env), args...)
}
func decodeWSLList(text string) []string {
	data := []byte(text)
	if len(data) > 1 && (data[1] == 0 || (data[0] == 0xff && data[1] == 0xfe)) {
		units := make([]uint16, 0, len(data)/2)
		for i := 0; i+1 < len(data); i += 2 {
			units = append(units, binary.LittleEndian.Uint16(data[i:i+2]))
		}
		text = string(utf16.Decode(units))
	}
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
`

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
		if err != nil || !ok || len(parts) < 4 || !strings.HasPrefix(parts[1], "/") {
			result.Message = "未完成检测，可在高级设置中手工配置"
			result.Codex = detectedTool{State: "unknown", Label: "未完成检测"}
			result.Claude = result.Codex
			return result
		}
		env.User = parts[0]
		env.Workspaces = []string{parts[1]}
		codex, claude = parts[2], parts[3]
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
