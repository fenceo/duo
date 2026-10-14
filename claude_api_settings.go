package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

func claudeAPIConfigLocation(path string) (string, string, error) {
	p := strings.ReplaceAll(path, `\`, "/")
	i := strings.LastIndex(p, "/")
	if i < 1 || p[i+1:] != "duo-api.json" || strings.ContainsAny(path, "\r\n\x00") {
		return "", "", errors.New("Claude Duo 配置路径无效")
	}
	return path[:i], "claude-api", nil
}

func nativeAPIConfigPath(env Environment, dir, name string) string {
	if env.Type == "windows" {
		return filepath.Join(dir, name)
	}
	return strings.TrimRight(dir, "/") + "/" + name
}

type ClaudeRoleModels struct {
	Mode     string `json:"mode"`
	Opus     string `json:"opus"`
	Sonnet   string `json:"sonnet"`
	Haiku    string `json:"haiku"`
	Fable    string `json:"fable,omitempty"`
	Subagent string `json:"subagent"`
}

func (r ClaudeRoleModels) values() []string {
	return []string{r.Opus, r.Sonnet, r.Haiku, r.Fable, r.Subagent}
}

func validateClaudeRoles(engine string, r *ClaudeRoleModels) error {
	if r == nil {
		return nil
	}
	if engine != "claude" || r.Mode != "uniform" && r.Mode != "custom" && r.Mode != "native" {
		return errors.New("Claude 模型角色模式无效")
	}
	for _, v := range r.values() {
		if len(v) > 120 || strings.ContainsAny(v, "\r\n\x00") || v != strings.TrimSpace(v) {
			return errors.New("Claude 角色模型 ID 无效")
		}
	}
	return nil
}

func claudeRolesFromEnv(vars map[string]any, model string) *ClaudeRoleModels {
	get := func(k string) string { v, _ := vars[k].(string); return v }
	r := &ClaudeRoleModels{Mode: "native", Opus: get("ANTHROPIC_DEFAULT_OPUS_MODEL"), Sonnet: get("ANTHROPIC_DEFAULT_SONNET_MODEL"), Haiku: get("ANTHROPIC_DEFAULT_HAIKU_MODEL"), Fable: get("ANTHROPIC_DEFAULT_FABLE_MODEL"), Subagent: get("CLAUDE_CODE_SUBAGENT_MODEL")}
	if r.Haiku == "" {
		r.Haiku = get("ANTHROPIC_SMALL_FAST_MODEL")
	}
	for _, v := range r.values() {
		if v != "" {
			r.Mode = "custom"
			break
		}
	}
	if model != "" && r.Opus == model && r.Sonnet == model && r.Haiku == model && (r.Fable == "" || r.Fable == model) && r.Subagent == model {
		r.Mode = "uniform"
	}
	return r
}

func applyClaudeRoles(vars map[string]string, model, base string, r *ClaudeRoleModels) {
	mode := "native"
	if r != nil {
		mode = r.Mode
	} else if base != "" && model != "" {
		mode = "uniform"
	}
	keys := []string{"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"}
	for _, k := range keys {
		vars[k] = ""
	}
	if mode == "uniform" {
		for _, k := range keys[:5] {
			vars[k] = model
		}
		return
	}
	if mode != "custom" {
		return
	}
	values := []string{r.Opus, r.Sonnet, r.Haiku, r.Fable, r.Subagent}
	for i, v := range values {
		if i < 3 && v == "" {
			v = model
		}
		vars[keys[i]] = v
	}
	// Clear the deprecated shortcut: it must not override the Haiku mapping.
}

func claudeAPIDirectory(ctx context.Context, env Environment) (string, error) {
	if env.Type == "windows" {
		return localAccountDirectory("", "claude-api")
	}
	result, err := remoteNativeAccountFiles(ctx, env, nativeAccountRequest{Operation: "directory", Engine: "claude-api"})
	if err != nil {
		return "", err
	}
	dir := string(result["directory"])
	if !strings.HasPrefix(dir, "/") || strings.ContainsAny(dir, "\r\n\x00") {
		return "", errors.New("Claude 配置目录无效")
	}
	return dir, nil
}
