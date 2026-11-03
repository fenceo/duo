package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func qualityInsert(t *testing.T, a *App, item CuratedKnowledge, deleted bool) {
	t.Helper()
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.store.Exec("INSERT INTO curated_knowledge(id,layer,scope_key,document,revision,updated,deleted) VALUES(?,?,?,?,1,1,?)", item.ID, item.Layer, item.ScopeKey, string(raw), deleted)
	if err != nil {
		t.Fatal(err)
	}
}

func TestKnowledgeQualityFindsEmptyAndScopedDuplicatesWithoutWriting(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	base := CuratedKnowledge{Layer: "system", ScopeKey: "Linux", Status: "observed", Title: "memory"}
	entries := []struct{ id, body string }{
		{"empty", " \n<!-- placeholder -->\n# memory\n## Steps\n"},
		{"title", "memory"},
		{"setext", "memory\n======\n"},
		{"short", "free -m"},
		{"code", "```sh\n# command comment\nfree -m\n```"},
		{"indented-comment", "    <!-- literal code comment -->"},
		{"link", "[Linux documentation](https://example.invalid/memory)"},
		{"first", "Check memory.\r\nKeep the units."},
		{"second", "\nCheck memory.\nKeep the units.\n"},
	}
	for _, entry := range entries {
		v := base
		v.ID, v.Content = entry.id, entry.body
		if entry.id == "second" {
			v.Evidence, v.Applicability = "different source", "different device"
		}
		qualityInsert(t, a, v, false)
	}
	for _, spec := range []struct {
		id, layer, scope string
		deleted          bool
	}{
		{"other-project", "project", "Linux", false},
		{"other-scope", "system", "BSD", false},
		{"retired", "system", "Linux", true},
	} {
		v := base
		v.ID, v.Layer, v.ScopeKey, v.Content = spec.id, spec.layer, spec.scope, "Check memory.\nKeep the units."
		qualityInsert(t, a, v, spec.deleted)
	}
	before := qualityDatabaseSnapshot(t, a)
	request := toolsClient(t, a)
	var report KnowledgeQualityReport
	if err := json.Unmarshal(request("/api/library/quality", "GET", nil, 200), &report); err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 11 || report.EmptyCount != 3 || report.DuplicateGroupCount != 1 || report.ScanTruncated || report.IssuesTruncated {
		t.Fatalf("unexpected report: %+v", report)
	}
	group := report.DuplicateGroups[0]
	if group.Layer != "system" || group.ScopeKey != "Linux" || group.Count != 2 || len(group.Items) != 2 || group.Items[0].ID != "curated:first" || group.Items[1].ID != "curated:second" {
		t.Fatalf("duplicate scope or identity lost: %+v", group)
	}
	if !strings.Contains(report.Notice, "适用条件仍可能不同") || !strings.Contains(report.Notice, "不扫描笔记目录") {
		t.Fatal("diagnostic boundaries missing", report.Notice)
	}
	if after := qualityDatabaseSnapshot(t, a); after != before {
		t.Fatal("read-only inspection changed knowledge")
	}
	w := httptest.NewRecorder()
	(&Server{app: a}).Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/library/quality", nil))
	if w.Code != 401 && w.Code != 403 {
		t.Fatal("quality report is not authenticated", w.Code)
	}
}

func qualityDatabaseSnapshot(t *testing.T, a *App) string {
	t.Helper()
	rows, err := a.store.Query("SELECT id,document,revision,updated,deleted FROM curated_knowledge ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out strings.Builder
	for rows.Next() {
		var id, raw string
		var revision, updated int64
		var deleted int
		if err := rows.Scan(&id, &raw, &revision, &updated, &deleted); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&out, "%s:%s:%d:%d:%d\n", id, raw, revision, updated, deleted)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestKnowledgeQualityPreservesShortCodeLinksAndCase(t *testing.T) {
	for _, body := range []string{"x", "#define FOO 1", "    # shell comment", "\t# indented code", "    <!-- literal comment -->", "[reference](file.md)", "```\n# code\n```", "~~~html\n<!-- literal comment -->\n~~~", "| A | B |", "a\n---\nActual text"} {
		if knowledgeQualityEmpty(body, "title") {
			t.Errorf("meaningful brief body classified empty: %q", body)
		}
	}
	if !knowledgeQualityEmpty("<!--\n    comment contents\n```\n-->\n# outline", "title") {
		t.Fatal("code-like text inside an HTML comment must remain a comment")
	}
	if normalizeKnowledgeQualityBody("Echo A") == normalizeKnowledgeQualityBody("echo a") || normalizeKnowledgeQualityBody("a\n    b") == normalizeKnowledgeQualityBody("a\nb") || normalizeKnowledgeQualityBody("    b") == normalizeKnowledgeQualityBody("b") {
		t.Fatal("case or code indentation was normalized away")
	}
}

func TestKnowledgeQualityBoundsScanAndOutput(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	for i := 0; i < 45; i++ {
		qualityInsert(t, a, CuratedKnowledge{ID: fmt.Sprintf("empty-%03d", i), Layer: "system", ScopeKey: "Linux", Title: "empty", Content: "# empty"}, false)
		for j := 0; j < 10; j++ {
			qualityInsert(t, a, CuratedKnowledge{ID: fmt.Sprintf("group-%03d-%02d", i, j), Layer: "system", ScopeKey: "Linux", Title: "body", Content: fmt.Sprintf("Actual explanation %03d.", i)}, false)
		}
	}
	report, err := a.store.knowledgeQuality(context.Background())
	if err != nil || report.EmptyCount != 45 || report.DuplicateGroupCount != 45 || len(report.Empty) != 40 || len(report.DuplicateGroups) != 40 || !report.IssuesTruncated {
		t.Fatal("unbounded output or incorrect scanned counts", report, err)
	}
	for _, group := range report.DuplicateGroups {
		if group.Count != 10 || len(group.Items) != 8 || !group.ItemsTruncated {
			t.Fatal("duplicate members are not bounded", group)
		}
	}
	report, err = a.store.knowledgeQualityWithLimits(context.Background(), 3, 1<<20)
	if err != nil || report.Scanned != 3 || report.ScanLimit != 3 || !report.ScanTruncated {
		t.Fatal("entry scan limit ignored", report, err)
	}
	report, err = a.store.knowledgeQualityWithLimits(context.Background(), 2000, 1)
	if err != nil || report.Scanned != 0 || !report.ScanTruncated {
		t.Fatal("byte scan limit ignored", report, err)
	}
}
