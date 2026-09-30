package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccountImportCodexRoutingAndSanitization(t *testing.T) {
	r := AccountImportRequest{Engine: "codex", JSON: `{"OPENAI_API_KEY":"synthetic-secret","extra":"not-account-data"}`, ConfigTOML: "model='relay-model'\nmodel_provider='relay'\n[mcp_servers.untrusted]\ncommand='never-run'\n[model_providers.relay]\nname='Relay'\nbase_url='https://relay.example/v1'\nwire_api='responses'\nrequires_openai_auth=true\n"}
	files, err := parseAccountImport(r)
	if err != nil {
		t.Fatal(err)
	}
	auth, _ := accountJSON(files["auth.json"])
	config, _ := accountTOML(files["config.toml"])
	if auth["OPENAI_API_KEY"] != "synthetic-secret" || auth["extra"] != nil || config["model"] != "relay-model" || config["mcp_servers"] != nil {
		t.Fatal("account routing lost or non-account configuration imported")
	}
	provider := config["model_providers"].(map[string]any)["relay"].(map[string]any)
	if provider["base_url"] != "https://relay.example/v1" || config["cli_auth_credentials_store"] != "file" {
		t.Fatal("relay/file credentials not retained")
	}
	for _, extra := range []string{"env_key='SECRET'", "env_http_headers={x='SECRET'}", "auth={command='never-run'}", "wire_api='chat'"} {
		bad := r
		bad.ConfigTOML = "model_provider='relay'\n[model_providers.relay]\n" + extra
		if _, err := parseAccountImport(bad); err == nil {
			t.Fatal("unsupported credential route accepted")
		}
	}
}

func TestAccountImportClaudeSubscriptionAndAPI(t *testing.T) {
	oauth := AccountImportRequest{Engine: "claude", JSON: `{"claudeAiOauth":{"accessToken":"fixture","refreshToken":"refresh","expiresAt":12345,"scopes":["user:inference"],"extra":"ignore"},"extra":"ignore"}`}
	files, err := parseAccountImport(oauth)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(files[".credentials.json"]), "extra") {
		t.Fatal("unknown credentials fields were retained")
	}
	r := AccountImportRequest{Engine: "claude", JSON: `{"env":{"ANTHROPIC_AUTH_TOKEN":"fixture","ANTHROPIC_BASE_URL":"https://relay.example","NODE_OPTIONS":"--require never-run"},"model":"fixture-model","hooks":{"SessionStart":[{"command":"never-run"}]},"apiKeyHelper":"never-run"}`}
	files, err = parseAccountImport(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(files["settings.json"]), "never-run") || strings.Contains(string(files["settings.json"]), "hooks") || len(files[".credentials.json"]) != 0 {
		t.Fatal("executable config or stale auth imported")
	}
	if !strings.Contains(string(files["settings.json"]), "https://relay.example") {
		t.Fatal("API route lost")
	}
	for _, raw := range []string{`{"claudeAiOauth":{}}`, `{"unknown":"synthetic-secret"}`, `{"env":{"ANTHROPIC_AUTH_TOKEN":"fixture","CLAUDE_CODE_USE_BEDROCK":"1"}}`} {
		r.JSON = raw
		if _, err = parseAccountImport(r); err == nil {
			t.Fatal("invalid credentials accepted")
		} else if strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatal("secret in error")
		}
	}
}

func TestAccountImportCopiesNativeAndRetriesWithoutOverwriting(t *testing.T) {
	a, env := engineSetupFixture(t)
	source := t.TempDir()
	t.Setenv("CODEX_HOME", source)
	original := []byte(`{"tokens":{"access_token":"synthetic-native","refresh_token":"fixture-refresh","id_token":"fixture-id"}}`)
	if err := os.WriteFile(filepath.Join(source, "auth.json"), original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "config.toml"), []byte("model='fixture-model'\n[mcp_servers.source]\ncommand='do-not-copy'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r := AccountImportRequest{ID: "fixture-import", Name: "Native copy", Engine: "codex", EnvironmentID: env.ID, Source: "native", SourceEnvironmentID: env.ID}
	profile, err := a.importAccount(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Reference == source || profile.Kind != "codex_home" || a.store.activeEngineProfile(env.ID, "codex") != "" {
		t.Fatal("source reused or profile activated")
	}
	after, _ := os.ReadFile(filepath.Join(source, "auth.json"))
	if string(after) != string(original) {
		t.Fatal("source auth changed")
	}
	imported, _ := os.ReadFile(filepath.Join(profile.Reference, "config.toml"))
	if strings.Contains(string(imported), "do-not-copy") {
		t.Fatal("source tools copied")
	}
	// Retrying the same operation returns its snapshot, even if source changes.
	if err := os.WriteFile(filepath.Join(source, "auth.json"), []byte(`{"OPENAI_API_KEY":"changed-source"}`), 0600); err != nil {
		t.Fatal(err)
	}
	again, err := a.importAccount(context.Background(), r)
	if err != nil || again.ID != profile.ID || len(a.store.engineProfiles()) != 1 {
		t.Fatal("retry duplicated import")
	}
	saved, _ := os.ReadFile(filepath.Join(profile.Reference, "auth.json"))
	if strings.Contains(string(saved), "changed-source") {
		t.Fatal("retry overwrote original snapshot")
	}
	r.Name = "different request"
	if _, err = a.importAccount(context.Background(), r); err == nil {
		t.Fatal("same id accepted different payload")
	}
}

func TestAccountImportRouteRedactionAndBusy(t *testing.T) {
	a, env := engineSetupFixture(t)
	do := toolsClient(t, a)
	req := AccountImportRequest{ID: "http-import", Name: "JSON fixture", Engine: "codex", EnvironmentID: env.ID, Source: "json", JSON: `{"OPENAI_API_KEY":"synthetic-secret"}`}
	response := do("/api/account-import", "POST", req, 200)
	if strings.Contains(string(response), "synthetic-secret") || strings.Contains(a.store.setting(engineProfilesSetting), "synthetic-secret") {
		t.Fatal("secret exposed in response/database")
	}
	var profile EngineCredentialProfile
	if json.Unmarshal(response, &profile) != nil || profile.ID != "imported-http-import" {
		t.Fatal("missing profile")
	}
	a.engineSetup.mu.Lock()
	a.engineSetup.active = true
	a.engineSetup.mu.Unlock()
	do("/api/account-import", "POST", req, 409)
	a.engineSetup.mu.Lock()
	a.engineSetup.active = false
	a.engineSetup.mu.Unlock()
	req.ID = "bad-import"
	req.JSON = `{"OPENAI_API_KEY":"synthetic-secret"`
	response = do("/api/account-import", "POST", req, 400)
	if strings.Contains(string(response), "synthetic-secret") {
		t.Fatal("parse error leaked content")
	}
	if len(a.store.engineProfiles()) != 1 {
		t.Fatal("failed import created a profile")
	}
}
