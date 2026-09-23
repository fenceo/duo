package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func registerHarnessStatusFixture(t *testing.T, session string, w *harnessWorker) {
	t.Helper()
	harnessRuntimes.Lock()
	harnessRuntimes.workers[session] = w
	harnessRuntimes.Unlock()
	t.Cleanup(func() {
		harnessRuntimes.Lock()
		delete(harnessRuntimes.workers, session)
		harnessRuntimes.Unlock()
	})
}

func createHarnessStoredTask(t *testing.T, a *App) Task {
	t.Helper()
	c := a.config.get()
	task, err := a.createWithExecution("Harness status fixture", c.Workspaces[0], "deepseek-flash", "deepseek-harness", "")
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestHarnessRuntimeStatusUsesNativeWorkerState(t *testing.T) {
	if got := harnessRuntimeStatus(Task{Engine: "codex", Session: "other-native-session"}); got != nil {
		t.Fatalf("non-Harness runtime exposed: %#v", got)
	}
	task := Task{Engine: "deepseek-harness"}
	if got := harnessRuntimeStatus(task); got.State != "new" || !got.CanContinue {
		t.Fatalf("new runtime = %#v", got)
	}
	task.Session = "missing-" + uid()
	if got := harnessRuntimeStatus(task); got.State != "closed" || got.CanContinue || !strings.Contains(got.Reason, "空白会话") {
		t.Fatalf("missing runtime = %#v", got)
	}
	for _, scenario := range []string{"live", "busy", "stopped", "exited", "stdout-closed", "read-failed"} {
		t.Run(scenario, func(t *testing.T) {
			w := harnessIOWorker()
			w.c.EngineEnv = map[string]string{"DSH_HOME": "/private/profile-secret"}
			w.diagnostic = "private diagnostic secret"
			task := Task{Engine: "deepseek-harness", Session: uid()}
			registerHarnessStatusFixture(t, task.Session, w)
			want := "closed"
			switch scenario {
			case "live":
				want = "live"
			case "busy":
				want = "busy"
				w.lease.Lock()
				defer w.lease.Unlock()
			case "stopped":
				close(w.halt)
			case "exited":
				close(w.done)
			case "stdout-closed":
				w.stdoutClosed.Store(true)
			case "read-failed":
				w.readErr = errors.New("private stream error")
			}
			got := harnessRuntimeStatus(task)
			if got.State != want || got.CanContinue != (want != "closed") {
				t.Fatalf("runtime = %#v, want %s", got, want)
			}
			raw, _ := json.Marshal(got)
			if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "DSH_HOME") || strings.Contains(string(raw), "secret") {
				t.Fatalf("runtime snapshot disclosed internal state: %s", raw)
			}
		})
	}
}

func TestHarnessRuntimeDetailIsAdditiveAndClosedSubmitWritesNothing(t *testing.T) {
	f := &fakeRunner{}
	a := fixture(t, f)
	harnessTask := createHarnessStoredTask(t, a)
	other := taskFor(t, a)
	request := toolsClient(t, a)
	var detail map[string]json.RawMessage
	if err := json.Unmarshal(request("/api/tasks/"+other.ID, "GET", nil, 200), &detail); err != nil {
		t.Fatal(err)
	}
	if _, exists := detail["runtime"]; exists {
		t.Fatal("non-Harness task gained a misleading runtime snapshot")
	}
	getRuntime := func() TaskRuntimeStatus {
		t.Helper()
		var response struct{ Runtime TaskRuntimeStatus }
		if err := json.Unmarshal(request("/api/tasks/"+harnessTask.ID, "GET", nil, 200), &response); err != nil {
			t.Fatal(err)
		}
		return response.Runtime
	}
	if got := getRuntime(); got.State != "new" || !got.CanContinue {
		t.Fatalf("new detail = %#v", got)
	}
	if _, err := a.store.Exec("UPDATE tasks SET session=? WHERE id=?", "expired-"+uid(), harnessTask.ID); err != nil {
		t.Fatal(err)
	}
	if got := getRuntime(); got.State != "closed" || got.CanContinue {
		t.Fatalf("expired detail = %#v", got)
	}
	request("/api/tasks/"+harnessTask.ID+"/messages", "POST", map[string]any{"content": "must not be queued"}, 409)
	if _, err := a.submit(harnessTask.ID, "must not be queued from Feishu", "chat", "feishu"); !errors.Is(err, errHarnessSessionClosed) {
		t.Fatalf("closed native session accepted outside HTTP: %v", err)
	}
	runs, _ := a.store.runs(harnessTask.ID)
	events, _ := a.store.events(harnessTask.ID, 0)
	if len(runs) != 0 || len(events) != 0 || len(f.inputs) != 0 {
		t.Fatalf("rejected submit changed history: runs=%d events=%d calls=%d", len(runs), len(events), len(f.inputs))
	}
	request("/api/tasks/"+harnessTask.ID+"/session/reset", "POST", map[string]any{}, 200)
	if got := getRuntime(); got.State != "new" || !got.CanContinue {
		t.Fatalf("explicit reset did not open a blank session: %#v", got)
	}
}

func TestSessionResetRejectsActiveQueuedAndArchivedTasks(t *testing.T) {
	for _, scenario := range []string{"running", "queued", "worker", "native-busy", "pending-run", "running-run", "archived", "deleted"} {
		t.Run(scenario, func(t *testing.T) {
			a := fixture(t, &fakeRunner{})
			task := createHarnessStoredTask(t, a)
			if _, err := a.store.Exec("UPDATE tasks SET session='preserve-session' WHERE id=?", task.ID); err != nil {
				t.Fatal(err)
			}
			var err error
			switch scenario {
			case "running", "queued":
				_, err = a.store.Exec("UPDATE tasks SET status=? WHERE id=?", scenario, task.ID)
			case "worker":
				a.workers[task.ID] = &worker{}
				t.Cleanup(func() { delete(a.workers, task.ID) })
			case "native-busy":
				w := harnessIOWorker()
				registerHarnessStatusFixture(t, "preserve-session", w)
				w.lease.Lock()
				defer w.lease.Unlock()
			case "pending-run", "running-run":
				status := "queued"
				if scenario == "running-run" {
					status = "running"
				}
				_, err = a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES(?,?,?,'chat','web',?,?)", uid(), task.ID, "pending input", status, now())
			case "archived":
				_, err = a.store.Exec("INSERT INTO task_preferences(task_id,archived) VALUES(?,1)", task.ID)
			case "deleted":
				_, err = a.store.Exec("INSERT INTO task_options(task_id,deleted) VALUES(?,1)", task.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			toolsClient(t, a)("/api/tasks/"+task.ID+"/session/reset", "POST", map[string]any{}, 409)
			saved, err := a.store.task(task.ID)
			if err != nil || saved.Session != "preserve-session" {
				t.Fatalf("rejected reset changed session: %#v, %v", saved, err)
			}
		})
	}
}

func TestSessionResetSerializesWithSubmissionState(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := createHarnessStoredTask(t, a)
	if _, err := a.store.Exec("UPDATE tasks SET session='old-session' WHERE id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	// Hold the same lock submit uses while accepting a message. Reset must not
	// observe the old idle state before that atomic acceptance is published.
	a.mu.Lock()
	started, returned := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		_, err := a.resetSession(task.ID)
		returned <- err
	}()
	<-started
	select {
	case err := <-returned:
		a.mu.Unlock()
		t.Fatalf("reset bypassed the submission lock: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	_, updateErr := a.store.Exec("UPDATE tasks SET status='queued' WHERE id=?", task.ID)
	a.mu.Unlock()
	if updateErr != nil {
		t.Fatal(updateErr)
	}
	select {
	case err := <-returned:
		if !errors.Is(err, errSessionResetBlocked) {
			t.Fatalf("reset missed a concurrently accepted queued message: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("reset did not resume after submission released its lock")
	}
	saved, err := a.store.task(task.ID)
	if err != nil || saved.Session != "old-session" {
		t.Fatalf("concurrent reset cleared the queued message's context: %#v, %v", saved, err)
	}
}

func TestSessionResetClosesHarnessAndRetainsHistoryWithoutReplay(t *testing.T) {
	f := &fakeRunner{}
	a := fixture(t, f)
	task := createHarnessStoredTask(t, a)
	if _, err := a.submit(task.ID, "old conversation text", "chat", "web"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.workers[task.ID] == nil
	})
	if _, err := a.store.saveNote(task.ID, "retained note", 0); err != nil {
		t.Fatal(err)
	}
	if err := a.store.writeKnowledge(Knowledge{ID: uid(), TaskID: task.ID, Title: "Kept", Content: "retained knowledge", Created: now(), Updated: now()}, true); err != nil {
		t.Fatal(err)
	}
	beforeRuns, _ := a.store.runs(task.ID)
	beforeEvents, _ := a.store.events(task.ID, 0)
	// The fixture child is the test binary: no provider access or credentials.
	c, nativeTask := harnessFixtureSetup(t, "")
	session, _, err := harnessFixtureRun(t, c, nativeTask, "fixture native conversation", func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	harnessRuntimes.Lock()
	w := harnessRuntimes.workers[session]
	harnessRuntimes.Unlock()
	if _, err := a.store.Exec("UPDATE tasks SET session=? WHERE id=?", session, task.ID); err != nil {
		t.Fatal(err)
	}
	updated, err := a.resetSession(task.ID)
	if err != nil || updated.Session != "" {
		t.Fatalf("reset failed: %#v %v", updated, err)
	}
	select {
	case <-w.done:
	case <-time.After(time.Second):
		t.Fatal("reset left the previous native process alive")
	}
	if got := harnessRuntimeStatus(Task{Engine: "deepseek-harness", Session: session}); got.State != "closed" {
		t.Fatalf("old runtime still registered after reset: %#v", got)
	}
	afterRuns, _ := a.store.runs(task.ID)
	afterEvents, _ := a.store.events(task.ID, 0)
	note, _ := a.store.note(task.ID)
	knowledge, _ := a.store.knowledgeList(task.ID)
	if len(beforeRuns) != len(afterRuns) || len(beforeEvents) != len(afterEvents) || note.Content != "retained note" || len(knowledge) != 1 || knowledge[0].Content != "retained knowledge" {
		t.Fatal("reset changed retained runs, events, note, or knowledge")
	}
	if _, err := a.submit(task.ID, "new blank conversation", "chat", "web"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.inputs) == 2
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sessions[1] != "" || strings.Contains(f.inputs[1], "old conversation text") || strings.Contains(f.inputs[1], "retained knowledge") {
		t.Fatalf("blank session replayed native/history context: session=%q input=%q", f.sessions[1], f.inputs[1])
	}
}
