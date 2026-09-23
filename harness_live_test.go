package main

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// This is deliberately excluded from ordinary test runs. Setting the opt-in
// acknowledges two small, real model turns using the selected WSL user's native
// credentials. The test never opens credential files or prints raw diagnostics.
func TestHarnessSDKPaidWSLRoundTrip(t *testing.T) {
	if os.Getenv("JIANZUO_TEST_HARNESS_PAID_WSL") != "1" {
		t.Skip("explicit authorization required: JIANZUO_TEST_HARNESS_PAID_WSL=1")
	}
	if os.Getenv("JIANZUO_TEST_HARNESS_SDK") == "1" {
		t.Fatal("live test refuses to run with the process fixture enabled")
	}
	distro := strings.TrimSpace(os.Getenv("JIANZUO_TEST_HARNESS_WSL_DISTRO"))
	user := strings.TrimSpace(os.Getenv("JIANZUO_TEST_HARNESS_WSL_USER"))
	provider := strings.TrimSpace(os.Getenv("JIANZUO_TEST_HARNESS_PROVIDER"))
	model := strings.TrimSpace(os.Getenv("JIANZUO_TEST_HARNESS_MODEL"))
	if distro == "" || user == "" {
		t.Fatal("set JIANZUO_TEST_HARNESS_WSL_DISTRO and JIANZUO_TEST_HARNESS_WSL_USER explicitly for the authorized WSL account")
	}
	if provider == "" || model == "" {
		t.Fatal("set JIANZUO_TEST_HARNESS_PROVIDER and JIANZUO_TEST_HARNESS_MODEL explicitly for the authorized native model route")
	}
	// Only safe identifiers may be included in diagnostics; never print raw
	// environment values or silently fall back to another provider or model.
	safeID := regexp.MustCompile(`^[A-Za-z0-9._:@/+\-]{1,100}$`)
	if !safeID.MatchString(provider) || !safeID.MatchString(model) {
		t.Fatal("provider and model must be safe identifiers of at most 100 characters")
	}
	closeHarnessRuntimes()
	c := Config{Distro: distro, User: user, Harness: "dsh", HarnessProvider: provider, HarnessModel: model}
	setupCtx, setupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer setupCancel()
	created, err := commandWithContext(setupCtx, command(c, "mktemp", "-d", "/tmp/jianzuo-harness-paid-XXXXXXXX")).Output()
	if err != nil {
		t.Fatalf("cannot create isolated WSL workspace: %s", publicProbeError(err))
	}
	workspace := strings.TrimSpace(string(created))
	if !regexp.MustCompile(`^/tmp/jianzuo-harness-paid-[A-Za-z0-9]{8}$`).MatchString(workspace) {
		t.Fatal("mktemp returned an unexpected path; refusing all cleanup or model work")
	}
	t.Cleanup(func() {
		// Stop the owning worker before deleting only the exact validated mktemp
		// leaf. Native Harness conversation records remain in its usual home.
		closeHarnessRuntimes()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := commandWithContext(cleanupCtx, command(c, "rm", "-rf", "--", workspace)).Run(); err != nil {
			t.Errorf("failed to clean the isolated paid-test workspace: %s", publicProbeError(err))
		}
	})
	c.Workspaces = []string{workspace}
	// A route may not advertise any reasoning controls. Omit the optional
	// setting so the native adapter can retain its configured default.
	task := Task{ID: "harness-paid-" + uid(), Engine: "deepseek-harness", Workspace: workspace, Model: model, Mode: &WorkMode{Permission: "read", Approval: "never", AllowNetwork: boolPtr(true)}}
	const marker = "JZ_HARNESS_OK_7B32D9A1"
	run := func(round int, prompt string) (string, *harnessWorker) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
		defer cancel()
		started := time.Now()
		toolSeen := false
		session, result, err := runHarnessSDK(ctx, c, task, prompt, func(kind, _ string) {
			if kind == "tool" {
				toolSeen = true
				cancel()
			}
		})
		if toolSeen {
			t.Fatalf("round %d attempted a tool call; cancelled immediately and will not continue", round)
		}
		if err != nil {
			// Preserve only a safe correlation identity and allowlisted failure
			// facts, so a later read of this exact session need not scan history.
			if regexp.MustCompile(`^jianzuo-[a-f0-9]{24}$`).MatchString(session) {
				t.Logf("failed native session=%s provider=%s model=%s", session, provider, model)
			} else {
				t.Log("failure occurred before a native session identity was returned")
			}
			failure := strings.ToUpper(err.Error())
			for _, code := range []string{"MISSING_CREDENTIAL", "INVALID_API_KEY", "INVALID_AUTH", "MODEL_NOT_FOUND", "MODEL_NOT_SUPPORTED", "UNSUPPORTED_MODEL", "INVALID_MODEL", "RATE_LIMITED", "INSUFFICIENT_QUOTA"} {
				if strings.Contains(failure, code) {
					t.Logf("provider failure code=%s", code)
				}
			}
			if status := regexp.MustCompile(`(?:HTTP|STATUS(?: CODE)?)\s*[:=]?\s*([1-5][0-9]{2})\b`).FindStringSubmatch(failure); len(status) == 2 {
				t.Logf("provider HTTP status=%s", status[1])
			}
			if ctx.Err() != nil {
				t.Fatalf("round %d cancelled/timed out after %s; no retry or later round", round, time.Since(started).Round(time.Millisecond))
			}
			t.Fatalf("round %d failed: %s; no retry or later round", round, publicProbeError(err))
		}
		if session == "" || strings.TrimSpace(result) != marker {
			t.Fatalf("round %d did not return the exact expected marker (reply length %d); no retry or later round", round, len(result))
		}
		harnessRuntimes.Lock()
		worker := harnessRuntimes.workers[session]
		harnessRuntimes.Unlock()
		if worker == nil {
			t.Fatalf("round %d did not retain its live native worker", round)
		}
		t.Logf("round %d PASS: exact marker, zero tools, provider=%s model=%s duration=%s", round, provider, model, time.Since(started).Round(time.Millisecond))
		return session, worker
	}
	firstSession, firstWorker := run(1, "不要调用任何工具、不读取文件，只回复 "+marker)
	task.Session = firstSession
	secondSession, secondWorker := run(2, "不要调用工具、不读文件，只回复我上一条要求你回复的内容")
	if secondSession != firstSession || secondWorker != firstWorker {
		t.Fatal("second turn changed the native session or worker instead of preserving conversation context")
	}
	t.Log("PASS: exactly two prompt submissions, same live worker and native session, second prompt omitted the marker")
}

// This native-route diagnostic sends initialize/shutdown only. It never creates
// a session or sends a prompt, and only reports fixed, non-sensitive categories.
func TestHarnessSDKNativeRouteHandshake(t *testing.T) {
	if os.Getenv("JIANZUO_TEST_HARNESS_NATIVE_ROUTE") != "1" {
		t.Skip("set JIANZUO_TEST_HARNESS_NATIVE_ROUTE=1 for a no-prompt native route diagnostic")
	}
	distro := strings.TrimSpace(os.Getenv("JIANZUO_TEST_HARNESS_WSL_DISTRO"))
	user := strings.TrimSpace(os.Getenv("JIANZUO_TEST_HARNESS_WSL_USER"))
	provider := strings.TrimSpace(os.Getenv("JIANZUO_TEST_HARNESS_PROVIDER"))
	model := strings.TrimSpace(os.Getenv("JIANZUO_TEST_HARNESS_MODEL"))
	safeID := regexp.MustCompile(`^[A-Za-z0-9._:@/+\-]{1,100}$`)
	if distro == "" || user == "" || !safeID.MatchString(provider) || !safeID.MatchString(model) {
		t.Fatal("explicit WSL account and safe provider/model identifiers are required")
	}
	c := Config{Distro: distro, User: user, Harness: "dsh", HarnessProvider: provider, HarnessModel: model}
	task := Task{Workspace: "/tmp", Engine: "deepseek-harness", Model: model, Mode: &WorkMode{Permission: "read", Approval: "never", AllowNetwork: boolPtr(true)}}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	w, err := startHarness(ctx, c, task, func(string, string) {})
	if err != nil {
		category := "UNCLASSIFIED_INITIALIZE_FAILURE"
		message := strings.ToLower(err.Error())
		for _, known := range []struct{ phrase, category string }{
			{"does not support reasoning effort", "UNSUPPORTED_REASONING_EFFORT"},
			{"no adapter registered", "NO_ADAPTER"},
			{"unknown model", "UNKNOWN_MODEL"},
			{"missing_credential", "MISSING_CREDENTIAL"},
			{"deadline exceeded", "INITIALIZE_TIMEOUT"},
			{"e_accessdenied", "WSL_ACCESS_DENIED"},
			{"0x8007274c", "WSL_SERVICE_TIMEOUT"},
		} {
			if strings.Contains(message, known.phrase) {
				category = known.category
				break
			}
		}
		t.Fatalf("initialize failed: category=%s provider=%s model=%s reasoningEffort=default; zero prompt submissions", category, provider, model)
	}
	if err := w.stop(); err != nil {
		t.Fatal("initialize passed but SDK cleanup failed; raw diagnostics withheld; zero prompt submissions")
	}
	t.Logf("initialize/shutdown PASS provider=%s model=%s reasoningEffort=default; zero prompt submissions", provider, model)
}
