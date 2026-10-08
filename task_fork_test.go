package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTaskForkCopiesHistoryAndContinuesIndependently(t *testing.T) {
	runner := &switchRunner{}
	a, source := switchFixture(t, runner)
	for index := 0; index < 31; index++ {
		id := fmt.Sprintf("history-%02d", index)
		if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,created,finished) VALUES(?,?,?,'chat','web','done',?,?,?)", id, source.ID, id, "answer-"+id, index/2+1, index/2+2); err != nil {
			t.Fatal(err)
		}
		if err := a.store.event(source.ID, id, "tool", "tool-"+id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.store.Exec(`INSERT INTO attachments VALUES('original-file',?,'note.txt','text/plain',?,1);
INSERT INTO run_options VALUES('history-00','null','[{"id":"original-file","name":"note.txt","mime":"text/plain","size":5}]');
INSERT INTO run_metrics VALUES('history-00',1,'{"input":10,"output":20,"total":30}');
INSERT INTO task_memories VALUES('history-00',?,'{"summary":"inherited memory"}',1);
INSERT INTO knowledge_entries VALUES('original-note',?,'Old knowledge','old content','verified','run','history-00',1,1,1);`, source.ID, []byte("hello"), source.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	a.store.event(source.ID, "history-00", "session", "native-original")
	a.store.event(source.ID, "history-00", "question", "old interactive request")
	before, err := a.store.usageSummary(t.Context(), "", 1, time.UTC, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	target, err := a.forkTask(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if target.ID == source.ID || target.Session != "" || target.Binding.HistoryID == "" || target.Binding.Revision == source.Binding.Revision || target.Workspace != source.Workspace || target.Archived || target.Pinned {
		t.Fatalf("wrong branch identity: %+v", target)
	}
	if len(runner.calls) != 0 {
		t.Fatal("fork invoked the model")
	}
	oldRuns, _ := a.store.runs(source.ID)
	newRuns, err := a.store.runs(target.ID)
	if err != nil || len(newRuns) != 31 {
		t.Fatal("fork copied only the visible history page", len(newRuns), err)
	}
	for index, run := range newRuns {
		if run.ID == oldRuns[index].ID || run.Input != oldRuns[index].Input || run.Result != oldRuns[index].Result || run.TaskID != target.ID {
			t.Fatal("history order or independence changed", run)
		}
	}
	files, err := a.store.messageAttachments(target.ID, []string{newRuns[0].Attachments[0].ID})
	if err != nil || files[0].ID == "original-file" {
		t.Fatal("attachment still belongs to original task", err)
	}
	var content string
	if err = a.store.QueryRow("SELECT CAST(data AS TEXT) FROM attachments WHERE id=? AND task_id=?", files[0].ID, target.ID).Scan(&content); err != nil || content != "hello" {
		t.Fatal("attachment data was not copied", content, err)
	}
	var count int
	a.store.QueryRow("SELECT count(*) FROM events WHERE task_id=? AND kind IN ('session','question')", target.ID).Scan(&count)
	if count != 0 {
		t.Fatal("branch inherited live session or interactive request")
	}
	var linkedRun string
	a.store.QueryRow("SELECT run_id FROM knowledge_entries WHERE task_id=?", target.ID).Scan(&linkedRun)
	if linkedRun != newRuns[0].ID {
		t.Fatal("knowledge source did not map to copied run")
	}
	after, err := a.store.usageSummary(t.Context(), "", 1, time.UTC, time.Now())
	if err != nil || after.Total != before.Total {
		t.Fatal("fork double-counted usage", before.Total, after.Total, err)
	}
	// Parent edits and new messages after the fork must never change its archive.
	a.store.Exec("UPDATE runs SET input='changed later' WHERE id='history-00'; UPDATE knowledge_entries SET content='changed later' WHERE id='original-note'")
	a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES('after-fork',?,'parent future','chat','web','done',?)", source.ID, now())
	run, err := a.submit(target.ID, "branch future", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		var status string
		a.store.QueryRow("SELECT status FROM runs WHERE id=?", run.ID).Scan(&status)
		return status == "done"
	})
	runner.mu.Lock()
	call := runner.calls[0]
	runner.mu.Unlock()
	if call.task.Session != "" || !strings.Contains(call.archive, "history-00") || !strings.Contains(call.archive, "history-30") || !strings.Contains(call.archive, "old content") || strings.Contains(call.archive, "changed later") || strings.Contains(call.archive, "parent future") || !strings.Contains(call.input, "branch future") {
		t.Fatalf("branch did not receive its frozen history: %+v", call)
	}
	unchanged, _ := a.store.task(source.ID)
	if unchanged.Session != source.Session {
		t.Fatal("fork reset the original native session")
	}
	a.store.QueryRow("SELECT count(*) FROM runs WHERE task_id=? AND input='branch future'", source.ID).Scan(&count)
	if count != 0 {
		t.Fatal("new branch messages appeared in parent")
	}
}

func TestTaskForkRejectsBusyAndRollsBackMissingAttachments(t *testing.T) {
	a, task := switchFixture(t, &switchRunner{})
	a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES('pending',?,'pending','chat','web','queued',1)", task.ID)
	if _, err := a.forkTask(task.ID); !errors.Is(err, errTaskForkBlocked) {
		t.Fatal("queued work was copied", err)
	}
	a.store.Exec("UPDATE runs SET status='done' WHERE id='pending'; INSERT INTO run_options VALUES('pending','null','[{\"id\":\"missing\",\"name\":\"missing.txt\"}]')")
	if _, err := a.forkTask(task.ID); !errors.Is(err, errTaskForkBlocked) {
		t.Fatal("missing attachment did not abort", err)
	}
	var count int
	a.store.QueryRow("SELECT count(*) FROM tasks").Scan(&count)
	if count != 1 {
		t.Fatal("failed fork left a partial task", count)
	}
}

func TestTaskForkKeepsVisibleRepliesAndStructuredMemory(t *testing.T) {
	a, source := switchFixture(t, &switchRunner{})
	const originalRun = "original-memory-run"
	const visible = "visible historical reply"
	const memory = `{"summary":"saved structured memory"}`
	marker := memoryMarker(originalRun)
	raw := visible + "\n\n" + marker + "\n" + memory + "\n<!-- /duo-memory:" + originalRun + " -->"
	partial := visible + "\n\n" + marker[:len(marker)-4]
	if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,created,finished) VALUES(?,?,'question','chat','web','done',?,1,2)", originalRun, source.ID, raw); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{partial, raw} {
		if err := a.store.event(source.ID, originalRun, "assistant", text); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.store.Exec("INSERT INTO task_memories VALUES(?,?,?,2)", originalRun, source.ID, memory); err != nil {
		t.Fatal(err)
	}
	branch, err := a.forkTask(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := a.store.runs(branch.ID)
	if err != nil || len(runs) != 1 || runs[0].Result != visible {
		t.Fatal("copied result exposed old memory protocol", runs, err)
	}
	events, err := a.store.events(branch.ID, 0)
	if err != nil || len(events) != 2 {
		t.Fatal("historical events were lost", events, err)
	}
	for _, event := range events {
		if event.Text != visible || event.RunID != runs[0].ID {
			t.Fatal("copied event exposed a full or partial old memory marker", event)
		}
	}
	var payload, archive string
	if err = a.store.QueryRow("SELECT payload FROM task_memories WHERE task_id=? AND run_id=?", branch.ID, runs[0].ID).Scan(&payload); err != nil || payload != memory {
		t.Fatal("structured memory did not survive the fork", payload, err)
	}
	if err = a.store.QueryRow("SELECT archive FROM task_handoff_context WHERE task_id=?", branch.ID).Scan(&archive); err != nil || strings.Contains(archive, "<!-- duo-") || !strings.Contains(archive, visible) || !strings.Contains(archive, "saved structured memory") {
		t.Fatal("frozen archive exposed memory protocol or lost context", archive, err)
	}
	originalRuns, err := a.store.runs(source.ID)
	if err != nil || originalRuns[0].Result != raw {
		t.Fatal("original result changed", originalRuns, err)
	}
	originalEvents, err := a.store.events(source.ID, 0)
	if err != nil || len(originalEvents) != 2 || originalEvents[0].Text != partial || originalEvents[1].Text != raw {
		t.Fatal("original events changed", originalEvents, err)
	}
}

func TestTaskForkHTTPAuthenticationAndArchivedSource(t *testing.T) {
	a, source := switchFixture(t, &switchRunner{})
	w := httptest.NewRecorder()
	(&Server{app: a}).Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/tasks/"+source.ID+"/fork", strings.NewReader("{}")))
	if w.Code != 401 && w.Code != 403 {
		t.Fatal("fork was not authenticated", w.Code)
	}
	request := toolsClient(t, a)
	request("/api/tasks/missing/fork", "POST", map[string]any{}, 404)
	a.store.Exec("INSERT INTO task_preferences VALUES(?,1,1)", source.ID)
	var branch Task
	if err := json.Unmarshal(request("/api/tasks/"+source.ID+"/fork", "POST", map[string]any{}, 201), &branch); err != nil || branch.Archived || branch.Pinned {
		t.Fatal("archived source could not create active branch", branch, err)
	}
	a.store.Exec("INSERT INTO task_options(task_id,deleted) VALUES(?,1) ON CONFLICT(task_id) DO UPDATE SET deleted=1", source.ID)
	request("/api/tasks/"+source.ID+"/fork", "POST", map[string]any{}, 409)
}
