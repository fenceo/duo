package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexMXCCapabilityRequiresAnAdvertisedFeature(t *testing.T) {
	for _, tc := range []struct {
		output string
		want   bool
	}{
		{"prefer_mxc under development false\r\n", true},
		{"prefer_mxc stable true\n", true},
		{"unified_exec stable true\nprefer_mxc experimental false\n", true},
		{"unified_exec stable true\n", false},
		{"unknown command features", false},
		{"WARNING: prefer_mxc is unsupported", false},
		{"prefer_mxc", false},
		{"prefer_mxc is unsupported false", false},
		{"prefer_mxc removed false", false},
	} {
		if got := codexHasMXCFeature(tc.output); got != tc.want {
			t.Errorf("capability(%q)=%v, want %v", tc.output, got, tc.want)
		}
	}
}

func TestCodexMXCPreferenceHonorsSelectedHomeAndProjectOptOut(t *testing.T) {
	for _, tc := range []struct {
		name, userConfig, projectConfig string
		want                            bool
	}{
		{"new configuration", "", "", true},
		{"existing configuration", "model='fixture'\n[windows]\nsandbox='elevated'\n", "", true},
		{"user enabled", "[features]\nprefer_mxc=true\n", "", true},
		{"user opt out", "[features]\nprefer_mxc=false\n", "", false},
		{"inline opt out", "features={prefer_mxc=false}\n", "", false},
		{"project opt out", "[features]\nprefer_mxc=true\n", "[features]\nprefer_mxc=false\n", false},
		{"invalid configuration", "[features]\nprefer_mxc='invalid'\n", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, workspace := t.TempDir(), t.TempDir()
			if tc.userConfig != "" {
				if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(tc.userConfig), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.projectConfig != "" {
				if err := os.Mkdir(filepath.Join(workspace, ".codex"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(workspace, ".codex", "config.toml"), []byte(tc.projectConfig), 0600); err != nil {
					t.Fatal(err)
				}
			}
			c := Config{EngineEnv: map[string]string{"CODEX_HOME": home}}
			if got := codexAllowsMXCPreference(c, workspace); got != tc.want {
				t.Fatalf("preference=%v, want %v", got, tc.want)
			}
			if tc.userConfig != "" {
				after, err := os.ReadFile(filepath.Join(home, "config.toml"))
				if err != nil || string(after) != tc.userConfig {
					t.Fatalf("user configuration modified: %v", err)
				}
			}
		})
	}
}

func TestCodexWindowsSandboxDoesNotProbeRemoteOrFullAccess(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    Config
		mode *WorkMode
	}{
		{"wsl", Config{Distro: "fixture", codexPreferMXC: true}, nil},
		{"ssh", Config{SSHHost: "fixture.invalid", codexPreferMXC: true}, nil},
		{"full", Config{codexPreferMXC: true}, &WorkMode{Permission: "full"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// No executable is configured; attempting a capability probe would
			// be a regression. Stale per-run flags must also be discarded.
			c := prepareCodexWindowsSandbox(context.Background(), tc.c, Task{Mode: tc.mode})
			if c.codexPreferMXC || strings.Contains(strings.Join(codexAppServerArgs(c, Task{Mode: tc.mode}), "|"), "features.prefer_mxc") {
				t.Fatal("Windows preference leaked to another execution policy")
			}
		})
	}
}

func TestCodexSandboxInitializationDiagnosisKeepsOriginalError(t *testing.T) {
	for _, message := range []string{
		"helper_unknown_error: setup refresh had errors",
		"runtime read/execute validation failed: open ACL target (os error 32)",
	} {
		original := errors.New(message)
		err := codexSandboxInitializationError(original)
		if !errors.Is(err, original) || !strings.Contains(err.Error(), "Windows 沙箱初始化失败") || !strings.Contains(err.Error(), message) {
			t.Fatal(err)
		}
	}
	for _, message := range []string{"Git authentication failed", "TerminateProcess: Access is denied", "already has an active writer", "MXC unavailable"} {
		original := errors.New(message)
		if codexSandboxInitializationError(original) != original {
			t.Fatalf("unrelated error misclassified: %s", message)
		}
	}
}

func TestCodexSandboxFailureDoesNotReplaceOrReplayTheNativeSession(t *testing.T) {
	c := codexFixtureConfig(t, "sandbox-setup-failure")
	task := Task{Workspace: t.TempDir(), Session: "existing-sandbox-failure-thread"}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var changedSession bool
	session, result, err := (CodexRunner{}).Run(ctx, c, task, "synthetic input", func(kind, value string) {
		if kind == "session" || kind == "assistant" || kind == "tool" {
			changedSession = true
		}
	})
	if err == nil || !strings.Contains(err.Error(), "Windows 沙箱初始化失败") || !strings.Contains(err.Error(), "setup refresh had errors") {
		t.Fatal(err)
	}
	if session != task.Session || result != "" || changedSession {
		t.Fatal("initialization failure changed or executed the session")
	}
}
