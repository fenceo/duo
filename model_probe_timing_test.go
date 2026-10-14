package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func latencyProbeContext(seconds int) context.Context {
	return context.WithValue(context.Background(), modelProbeOptionsKey{}, modelProbeOptions{mode: "latency", seconds: seconds})
}

func TestModelProbeLatencyOptions(t *testing.T) {
	for _, mode := range []string{"", "availability"} {
		options, err := normalizeModelProbeOptions(ModelProbeRequest{Mode: mode})
		if err != nil || options.timeout() != 25*time.Second || options.publicTimeout() != 0 {
			t.Fatal(options, err)
		}
		seconds := 25
		if _, err := normalizeModelProbeOptions(ModelProbeRequest{Mode: mode, TimeoutSeconds: &seconds}); err == nil {
			t.Fatal("availability accepted timeout")
		}
	}
	request := ModelProbeRequest{Mode: "latency", Models: []string{"selected-model"}}
	options, err := normalizeModelProbeOptions(request)
	if err != nil || options.timeout() != 180*time.Second {
		t.Fatal(options, err)
	}
	for _, seconds := range []int{-1, 0, 25, 59, 60, 180, 600, 601} {
		request.TimeoutSeconds = &seconds
		_, err := normalizeModelProbeOptions(request)
		if (err == nil) != (seconds >= 60 && seconds <= 600) {
			t.Fatal(seconds, err)
		}
	}
	for _, request := range []ModelProbeRequest{
		{Mode: "invalid", Models: []string{"a"}},
		{Mode: "latency"}, {Mode: "latency", Models: []string{"a", "a"}},
		{Mode: "latency", Models: []string{" "}},
		{Mode: "latency", Engine: "mimo", Models: []string{"duo-mimo/test"}},
	} {
		if _, err := normalizeModelProbeOptions(request); err == nil {
			t.Fatal("invalid request accepted", request)
		}
	}
	if modelProbeStageTimeout(context.Background(), 30*time.Second) != 30*time.Second || modelProbeStageTimeout(latencyProbeContext(180), 30*time.Second) != 180*time.Second {
		t.Fatal("startup timeout changed outside probe")
	}
}

func TestModelProbeLatencyExceedsOldDeadlinesWithoutCountingProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		result := probeOneModelUsing(latencyProbeContext(180), Config{}, "deepseek-harness", "fixture", "model", func(ctx context.Context, c Config, task Task, input string, emit func(string, string)) (string, string, error) {
			if task.Session != "" || task.ID != "" || task.Mode.Permission != "read" || c.HardwareAI != nil || input != modelProbePrompt {
				t.Fatal("not isolated read-only probe")
			}
			for _, kind := range []string{"progress", "log", "session", "tool", "usage", "thought"} {
				emit(kind, "must not be timed")
			}
			emit("assistant", " \n ")
			time.Sleep(70 * time.Second)
			if ctx.Err() != nil {
				t.Fatal("long probe ended before selected timeout", ctx.Err())
			}
			emit("assistant", "OK")
			time.Sleep(3 * time.Second)
			return "new-session", "OK", nil
		})
		if result.Status != "available" || result.FirstResponseMS == nil || *result.FirstResponseMS != 70000 || result.DurationMS != 73000 || result.TimeoutSeconds != 180 || result.TimingSource != "stream_text" {
			t.Fatalf("%#v", result)
		}
	})
}

func TestModelProbeLatencyKeepsFirstOnFailureTimeoutAndCancel(t *testing.T) {
	for _, scenario := range []string{"failure", "timeout", "cancel", "no-response"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				parent, cancel := context.WithCancel(latencyProbeContext(60))
				defer cancel()
				result := probeOneModelUsing(parent, Config{}, "deepseek-harness", "fixture", "model", func(ctx context.Context, _ Config, _ Task, _ string, emit func(string, string)) (string, string, error) {
					time.Sleep(2 * time.Second)
					if scenario != "no-response" {
						emit("assistant", "private-text")
					}
					if scenario == "failure" {
						return "", "", errors.New("429 busy private-key")
					}
					if scenario == "cancel" {
						cancel()
					}
					<-ctx.Done()
					return "", "", ctx.Err()
				})
				if scenario == "no-response" {
					if result.FirstResponseMS != nil || result.TimingSource != "" {
						t.Fatal("fabricated first response", result)
					}
				} else if result.FirstResponseMS == nil || *result.FirstResponseMS != 2000 {
					t.Fatal("lost first response", result)
				}
				if (scenario == "timeout" || scenario == "no-response") && (result.Status != "timeout" || result.DurationMS != 60000) {
					t.Fatal(result)
				}
				if scenario == "cancel" && result.Message != "请求已取消" {
					t.Fatal(result)
				}
				raw, _ := json.Marshal(result)
				if strings.Contains(string(raw), "private-") {
					t.Fatal("private output leaked", string(raw))
				}
			})
		})
	}
}

func TestModelProbeLatencyEventsAndDisconnect(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "disconnect"}[disconnect], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				plan := modelProbePlan{engine: "deepseek-harness", workspace: "fixture", models: []string{"one"}, options: modelProbeOptions{mode: "latency", seconds: 180}}
				var events []ModelProbeEvent
				cleaned := false
				run := func(ctx context.Context, c Config, engine, workspace, model string) ModelProbeResult {
					return probeOneModelUsing(ctx, c, engine, workspace, model, func(ctx context.Context, _ Config, _ Task, _ string, emit func(string, string)) (string, string, error) {
						defer func() { cleaned = true }()
						select {
						case <-time.After(70 * time.Second):
						case <-ctx.Done():
							return "", "", ctx.Err()
						}
						emit("assistant", "private-model-text")
						return "", "OK", nil
					})
				}
				response := executeModelProbe(context.Background(), plan, run, func(event ModelProbeEvent) error {
					events = append(events, event)
					if disconnect && event.Type == "heartbeat" {
						return io.ErrClosedPipe
					}
					return nil
				})
				if !cleaned || len(response.Results) != 1 {
					t.Fatal("runner cleanup not awaited", response)
				}
				first, result, done, heartbeat := 0, 0, 0, 0
				for _, event := range events {
					switch event.Type {
					case "model_response":
						first++
						if result > 0 || event.FirstResponseMS == nil || *event.FirstResponseMS != 70000 {
							t.Fatal(event)
						}
					case "result":
						result++
					case "done":
						done++
					case "heartbeat":
						heartbeat++
					}
				}
				if heartbeat == 0 {
					t.Fatal("no keepalive")
				}
				if disconnect {
					if first+result+done != 0 {
						t.Fatal(events)
					}
				} else if first != 1 || result != 1 || done != 1 {
					t.Fatal(events)
				}
				raw, _ := json.Marshal(events)
				if strings.Contains(string(raw), "private-model-text") {
					t.Fatal("raw text in events")
				}
			})
		})
	}
}

func TestModelProbeCodexObserverIdentityAndTextTypes(t *testing.T) {
	var texts []string
	ctx := context.WithValue(context.Background(), modelProbeTextKey{}, func(text, source string) {
		if strings.TrimSpace(text) != "" {
			texts = append(texts, source+":"+text)
		}
	})
	observe := codexModelProbeObserver(ctx)
	event := func(method string, p map[string]any) {
		raw, _ := json.Marshal(p)
		observe(method, raw, "thread", "turn")
	}
	for _, ids := range [][2]string{{"", ""}, {"other", "turn"}, {"thread", "other"}} {
		event("item/agentMessage/delta", map[string]any{"threadId": ids[0], "turnId": ids[1], "delta": "foreign"})
	}
	event("item/started", map[string]any{"threadId": "thread", "turnId": "turn", "item": map[string]any{"id": "comment", "type": "agentMessage", "phase": "commentary"}})
	event("item/agentMessage/delta", map[string]any{"threadId": "thread", "turnId": "turn", "itemId": "comment", "delta": "progress"})
	event("item/reasoning/textDelta", map[string]any{"threadId": "thread", "turnId": "turn", "delta": "thinking"})
	event("item/agentMessage/delta", map[string]any{"threadId": "thread", "turnId": "turn", "itemId": "final", "delta": "OK"})
	event("item/completed", map[string]any{"threadId": "thread", "turnId": "turn", "item": map[string]any{"id": "final", "type": "agentMessage", "text": "OK", "phase": "final_answer"}})
	if strings.Join(texts, ",") != "stream_text:OK,completed_text:OK" {
		t.Fatal(texts)
	}
	if codexModelProbeObserver(context.Background()) != nil {
		t.Fatal("ordinary conversation observer")
	}
}

func TestModelProbeClaudeErrorResultIsNotFirstText(t *testing.T) {
	var texts []string
	s := claudeStream{probeText: func(text, source string) { texts = append(texts, source+":"+text) }}
	s.consume(`{"type":"result","is_error":true,"result":"private-key error"}`, func(string, string) {})
	s.consume(`{"type":"assistant","parent_tool_use_id":"subagent","message":{"content":[{"type":"text","text":"nested"}]}}`, func(string, string) {})
	s.consume(`{"type":"assistant","message":{"content":[{"type":"thinking","text":"reasoning"},{"type":"tool_use","name":"private-tool"}]}}`, func(string, string) {})
	if len(texts) != 0 {
		t.Fatal(texts)
	}
	s.consume(`{"type":"assistant","message":{"content":[{"type":"text","text":"OK"}]}}`, func(string, string) {})
	if len(texts) != 1 || texts[0] != "completed_text:OK" {
		t.Fatal(texts)
	}
}

func TestPublicProbeErrorLatencyCategoriesDoNotExposeDiagnostics(t *testing.T) {
	for _, tc := range []struct{ diagnostic, want string }{
		{"HTTP 429 busy private-key", "服务繁忙或请求频率受限，请稍后重试"},
		{"RESOURCE_EXHAUSTED private-key", "服务繁忙或请求频率受限，请稍后重试"},
		{"The model is experiencing high demand private-key", "服务繁忙或请求频率受限，请稍后重试"},
		{"HighDemandError private-key", "服务繁忙或请求频率受限，请稍后重试"},
		{"upstream timed out private-key", "等待模型响应超时"},
		{"unexpected EOF private-key", "模型响应连接中断，请重试"},
		{"Codex app-server 在 turn/completed 前断开：private-key", "模型响应连接中断，请重试"},
	} {
		if got := publicProbeError(errors.New(tc.diagnostic)); got != tc.want || strings.Contains(got, "private-key") {
			t.Fatal(tc, got)
		}
	}
}

func TestModelProbeLatencyHTTPAndLegacyJSON(t *testing.T) {
	_, _, handler, path, workspace := modelProbeHTTPFixture(t)
	for _, stream := range []bool{false, true} {
		r := modelProbeHTTPRequest(path, workspace, []string{"one"})
		raw, _ := json.Marshal(ModelProbeRequest{Engine: "claude", Workspace: workspace, Models: []string{"one"}, Mode: "latency"})
		r.Body = io.NopCloser(bytes.NewReader(raw))
		if !stream {
			r.Header.Set("Accept", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatal(w.Code, w.Body)
		}
		var result ModelProbeResult
		if stream {
			decoder := json.NewDecoder(w.Body)
			var first bool
			for {
				var event ModelProbeEvent
				err := decoder.Decode(&event)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if event.Type == "model_response" {
					first = event.FirstResponseMS != nil
				}
				if event.Type == "result" {
					if !first {
						t.Fatal("result before observation")
					}
					result = *event.Result
				}
			}
		} else {
			var response ModelProbeResponse
			if json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Results) != 1 {
				t.Fatal(w.Body)
			}
			result = response.Results[0]
		}
		if result.FirstResponseMS == nil || result.TimeoutSeconds != 180 || result.TimingSource != "completed_text" || result.Status != "available" {
			t.Fatal(result)
		}
	}
	r := modelProbeHTTPRequest(path, workspace, []string{"one"})
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	for _, field := range []string{"first_response_ms", "timeout_seconds", "timing_source"} {
		if strings.Contains(w.Body.String(), field) {
			t.Fatal("legacy JSON changed", w.Body)
		}
	}
	for _, raw := range []string{`{"engine":"claude","mode":"latency","models":["one","two"]}`, `{"engine":"claude","mode":"latency","models":["one"],"timeout_seconds":601}`, `{"engine":"mimo","mode":"latency","models":["duo-mimo/one"]}`} {
		r := modelProbeHTTPRequest(path, workspace, []string{"one"})
		r.Body = io.NopCloser(strings.NewReader(raw))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 400 || strings.Contains(w.Body.String(), `"type":"start"`) {
			t.Fatal(w.Code, w.Body)
		}
	}
}

func TestModelProbeLatencyNativeCodexDelta(t *testing.T) {
	c := codexFixtureConfig(t, "probe-latency")
	result := probeOneModel(latencyProbeContext(60), c, "codex", t.TempDir(), "synthetic-model")
	if result.Status != "available" || result.FirstResponseMS == nil || result.TimingSource != "stream_text" {
		t.Fatal(result)
	}
}

// Opt-in integration proof: an owned synthetic app-server delays startup for
// 65 seconds. It never loads real credentials or sends a model request.
func TestModelProbeLatencyNativeBeyondMinute(t *testing.T) {
	if os.Getenv("DUO_TEST_PROBE_LATENCY_SLOW") != "1" {
		t.Skip("opt-in 65-second synthetic native probe")
	}
	c := codexFixtureConfig(t, "probe-latency-slow")
	result := probeOneModel(latencyProbeContext(120), c, "codex", t.TempDir(), "synthetic-model")
	if result.Status != "available" || result.FirstResponseMS == nil || *result.FirstResponseMS < 65000 || result.TimeoutSeconds != 120 || result.TimingSource != "stream_text" {
		t.Fatal(result)
	}
}

// The protocol acknowledges turn/start immediately, but produces no model
// reply for 65 seconds. This separately exercises first-reply waiting after
// initialization, using only the owned synthetic app-server.
func TestModelProbeLatencyNativeDelayedReplyBeyondMinute(t *testing.T) {
	if os.Getenv("DUO_TEST_PROBE_LATENCY_SLOW") != "1" {
		t.Skip("opt-in 65-second synthetic delayed reply")
	}
	c := codexFixtureConfig(t, "probe-latency-slow-output")
	result := probeOneModel(latencyProbeContext(120), c, "codex", t.TempDir(), "synthetic-model")
	if result.Status != "available" || result.FirstResponseMS == nil || *result.FirstResponseMS < 65000 || result.TimeoutSeconds != 120 || result.TimingSource != "stream_text" {
		t.Fatal(result)
	}
}
