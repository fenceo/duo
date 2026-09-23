package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func harnessTestTurnFailure(t *testing.T, code, message string) string {
	t.Helper()
	state := harnessTurn{session: "root", receipt: "accepted", active: true}
	raw, err := json.Marshal(map[string]any{
		"sessionId": "root", "event": map[string]any{"type": "turn/end", "data": map[string]any{
			"reason": map[string]any{"kind": "error", "error": map[string]any{"code": code, "message": message}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	state.consume(codexRPC{Method: "session.event", Params: raw}, func(string, string) {})
	if !state.ended {
		t.Fatal("error did not finish the turn")
	}
	return state.failure
}

func TestHarnessMissingCredentialCodeOverridesSensitiveMessage(t *testing.T) {
	failure := harnessTestTurnFailure(t, "MISSING_CREDENTIAL", "invalid model configuration: bearer-secret-do-not-print")
	for _, want := range []string{"MISSING_CREDENTIAL", "执行环境", "原生凭据", "新建任务"} {
		if !strings.Contains(failure, want) {
			t.Errorf("missing credential guidance %q in %q", want, failure)
		}
	}
	if strings.Contains(failure, "bearer-secret") || strings.Contains(failure, "invalid model") {
		t.Fatalf("native credential diagnostic was exposed: %q", failure)
	}
	if got := publicProbeError(errors.New(failure)); !strings.Contains(got, "缺少 AI 凭据") {
		t.Fatalf("structured credential code lost before probe classification: %q", got)
	}
}

type harnessErrorCaptureWriter struct{ bytes.Buffer }

func (*harnessErrorCaptureWriter) Close() error { return nil }

func TestHarnessInitializeUnsupportedReasoningPreservesExplicitChoice(t *testing.T) {
	for _, message := range []string{
		"UNSUPPORTED_REASONING_EFFORT",
		`provider "custom" model "demo" does not support reasoning effort "off"`,
	} {
		w := harnessIOWorker()
		writer := &harnessErrorCaptureWriter{}
		w.in = writer
		raw, _ := json.Marshal(map[string]any{"id": 1, "error": map[string]any{"code": -32603, "message": message}})
		var response codexRPC
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatal(err)
		}
		w.frames <- response
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := w.request(ctx, "initialize", map[string]any{"provider": "custom", "model": "demo", "reasoningEffort": "off"})
		cancel()
		if err == nil || !strings.Contains(err.Error(), harnessUnsupportedReasoningMessage) || !strings.Contains(err.Error(), "UNSUPPORTED_REASONING_EFFORT") {
			t.Fatalf("missing actionable reasoning diagnostic: %v", err)
		}
		var request struct {
			Params struct {
				Effort string `json:"reasoningEffort"`
			} `json:"params"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(writer.Bytes()), &request); err != nil || request.Params.Effort != "off" || w.nextID != 1 {
			t.Fatalf("explicit effort was changed or silently retried: %s, %v", writer.String(), err)
		}
	}
}

func TestHarnessFailureCodeOnlyPreservesShortIdentifiers(t *testing.T) {
	if failure := harnessTestTurnFailure(t, "MODEL_NOT_FOUND", "model not found"); !strings.Contains(failure, "[MODEL_NOT_FOUND]") {
		t.Fatalf("valid provider code was lost: %q", failure)
	}
	for _, invalid := range []string{"Bearer secret-code", "sk-secret-key", "CODE\nSECRET", strings.Repeat("A", 65), "1234", "错误码"} {
		if failure := harnessTestTurnFailure(t, invalid, "safe diagnostic"); strings.Contains(failure, invalid) {
			t.Errorf("untrusted error code echoed: %q", failure)
		}
	}
}
