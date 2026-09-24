package main

import (
	"strings"
	"testing"
)

func TestRedactContinuation(t *testing.T) {
	input := "api_key=abc123 password: hunter2 Authorization: Bearer abcdefghijklmnop sk-abcdefghijklmnopqr\n-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----"
	got := redactContinuation(input)
	for _, secret := range []string{"abc123", "hunter2", "abcdefghijklmnop", "sk-abcdefghijklmnopqr", "BEGIN PRIVATE KEY", "secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redacted output contains %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "[已隐藏]") || !strings.Contains(got, "[已隐藏的私钥]") {
		t.Fatalf("redaction markers missing: %s", got)
	}
}

func TestClipContinuation(t *testing.T) {
	got, truncated := clipContinuation("一二三四五六", 4)
	if !truncated || !strings.HasPrefix(got, "一二三四") {
		t.Fatalf("unexpected clipped text: %q truncated=%v", got, truncated)
	}
	if got, truncated := clipContinuation("abc", 4); truncated || got != "abc" {
		t.Fatalf("short text changed: %q truncated=%v", got, truncated)
	}
}

func TestContinuationPreviewUsesKnowledgeAndCompletedRuns(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	if _, err := a.store.Exec(`INSERT INTO runs(id,task_id,input,kind,source,status,result,error,created,finished) VALUES(?,?,?,?,?,?,?,?,?,?)`, "run-preview", task.ID, "继续处理 api_key=topsecret", "chat", "web", "done", "已完成 sk-abcdefghijklmnopqr", "", now(), now()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Exec(`INSERT INTO knowledge_entries(id,task_id,title,content,status,source,run_id,revision,created,updated) VALUES(?,?,?,?,?,?,?,?,?,?)`, "knowledge-preview", task.ID, "验证结果", "password: hidden-value", "verified", "manual", "", 1, now(), now()); err != nil {
		t.Fatal(err)
	}
	preview, err := a.continuationPreview(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Runs != 1 || preview.Knowledge != 1 || !strings.Contains(preview.Context, "验证结果") {
		t.Fatalf("unexpected preview counts/content: %+v", preview)
	}
	for _, secret := range []string{"topsecret", "hidden-value", "sk-abcdefghijklmnopqr"} {
		if strings.Contains(preview.Context, secret) {
			t.Fatalf("preview leaked %q: %s", secret, preview.Context)
		}
	}
}
