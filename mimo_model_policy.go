package main

import (
	"encoding/json"
	"errors"
	"strings"
)

// A live API directory can contain models not present in the last saved CLI
// catalog. Register only this selection in the process overlay; do not rewrite
// the user's config, key, provider route, agents, or native session data.
func projectMimoSelectedModel(vars map[string]string, model string) error {
	if !strings.HasPrefix(model, "duo-mimo/") {
		return nil
	}
	var doc map[string]any
	if raw := vars["MIMOCODE_CONFIG_CONTENT"]; raw != "" {
		if json.Unmarshal([]byte(raw), &doc) != nil || doc == nil {
			return errors.New("MiMo 配置覆盖格式无效")
		}
	}
	if doc == nil {
		doc = map[string]any{}
	}
	providers, _ := doc["provider"].(map[string]any)
	if doc["provider"] != nil && providers == nil {
		return errors.New("MiMo provider 覆盖格式无效")
	}
	if providers == nil {
		providers = map[string]any{}
	}
	provider, _ := providers["duo-mimo"].(map[string]any)
	if providers["duo-mimo"] != nil && provider == nil {
		return errors.New("MiMo API provider 覆盖格式无效")
	}
	if provider == nil {
		provider = map[string]any{}
	}
	models, _ := provider["models"].(map[string]any)
	if provider["models"] != nil && models == nil {
		return errors.New("MiMo 模型覆盖格式无效")
	}
	if models == nil {
		models = map[string]any{}
	}
	id := strings.TrimPrefix(model, "duo-mimo/")
	definition, _ := models[id].(map[string]any)
	if definition == nil {
		definition = map[string]any{}
	}
	for key, value := range mimoModelDefinition(id, id, "openai-chat") {
		if _, exists := definition[key]; !exists {
			definition[key] = value
		}
	}
	models[id] = definition
	provider["models"] = models
	providers["duo-mimo"] = provider
	doc["provider"] = providers
	doc["model"] = model
	raw, _ := json.Marshal(doc)
	vars["MIMOCODE_CONFIG_CONTENT"] = string(raw)
	return nil
}

// The official /models endpoint includes ASR/TTS endpoints. They cannot run
// the coding agent's chat/tool protocol. Do not guess about third-party IDs.
func mimoSpeechModel(id string) bool {
	_, name, ok := strings.Cut(strings.ToLower(id), "/")
	if !ok {
		name = strings.ToLower(id)
	}
	return strings.HasPrefix(name, "mimo-") && (strings.Contains(name, "-asr") || strings.Contains(name, "-tts"))
}

func mimoModelDefinition(id, name, protocol string) map[string]any {
	if name == "" {
		name = id
	}
	doc := map[string]any{"name": name}
	if strings.HasPrefix(strings.ToLower(id), "mimo-") && !mimoSpeechModel(id) {
		doc["reasoning"] = true
		doc["tool_call"] = true
		if protocol != "anthropic-messages" {
			doc["interleaved"] = map[string]string{"field": "reasoning_content"}
		}
	}
	return doc
}
