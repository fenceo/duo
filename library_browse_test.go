package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestLibraryBrowsePagesAndSources(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	for i := 0; i < 73; i++ {
		k := Knowledge{ID: fmt.Sprintf("browse-%03d", i), TaskID: task.ID, Title: "分页测试", Content: fmt.Sprintf("第 %d 条资料", i), Status: "observed", Source: "manual", Revision: 1, Created: int64(i + 1), Updated: int64(i + 1)}
		if i%2 == 0 {
			k.Source = "auto"
		}
		if err := a.store.writeKnowledge(k, true); err != nil {
			t.Fatal(err)
		}
	}
	first, err := a.store.searchLibrary(context.Background(), "", task.ID, "knowledge", false)
	if err != nil || len(first.Documents) != 60 || first.Total != 73 || !first.Truncated || first.NextOffset != 60 {
		t.Fatalf("first page: %+v %v", first, err)
	}
	second, err := a.store.searchLibrary(context.Background(), "", task.ID, "knowledge", false, LibrarySearchOptions{Offset: first.NextOffset})
	if err != nil || len(second.Documents) != 13 || second.Truncated || second.NextOffset != 73 {
		t.Fatalf("second page: %+v %v", second, err)
	}
	seen := map[string]bool{}
	for _, d := range append(first.Documents, second.Documents...) {
		if seen[d.ID] {
			t.Fatal("overlapping pages", d.ID)
		}
		seen[d.ID] = true
	}
	for source, count := range map[string]int{"auto": 37, "manual": 36} {
		page, err := a.store.searchLibrary(context.Background(), "", task.ID, "knowledge", false, LibrarySearchOptions{Source: source})
		if err != nil || page.Total != count || len(page.Documents) != count {
			t.Fatalf("source %s: %+v %v", source, page, err)
		}
		for _, d := range page.Documents {
			if d.Source != source || d.Content != "" {
				t.Fatal("wrong source or full content leaked into listing", d.ID)
			}
		}
	}
	for _, options := range []LibrarySearchOptions{{Source: "unknown"}, {Offset: -1}, {Offset: 1000001}} {
		if _, err := a.store.searchLibrary(context.Background(), "", "", "", false, options); err == nil {
			t.Fatal("invalid filter accepted", options)
		}
	}
	// A source label must not invalidate existing Vault synchronization baselines.
	d := first.Documents[0]
	before := documentHash(d)
	d.Source = "different-display-origin"
	if documentHash(d) != before {
		t.Fatal("display metadata changed an existing document fingerprint")
	}
}

func TestLibraryPreviewRedactionAndRevisionGuard(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	k := libraryKnowledge(t, a, task, "password: hunter2\n"+strings.Repeat("历史证据。", 1500), "observed")
	request := toolsClient(t, a)
	var response struct {
		Preview   string          `json:"preview"`
		Reference string          `json:"reference"`
		Truncated bool            `json:"truncated"`
		Document  LibraryDocument `json:"document"`
	}
	url := "/api/library/reference?id=knowledge:" + k.ID + "&revision=1"
	if err := json.Unmarshal(request(url, "GET", nil, 200), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Truncated || !strings.Contains(response.Preview, "历史证据") || strings.Contains(response.Preview+response.Reference, "hunter2") || response.Document.Content != "" {
		t.Fatalf("invalid preview: truncated=%t, contains evidence=%t, secret visible=%t, raw document bytes=%d", response.Truncated, strings.Contains(response.Preview, "历史证据"), strings.Contains(response.Preview+response.Reference, "hunter2"), len(response.Document.Content))
	}
	if len([]rune(response.Preview)) > 6050 {
		t.Fatal("preview must be bounded")
	}
	k.Content = "已经修订的知识"
	if err := a.store.writeKnowledge(k, false); err != nil {
		t.Fatal(err)
	}
	request(url, "GET", nil, 409)
	request("/api/library/reference?id=knowledge:"+k.ID+"&revision=invalid", "GET", nil, 400)
	request("/api/library/search?offset=invalid", "GET", nil, 400)
}

func TestLibraryDirectReferenceMatchesSearchAndDeletion(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task, _ := knowledgeFixture(t, a)
	k := libraryKnowledge(t, a, task, "可重复引用的合成结论", "verified")
	portable := LibraryDocument{ID: "external-note", Kind: "note", TaskID: task.ID, TaskTitle: task.Title, Title: "独立文档", Status: "observed", Revision: 1, Content: "外部历史资料"}
	raw, err := json.Marshal(portable)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.Exec("INSERT INTO library_files(path,document) VALUES(?,?)", "fixture.md", string(raw)); err != nil {
		t.Fatal(err)
	}
	result, err := a.store.searchLibrary(context.Background(), "", task.ID, "", false)
	if err != nil || len(result.Documents) < 3 {
		t.Fatal(result, err)
	}
	for _, hit := range result.Documents {
		d, text, _, err := a.store.libraryReference(context.Background(), hit.ID, hit.Hash)
		if err != nil || d.Hash != hit.Hash || text == "" {
			t.Fatal("direct read differs from search", hit.ID, err)
		}
	}
	// An unrelated broken cache row must not prevent reading a known local entry.
	if _, err = a.store.Exec("INSERT INTO library_files(path,document) VALUES('broken.md','invalid json')"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = a.store.libraryReference(context.Background(), "knowledge:"+k.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.Exec("INSERT INTO task_options(task_id,deleted) VALUES(?,1) ON CONFLICT(task_id) DO UPDATE SET deleted=1", task.ID); err != nil {
		t.Fatal(err)
	}
	for _, hit := range result.Documents {
		if _, _, _, err = a.store.libraryReference(context.Background(), hit.ID, hit.Hash); err == nil {
			t.Fatal("deleted task was still readable", hit.ID)
		}
	}
}
