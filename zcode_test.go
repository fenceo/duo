package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestZCodePromptFixture(t *testing.T) {
	if os.Getenv("DUO_TEST_ZCODE_PROMPT") != "1" {
		t.Skip("fixture only")
	}
	payload := map[string]any{
		"sessionId":  "sess_fixture",
		"traceId":    "trace",
		"turnId":     "turn",
		"response":   "pong from fixture",
		"usage":      map[string]any{"inputTokens": 10, "outputTokens": 2, "totalTokens": 12},
		"projection": map[string]any{"status": "idle", "contextUsed": 12, "contextWindow": 200000},
		"eventCount": 1,
	}
	raw, _ := json.Marshal(payload)
	os.Stdout.Write(raw)
}

func TestZCodeModeAndArgs(t *testing.T) {
	if zcodeMode(Task{}) != "build" {
		t.Fatal("default mode")
	}
	if zcodeMode(Task{Mode: &WorkMode{Permission: "read"}}) != "plan" || zcodeMode(Task{Mode: &WorkMode{Permission: "full"}}) != "yolo" {
		t.Fatal("mode mapping")
	}
	args := zcodeArgs(Task{Workspace: "/work", Session: "sess_1"}, "hello")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--prompt hello") || !strings.Contains(joined, "--resume sess_1") || !strings.Contains(joined, "--json") {
		t.Fatal(joined)
	}
	if err := zcodeInvocation(Task{Mode: &WorkMode{Approval: "auto"}}); err == nil {
		t.Fatal("auto review accepted")
	}
	if err := zcodeInvocation(Task{NoTools: true}); err == nil {
		t.Fatal("knowledge summarizer accepted")
	}
}

func TestZCodePromptResultParsing(t *testing.T) {
	got, err := parseZcodePromptResult([]byte(`{"sessionId":"sess_a","response":"ok"}`))
	if err != nil || got.SessionID != "sess_a" {
		t.Fatal(got, err)
	}
	got, err = parseZcodePromptResult([]byte("warning text\n{\"sessionId\":\"sess_b\",\"response\":\"ok\"}"))
	if err != nil || got.SessionID != "sess_b" {
		t.Fatal(got, err)
	}
	if _, err = parseZcodePromptResult([]byte("no json")); err == nil {
		t.Fatal("invalid body accepted")
	}
}
