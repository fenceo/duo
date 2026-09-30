package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCancelQueuedRunPreservesOtherWork(t *testing.T) {
	f := &fakeRunner{gate: make(chan struct{}), started: make(chan struct{}, 1)}
	a := fixture(t, f)
	task := taskFor(t, a)
	active, err := a.submit(task.ID, "active", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.started:
	case <-time.After(3 * time.Second):
		t.Fatal("not running")
	}
	withdraw, err := a.submit(task.ID, "withdraw exact text", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	keep, err := a.submit(task.ID, "keep", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	files := `[ {"id":"synthetic-file","name":"fixture.txt","mime":"text/plain","size":7} ]`
	if _, err = a.store.Exec("UPDATE run_options SET attachments=? WHERE run_id=?", files, withdraw.ID); err != nil {
		t.Fatal(err)
	}
	// A mismatching task path must never target another task's queue.
	other := taskFor(t, a)
	if _, err = a.cancelQueuedRun(other.ID, withdraw.ID); err == nil {
		t.Fatal("cross-task cancellation")
	}
	if _, err = a.cancelQueuedRun(task.ID, active.ID); !errors.Is(err, errRunNotQueued) {
		t.Fatal("cancelled running work", err)
	}
	for range 2 {
		run, err := a.cancelQueuedRun(task.ID, withdraw.ID)
		if err != nil || run.Status != "interrupted" || run.Error != queueWithdrawn || run.Input != withdraw.Input || len(run.Attachments) != 1 || run.Attachments[0].ID != "synthetic-file" {
			t.Fatal(run, err)
		}
	}
	current, _ := a.store.task(task.ID)
	if current.Status != "running" {
		t.Fatal("lost running state", current.Status)
	}
	var events int
	a.store.QueryRow("SELECT count(*) FROM events WHERE run_id=? AND kind='status'", withdraw.ID).Scan(&events)
	if events != 1 {
		t.Fatal("non-idempotent cancellation", events)
	}
	close(f.gate)
	waitUntil(t, func() bool {
		var status string
		a.store.QueryRow("SELECT status FROM runs WHERE id=?", keep.ID).Scan(&status)
		return status == "done"
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.inputs) != 2 || f.inputs[0] != "active" || f.inputs[1] != "keep" {
		t.Fatal("queue changed", f.inputs)
	}
}

func TestCancelLastQueuedRunClearsTaskStatus(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES('pending',?,'draft','chat','web','queued',1)", task.ID); err != nil {
		t.Fatal(err)
	}
	a.store.Exec("UPDATE tasks SET status='queued' WHERE id=?", task.ID)
	if _, err := a.cancelQueuedRun(task.ID, "pending"); err != nil {
		t.Fatal(err)
	}
	saved, _ := a.store.task(task.ID)
	if saved.Status != "idle" {
		t.Fatal(saved.Status)
	}
}

func usageFixtureRun(t *testing.T, a *App, task, id, status, engine, model, raw string, started time.Time) {
	t.Helper()
	if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES(?,?,'fixture','chat','web',?,?)", id, task, status, started.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	stamp := started.UnixMilli()
	if status == "queued" || status == "interrupted" && raw == "null" {
		stamp = 0
	}
	if _, err := a.store.Exec("INSERT INTO run_metrics(run_id,started,usage) VALUES(?,?,?)", id, stamp, raw); err != nil {
		t.Fatal(err)
	}
	if engine != "" || model != "" {
		snapshot, _ := json.Marshal(RunExecution{Engine: engine, Model: model})
		if _, err := a.store.Exec("INSERT INTO run_execution(run_id,snapshot) VALUES(?,?)", id, string(snapshot)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUsageSummaryModelsCoverageAndDays(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	one, two := taskFor(t, a), taskFor(t, a)
	zone, _ := time.LoadLocation("Asia/Shanghai")
	at := time.Date(2026, 9, 30, 12, 0, 0, 0, zone)
	// The same model through two engines has disjoint versus inclusive cache
	// counters, but both yield inclusive input via total-output.
	usageFixtureRun(t, a, one.ID, "codex", "done", "codex", "shared", `{"input":100,"output":10,"cached":80,"total":110}`, at)
	usageFixtureRun(t, a, two.ID, "claude", "done", "claude", "shared", `{"input":20,"output":5,"cached":50,"cache_write":30,"total":105}`, at)
	usageFixtureRun(t, a, one.ID, "missing", "done", "codex", "other", "null", at)
	usageFixtureRun(t, a, one.ID, "running", "running", "codex", "other", `{"input":3,"output":2,"total":5}`, at)
	usageFixtureRun(t, a, one.ID, "cancelled", "interrupted", "codex", "shared", "null", at)
	usageFixtureRun(t, a, one.ID, "queued", "queued", "codex", "shared", "null", at)
	usageFixtureRun(t, a, one.ID, "yesterday", "done", "", "", `{"input":1,"output":1,"total":2}`, time.Date(2026, 9, 29, 15, 59, 59, 0, time.UTC))
	usageFixtureRun(t, a, one.ID, "midnight", "failed", "codex", "shared", `{"input":7,"output":1,"total":8}`, time.Date(2026, 9, 29, 16, 0, 0, 0, time.UTC))
	usageFixtureRun(t, a, one.ID, "old", "done", "codex", "shared", `{"input":1000,"output":1,"total":1001}`, at.AddDate(0, 0, -40))
	// Historical task model changes must not relabel run snapshots.
	a.store.Exec("UPDATE tasks SET model='different' WHERE id=?", one.ID)
	report, err := a.store.usageSummary(context.Background(), "", 2, zone, at)
	if err != nil {
		t.Fatal(err)
	}
	if report.Total.Total != 1231 || report.Total.Input != 1211 || report.Total.Output != 20 || report.Total.Runs != 7 || report.Total.Missing != 1 || report.Total.Running != 1 {
		t.Fatalf("bad total: %+v", report.Total)
	}
	if report.Period.Total != 230 || report.Days[0].Total != 228 || report.Days[1].Total != 2 {
		t.Fatalf("bad date assignment: %+v", report)
	}
	if len(report.Days[0].Models) != 2 {
		t.Fatal(report.Days[0].Models)
	}
	shared := report.Days[0].Models[1]
	if shared.Model != "shared" || shared.Total != 223 || shared.Runs != 3 || strings.Join(shared.Engines, ",") != "claude,codex" {
		t.Fatal(shared)
	}
	if report.Days[1].Models[0].Model != "" {
		t.Fatal("guessed legacy model")
	}
	only, err := a.store.usageSummary(context.Background(), one.ID, 2, zone, at)
	if err != nil || only.Total.Total != 1126 {
		t.Fatal(only.Total, err)
	}
	empty, err := a.store.usageSummary(context.Background(), "none", 2, zone, at)
	if err != nil || len(empty.Days) != 2 || len(empty.Days[0].Models) != 0 || empty.Total.Runs != 0 {
		t.Fatal(empty, err)
	}
}

func TestUsageMissingAndDST(t *testing.T) {
	for _, raw := range []string{"null", "{}", "bad", `{"input":0,"output":null,"total":0}`, `{"input":-1,"output":1,"total":0}`, `{"input":5,"output":2,"total":3}`} {
		var totals UsageTotals
		totals.add(raw, "done")
		if totals.Missing != 1 || totals.Reported != 0 {
			t.Fatal(raw, totals)
		}
	}
	var zero UsageTotals
	zero.add(`{"input":0,"output":0,"total":0}`, "done")
	if zero.Missing != 0 || zero.Reported != 1 {
		t.Fatal(zero)
	}
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 2, 0, 30, 0, 0, zone)
	for i, stamp := range []string{"2026-11-01T05:30:00Z", "2026-11-01T06:30:00Z"} {
		v, _ := time.Parse(time.RFC3339, stamp)
		usageFixtureRun(t, a, task.ID, string(rune('a'+i)), "done", "codex", "model", `{"input":1,"output":1,"total":2}`, v)
	}
	r, err := a.store.usageSummary(context.Background(), "", 3, zone, at)
	if err != nil || r.Days[1].Date != "2026-11-01" || r.Days[1].Runs != 2 || r.Days[2].Date != "2026-10-31" {
		t.Fatal(r, err)
	}
}

func TestUsageAndQueueHTTPGuards(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	usageFixtureRun(t, a, task.ID, "queue", "queued", "codex", "model", "null", time.Now())
	a.store.Exec("INSERT INTO sessions VALUES(?,?,?)", hash("usage-fixture"), "usage-csrf", now()+60000)
	handler := (&Server{app: a}).Handler()
	for _, tc := range []struct {
		method, path string
		login, csrf  bool
		code         int
	}{
		{"GET", "/api/usage", false, false, 401},
		{"GET", "/api/usage?days=0", true, false, 400},
		{"GET", "/api/usage?days=91", true, false, 400},
		{"GET", "/api/usage?timezone=invalid", true, false, 400},
		{"GET", "/api/usage?task_id=unknown", true, false, 404},
		{"GET", "/api/usage?days=7&timezone=Asia%2FShanghai", true, false, 200},
		{"POST", "/api/tasks/" + task.ID + "/runs/queue/cancel", true, false, 403},
		{"POST", "/api/tasks/unknown/runs/queue/cancel", true, true, 404},
		{"POST", "/api/tasks/" + task.ID + "/runs/queue/cancel", true, true, 200},
	} {
		req := httptest.NewRequest(tc.method, "http://127.0.0.1"+tc.path, strings.NewReader("{}"))
		if tc.login {
			req.AddCookie(&http.Cookie{Name: "jianzuo_session", Value: "usage-fixture"})
		}
		if tc.csrf {
			req.Header.Set("X-CSRF-Token", "usage-csrf")
			req.Header.Set("Origin", "http://127.0.0.1")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
}
