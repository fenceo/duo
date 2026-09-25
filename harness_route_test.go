package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func harnessSettingsFixture(t *testing.T, raw string) Config {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "settings.yaml"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return Config{Harness: "dsh.cmd", HarnessProvider: defaultHarnessProvider, HarnessModel: defaultHarnessModel, EngineEnv: map[string]string{"DSH_HOME": home}}
}

func TestHarnessSelectedModelKeepsItsProvider(t *testing.T) {
	c := harnessSettingsFixture(t, `llm-pi-ai:
  providers:
    gateway:
      apiKeyEnv: SYNTHETIC_KEY
      baseURL: https://fixture.invalid
      models:
        - id: first-unrelated-model
        - id: "deepseek-v4.1-flash"
        - id: last-model
`)
	for _, selected := range []string{"deepseek-v4.1-flash", "last-model"} {
		p, m, err := harnessRouteForConfig(c, selected)
		if err != nil || p != "gateway" || m != selected {
			t.Fatalf("selected %q became %q/%q (%v)", selected, p, m, err)
		}
	}
	for _, selected := range []string{"", defaultHarnessModel, "unknown-model"} {
		if _, _, err := harnessRouteForConfig(c, selected); err == nil {
			t.Fatalf("silently guessed a route for %q", selected)
		}
	}
	c.HarnessProvider = "explicit-provider"
	p, m, err := harnessRouteForConfig(c, "explicit-model")
	if err != nil || p != "explicit-provider" || m != "explicit-model" {
		t.Fatal("explicit route changed")
	}
}

func TestHarnessSettingsDefaultAndAmbiguity(t *testing.T) {
	c := harnessSettingsFixture(t, `agent-default-model: {provider: second, model: shared}
llm-pi-ai:
  providers:
    first: {models: [{id: shared}, {id: first-only}]}
    second: {models: [{id: shared}, {id: second-only}]}
`)
	p, m, err := harnessRouteForConfig(c, "")
	if err != nil || p != "second" || m != "shared" {
		t.Fatalf("saved default lost: %s/%s %v", p, m, err)
	}
	if _, _, err := harnessRouteForConfig(c, "shared"); err == nil {
		t.Fatal("ambiguous selected model picked arbitrary provider")
	}
	p, m, err = harnessRouteForConfig(c, "second-only")
	if err != nil || p != "second" || m != "second-only" {
		t.Fatal("unique route was not selected")
	}
	c.HarnessProvider = "first"
	p, m, err = harnessRouteForConfig(c, "shared")
	if err != nil || p != "first" || m != "shared" {
		t.Fatal("explicit provider cannot disambiguate")
	}
}

func TestHarnessCatalogCapabilitiesAndSecrets(t *testing.T) {
	c := harnessSettingsFixture(t, `llm-pi-ai:
  providers:
    gateway:
      apiKey: synthetic-secret
      models:
        - {id: opaque-model}
        - id: supported-model
          reasoningEfforts: {off: null, high: high, max: ultra, medium: ignored}
        - {id: plain-model, reasoningEfforts: false}
unrelated:
  providers: {fake: {models: [{id: must-not-leak}]}}
`)
	env := Environment{Type: "windows", Harness: c.Harness, HarnessProvider: defaultHarnessProvider, HarnessModel: defaultHarnessModel}
	list, err := modelsForEngine(context.Background(), env, "deepseek-harness", c.EngineEnv)
	if err != nil || len(list.Models) != 3 || list.Status != "ready" || list.DefaultModel != "" {
		t.Fatalf("catalog: %#v %v", list, err)
	}
	if len(list.Models[0].ReasoningLevels) != 0 || len(list.Models[2].ReasoningLevels) != 0 {
		t.Fatal("invented reasoning capability")
	}
	if !reflect.DeepEqual(list.Models[1].ReasoningLevels, []string{"off", "high", "max"}) {
		t.Fatalf("capabilities lost: %v", list.Models[1])
	}
	raw, _ := json.Marshal(list)
	for _, secret := range []string{"synthetic-secret", "must-not-leak", "deepseek-flash"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("unexpected metadata: %s", secret)
		}
	}
}

func TestHarnessSettingsTargetsAndInvalidYAML(t *testing.T) {
	c := harnessSettingsFixture(t, `llm-pi-ai: {providers: {gateway: {models: [{id: native-model}]}}}`)
	t.Setenv("DSH_HOME", c.EngineEnv["DSH_HOME"])
	c.EngineEnv = nil
	p, m, err := harnessRouteForConfig(c, "native-model")
	if err != nil || p != "gateway" || m != "native-model" {
		t.Fatal("inherited DSH_HOME ignored")
	}
	c.EngineEnv = map[string]string{"DSH_HOME": t.TempDir()}
	p, _, err = harnessRouteForConfig(c, "native-model")
	if err != nil || p != defaultHarnessProvider {
		t.Fatal("profile did not override inherited home")
	}
	c.EngineEnv = nil
	c.Distro = "Ubuntu"
	p, _, err = harnessRouteForConfig(c, "native-model")
	if err != nil || p != defaultHarnessProvider {
		t.Fatal("Windows settings leaked into WSL")
	}
	c.Distro = ""
	c.SSHHost = "example.test"
	p, _, err = harnessRouteForConfig(c, "native-model")
	if err != nil || p != defaultHarnessProvider {
		t.Fatal("Windows settings leaked into SSH")
	}
	c = harnessSettingsFixture(t, "llm-pi-ai: [synthetic-secret: [")
	_, _, err = harnessRouteForConfig(c, "native-model")
	if err == nil || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatal("malformed YAML did not fail safely")
	}
}
