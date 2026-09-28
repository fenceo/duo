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
	p, err := a.continuationPreview(task.ID, "summary")
	if err != nil {
		t.Fatal(err)
	}
	if p.Knowledge != 1 || strings.Contains(p.Context, "audit-observed") || strings.Contains(p.Context, "audit-stale") {
		t.Fatalf("summary should contain only verified: got %d entries, observed=%v stale=%v", p.Knowledge, strings.Contains(p.Context, "audit-observed"), strings.Contains(p.Context, "audit-stale"))
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
