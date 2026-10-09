package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Prefer native isolation on supported Windows clients, without changing the
// user's saved configuration or the task's filesystem/network/approval policy.
// Codex itself selects MXC and enforces managed requirements and legacy fallback.
func prepareCodexWindowsSandbox(ctx context.Context, c Config, t Task) Config {
	c.codexPreferMXC = false
	if runtime.GOOS != "windows" || c.Distro != "" || c.SSHHost != "" {
		return c
	}
	sandbox, _, _, _ := codexPermissionSettings(t)
	if sandbox == "danger-full-access" || !codexAllowsMXCPreference(c, t.Workspace) {
		return c
	}
	c.codexPreferMXC = codexSupportsMXC(ctx, c)
	return c
}

func codexAllowsMXCPreference(c Config, workspace string) bool {
	home := c.EngineEnv["CODEX_HOME"]
	if home == "" {
		home = os.Getenv("CODEX_HOME")
	}
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		home = filepath.Join(userHome, ".codex")
	}
	// An explicit opt-out stays effective, including a project-local opt-out.
	// Unreadable or invalid configuration is left to Codex's own diagnostics.
	paths := []string{filepath.Join(home, "config.toml")}
	if workspace != "" {
		paths = append(paths, filepath.Join(workspace, ".codex", "config.toml"))
	}
	for _, path := range paths {
		file, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false
		}
		data, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
		_ = file.Close()
		if err != nil || len(data) > 1024*1024 {
			return false
		}
		var config struct {
			Features struct {
				PreferMXC *bool `toml:"prefer_mxc"`
			} `toml:"features"`
		}
		if toml.Unmarshal(data, &config) != nil || (config.Features.PreferMXC != nil && !*config.Features.PreferMXC) {
			return false
		}
	}
	return true
}

var codexMXCFeatures = struct {
	sync.Mutex
	known map[string]bool
}{known: map[string]bool{}}

func codexSupportsMXC(ctx context.Context, c Config) bool {
	cmd := command(c, c.Codex, "features", "list")
	info, err := os.Stat(cmd.Path)
	if err != nil || ctx.Err() != nil {
		return false
	}
	key := fmt.Sprintf("%s:%d:%d", cmd.Path, info.Size(), info.ModTime().UnixNano())
	codexMXCFeatures.Lock()
	defer codexMXCFeatures.Unlock()
	if supported, ok := codexMXCFeatures.known[key]; ok {
		return supported
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd = commandWithContext(bounded, cmd)
	applyEngineEnv(cmd, c.EngineEnv)
	hideCommand(cmd)
	out := &limitedBuffer{limit: 64 * 1024}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		return false // Do not cache a transient failure or pass unknown flags.
	}
	supported := codexHasMXCFeature(out.String())
	if len(codexMXCFeatures.known) >= 64 {
		clear(codexMXCFeatures.known)
	}
	codexMXCFeatures.known[key] = supported
	return supported
}

func codexHasMXCFeature(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "prefer_mxc" && (fields[len(fields)-1] == "true" || fields[len(fields)-1] == "false") {
			switch strings.Join(fields[1:len(fields)-1], " ") {
			case "stable", "experimental", "under development":
				return true
			}
		}
	}
	return false
}

func codexSandboxInitializationError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "setup refresh had errors") && !strings.Contains(message, "runtime read/execute validation failed") {
		return err
	}
	return fmt.Errorf("%w；Windows 沙箱初始化失败，请检查 Codex 版本、运行文件占用和设备沙箱策略；自动审批不能修复初始化错误", err)
}
