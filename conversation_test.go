package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func conversationFixture(t *testing.T) *Store {
	t.Helper()
	s, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	tx, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, task := range []string{"conversation", "other"} {
		if _, err = tx.Exec("INSERT INTO tasks(id,title,workspace,model,created,updated) VALUES(?,?,?,'',1,1)", task, task, "<workspace>"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 9; i++ {
		id := fmt.Sprintf("run-%02d", i)
		result := "final answer " + id
		if _, err = tx.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,created,finished) VALUES(?,'conversation',?,'chat','web','done',?,?,20)", id, "request "+id, result, i/2+1); err != nil {
			t.Fatal(err)
		}
		for n := 0; n < 225; n++ {
			kind, text := "tool", strings.Repeat("synthetic output ", 256)
			if n == 0 {
				kind, text = "user", "request "+id
			}
			if n == 224 {
				kind, text = "assistant", result
			}
			if _, err = tx.Exec("INSERT INTO events(task_id,run_id,kind,text,created) VALUES('conversation',?,?,?,?)", id, kind, text, i*225+n+1); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = tx.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES('foreign','other','private','chat','web','done',1)"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestConversationRecentWindowAndHistory(t *testing.T) {
	s := conversationFixture(t)
	runs, events, window, err := s.conversationPage(context.Background(), "conversation", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 5 || runs[0].ID != "run-04" || runs[4].ID != "run-08" || !window.HasOlder || window.Before != "run-04" || window.Sequence != 2025 {
		t.Fatalf("unexpected initial window: %#v %#v", runs, window)
	}
	if len(events) != 505 {
		t.Fatalf("got %d events, want five bounded pages with pinned requests", len(events))
	}
	for _, event := range events {
		if event.Kind == "tool" && (!event.Truncated || len(event.Text) != 240) {
			t.Fatal("large tools should return only a preview")
		}
	}
	for _, run := range runs {
		user, final := false, false
		for _, event := range events {
			if event.RunID == run.ID {
				user = user || event.Kind == "user"
				final = final || event.Kind == "assistant" && event.Text == run.Result
			}
		}
		if !user || !final || !window.Records[run.ID].HasOlder {
			t.Fatal("request, final answer or older-page cursor is missing")
		}
	}
	payload, _ := json.Marshal(map[string]any{"runs": runs, "events": events, "conversation": window})
	if len(payload) > 200000 {
		t.Fatalf("initial payload is unexpectedly large: %d", len(payload))
	}
	legacyRuns, _ := s.runs("conversation")
	legacyEvents, _ := s.events("conversation", 0)
	legacy, _ := json.Marshal(map[string]any{"runs": legacyRuns, "events": legacyEvents})
	if len(payload)*5 >= len(legacy) {
		t.Fatalf("expected a substantial first-load reduction: %d vs %d bytes", len(payload), len(legacy))
	}
	older, _, oldWindow, err := s.conversationPage(context.Background(), "conversation", url.Values{"before": {window.Before}})
	if err != nil || len(older) != 4 || older[0].ID != "run-00" || older[3].ID != "run-03" || oldWindow.HasOlder {
		t.Fatalf("stable run pagination failed: %v %#v %#v", err, older, oldWindow)
	}
	// Equal creation times must neither skip nor repeat runs at the boundary.
	_, history, records, err := s.conversationPage(context.Background(), "conversation", url.Values{"run": {"run-08"}, "before_event": {strconv.FormatInt(window.Records["run-08"].Before, 10)}})
	if err != nil || len(history) != 100 || !records.Records["run-08"].HasOlder {
		t.Fatalf("older records: %v %#v", err, records)
	}
	_, last, lastWindow, err := s.conversationPage(context.Background(), "conversation", url.Values{"run": {"run-08"}, "before_event": {strconv.FormatInt(records.Records["run-08"].Before, 10)}})
	if err != nil || len(last) != 25 || lastWindow.Records["run-08"].HasOlder {
		t.Fatalf("last records: %v %#v", err, lastWindow)
	}
	var original string
	if err = s.QueryRow("SELECT text FROM events WHERE seq=?", history[0].Seq).Scan(&original); err != nil || len(original) <= 240 {
		t.Fatal("preview projection must preserve original output")
	}
	t.Logf("recent window: %d runs, %d event previews, %d JSON bytes (legacy first page %d) for 2025 stored events", len(runs), len(events), len(payload), len(legacy))
}

func TestConversationPollingHighWaterAndActiveRuns(t *testing.T) {
	s := conversationFixture(t)
	ctx := context.Background()
	_, _, initial, err := s.conversationPage(ctx, "conversation", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	_, events, page, err := s.conversationPage(ctx, "conversation", url.Values{"after": {strconv.FormatInt(initial.Sequence, 10)}})
	if err != nil || len(events) != 0 || page.HasMore || page.Sequence != initial.Sequence {
		t.Fatal("idle polling replayed intentionally unloaded history")
	}
	if _, err = s.Exec("UPDATE runs SET status='running' WHERE id='run-00'"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 250; i++ {
		if err = s.event("conversation", "run-00", "tool", "new delta"); err != nil {
			t.Fatal(err)
		}
	}
	runs, events, page, err := s.conversationPage(ctx, "conversation", url.Values{"after": {strconv.FormatInt(initial.Sequence, 10)}})
	if err != nil || len(runs) != 6 || len(events) != 200 || !page.HasMore || events[0].Seq != initial.Sequence+1 || page.Sequence != initial.Sequence+200 {
		t.Fatalf("delta page incorrect: %v %#v", err, page)
	}
	_, tail, tailPage, err := s.conversationPage(ctx, "conversation", url.Values{"after": {strconv.FormatInt(page.Sequence, 10)}})
	if err != nil || len(tail) != 50 || tailPage.HasMore || tail[0].Seq != page.Sequence+1 {
		t.Fatalf("delta tail incorrect: %v %#v", err, tailPage)
	}
}

func TestConversationReadIsolationAndKnowledgeMetadata(t *testing.T) {
	s := conversationFixture(t)
	ctx := context.Background()
	if _, _, _, err := s.conversationPage(ctx, "conversation", url.Values{"before": {"foreign"}}); err == nil {
		t.Fatal("foreign cursor accepted")
	}
	for _, query := range []url.Values{{"after": {"bad"}}, {"before_event": {"-1"}}, {"after": {"1"}, "run": {"run-00"}}} {
		if _, _, _, err := s.conversationPage(ctx, "conversation", query); err != errConversationCursor {
			t.Fatalf("invalid cursor accepted: %v", err)
		}
	}
	server := &Server{app: &App{store: s}}
	for _, tc := range []struct {
		task string
		code int
	}{{"conversation", 200}, {"other", 404}} {
		r := httptest.NewRequest("GET", "/api/tasks/"+tc.task+"?event=2", nil)
		r.SetPathValue("id", tc.task)
		w := httptest.NewRecorder()
		server.detail(w, r)
		if w.Code != tc.code {
			t.Fatalf("full event for %s: %d", tc.task, w.Code)
		}
		if tc.code == 200 {
			var event Event
			if err := json.Unmarshal(w.Body.Bytes(), &event); err != nil || event.Truncated || len(event.Text) <= 240 {
				t.Fatal("full record missing")
			}
		}
	}
	if _, err := s.Exec("INSERT INTO knowledge_entries(id,task_id,title,content) VALUES('note','conversation','fixture',?)", strings.Repeat("long note ", 10000)); err != nil {
		t.Fatal(err)
	}
	brief, err := s.knowledgeListPage("conversation", false, true)
	if err != nil || len(brief) != 1 || brief[0].Content != "" {
		t.Fatal("knowledge metadata should not load bodies")
	}
	full, err := s.knowledgeListPage("conversation", false)
	if err != nil || len(full) != 1 || len(full[0].Content) != 100000 {
		t.Fatal("full knowledge must remain readable")
	}
}
