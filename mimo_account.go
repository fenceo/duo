package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

func mimoAPIConfig(r EngineSetupRequest) []byte {
	// The official OpenAI-compatible adapter expects exact baseURL and apiKey.
	// A separate config file changes routing without changing native session data.
	model := "duo-mimo/" + strings.TrimSpace(r.Model)
	sdk := "@ai-sdk/openai-compatible"
	if r.APIProtocol == "anthropic-messages" {
		sdk = "@ai-sdk/anthropic"
	}
	provider := map[string]any{"name": strings.TrimSpace(r.Name), "npm": sdk, "only_configured_models": true, "models": map[string]any{r.Model: mimoModelDefinition(r.Model, r.Model, r.APIProtocol)}, "options": map[string]string{"baseURL": strings.TrimRight(r.BaseURL, "/"), "apiKey": r.APIKey}}
	data, _ := json.MarshalIndent(map[string]any{"$schema": "https://mimo.xiaomi.com/mimocode/config.json", "model": model, "enabled_providers": []string{"duo-mimo"}, "provider": map[string]any{"duo-mimo": provider}}, "", "  ")
	return data
}

func (a *App) performMimoAccount(ctx context.Context, env Environment, r EngineSetupRequest) error {
	data := mimoAPIConfig(r)
	dir, err := a.engineSetup.accountHome(ctx, env, a.store.directory, r.ID, "mimo", map[string][]byte{"mimocode.json": data})
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	p := EngineCredentialProfile{ID: "managed-" + r.ID, Name: strings.TrimSpace(r.Name), Engine: "mimo", EnvironmentID: env.ID, Kind: "mimo_config", Reference: dir, Created: now(), Updated: now()}
	a.mu.Lock()
	current, e := a.config.get().environment(env.ID)
	if e != nil || !sameAccountEnvironment(env, current) {
		a.mu.Unlock()
		return errors.New("MiMo 配置文件已保留，但目标环境变化，未保存旧目标引用")
	}
	err = validateEngineProfile(p, a.config.get().Environments)
	if err == nil {
		profiles := a.store.engineProfiles()
		profiles = append(profiles, p)
		err = a.store.saveEngineProfiles(profiles)
	}
	a.mu.Unlock()
	if err != nil {
		return errors.New("MiMo 配置文件已保存，但保存引用失败")
	}
	a.engineSetup.mu.Lock()
	a.engineSetup.jobs[r.ID].ProfileID = p.ID
	a.engineSetup.mu.Unlock()
	a.engineSetup.update(r.ID, "done", "MiMo API 配置已保存。点击账号卡片“用于此环境”后生效；保留原生会话目录，尚未调用模型验证服务。", "", "")
	return nil
}
