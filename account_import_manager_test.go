package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cockpitCodexFixture = `[{"type":"codex","email":"fixture@example.invalid","access_token":"synthetic-access","refresh_token":"synthetic-refresh","id_token":"synthetic-id","account_id":"fixture-account","account_name":"Subscription","account_password":"never-copy","group":"ignored"},{"auth_mode":"apikey","OPENAI_API_KEY":"synthetic-api","api_base_url":"https://relay.example/v1","account_name":"Relay","hooks":{"command":"never-run"}},{"auth_mode":"agentIdentity","agent_identity":{"agent_private_key":"never-expose"}}]`

func TestManagerImportCodexFormats(t *testing.T) {
	items, err := parseManagerImport("codex", cockpitCodexFixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || !items[0].Valid || !items[1].Valid || items[2].Valid {
		t.Fatal("portable export classification")
	}
	auth, _ := accountJSON(items[0].files["auth.json"])
	tokens := auth["tokens"].(map[string]any)
	if tokens["account_id"] != "fixture-account" || tokens["access_token"] != "synthetic-access" || items[0].Name != "Subscription" {
		t.Fatal("subscription credentials lost")
	}
	config, _ := accountTOML(items[1].files["config.toml"])
	provider := config["model_providers"].(map[string]any)["duo_import"].(map[string]any)
	if provider["base_url"] != "https://relay.example/v1" || provider["wire_api"] != "responses" {
		t.Fatal("relay route lost")
	}
	for _, item := range items {
		for _, file := range item.files {
			if strings.Contains(string(file), "never-") {
				t.Fatal("unrelated fields imported")
			}
		}
	}
	preview, _ := json.Marshal(items)
	if strings.Contains(string(preview), "synthetic-") || strings.Contains(string(preview), "never-") {
		t.Fatal("preview leaked credentials")
	}
	for _, raw := range []string{
		`{"email":"legacy","tokens":{"access_token":"a","refresh_token":"r","id_token":"i"},"account_id":"outer"}`,
		`{"accounts":[{"auth_mode":"apikey","openai_api_key":"fixture","api_startup_model":"fixture-model","api_wire_api":"responses"}]}`,
		"\ufeff" + `[{"OPENAI_API_KEY":"fixture"}]`,
	} {
		rows, err := parseManagerImport("codex", raw)
		if err != nil || !rows[0].Valid {
			t.Fatal("legacy/object/envelope rejected", err)
		}
	}
	for _, raw := range []string{
		`{"access_token":"a"}`,
		`{"auth_mode":"apikey","OPENAI_API_KEY":"fixture","api_provider_mode":"custom"}`,
		`{"OPENAI_API_KEY":"fixture","api_provider_id":"relay"}`,
		`{"OPENAI_API_KEY":"fixture","api_wire_api":"chat_completions"}`,
		`{"OPENAI_API_KEY":"fixture","api_instance_access_mode":"gateway"}`,
		`{"OPENAI_API_KEY":"fixture","api_model_mappings":[{"client_model":"x","upstream_model":"y"}]}`,
		`{"OPENAI_API_KEY":"fixture","api_base_url":"https://user:secret@relay.example"}`,
		`{"OPENAI_API_KEY":"fixture","openai_api_key":"different"}`,
		`{"OPENAI_API_KEY":"fixture","api_base_url":42}`,
		`{"auth_mode":"oauth","claude_credentials_raw":{"claudeAiOauth":{"accessToken":"fixture"}}}`,
	} {
		rows, err := parseManagerImport("codex", raw)
		if err != nil || rows[0].Valid {
			t.Fatal("unsupported/ambiguous entry accepted")
		}
	}
}

func TestManagerImportClaudeFormats(t *testing.T) {
	for _, mode := range []string{"oauth", "o_auth", "setup_token"} {
		rows, err := parseManagerImport("claude", `[{"auth_mode":"`+mode+`","email":"fixture","claude_credentials_raw":{"claudeAiOauth":{"accessToken":"synthetic-token","refreshToken":"r","expiresAt":123,"scopes":["user:inference"]}},"claude_config_raw":{"hooks":"never-copy"}}]`)
		if err != nil || !rows[0].Valid {
			t.Fatal("Claude subscription rejected", err)
		}
		if !strings.Contains(string(rows[0].files[".credentials.json"]), "synthetic-token") || strings.Contains(string(rows[0].files["settings.json"]), "never-copy") {
			t.Fatal("credential normalization")
		}
	}
	rows, err := parseManagerImport("claude", `{"accounts":[{"auth_mode":"api_key","api_key":"synthetic-key","api_base_url":"https://relay.example","api_extra_env":{"ANTHROPIC_MODEL":"relay-model","NODE_OPTIONS":"never-run"},"claude_credentials_raw":{"claudeAiOauth":{"accessToken":"stale"}}}]}`)
	if err != nil || !rows[0].Valid {
		t.Fatal("Claude API rejected", err)
	}
	settings, _ := accountJSON(rows[0].files["settings.json"])
	env := settings["env"].(map[string]any)
	if env["ANTHROPIC_AUTH_TOKEN"] != "synthetic-key" || env["ANTHROPIC_BASE_URL"] != "https://relay.example" || env["ANTHROPIC_MODEL"] != "relay-model" || env["NODE_OPTIONS"] != nil || len(rows[0].files[".credentials.json"]) > 0 {
		t.Fatal("API route/extra env normalization")
	}
	for _, raw := range []string{
		`{"auth_mode":"desktop_gateway"}`,
		`{"auth_mode":"api_key","api_key":"fixture","api_extra_env":{"CLAUDE_CODE_USE_BEDROCK":"1"}}`,
		`{"auth_mode":"api_key","api_key":"fixture","api_extra_env":{"ANTHROPIC_BASE_URL":"https://other.example"}}`,
		`{"auth_mode":"api_key","api_key":"fixture","api_key_field":"NODE_OPTIONS"}`,
		`{"auth_mode":"oauth","api_key":"fixture"}`,
		`{"auth_mode":"api_key","api_key":"fixture","api_provider_id":"relay"}`,
		`{"tokens":{"access_token":"a","refresh_token":"r"}}`,
	} {
		rows, err := parseManagerImport("claude", raw)
		if err != nil || rows[0].Valid {
			t.Fatal("unsupported Claude entry accepted")
		}
	}
}

func TestManagerImportBoundsAndMalformed(t *testing.T) {
	for _, raw := range []string{"null", "[]", "not-json", `{"accounts":{}}`, `{"accounts":[],"type":"sub2api-data"}`, strings.Repeat(" ", managerImportLimit+1), "[" + strings.Repeat(`{},`, 50) + "{}]"} {
		if _, err := parseManagerImport("codex", raw); err == nil {
			t.Fatal("invalid container accepted")
		}
	}
	rows, err := parseManagerImport("codex", `[null,42,"text",{"OPENAI_API_KEY":"fixture","account_name":"<script>fixture</script>"}]`)
	if err != nil || len(rows) != 4 || rows[0].Valid || rows[1].Valid || rows[2].Valid || !rows[3].Valid {
		t.Fatal("mixed entry classification")
	}
}

func TestManagerImportBatchAtomicAndRetry(t *testing.T) {
	a, env := engineSetupFixture(t)
	r := ManagerImportRequest{ID: "fixture", Engine: "codex", EnvironmentID: env.ID, JSON: cockpitCodexFixture, Indexes: []int{1, 0}}
	r.Indexes = []int{0, 2}
	if _, err := a.importManagerAccounts(context.Background(), r); err == nil || len(a.store.engineProfiles()) != 0 {
		t.Fatal("invalid selection partially saved")
	}
	r.Indexes = []int{0, 0}
	if _, err := a.importManagerAccounts(context.Background(), r); err == nil {
		t.Fatal("duplicate indexes accepted")
	}
	r.Indexes = []int{1, 0}
	profiles, err := a.importManagerAccounts(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 || profiles[0].Name != "Subscription" || a.store.activeEngineProfile(env.ID, "codex") != "" {
		t.Fatal("batch selection/default")
	}
	// A retry cannot overwrite tokens that the CLI has since refreshed.
	updated := []byte(`{"OPENAI_API_KEY":"synthetic-refreshed"}`)
	if err = os.WriteFile(filepath.Join(profiles[0].Reference, "auth.json"), updated, 0600); err != nil {
		t.Fatal(err)
	}
	r.Indexes = []int{0, 1}
	again, err := a.importManagerAccounts(context.Background(), r)
	if err != nil || len(again) != 2 || len(a.store.engineProfiles()) != 2 {
		t.Fatal("retry duplicated batch", err)
	}
	saved, _ := os.ReadFile(filepath.Join(profiles[0].Reference, "auth.json"))
	if string(saved) != string(updated) {
		t.Fatal("retry overwrote refreshed account")
	}
	r.Indexes = []int{1}
	if _, err = a.importManagerAccounts(context.Background(), r); err == nil {
		t.Fatal("changed request accepted")
	}
	// A pre-existing directory at a later index must survive; earlier staged
	// directories and the database must roll back together.
	r.ID = "collision"
	r.Indexes = []int{0, 1}
	blocked := filepath.Join(a.store.directory, "engine-accounts", "batch-collision-1")
	if err = os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(blocked, "keep"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = a.importManagerAccounts(context.Background(), r); err == nil {
		t.Fatal("collision accepted")
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(blocked), "batch-collision-0")); !os.IsNotExist(err) {
		t.Fatal("staged directory not rolled back")
	}
	if _, err = os.Stat(filepath.Join(blocked, "keep")); err != nil || len(a.store.engineProfiles()) != 2 {
		t.Fatal("existing data lost")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.ID = "cancelled"
	if _, err = a.importManagerAccounts(ctx, r); err == nil {
		t.Fatal("cancelled import saved")
	}
	all := a.store.engineProfiles()
	for i := 0; i < 48; i++ {
		p := profiles[0]
		p.ID = "limit-" + strings.Repeat("x", i+1)
		all = append(all, p)
	}
	if err = a.store.saveEngineProfiles(all); err != nil {
		t.Fatal(err)
	}
	r.ID = "overlimit"
	if _, err = a.importManagerAccounts(context.Background(), r); err == nil || len(a.store.engineProfiles()) != 50 {
		t.Fatal("profile cap not enforced before saving")
	}
}

func TestManagerImportRoutesRedactionAndBusy(t *testing.T) {
	a, env := engineSetupFixture(t)
	do := toolsClient(t, a)
	r := ManagerImportRequest{ID: "routes", Engine: "codex", EnvironmentID: env.ID, JSON: cockpitCodexFixture, Indexes: []int{1}}
	for _, path := range []string{"/api/account-import/preview", "/api/account-import/batch"} {
		response := do(path, "POST", r, 200)
		if strings.Contains(string(response), "synthetic-") || strings.Contains(string(response), "never-") {
			t.Fatal("response leaked credentials")
		}
	}
	if len(a.store.engineProfiles()) != 1 {
		t.Fatal("unselected entries saved")
	}
	a.engineSetup.mu.Lock()
	a.engineSetup.active = true
	a.engineSetup.mu.Unlock()
	do("/api/account-import/batch", "POST", r, 409)
	a.engineSetup.mu.Lock()
	a.engineSetup.active = false
	a.engineSetup.mu.Unlock()
	r.JSON = `{"OPENAI_API_KEY":"synthetic-secret"`
	response := do("/api/account-import/preview", "POST", r, 400)
	if strings.Contains(string(response), "synthetic-secret") {
		t.Fatal("parse error leaked file")
	}
}
