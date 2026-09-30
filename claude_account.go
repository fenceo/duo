package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func (a *App) performClaudeAccount(ctx context.Context, env Environment, r EngineSetupRequest) error {
	dir := filepath.Join(a.store.directory, "engine-accounts", r.ID)
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return errors.New("无法创建账号目录")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return errors.New("账号目录已存在或无法创建，请重新开始")
	}
	variables := map[string]string{}
	for _, key := range claudeAccountEnvKeys {
		variables[key] = ""
	}
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "ANTHROPIC_PROFILE"} {
		variables[key] = ""
	}
	if r.BaseURL == "" {
		variables["ANTHROPIC_API_KEY"] = r.APIKey
	} else {
		variables["ANTHROPIC_AUTH_TOKEN"] = r.APIKey
		variables["ANTHROPIC_BASE_URL"] = strings.TrimRight(r.BaseURL, "/")
	}
	variables["ANTHROPIC_MODEL"] = r.Model
	data, _ := json.MarshalIndent(map[string]any{"env": variables}, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), data, 0600); err != nil {
		return errors.New("无法保存 Claude 原生账号配置")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	p := EngineCredentialProfile{ID: "managed-" + r.ID, Name: strings.TrimSpace(r.Name), Engine: "claude", EnvironmentID: env.ID, Kind: "claude_home", Reference: dir, Created: now(), Updated: now()}
	a.mu.Lock()
	err := validateEngineProfile(p, a.config.get().Environments)
	if err == nil {
		profiles := a.store.engineProfiles()
		profiles = append(profiles, p)
		err = a.store.saveEngineProfiles(profiles)
	}
	a.mu.Unlock()
	if err != nil {
		return errors.New("账号文件已保留，但保存配置引用失败")
	}
	a.engineSetup.mu.Lock()
	a.engineSetup.jobs[r.ID].ProfileID = p.ID
	a.engineSetup.mu.Unlock()
	a.engineSetup.update(r.ID, "done", "Claude API 配置已保存，可设为新任务默认或同步到其他环境。尚未调用模型验证服务。", "", "")
	return nil
}
