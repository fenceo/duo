package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMergeCodexAccountKeepsOnlyNativeRoutingAndLogin(t *testing.T) {
	source := map[string][]byte{
		"auth.json":   []byte("{\"tokens\":{\"access_token\":\"synthetic\"}}"),
		"config.toml": []byte("model = \"relay-model\"\nmodel_provider = \"relay\"\n[model_providers.relay]\nname = \"Relay\"\nbase_url = \"https://relay.example/v1\"\nwire_api = \"responses\"\n"),
	}
	target := map[string][]byte{
		"auth.json":   []byte("{\"tokens\":{\"access_token\":\"old\"}}"),
		"config.toml": []byte("model = \"old\"\n[model_providers.old]\nbase_url=\"https://old.example\"\n"),
	}
	next, err := mergeAccountFiles("codex", source, target)
	if err != nil {
		t.Fatal(err)
	}
	if string(next["auth.json"]) != string(source["auth.json"]) || !strings.Contains(string(next["config.toml"]), "relay.example") || !strings.Contains(string(next["config.toml"]), "relay-model") {
		t.Fatalf("routing or auth was not projected: %s", next["config.toml"])
	}
	if !strings.Contains(string(next["config.toml"]), "old.example") {
		t.Fatal("unselected provider settings should remain available")
	}
}

func TestMergeClaudeAccountProjectsRelayEnvironment(t *testing.T) {
	source := map[string][]byte{"settings.json": []byte("{\"env\":{\"ANTHROPIC_BASE_URL\":\"https://relay.example\",\"ANTHROPIC_AUTH_TOKEN\":\"synthetic\"}}"), ".credentials.json": nil}
	target := map[string][]byte{"settings.json": []byte("{\"env\":{\"ANTHROPIC_AUTH_TOKEN\":\"old\"}}"), ".credentials.json": []byte("{\"claudeAiOauth\":{\"accessToken\":\"old\"}}")}
	next, err := mergeAccountFiles("claude", source, target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(next["settings.json"]), "relay.example") || next[".credentials.json"] != nil {
		t.Fatal("Claude relay settings were not projected safely")
	}
	var parsed map[string]any
	if json.Unmarshal(next["settings.json"], &parsed) != nil {
		t.Fatal("invalid merged settings")
	}
}

func TestAccountSwitchBackToSubscriptionPreservesTools(t *testing.T) {
	source := map[string][]byte{"auth.json": []byte(`{"tokens":{"access_token":"fixture"}}`)}
	target := map[string][]byte{"config.toml": []byte("model_provider='relay'\nprofile='stale'\n[mcp_servers.local]\ncommand='fixture-tool'\n[model_providers.openai]\nbase_url='https://stale.example'\n")}
	next, err := mergeAccountFiles("codex", source, target)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := accountTOML(next["config.toml"])
	if v["model_provider"] != nil || v["profile"] != nil || v["mcp_servers"] == nil || strings.Contains(string(next["config.toml"]), "stale.example") {
		t.Fatal("official account retained relay routing or lost tools")
	}
	claude := map[string][]byte{".credentials.json": []byte(`{"claudeAiOauth":{"accessToken":"fixture"}}`)}
	old := map[string][]byte{"settings.json": []byte(`{"hooks":{"fixture":[]},"env":{"ANTHROPIC_BASE_URL":"https://old.example","ANTHROPIC_AUTH_TOKEN":"old","OTHER":"preserve"},"apiKeyHelper":"old-helper"}`)}
	merged, err := mergeAccountFiles("claude", claude, old)
	if err != nil {
		t.Fatal(err)
	}
	v, _ = accountJSON(merged["settings.json"])
	env := v["env"].(map[string]any)
	if v["hooks"] == nil || v["apiKeyHelper"] != nil || env["ANTHROPIC_BASE_URL"] != "" || env["ANTHROPIC_AUTH_TOKEN"] != "" || env["OTHER"] != "preserve" {
		t.Fatal("subscription routing not reset or unrelated settings lost")
	}
	for _, bad := range []map[string][]byte{{"auth.json": []byte(`{}`)}, {"auth.json": source["auth.json"], "config.toml": []byte("broken = [")}} {
		if _, err := mergeAccountFiles("codex", bad, target); err == nil {
			t.Fatal("malformed source accepted")
		}
	}
}
