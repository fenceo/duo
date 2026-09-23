package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
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

func TestPublicProbeErrorDistinguishesUnsupportedReasoning(t *testing.T) {
	for _, message := range []string{
		"UNSUPPORTED_REASONING_EFFORT",
		`provider "custom" model "demo" does not support reasoning effort "off"`,
		"Harness initialize [UNSUPPORTED_REASONING_EFFORT]：" + harnessUnsupportedReasoningMessage,
	} {
		if got := publicProbeError(errors.New(message)); got != harnessUnsupportedReasoningMessage {
			t.Errorf("unsupported effort misclassified: %q => %q", message, got)
		}
	}
	if got := publicProbeError(errors.New("MISSING_CREDENTIAL alongside UNSUPPORTED_REASONING_EFFORT")); !strings.Contains(got, "缺少 AI 凭据") {
		t.Fatalf("missing credential precedence changed: %q", got)
	}
}

func TestPublicProbeErrorClassifiesCredentialsBeforeModelErrors(t *testing.T) {
	for _, message := range []string{
		"Harness turn error [MISSING_CREDENTIAL]: invalid model configuration",
		"MISSING_CREDENTIAL: API key not found for model deepseek-flash",
		"missing API key: unsupported model",
	} {
		got := publicProbeError(errors.New(message))
		if !strings.Contains(got, "缺少 AI 凭据") || !strings.Contains(got, "对应执行环境") || strings.Contains(got, "模型不受") {
			t.Errorf("missing credentials misclassified: %q => %q", message, got)
		}
	}
	for _, message := range []string{
		"invalid provider configuration", "invalid model configuration", "configuration file not found",
		"unsupported SDK method", "model invocation failed", "invalid response from model service",
		"MODEL_CONFIGURATION_NOT_FOUND", "invalid_model_configuration",
	} {
		if got := publicProbeError(errors.New(message)); got != "调用失败，请检查该环境的登录状态和网络配置" {
			t.Errorf("generic failure misclassified: %q => %q", message, got)
		}
	}
	for _, message := range []string{
		"MODEL_NOT_FOUND", "UNSUPPORTED_MODEL", "invalid_model", "unsupported model: demo",
		"model not found", "model 'demo' does not exist", "model `demo` is not supported",
	} {
		if got := publicProbeError(errors.New(message)); got != "模型不受当前服务支持" {
			t.Errorf("explicit model error misclassified: %q => %q", message, got)
		}
	}
	if got := publicProbeError(&exec.Error{Name: "dsh", Err: exec.ErrNotFound}); got != "找不到 AI 工具程序" {
		t.Fatalf("executable lookup failure misclassified: %q", got)
	}
}
