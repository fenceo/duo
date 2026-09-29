package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const maxCodexAuthBytes = 4 * 1024 * 1024

// CodexSyncRequest asks Duo to project one locally readable Codex login into
// the native ~/.codex/auth.json of one or more configured environments. The
// login itself is never returned by the API or included in a command line.
type CodexSyncRequest struct {
	SourceProfileID string   `json:"source_profile_id"`
	EnvironmentIDs  []string `json:"environment_ids"`
}

type CodexSyncTargetResult struct {
	EnvironmentID string `json:"environment_id"`
	State         string `json:"state"`
	Message       string `json:"message"`
}

type CodexSyncResponse struct {
	SourceProfileID string                  `json:"source_profile_id"`
	Results         []CodexSyncTargetResult `json:"results"`
}

func readCodexAuth(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" || strings.ContainsAny(path, "\x00\r\n") {
		return nil, errors.New("Codex 配置目录无效")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		return nil, errors.New("账号配置目录不可读取")
	}
	authPath := filepath.Join(path, "auth.json")
	fileInfo, err := os.Lstat(authPath)
	if err != nil || !fileInfo.Mode().IsRegular() {
		return nil, errors.New("账号配置中没有可读取的 auth.json")
	}
	f, err := os.Open(authPath)
	if err != nil {
		return nil, errors.New("无法读取 Codex 登录信息")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxCodexAuthBytes+1))
	if err != nil || len(raw) > maxCodexAuthBytes {
		return nil, errors.New("Codex 登录信息文件过大或无法读取")
	}
	var object map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, errors.New("Codex auth.json 不是有效的 JSON 对象")
	}
	return raw, nil
}

// writeLocalCodexAuth keeps the previous file in a clearly named backup and
// installs the new content through a temporary file. The target must not be a
// symlink; this prevents a profile sync from unexpectedly writing elsewhere.
func writeLocalCodexAuth(home string, auth []byte) error {
	home = strings.TrimSpace(home)
	if home == "" || strings.ContainsAny(home, "\x00\r\n") {
		return errors.New("Windows 用户目录无效")
	}
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("无法创建 Windows Codex 配置目录")
	}
	target := filepath.Join(dir, "auth.json")
	backup := target + ".duo-backup"
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("Windows Codex auth.json 不是普通文件")
		}
	} else if !os.IsNotExist(err) {
		return errors.New("无法检查 Windows Codex auth.json")
	}
	tmp, err := os.CreateTemp(dir, ".auth.json.duo-*")
	if err != nil {
		return errors.New("无法准备 Windows Codex 临时文件")
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(auth)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return errors.New("无法写入 Windows Codex 登录信息")
	}
	oldExists := false
	if _, err = os.Lstat(target); err == nil {
		oldExists = true
		_ = os.Remove(backup)
		if err = os.Rename(target, backup); err != nil {
			return errors.New("无法备份现有 Windows Codex 登录信息")
		}
	}
	if err = os.Rename(tmpPath, target); err != nil {
		if oldExists {
			_ = os.Rename(backup, target)
		}
		return errors.New("无法替换 Windows Codex 登录信息")
	}
	return nil
}

// remoteCodexSyncScript reads auth.json from stdin. Keeping the secret in
// stdin means neither WSL/SSH argv inspection nor Duo's command diagnostics
// can expose it.
func remoteCodexSyncScript() string {
	return `set -eu
umask 077
dir="$HOME/.codex"
mkdir -p -- "$dir"
target="$dir/auth.json"
tmp="$dir/.auth.json.duo-tmp.$$"
backup="$target.duo-backup"
trap 'rm -f -- "$tmp"' EXIT
if [ -e "$target" ]; then
  if [ ! -f "$target" ]; then exit 21; fi
  rm -f -- "$backup"
  mv -- "$target" "$backup"
fi
cat >"$tmp"
chmod 600 "$tmp"
if ! mv -- "$tmp" "$target"; then
  if [ -e "$backup" ]; then mv -- "$backup" "$target"; fi
  exit 22
fi
trap - EXIT
`
}

func runRemoteCodexSync(ctx context.Context, env Environment, auth []byte) error {
	script := remoteCodexSyncScript()
	var cmdArgs []string
	var cmdPath string
	if env.Type == "wsl" {
		cmdPath = wslExecutable()
		cmdArgs = []string{"-d", env.Distro}
		if env.User != "" {
			cmdArgs = append(cmdArgs, "-u", env.User)
		}
		cmdArgs = append(cmdArgs, "--exec", "sh", "-c", script)
	} else if env.Type == "ssh" {
		c := runtimeConfig(Config{}, env)
		base := sshCommand(c, "sh", "-c", script)
		cmdPath, cmdArgs = base.Path, base.Args[1:]
	} else {
		return errors.New("不是远程 Linux 环境")
	}
	cmd := execCommandContext(ctx, cmdPath, cmdArgs...)
	cmd.Stdin = bytes.NewReader(auth)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	hideCommand(cmd)
	if err := cmd.Run(); err != nil {
		return errors.New("远程 Codex 登录信息写入失败，请检查连接、权限和目标目录")
	}
	return nil
}

// execCommandContext is a small seam for tests and keeps remote command
// creation in one place without ever putting auth bytes in argv.
var execCommandContext = func(ctx context.Context, path string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, path, args...)
}

func syncCodexAuth(ctx context.Context, env Environment, auth []byte) error {
	switch env.Type {
	case "windows":
		home, err := os.UserHomeDir()
		if err != nil {
			return errors.New("无法确定 Windows 用户目录")
		}
		return writeLocalCodexAuth(home, auth)
	case "wsl", "ssh":
		return runRemoteCodexSync(ctx, env, auth)
	default:
		return errors.New("目标环境类型不支持 Codex 登录同步")
	}
}

func (s *Server) codexSyncRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/codex-sync", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var req CodexSyncRequest
		if !body(w, r, &req) {
			return
		}
		req.SourceProfileID = strings.TrimSpace(req.SourceProfileID)
		if req.SourceProfileID == "" || len(req.EnvironmentIDs) == 0 || len(req.EnvironmentIDs) > 30 {
			fail(w, http.StatusBadRequest, "请选择账号配置和至少一个目标环境")
			return
		}
		profiles := s.app.store.engineProfiles()
		var source *EngineCredentialProfile
		for _, p := range profiles {
			if p.ID == req.SourceProfileID {
				copy := p
				source = &copy
				break
			}
		}
		if source == nil || source.Engine != "codex" || source.Kind != "codex_home" {
			fail(w, http.StatusBadRequest, "只能同步 Codex 配置目录类型的账号")
			return
		}
		auth, err := readCodexAuth(source.Reference)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		config := s.app.config.get()
		envs := make(map[string]Environment, len(config.Environments))
		for _, env := range config.Environments {
			envs[env.ID] = env
		}
		seen := map[string]bool{}
		selected := make([]Environment, 0, len(req.EnvironmentIDs))
		for _, id := range req.EnvironmentIDs {
			id = strings.TrimSpace(id)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			env, ok := envs[id]
			if !ok || (env.Type != "windows" && env.Type != "wsl" && env.Type != "ssh") {
				fail(w, http.StatusBadRequest, "目标环境不存在或不支持 Codex")
				return
			}
			selected = append(selected, env)
		}
		if len(selected) == 0 {
			fail(w, http.StatusBadRequest, "没有有效的目标环境")
			return
		}
		s.app.codexSyncMu.Lock()
		defer s.app.codexSyncMu.Unlock()
		results := make([]CodexSyncTargetResult, 0, len(selected))
		for _, env := range selected {
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			err := syncCodexAuth(ctx, env, auth)
			cancel()
			result := CodexSyncTargetResult{EnvironmentID: env.ID, State: "done", Message: "Codex 登录信息已写入目标环境；已运行的 app-server 需要重启后读取新账号"}
			if err != nil {
				result.State = "failed"
				result.Message = err.Error()
			}
			results = append(results, result)
		}
		jsonOut(w, http.StatusOK, CodexSyncResponse{SourceProfileID: source.ID, Results: results})
	}))
}
