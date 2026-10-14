package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCodexRetryDiagnosticsNativeNotifications(t *testing.T) {
	for _, scenario := range []string{"retry-success", "retry-failed", "retry-no-cause", "retry-foreign", "retry-other-failure"} {
		t.Run(scenario, func(t *testing.T) {
			config := codexFixtureConfig(t, scenario)
			task := Task{Workspace: t.TempDir(), Session: "retained-retry-thread"}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			var mu sync.Mutex
			var retries, failures []string
			session, result, err := runCodexAppServer(ctx, config, task, "one synthetic turn only", func(kind, text string) {
				mu.Lock()
				defer mu.Unlock()
				if strings.Contains(text, "private-") || strings.Contains(text, "foreign retry") {
					t.Errorf("private or unrelated diagnostic leaked: %s", text)
				}
				if kind == "progress" && strings.HasPrefix(text, "Codex 正在重试：") {
					retries = append(retries, text)
				}
				if kind == "error" {
					failures = append(failures, text)
				}
			})
			if session != task.Session {
				t.Fatal("diagnostics replaced the native session", session)
			}
			mu.Lock()
			defer mu.Unlock()
			if scenario == "retry-success" {
				if err != nil || !json.Valid([]byte(result)) || len(failures) != 0 || len(retries) != 2 {
					t.Fatalf("retry notifications must not fail a successful turn: err=%v retries=%v failures=%v", err, retries, failures)
				}
				return
			}
			if err == nil || result != "" || len(failures) != 1 || strings.Contains(err.Error(), "private-") {
				t.Fatalf("expected only the native terminal failure: err=%v failures=%v", err, failures)
			}
			if scenario == "retry-failed" {
				if len(retries) != 2 || !strings.Contains(retries[0], "服务繁忙") || !strings.Contains(retries[1], "HTTP 429") || !strings.Contains(err.Error(), "本轮原生重试曾报告：服务繁忙；上游返回 HTTP 429") {
					t.Fatalf("lost the native retry evidence: %v; %v", err, retries)
				}
			} else if strings.Contains(err.Error(), "本轮原生重试曾报告") {
				t.Fatalf("invented a cause, mixed threads, or obscured another terminal error: %v", err)
			}
			if scenario == "retry-foreign" && len(retries) != 0 {
				t.Fatal("foreign or unidentified retries were displayed", retries)
			}
		})
	}
}

func TestCodexRetryClassificationUsesOnlyBoundedPublicSignals(t *testing.T) {
	identity := func(value string) string { return value }
	private := "private-token private-url private-request"
	tests := []struct {
		info, details, want string
	}{
		{`"serverOverloaded"`, private, "服务繁忙"},
		{`"rateLimitExceeded"`, private, "请求受到限流"},
		{`{"responseStreamDisconnected":{"httpStatusCode":429}}`, private, "HTTP 429"},
		{`null`, "The model is experiencing high demand " + private, "服务繁忙"},
		{`null`, "unexpected status: 429 Too Many Requests " + private, "HTTP 429"},
		{`{"futureError":{"httpStatusCode":429}}`, private, ""},
		{`null`, strings.Repeat("x", 16384) + " high demand HTTP 429", ""},
		{`{"responseStreamDisconnected":{"httpStatusCode":200}}`, private, ""},
	}
	for _, test := range tests {
		diagnostic := codexRetryDiagnostics{}
		got := diagnostic.progress(codexTurnError{Message: "Reconnecting... 1/5", CodexErrorInfo: json.RawMessage(test.info), AdditionalDetails: &test.details}, identity)
		if strings.Contains(got, "private-") || (test.want != "" && !strings.Contains(got, test.want)) || (test.want == "" && len(diagnostic.causes) != 0) {
			t.Fatalf("unsafe or inaccurate diagnostic %q for %s", got, test.info)
		}
	}
	diagnostic := codexRetryDiagnostics{}
	for status := 400; status < 600; status++ {
		diagnostic.progress(codexTurnError{Message: "Reconnecting", CodexErrorInfo: json.RawMessage(fmt.Sprintf(`{"httpConnectionFailed":{"httpStatusCode":%d}}`, status))}, identity)
	}
	if len(diagnostic.causes) != codexRetryMaxCauses {
		t.Fatal("retry summary must remain bounded", diagnostic.causes)
	}
	text := diagnostic.progress(codexTurnError{Message: strings.Repeat("界", 10000)}, identity)
	if len(text) > 4096 {
		t.Fatal("retry display was not bounded", len(text))
	}
}
