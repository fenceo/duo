package main

import "strings"

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
