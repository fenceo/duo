package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func syntheticAccountJWT(email string) string {
	raw, _ := json.Marshal(map[string]any{"email": email, "name": "Fixture Name", "https://api.openai.com/auth": map[string]any{"chatgpt_plan_type": "pro", "chatgpt_account_id": "claim-workspace", "chatgpt_subscription_active_until": "2027-01-01T00:00:00Z"}})
	return "e30." + base64.RawURLEncoding.EncodeToString(raw) + ".synthetic"
}
func syntheticCodexFiles(email string) map[string][]byte {
	raw, _ := json.Marshal(map[string]any{"tokens": map[string]string{"access_token": "secret-access", "refresh_token": "secret-refresh", "id_token": syntheticAccountJWT(email), "account_id": "selected-workspace"}})
	return map[string][]byte{"auth.json": raw, "config.toml": []byte("")}
}
func TestCodexAccountIdentityClaimsAndRedaction(t *testing.T) {
	info := codexInfoFromFiles(syntheticCodexFiles("fixture@example.invalid"))
	if info.Email != "fixture@example.invalid" || info.DisplayName != "Fixture Name" || info.Plan != "pro" || info.AccountID != "selected-workspace" || info.State != "local" || info.SubscriptionUntil == "" {
		t.Fatalf("identity missing: %#v", info)
	}
	raw, _ := json.Marshal(info)
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "synthetic") {
		t.Fatal("credential leaked")
	}
	api := codexInfoFromFiles(map[string][]byte{"auth.json": []byte(`{"OPENAI_API_KEY":"secret-api"}`), "config.toml": []byte("model_provider='relay'\n[model_providers.relay]\nname='Fixture relay'\nbase_url='https://user:password@relay.invalid/v1?key=secret#fragment'\n")})
	if api.AuthType != "apikey" || api.BaseURL != "https://relay.invalid/v1" || api.Provider != "Fixture relay" || api.State != "api" {
		t.Fatal(api)
	}
	for _, token := range []string{"garbage", "a.%%%%.b", "a.e30.b"} {
		files := syntheticCodexFiles("")
		var a map[string]any
		json.Unmarshal(files["auth.json"], &a)
		a["tokens"].(map[string]any)["id_token"] = token
		files["auth.json"], _ = json.Marshal(a)
		if got := codexInfoFromFiles(files); got.Email != "" || got.Plan != "" {
			t.Fatal("invented identity")
		}
	}
}
func TestCodexAccountQuotaWindows(t *testing.T) {
	raw := json.RawMessage(`{"rateLimits":{"primary":{"usedPercent":99,"windowDurationMins":1}},"rateLimitsByLimitId":{"codex":{"planType":"pro","primary":{"usedPercent":0,"windowDurationMins":300,"resetsAt":1900000000},"secondary":{"usedPercent":77,"windowDurationMins":10080,"resetsAt":1900600000}},"spark":{"primary":{"usedPercent":12,"windowDurationMins":60}},"invalid":{"primary":{"windowDurationMins":60}}}}`)
	limits, plan, err := parseCodexQuota(raw)
	if err != nil || plan != "pro" || len(limits) != 3 || limits[0].UsedPercent != 0 || limits[1].WindowMinutes != 10080 {
		t.Fatalf("bad windows: %#v %s %v", limits, plan, err)
	}
	if _, _, err = parseCodexQuota(json.RawMessage(`{"rateLimits":{"primary":{"windowDurationMins":300}}}`)); err == nil {
		t.Fatal("missing percentage became zero")
	}
}

// A fake native executable refuses model calls, login/logout and extra requests.
func init() {
	if os.Getenv("DUO_TEST_ACCOUNT_INFO") != "1" || len(os.Args) < 2 || os.Args[len(os.Args)-1] != "app-server" {
		return
	}
	decoder, encoder := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	read := func(method string) codexRPC {
		var msg codexRPC
		if decoder.Decode(&msg) != nil || msg.Method != method {
			os.Exit(41)
		}
		return msg
	}
	reply := func(msg codexRPC, value any) { encoder.Encode(map[string]any{"id": msg.ID, "result": value}) }
	scenario := os.Getenv("DUO_TEST_ACCOUNT_INFO_SCENARIO")
	reply(read("initialize"), map[string]any{})
	read("initialized")
	msg := read("account/read")
	if os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("CODEX_HOME") == "" {
		os.Exit(42)
	}
	var params map[string]bool
	json.Unmarshal(msg.Params, &params)
	if !params["refreshToken"] {
		os.Exit(43)
	}
	if scenario == "unsupported" {
		encoder.Encode(map[string]any{"id": msg.ID, "error": map[string]any{"code": -32601, "message": "secret-error"}})
		os.Exit(0)
	}
	if scenario == "interaction" {
		encoder.Encode(map[string]any{"id": "unexpected", "method": "item/tool/call", "params": map[string]any{}})
		os.Exit(0)
	}
	if scenario == "logout" {
		reply(msg, map[string]any{"account": nil, "requiresOpenaiAuth": true})
	} else {
		email := "fixture@example.invalid"
		if scenario == "mismatch" {
			email = "other@example.invalid"
		}
		reply(msg, map[string]any{"account": map[string]string{"type": "chatgpt", "email": email, "planType": "pro"}, "requiresOpenaiAuth": true})
		if scenario != "mismatch" {
			msg = read("account/rateLimits/read")
			if scenario == "quota-error" {
				encoder.Encode(map[string]any{"id": msg.ID, "error": map[string]any{"code": -32000, "message": "secret-error"}})
			} else {
				reply(msg, map[string]any{"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": 20, "windowDurationMins": 300, "resetsAt": 1900000000}}})
			}
		}
	}
	var extra codexRPC
	if decoder.Decode(&extra) != io.EOF {
		os.Exit(44)
	}
	os.Exit(0)
}
func TestCodexAccountNativeMetadataOnly(t *testing.T) {
	t.Setenv("DUO_TEST_ACCOUNT_INFO", "1")
	t.Setenv("OPENAI_API_KEY", "foreign-secret")
	binary, _ := os.Executable()
	env := Environment{Type: "windows", Codex: binary, Workspaces: []string{"Z:/must-not-be-used"}}
	for _, scenario := range []string{"ready", "unsupported", "interaction", "logout", "mismatch", "quota-error"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("DUO_TEST_ACCOUNT_INFO_SCENARIO", scenario)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			info, err := nativeCodexAccountInfo(ctx, env, map[string]string{"CODEX_HOME": t.TempDir()}, codexInfoFromFiles(syntheticCodexFiles("fixture@example.invalid")))
			if scenario == "ready" {
				if err != nil || info.State != "ready" || len(info.Limits) != 1 {
					t.Fatal(info, err)
				}
			} else if scenario == "logout" {
				if err != nil || info.State != "login_required" || info.Email != "" {
					t.Fatal(info, err)
				}
			} else if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unredacted or missing error: %v", err)
			}
		})
	}
}
func TestCodexAccountCacheRouteAndHTTP(t *testing.T) {
	a, env := engineSetupFixture(t)
	do := toolsClient(t, a)
	dir := t.TempDir()
	files := syntheticCodexFiles("fixture@example.invalid")
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	profile := EngineCredentialProfile{ID: "account", Name: "Custom label", Engine: "codex", EnvironmentID: env.ID, Kind: "codex_home", Reference: dir, Created: 1, Updated: 1}
	a.store.saveEngineProfiles([]EngineCredentialProfile{profile})
	a.store.activateEngineProfile(env.ID, "codex", profile.ID)
	do("/api/engine-profiles/account/account/refresh", "POST", map[string]bool{"online": false}, 200)
	info := a.store.cachedCodexInfo(profile, env)
	if info.Email != "fixture@example.invalid" {
		t.Fatal("HTTP failed to read actual identity")
	}
	if a.store.engineProfiles()[0].Name != "Custom label" || a.store.activeEngineProfile(env.ID, "codex") != "account" {
		t.Fatal("read modified profile or default")
	}
	if strings.Contains(a.store.setting("codex_account_info:account"), "secret-") {
		t.Fatal("stored credentials in metadata")
	}
	profile.Reference = t.TempDir()
	if a.store.cachedCodexInfo(profile, env).Email != "" {
		t.Fatal("route change reused other account cache")
	}
	do("/api/engine-profiles/account/organization", "PATCH", map[string]any{"note": "Fixture note", "tags": []string{"work", "work", "backup"}}, 200)
	organization := a.store.accountOrganization("account")
	if organization.Note != "Fixture note" || len(organization.Tags) != 2 {
		t.Fatal(organization)
	}
	do("/api/engine-profiles/account", "DELETE", nil, 200)
	if a.store.setting("codex_account_info:account") != "" || a.store.setting("account_organization:account") != "" {
		t.Fatal("removed account retained display metadata")
	}
	do("/api/engine-profiles/account/account/refresh", "POST", map[string]bool{"online": false}, 404)
}

func TestCodexAPIInfoNeverStartsNativeProcess(t *testing.T) {
	a, env := engineSetupFixture(t)
	do := toolsClient(t, a)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"OPENAI_API_KEY":"synthetic-api"}`), 0600); err != nil {
		t.Fatal(err)
	}
	p := EngineCredentialProfile{ID: "api", Name: "Relay", Engine: "codex", EnvironmentID: env.ID, Kind: "codex_home", Reference: dir, Created: 1, Updated: 1}
	a.store.saveEngineProfiles([]EngineCredentialProfile{p})
	do("/api/engine-profiles/api/account/refresh", "POST", map[string]bool{"online": true}, 200)
	if info := a.store.cachedCodexInfo(p, env); info.State != "api" || info.QuotaUpdated != 0 {
		t.Fatal(info)
	}
	a.engineSetup.mu.Lock()
	active := a.engineSetup.active
	a.engineSetup.mu.Unlock()
	if active {
		t.Fatal("refresh left account operations locked")
	}
}
