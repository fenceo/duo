package main

import (
	"fmt"
	"strings"
	"testing"
)

func reliabilityKnowledge(t *testing.T, a *App, task Task, title, body, status string) {
	t.Helper()
	k := Knowledge{ID: uid(), TaskID: task.ID, Title: title, Content: body, Status: status, Source: "manual", Revision: 1, Created: now(), Updated: now()}
	if err := prepareKnowledge(&k); err != nil {
		t.Fatal(err)
	}
	if err := a.store.writeKnowledge(k, true); err != nil {
		t.Fatal(err)
	}
}
func TestKnowledgeReliabilitySummaryOnlyVerified(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	for _, s := range []string{"verified", "observed", "stale"} {
		reliabilityKnowledge(t, a, task, s, "audit-"+s, s)
	}
	if _, err := a.store.Exec("INSERT INTO task_memories(run_id,task_id,payload,updated) VALUES('memory',?, ?,1)", task.ID, `{"summary":"audit-unverified-memory"}`); err != nil {
		t.Fatal(err)
	}
	p, err := a.continuationPreview(task.ID, "summary")
	if err != nil {
		t.Fatal(err)
	}
	if p.Knowledge != 1 || strings.Contains(p.Context, "audit-observed") || strings.Contains(p.Context, "audit-stale") || strings.Contains(p.Context, "audit-unverified-memory") {
		t.Fatalf("summary should contain only verified: got %d entries, observed=%v stale=%v", p.Knowledge, strings.Contains(p.Context, "audit-observed"), strings.Contains(p.Context, "audit-stale"))
	}
}

func TestKnowledgeReliabilityNotesScopeKeepsUnverifiedContextExplicit(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	for _, state := range []string{"verified", "observed", "stale"} {
		reliabilityKnowledge(t, a, task, state, "notes-"+state, state)
	}
	if _, err := a.store.Exec("INSERT INTO task_memories(run_id,task_id,payload,updated) VALUES('notes-memory',?,?,1)", task.ID, `{"summary":"notes-memory-context"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES('notes-chat',?,'excluded-chat','chat','web','done',1)", task.ID); err != nil {
		t.Fatal(err)
	}
	p, archive, err := a.buildContinuation(task, "notes")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{p.Context, archive} {
		if !strings.Contains(text, "notes-observed") || !strings.Contains(text, "notes-verified") || !strings.Contains(text, "notes-memory-context") || !strings.Contains(text, "未经独立验证") || strings.Contains(text, "notes-stale") || strings.Contains(text, "excluded-chat") {
			t.Fatalf("wrong notes scope: %s", text)
		}
	}
	if p.Knowledge != 2 || p.Runs != 0 {
		t.Fatal("notes scope must not include chat", p.Knowledge, p.Runs)
	}
}
func TestKnowledgeReliabilitySummaryBudget(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	reliabilityKnowledge(t, a, task, "valid maximum entry", strings.Repeat("x", knowledgeMaxBytes), "verified")
	if _, err := a.knowledge(task.ID, "web"); err != nil {
		t.Fatalf("valid stored knowledge should be clipped before summary submission: %v", err)
	}
}
func TestKnowledgeReliabilityExportAllKnowledge(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	tx, err := a.store.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 501; i++ {
		if _, err = tx.Exec("INSERT INTO knowledge_entries(id,task_id,title,content,status,source,run_id,revision,created,updated) VALUES(?,?,?,'body','observed','manual','',1,?,?)", uid(), task.ID, fmt.Sprintf("audit-entry-%03d", i), now()+int64(i), now()+int64(i)); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	request := toolsClient(t, a)
	body := string(request("/api/tasks/"+task.ID+"/knowledge?download=1", "GET", nil, 200))
	if got := strings.Count(body, "## audit-entry-"); got != 501 {
		t.Fatalf("full export should include 501 entries, got %d", got)
	}
}
func TestKnowledgeReliabilityContinuationTruncationVisible(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	reliabilityKnowledge(t, a, task, "long item", strings.Repeat("x", 3000), "verified")
	p, err := a.continuationPreview(task.ID, "summary")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Context, "上下文已截断") && !p.Truncated {
		t.Fatal("per-item content was clipped but context_truncated=false")
	}
}
