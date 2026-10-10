package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

func (a *App) performClaudeAccount(ctx context.Context, env Environment, r EngineSetupRequest) error {
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
	dir, err := a.engineSetup.accountHome(ctx, env, a.store.directory, r.ID, "claude", map[string][]byte{"settings.json": data})
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	p := EngineCredentialProfile{ID: "managed-" + r.ID, Name: strings.TrimSpace(r.Name), Engine: "claude", EnvironmentID: env.ID, Kind: "claude_home", Reference: dir, Created: now(), Updated: now()}
	a.mu.Lock()
	current, envErr := a.config.get().environment(env.ID)
	if envErr != nil || !sameAccountEnvironment(env, current) {
		a.mu.Unlock()
		return errors.New("账号配置已保留，但目标连接已更改，未保存旧目标的账号引用")
	}
	err = validateEngineProfile(p, a.config.get().Environments)
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
	a.engineSetup.update(r.ID, "done", "Claude API 配置已保存到所选环境。在账号卡片点击“切换到环境”后生效；不绑定任务或对话。尚未调用模型验证服务。", "", "")
	return nil
}
