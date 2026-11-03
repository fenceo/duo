package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// The subprocess is the Go test executable, never a CLI or model request.
// Deliberately much larger session totals expose accidental billing/context
// mixing, while the final sample simulates compaction in the same native turn.
func runCodexContextFixture(notify func(string, any), threadID, turnID string) {
	total := codexTokenUsage{Input: 1_000_000, Output: 100_000, Cached: 500_000}
	send := func(targetThread, targetTurn string, last any, window any, includeWindow bool) {
		usage := map[string]any{"total": total, "last": last}
		if includeWindow {
			usage["modelContextWindow"] = window
		}
		notify("thread/tokenUsage/updated", map[string]any{
			"threadId": targetThread, "turnId": targetTurn, "tokenUsage": usage,
		})
	}
	last := codexTokenUsage{Input: 80_000, Output: 20, Cached: 40_000}
	send(threadID, turnID, last, 100_000, true)
	send(threadID, turnID, last, 100_000, true) // Repeated gauge is not a new sample.
	for _, target := range [][2]string{{"other-thread", turnID}, {threadID, "other-turn"}, {"", turnID}, {threadID, ""}} {
		send(target[0], target[1], codexTokenUsage{Input: 90_000}, 100_000, true)
	}
	for _, window := range []any{nil, 0, -1, "100000", 100_000.5, int64(1<<40) + 1, json.RawMessage(`9223372036854775808`)} {
		send(threadID, turnID, last, window, true)
	}
	send(threadID, turnID, last, nil, false)
	for _, invalidLast := range []any{nil, map[string]any{}, codexTokenUsage{Input: 0}, codexTokenUsage{Input: -1}, codexTokenUsage{Input: int64(1<<40) + 1}} {
		send(threadID, turnID, invalidLast, 100_000, true)
	}
	total.Input += 50
	total.Output += 10
	total.Cached += 20
	send(threadID, turnID, codexTokenUsage{Input: 20_000, Output: 10, Cached: 10_000}, 100_000, true)
	// Missing samples after compaction must not erase the last valid gauge or
	// reuse a stale model window with a new input count.
	send(threadID, turnID, codexTokenUsage{Input: 30_000}, nil, false)
}

func TestCodexAppServerCurrentContextOccupancy(t *testing.T) {
	config := codexFixtureConfig(t, "context-occupancy")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var mu sync.Mutex
	var contexts []RunContext
	var usages []RunUsage
	_, result, err := (CodexRunner{}).Run(ctx, config, Task{Engine: "codex", Workspace: t.TempDir()}, "synthetic context fixture", func(kind, value string) {
		mu.Lock()
		defer mu.Unlock()
		switch kind {
		case "context":
			var sample RunContext
			if err := json.Unmarshal([]byte(value), &sample); err != nil {
				t.Errorf("decode context: %v", err)
			}
			contexts = append(contexts, sample)
		case "usage":
			var usage RunUsage
			if err := json.Unmarshal([]byte(value), &usage); err != nil {
				t.Errorf("decode billing usage: %v", err)
			}
			usages = append(usages, usage)
		}
	})
	if err != nil || result != "context fixture complete" {
		t.Fatalf("native fixture: result=%q err=%v", result, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(contexts) != 2 || contexts[0] != (RunContext{Used: 80_000, Size: 100_000}) || contexts[1] != (RunContext{Used: 20_000, Size: 100_000}) {
		t.Fatalf("current context must follow only valid native samples, including compaction: %#v", contexts)
	}
	if len(usages) == 0 {
		t.Fatal("native billing usage was lost")
	}
	if usage := usages[len(usages)-1]; usage.Input != 80_050 || usage.Output != 30 || usage.Cached != 40_020 || usage.Total != 80_080 {
		t.Fatalf("context gauge changed cumulative billing behavior: %#v", usage)
	}
}
