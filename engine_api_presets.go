package main

import (
	"encoding/json"
	"strings"
)

// Engine API providers are independent of engine CLIs. A provider is an
// endpoint + auth + protocol that any compatible engine can use. Official
// presets only pre-fill known URLs; credentials stay in the target environment
// under that engine's native config format.

type EngineAPIPreset struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"` // official | partner | custom
	Protocol   string `json:"protocol"`
	BaseURL    string `json:"base_url"`
	AuthMode   string `json:"auth_mode"`
	Hint       string `json:"hint"`
	EngineHint string `json:"engine_hint,omitempty"`
}

var engineAPIPresets = []EngineAPIPreset{
	{ID: "anthropic", Name: "Anthropic 官方 API", Kind: "official", Protocol: "anthropic-messages", BaseURL: "", AuthMode: "api-key", Hint: "Claude Code 官方入口；模型 ID 以订阅或控制台为准。"},
	{ID: "openai", Name: "OpenAI 官方 API", Kind: "official", Protocol: "openai-chat", BaseURL: "https://api.openai.com/v1", AuthMode: "api-key", Hint: "OpenAI Chat Completions；Codex 账号请用账号管理 OAuth。"},
	{ID: "deepseek", Name: "DeepSeek 官方 API", Kind: "official", Protocol: "openai-chat", BaseURL: "https://api.deepseek.com/v1", AuthMode: "api-key", Hint: "官方 OpenAI 兼容接口；V4.1 系列可用 deepseek-v4.1-flash / deepseek-chat / deepseek-reasoner。Search / Fetch 工具单独计费。", EngineHint: "deepseek-harness"},
	{ID: "zai", Name: "Z.ai · GLM", Kind: "official", Protocol: "anthropic-messages", BaseURL: "https://api.z.ai/api/anthropic", AuthMode: "auth-token", Hint: "智谱 GLM 官方 Anthropic 兼容入口；也可用 OpenAI 兼容地址。"},
	{ID: "moonshot", Name: "Moonshot · Kimi", Kind: "official", Protocol: "openai-chat", BaseURL: "https://api.moonshot.cn/v1", AuthMode: "api-key", Hint: "Kimi 官方 OpenAI 兼容接口。", EngineHint: "kimi"},
	{ID: "minimax", Name: "MiniMax 官方 API", Kind: "official", Protocol: "openai-chat", BaseURL: "https://api.minimaxi.com/v1", AuthMode: "auth-token", Hint: "MiniMax 开放平台；请按控制台选择国际或国内域名。"},
	{ID: "mimo", Name: "MiMo 官方 API", Kind: "official", Protocol: "openai-chat", BaseURL: "https://api.xiaomimimo.com/v1", AuthMode: "auth-token", Hint: "小米 MiMo 按量付费。", EngineHint: "mimo"},
	{ID: "mimo-cn", Name: "MiMo Token Plan · 中国", Kind: "partner", Protocol: "openai-chat", BaseURL: "https://token-plan-cn.xiaomimimo.com/v1", AuthMode: "auth-token", Hint: "Token Plan Key 需匹配套餐区域地址。", EngineHint: "mimo"},
	{ID: "mimo-sgp", Name: "MiMo Token Plan · 新加坡", Kind: "partner", Protocol: "openai-chat", BaseURL: "https://token-plan-sgp.xiaomimimo.com/v1", AuthMode: "auth-token", Hint: "Token Plan Key 需匹配套餐区域地址。", EngineHint: "mimo"},
	{ID: "mimo-ams", Name: "MiMo Token Plan · 欧洲", Kind: "partner", Protocol: "openai-chat", BaseURL: "https://token-plan-ams.xiaomimimo.com/v1", AuthMode: "auth-token", Hint: "Token Plan Key 需匹配套餐区域地址。", EngineHint: "mimo"},
	{ID: "custom", Name: "自定义第三方 API", Kind: "custom", Protocol: "", BaseURL: "", AuthMode: "api-key", Hint: "自有网关或任意兼容服务；按引擎选择协议，同一预设可写入多个引擎。"},
}

// OpenCode native provider document. Unlike MiMo, OpenCode reads provider
// entries from its own config schema and does not use duo-mimo provider IDs.
func opencodeAPIConfig(r EngineSetupRequest) []byte {
	sdk := "@ai-sdk/openai-compatible"
	if r.APIProtocol == "anthropic-messages" {
		sdk = "@ai-sdk/anthropic"
	}
	model := strings.TrimSpace(r.Model)
	models := map[string]any{}
	if model != "" {
		models[model] = map[string]any{"name": model}
	}
	provider := map[string]any{
		"name":                   strings.TrimSpace(r.Name),
		"npm":                    sdk,
		"only_configured_models": true,
		"models":                 models,
		"options": map[string]string{
			"baseURL": strings.TrimRight(r.BaseURL, "/"),
			"apiKey":  r.APIKey,
		},
	}
	out := map[string]any{
		"$schema":           "https://opencode.ai/config.json",
		"provider":          map[string]any{"duo-api": provider},
		"enabled_providers": []string{"duo-api"},
	}
	if model != "" {
		out["model"] = "duo-api/" + model
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	return data
}

// Kimi Code follows Anthropic-style env for Moonshot-compatible relays when
// used through Claude; the native kimi CLI keeps its own auth store. Duo writes
// a settings-style override only when the engine is configured as custom API.
func kimiAPIConfig(r EngineSetupRequest) []byte {
	variables := map[string]string{
		"ANTHROPIC_BASE_URL": strings.TrimRight(r.BaseURL, "/"),
		"ANTHROPIC_MODEL":    strings.TrimSpace(r.Model),
	}
	if r.AuthMode == "auth-token" {
		variables["ANTHROPIC_AUTH_TOKEN"] = r.APIKey
	} else {
		variables["ANTHROPIC_API_KEY"] = r.APIKey
	}
	data, _ := json.MarshalIndent(map[string]any{"env": variables}, "", "  ")
	return data
}

func engineAPIPresetList() []EngineAPIPreset {
	return engineAPIPresets
}
