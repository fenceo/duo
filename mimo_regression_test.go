package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestMimoTranscriptReconcilesCurrentTurnOnly(t *testing.T) {
	raw := []byte(`{"info":{"id":"session"},"messages":[
	 {"info":{"id":"old","role":"user"},"parts":[{"type":"text","text":"before"}]},
	 {"info":{"parentID":"old","role":"assistant","providerID":"duo-mimo","modelID":"mimo-v2.5"},"parts":[{"type":"text","text":"old answer"}]},
	 {"info":{"id":"user","role":"user"},"parts":[{"type":"text","text":"current"}]},
	 {"info":{"parentID":"user","role":"assistant","providerID":"duo-mimo","modelID":"mimo-v2.5-pro","tokens":{"input":5,"reasoning":2,"cache":{"read":3}}},"parts":[{"type":"text","text":"tool preamble"}]},
	 {"info":{"parentID":"user","role":"assistant","providerID":"duo-mimo","modelID":"mimo-v2.5-pro","tokens":{"input":7,"output":3}},"parts":[{"type":"reasoning","text":"secret thought"},{"type":"text","text":"ok"},{"type":"text","text":"ignored","ignored":true}]}
	]}`)
	result, usage, err := parseMimoTranscript(raw, "session", "current", "duo-mimo/mimo-v2.5-pro")
	if err != nil || result != "ok" || usage == nil || usage.Total != 20 || usage.Input != 12 || usage.Output != 5 || usage.Cached != 3 {
		t.Fatalf("current final result/usage: %q %#v %v", result, usage, err)
	}
	if _, _, err = parseMimoTranscript(raw, "session", "different input", ""); !errors.Is(err, errMimoTranscriptUnavailable) {
		t.Fatal("old response reused", err)
	}
	if _, _, err = parseMimoTranscript(raw, "foreign", "current", ""); !errors.Is(err, errMimoTranscriptUnavailable) {
		t.Fatal("foreign session reused", err)
	}
	if _, _, err = parseMimoTranscript(raw, "session", "current", "xiaomi/mimo-v2.5-pro"); err == nil || errors.Is(err, errMimoTranscriptUnavailable) {
		t.Fatal("ignored model switch", err)
	}
	broken := strings.Replace(string(raw), `"tokens":{"input":7,"output":3}`, `"error":{"name":"APIError","data":{"statusCode":401,"message":"synthetic-secret","responseBody":"synthetic-secret"}}`, 1)
	if _, _, err = parseMimoTranscript([]byte(broken), "session", "current", "duo-mimo/mimo-v2.5-pro"); err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatal("auth error lost/leaked", err)
	}
}

func TestMimoAPIRoutingAndModelCapabilities(t *testing.T) {
	manager := &EngineSetup{harnessDiscover: func(context.Context, Environment, HarnessAPIRequest) HarnessDiscovery {
		return HarnessDiscovery{Status: "ready", Models: []ModelOption{{ID: "mimo-v2.5-pro"}, {ID: "mimo-v2.5-asr"}, {ID: "mimo-v2.5-tts-voiceclone"}, {ID: "third-party/model"}}}
	}}
	v := EngineAPIRequest{Engine: "mimo", Model: "mimo-v2.5-pro", BaseURL: "https://token-plan-cn.xiaomimimo.com/v1", APIProtocol: "openai-chat", APIKey: "tp-synthetic"}
	discovery := engineAPIDiscover(context.Background(), manager, Environment{}, v)
	if len(discovery.Models) != 2 || discovery.Models[0].Engine != "mimo" || !strings.Contains(discovery.Message, "语音") {
		t.Fatal(discovery)
	}
	v.Models = discovery.Models
	files, err := mergeEngineAPI(map[string][]byte{"duo-api.json": []byte(`{"permission":{"read":"ask"},"provider":{"other":{"models":{"custom":{}}}},"enabled_providers":["other"]}`)}, "mimo-api", v)
	var doc map[string]any
	if err != nil || json.Unmarshal(files["duo-api.json"], &doc) != nil {
		t.Fatal(err)
	}
	provider := doc["provider"].(map[string]any)["duo-mimo"].(map[string]any)
	models := provider["models"].(map[string]any)
	pro := models[v.Model].(map[string]any)
	if pro["reasoning"] != true || pro["interleaved"].(map[string]any)["field"] != "reasoning_content" || len(models) != 2 {
		t.Fatal("model metadata/filter", models)
	}
	if !strings.Contains(string(files["duo-api.json"]), `"other"`) || !strings.Contains(string(files["duo-api.json"]), `"ask"`) {
		t.Fatal("native preferences lost")
	}
	if err = validateMimoAPI(v); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ url, key, protocol, model string }{
		{"https://api.xiaomimimo.com/v1", "tp-synthetic", "openai-chat", "mimo-v2.5"},
		{"https://token-plan-cn.xiaomimimo.com/v1", "sk-synthetic", "openai-chat", "mimo-v2.5"},
		{v.BaseURL, v.APIKey, "openai-chat", "mimo-v2.5-tts"},
		{v.BaseURL, v.APIKey, "anthropic-messages", "mimo-v2.5-pro"},
	} {
		v.BaseURL, v.APIKey, v.APIProtocol, v.Model = test.url, test.key, test.protocol, test.model
		if validateMimoAPI(v) == nil {
			t.Fatal("accepted incompatible config", test.model)
		}
	}
	v.BaseURL, v.APIKey, v.APIProtocol, v.Model = "https://relay.example/v1", "tp-synthetic", "anthropic-messages", "custom-model"
	if err = validateMimoAPI(v); err != nil {
		t.Fatal("blocked third-party service", err)
	}
	modelsList := parseMimoModels("duo-mimo/mimo-v2.5\nduo-mimo/mimo-v2.5-asr\nduo-mimo/mimo-v2.5-tts\nother/custom-tts-model\n")
	if len(modelsList) != 2 {
		t.Fatal(modelsList)
	}
}

func TestMimoACPThoughtAndToolArguments(t *testing.T) {
	s := harnessACPTurn{session: "session", engine: "mimo"}
	var emitted []string
	consume := func(update map[string]any) {
		raw, _ := json.Marshal(map[string]any{"sessionId": "session", "update": update})
		s.consume(codexRPC{Method: "session/update", Params: raw}, func(kind, value string) { emitted = append(emitted, kind+":"+value) })
	}
	consume(map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]string{"type": "text", "text": "plan"}})
	consume(map[string]any{"sessionUpdate": "agent_message_chunk", "messageId": "pre", "content": map[string]string{"type": "text", "text": "preamble"}})
	consume(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "read", "title": "read", "rawInput": map[string]any{}})
	consume(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "read", "status": "in_progress", "rawInput": map[string]string{"file_path": "fixture.txt"}})
	consume(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "read", "status": "in_progress", "rawInput": map[string]string{"file_path": "changed.txt"}})
	if strings.Contains(string(s.tools["read"].RawInput), "changed") {
		t.Fatal("approval details replaced")
	}
	consume(map[string]any{"sessionUpdate": "agent_message_chunk", "messageId": "final", "content": map[string]string{"type": "text", "text": "ok"}})
	if s.result != "ok" || emitted[0] != "thought:plan" || !strings.Contains(strings.Join(emitted, "|"), "fixture.txt") {
		t.Fatal(s.result, emitted)
	}
}
