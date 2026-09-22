package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCodexPermissionSettingsSeparateSandboxFromApproval(t *testing.T) {
	tests := []struct {
		name     string
		mode     *WorkMode
		sandbox  string
		approval string
		reviewer string
		network  bool
	}{
		{"default", nil, "workspace-write", "on-request", "user", true},
		{"request", &WorkMode{Permission: "workspace", Approval: "request"}, "workspace-write", "on-request", "user", true},
		{"auto retains sandbox", &WorkMode{Permission: "workspace", Approval: "auto"}, "workspace-write", "on-request", "auto_review", true},
		{"never does not bypass", &WorkMode{Permission: "workspace", Approval: "never"}, "workspace-write", "never", "user", true},
		{"explicit offline", &WorkMode{Permission: "workspace", Approval: "request", AllowNetwork: boolPtr(false)}, "workspace-write", "on-request", "user", false},
		{"offline auto", &WorkMode{Permission: "workspace", Approval: "auto", AllowNetwork: boolPtr(false)}, "workspace-write", "on-request", "auto_review", false},
		{"read cannot escalate", &WorkMode{Permission: "read", Approval: "auto", AllowNetwork: boolPtr(true)}, "read-only", "never", "user", false},
		{"full explicit bypass", &WorkMode{Permission: "full", Approval: "auto", AllowNetwork: boolPtr(false)}, "danger-full-access", "never", "user", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sandbox, approval, reviewer, network := codexPermissionSettings(Task{Mode: tc.mode})
			if sandbox != tc.sandbox || approval != tc.approval || reviewer != tc.reviewer || network != tc.network {
				t.Fatalf("settings = (%q, %q, %q, %v), want (%q, %q, %q, %v)", sandbox, approval, reviewer, network, tc.sandbox, tc.approval, tc.reviewer, tc.network)
			}
		})
	}
}

func TestCodexCatalogApprovalValidationAndOfflineSnapshot(t *testing.T) {
	for _, approval := range []string{"request", "auto", "never", ""} {
		catalog := WorkCatalog{Modes: []WorkMode{{ID: "offline", Name: "离线", Permission: "workspace", Approval: approval, AllowNetwork: boolPtr(false)}}}
		if err := validateCatalog(&catalog); err != nil {
			t.Fatalf("valid approval %q rejected: %v", approval, err)
		}
		mode := catalog.Modes[0]
		if mode.AllowNetwork == nil || *mode.AllowNetwork || mode.Approval == "" {
			t.Fatalf("explicit offline/default approval lost: %#v", mode)
		}
		var roundTrip WorkMode
		encoded, err := json.Marshal(mode)
		if err != nil || json.Unmarshal(encoded, &roundTrip) != nil || roundTrip.AllowNetwork == nil || *roundTrip.AllowNetwork {
			t.Fatalf("false omitted from queued mode snapshot: %s (%v)", encoded, err)
		}
	}
	for _, approval := range []string{"bypass", "acceptForSession", "on-failure"} {
		catalog := WorkCatalog{Modes: []WorkMode{{ID: "custom", Name: "custom", Permission: "workspace", Approval: approval}}}
		if err := validateCatalog(&catalog); err == nil {
			t.Fatalf("unsupported approval %q accepted", approval)
		}
	}
	if err := validateCatalog(&WorkCatalog{Modes: []WorkMode{{ID: "codex:auto", Name: "shadow", Permission: "full"}}}); err == nil {
		t.Fatal("custom mode replaced native auto-approval preset")
	}
}

func TestCodexBuiltinApprovalUpgrade(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	for _, expected := range builtinModes() {
		old := WorkMode{ID: expected.ID, Permission: "workspace", Approval: "never", AllowNetwork: boolPtr(false)}
		got, err := a.store.resolveMode("", &old)
		if err != nil || got.Permission != expected.Permission || got.Approval != expected.Approval || got.AllowNetwork == nil || *got.AllowNetwork != *expected.AllowNetwork {
			t.Fatalf("builtin %q not refreshed: %#v %v", expected.ID, got, err)
		}
	}
}

func TestClaudeCannotQueueCodexAutomaticApproval(t *testing.T) {
	f := &fakeRunner{}
	a := fixture(t, f)
	c := a.config.get()
	task, err := a.createWithExecution("Claude task", c.Workspaces[0], "", "claude", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.submitWithOptions(task.ID, "work", "chat", "web", SubmitOptions{ModeID: "codex:auto"}); err == nil || !strings.Contains(err.Error(), "Codex") {
		t.Fatalf("Claude silently accepted Codex native auto-review: %v", err)
	}
	runs, err := a.store.runs(task.ID)
	if err != nil || len(runs) != 0 {
		t.Fatalf("rejected mode created a queued run: %#v %v", runs, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.inputs) != 0 {
		t.Fatal("rejected request reached runner")
	}
}

type codexSummaryProbe struct {
	calls chan Task
}

func (f *codexSummaryProbe) Run(_ context.Context, cfg Config, task Task, _ string, _ func(string, string)) (string, string, error) {
	f.calls <- task
	return "summary-session", "summary", nil
}

func TestKnowledgeRunCannotInheritFullAccess(t *testing.T) {
	f := &codexSummaryProbe{calls: make(chan Task, 1)}
	a := fixture(t, f)
	task := taskFor(t, a)
	if _, err := a.submitWithOptions(task.ID, "summarize", "knowledge", "web", SubmitOptions{ModeID: "full"}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-f.calls:
		sandbox, approval, reviewer, network := codexPermissionSettings(got)
		if sandbox != "read-only" || approval != "never" || reviewer != "user" || network {
			t.Fatalf("summary inherited writable permissions: %#v", got.Mode)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("summary did not execute")
	}
}

func TestCodexAutoPresetDoesNotReplaceExistingCustomAuto(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	old := WorkMode{ID: "auto", Name: "原有模式", Permission: "read", Prompt: "只分析"}
	catalog := WorkCatalog{Modes: []WorkMode{old}}
	if err := validateCatalog(&catalog); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(catalog)
	if err := a.store.set("workbench_catalog", string(raw)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "auto"} {
		got, err := a.store.resolveMode(id, &old)
		if err != nil || got.ID != "auto" || got.Permission != "read" || got.Prompt != "只分析" {
			t.Fatalf("custom mode overwritten: %#v %v", got, err)
		}
	}
	got, err := a.store.resolveMode("codex:auto", nil)
	if err != nil || got.Approval != "auto" || !got.Builtin {
		t.Fatalf("missing native auto preset: %#v %v", got, err)
	}
}
