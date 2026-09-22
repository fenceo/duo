package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestProbeModelsUsesReadOnlyClaudeCall(t *testing.T) {
	t.Setenv("JIANZUO_TEST_CLAUDE", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	result, err := probeModels(context.Background(), Config{Claude: exe}, Environment{Type: "windows"}, "claude", workspace, []string{"sonnet", "opus", "sonnet"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 2 || result.Results[0].Status != "available" || result.Results[1].Status != "available" {
		t.Fatalf("unexpected probe result: %#v", result)
	}
}

func TestProbeModelsRejectsUnsafeInputAndDoesNotExposeErrors(t *testing.T) {
	if _, err := normalizeProbeModels([]string{"bad\nmodel"}); err == nil {
		t.Fatal("newline model was accepted")
	}
	tooMany := make([]string, modelProbeMaxModels+1)
	for i := range tooMany {
		tooMany[i] = "model-" + string(rune('a'+i))
	}
	if _, err := normalizeProbeModels(tooMany); err == nil {
		t.Fatal("too many models were accepted")
	}
	message := publicProbeError(errors.New("authorization failed: bearer-secret-should-not-leak"))
	if strings.Contains(message, "bearer-secret-should-not-leak") {
		t.Fatal(message)
	}
}

func TestModelProbeAPI(t *testing.T) {
	t.Setenv("JIANZUO_TEST_CLAUDE", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a := fixture(t, &fakeRunner{})
	config := a.config.get()
	environmentID := ""
	workspace := t.TempDir()
	for i := range config.Environments {
		if config.Environments[i].Type == "windows" {
			environmentID = config.Environments[i].ID
			config.Environments[i].Claude = exe
			config.Environments[i].Workspaces = []string{workspace}
			break
		}
	}
	if environmentID == "" {
		t.Fatal("fixture did not create a Windows environment")
	}
	if err := a.config.save(config); err != nil {
		t.Fatal(err)
	}
	raw := toolsClient(t, a)("/api/environments/"+environmentID+"/models/test", "POST", map[string]any{
		"engine":    "claude",
		"workspace": workspace,
		"models":    []string{"sonnet", "opus"},
	}, 200)
	var response ModelProbeResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response.Engine != "claude" || len(response.Results) != 2 {
		t.Fatalf("unexpected API response: %#v", response)
	}
	for _, result := range response.Results {
		if result.Status != "available" || result.Message != "调用成功" {
			t.Fatalf("unexpected model result: %#v", result)
		}
	}
}
