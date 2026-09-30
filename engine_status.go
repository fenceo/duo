package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type EngineEnvironmentStatus struct {
	EnvironmentID string       `json:"environment_id"`
	Name          string       `json:"name"`
	Type          string       `json:"type"`
	Distro        string       `json:"distro,omitempty"`
	User          string       `json:"user,omitempty"`
	Host          string       `json:"host,omitempty"`
	Codex         detectedTool `json:"codex"`
	Claude        detectedTool `json:"claude"`
	Harness       detectedTool `json:"harness"`
	Kimi          detectedTool `json:"kimi"`
	Mimo          detectedTool `json:"mimo"`
	Message       string       `json:"message,omitempty"`
}

type EngineStatusResponse struct {
	Items   []EngineEnvironmentStatus `json:"items"`
	Message string                    `json:"message,omitempty"`
}

func (s *Server) engineStatusRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/engine-status", s.secure(func(w http.ResponseWriter, r *http.Request) {
		config := s.app.config.get()
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		items := make([]EngineEnvironmentStatus, len(config.Environments))
		var wg sync.WaitGroup
		sem := make(chan struct{}, 3)
		for i, env := range config.Environments {
			wg.Add(1)
			go func(i int, env Environment) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				found := inspectEngineEnvironment(ctx, exactEngineProbe, env)
				items[i] = EngineEnvironmentStatus{
					EnvironmentID: env.ID,
					Name:          found.Environment.Name,
					Type:          found.Environment.Type,
					Distro:        found.Environment.Distro,
					User:          found.Environment.User,
					Host:          found.Environment.Host,
					Codex:         found.Codex,
					Claude:        found.Claude,
					Harness:       found.Harness,
					Kimi:          found.Kimi,
					Mimo:          found.Mimo,
					Message:       found.Message,
				}
			}(i, env)
		}
		wg.Wait()
		jsonOut(w, http.StatusOK, EngineStatusResponse{Items: items, Message: "检测只读取目标 CLI 的安装/登录状态，不调用模型"})
	}))
}

func exactEngineProbe(ctx context.Context, env Environment, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := commandWithContext(ctx, environmentProbeCommand(env, args...))
	out := &limitedBuffer{limit: 64 * 1024}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	hideCommand(cmd)
	err := cmd.Run()
	return out.String(), err
}

func enginePresenceScript(env Environment) string {
	return "set -eu\nprintf '__DUO_TOOLS__\\n'\n" +
		"find_cli() { for candidate in \"$@\"; do if [ -n \"$candidate\" ] && [ -f \"$candidate\" ] && [ -x \"$candidate\" ]; then printf '%s\\n' \"$candidate\"; return; fi; done; printf '\\n'; }\n" +
		"find_cli " + posixQuote(env.Codex) + " \"$(command -v codex 2>/dev/null || true)\" \"$HOME/.local/bin/codex\" \"$HOME/.codex/packages/standalone/current/bin/codex\"\n" +
		"find_cli " + posixQuote(env.Claude) + " \"$(command -v claude 2>/dev/null || true)\" \"$HOME/.local/bin/claude\"\n" +
		"find_cli " + posixQuote(env.Harness) + " \"$(command -v dsh 2>/dev/null || true)\" \"$HOME/.local/bin/dsh\"\n" +
		"find_cli \"$(command -v kimi 2>/dev/null || true)\" \"$(command -v kimi-code 2>/dev/null || true)\" \"$HOME/.local/bin/kimi\"\n" +
		"find_cli \"$(command -v mimo 2>/dev/null || true)\" \"$(command -v mimo-code 2>/dev/null || true)\" \"$HOME/.local/bin/mimo\"\n"
}

func inspectEngineEnvironment(ctx context.Context, probe environmentProbe, env Environment) detectedEnvironment {
	result := detectedEnvironment{Environment: env}
	paths := make([]string, 5)
	if env.Type == "windows" {
		home, _ := os.UserHomeDir()
		paths[0] = executableCandidate(env.Codex, "codex.exe", "codex.cmd", filepath.Join(home, ".codex", "packages", "standalone", "current", "bin", "codex.exe"))
		paths[1] = executableCandidate(env.Claude, "claude.exe", "claude.cmd", filepath.Join(home, ".local", "bin", "claude.exe"))
		paths[2] = executableCandidate(env.Harness, "dsh.cmd", "dsh.exe")
		paths[3] = executableCandidate("kimi.exe", "kimi.cmd", "kimi-code.exe")
		paths[4] = executableCandidate("mimo.exe", "mimo.cmd", "mimo-code.exe")
	} else {
		out, err := probe(ctx, env, "sh", "-lc", enginePresenceScript(env))
		_, payload, ok := strings.Cut(strings.ReplaceAll(out, "\r\n", "\n"), "__DUO_TOOLS__\n")
		lines := strings.Split(payload, "\n")
		if !ok || len(lines) < 6 {
			err = errors.New("invalid probe")
		}
		if err != nil {
			unknown := detectedTool{State: "unknown", Label: "未完成检测"}
			result.Codex, result.Claude, result.Harness, result.Kimi, result.Mimo = unknown, unknown, unknown, unknown, unknown
			result.Message = "请检查目标环境连接、用户及 shell；未将连接失败当作未安装"
			return result
		}
		copy(paths, lines[:5])
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); result.Codex = discoverTool(ctx, probe, env, paths[0], "codex") }()
	go func() { defer wg.Done(); result.Claude = discoverTool(ctx, probe, env, paths[1], "claude") }()
	wg.Wait()
	result.Harness = detectedInstalledTool(paths[2])
	result.Kimi = detectedInstalledTool(paths[3])
	result.Mimo = detectedInstalledTool(paths[4])
	return result
}
