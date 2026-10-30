package main

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func TestKnowledgeReadingPagesExpandOriginalsAndBoundLongCollections(t *testing.T) {
	items := []CuratedKnowledge{}
	paths := map[string]string{}
	for i := 0; i < 80; i++ {
		v := CuratedKnowledge{ID: fmt.Sprintf("note-%03d", i), Layer: "project", ScopeKey: "合成平台", Title: fmt.Sprintf("排查主题 %03d", i), Content: "# 原文主题\n\n" + strings.Repeat("仅为合成方法及其适用边界。", 1800), Evidence: "这是旧资料中的报告，未独立复验。", Applicability: "只适用于合成版本。", SourceID: "run:synthetic", Status: "observed"}
		items = append(items, v)
		paths["curated:"+v.ID] = "项目知识/合成平台/自定义子目录/" + v.Title + ".md"
	}
	landing := "项目知识/合成平台/README.md"
	pages := knowledgeReadingDocuments(items, paths, landing, "项目知识/README.md", "curated-index:project:synthetic", "合成平台", "project", "合成平台", 123)
	if len(pages) < 2 || pages[0].Path != landing {
		t.Fatal("long collection was not paginated", len(pages))
	}
	seen := map[string]bool{}
	for i, page := range pages {
		if seen[page.Path] || len(page.Content) > 128<<10 || page.Layer != "project" || page.ScopeKey != "合成平台" || page.Kind != "index" || !page.Generated {
			t.Fatal("reading page lost identity or byte bound", page.Path, len(page.Content))
		}
		seen[page.Path] = true
		if !strings.Contains(page.Content, "### 原文主题") || !strings.Contains(page.Content, "未独立复验") || !strings.Contains(page.Content, "只适用于合成版本") || !strings.Contains(page.Content, "正文摘录") {
			t.Fatal("reading body is still only an index, or lost conditions", page.Path)
		}
		navigation := []int{0}
		if i > 0 {
			navigation = append(navigation, i-1)
		}
		if i+1 < len(pages) {
			navigation = append(navigation, i+1)
		}
		for _, j := range navigation {
			if !strings.Contains(page.Content, knowledgeMarkdownLink(page.Path, pages[j].Path)) {
				t.Fatal("page navigation is not closed", page.Path, pages[j].Path)
			}
		}
	}
	for _, item := range items {
		count := 0
		for _, page := range pages {
			if strings.Contains(page.Content, "## "+item.Title+"\n") {
				count++
				if !strings.Contains(page.Content, knowledgeMarkdownLink(page.Path, paths["curated:"+item.ID])) {
					t.Fatal("reading page links a guessed rather than tracked note path", item.ID)
				}
			}
		}
		if count != 1 {
			t.Fatal("a note was lost or duplicated across reading pages", item.ID, count)
		}
	}
}

func TestKnowledgeMarkdownLinksEncodeSpecialFileNames(t *testing.T) {
	from, target := "项目/手册.md", "项目 #50% (测试)/笔记 #25% (调试)?.md"
	href := knowledgeMarkdownLink(from, target)
	u, err := url.Parse(href)
	if err != nil || u.Path != notebookLink(from, target) || u.Fragment != "" || u.RawQuery != "" || strings.ContainsAny(href, " #?()") || !strings.Contains(href, "%25") || !strings.Contains(href, "%28") || !strings.Contains(href, "%29") {
		t.Fatal("generated Markdown href did not preserve the literal filename", href, err)
	}
	c := VaultConfig{HumanLayout: true, ConsensusDirectory: "共识 #50% (C)", SystemDirectory: "系统 #50% (S)", ProjectDirectory: "项目 #50% (P)"}
	scope := "平台 #50% (A)"
	items, paths := []CuratedKnowledge{}, map[string]string{}
	for i := 0; i < 26; i++ {
		v := CuratedKnowledge{ID: fmt.Sprintf("special-%02d", i), Layer: "project", ScopeKey: scope, Title: fmt.Sprintf("启动 #50%% (验证) %02d", i), Content: "合成正文。", Status: "observed"}
		items = append(items, v)
		paths["curated:"+v.ID] = path.Join(c.ProjectDirectory, scope, "原笔记 #25% (跟踪)", v.Title+".md")
	}
	docs := append(humanCuratedIndexes(items, paths, c), knowledgeHome(c))
	known := map[string]bool{}
	for _, p := range paths {
		known[p] = true
	}
	hasPagination := false
	for _, doc := range docs {
		known[doc.Path] = true
		hasPagination = hasPagination || strings.HasSuffix(doc.ID, ":page:2")
	}
	if !hasPagination {
		t.Fatal("special-name fixture did not exercise page navigation")
	}
	for _, doc := range docs {
		for _, match := range knowledgeReadingInlineLink.FindAllStringSubmatch(doc.Content, -1) {
			href := match[2]
			u, err := url.Parse(href)
			if err != nil || u.Fragment != "" || u.RawQuery != "" || strings.ContainsAny(href, " #?()") {
				t.Fatal("home, category, table of contents or page navigation contains an ambiguous href", doc.Path, href, err)
			}
			resolved := path.Clean(path.Join(path.Dir(doc.Path), u.Path))
			if !known[resolved] {
				t.Fatal("generated href does not resolve to its exact original document after one URL decode", doc.Path, href, resolved)
			}
		}
	}
	index, original := "项目/README.md", "项目/子目录/原笔记.md"
	rebased := knowledgeReadingLinks("[原文](证据/%23%2025%25%20%28日志%29.md#原理)", original, index)
	match := knowledgeReadingInlineLink.FindStringSubmatch(rebased)
	if match == nil {
		t.Fatal("rebased body link is not valid Markdown", rebased)
	}
	u, err = url.Parse(match[2])
	if err != nil || u.Fragment != "原理" || u.Path != notebookLink(index, "项目/子目录/证据/# 25% (日志).md") || strings.ContainsAny(match[2], "() ") {
		t.Fatal("rebased body link lost encoded parentheses or its original fragment", rebased, err)
	}
}

func TestKnowledgeReadingLinksPreserveOriginalLocationAndCode(t *testing.T) {
	from, to := "项目知识/合成平台/调试/排查笔记.md", "项目知识/合成平台/README.md"
	input := "[日志](../证据/运行 01.md#失败原因)\n[原段落](#原理)\n[网站](https://example.invalid/page)\n`[代码](../不改.md)` 和 [正文](../证据/正文.md)\n````md\n[围栏](../不改.md)\n````\n[来源]: ../证据/来源.md\n"
	got := knowledgeReadingLinks(input, from, to)
	want := (&url.URL{Path: notebookLink(to, "项目知识/合成平台/证据/运行 01.md"), Fragment: "失败原因"}).String()
	fragment := (&url.URL{Path: notebookLink(to, from), Fragment: "原理"}).String()
	if !strings.Contains(got, "[日志]("+want+")") || !strings.Contains(got, "[原段落]("+fragment+")") || !strings.Contains(got, "[代码](../不改.md)") || !strings.Contains(got, "[围栏](../不改.md)") || !strings.Contains(got, "https://example.invalid/page") {
		t.Fatal("composed reading links lost original context or rewrote code", got)
	}
	if strings.Count(got, "[正文](../") != 1 || strings.Contains(got, "[来源]: ../证据/") {
		t.Fatal("ordinary and reference links did not both rebase", got)
	}
	titled := knowledgeReadingLinks("[标题](../证据/来源.md '来源说明')\n[带括号](../证据/(来源).md)", from, to)
	quoted := (&url.URL{Path: notebookLink(to, "项目知识/合成平台/证据/来源.md")}).String()
	if !strings.Contains(titled, "[标题]("+quoted+" '来源说明')") || !strings.Contains(titled, "[带括号](../证据/(来源).md)") {
		t.Fatal("optional title or unsupported nested destination was corrupted", titled)
	}
	section := knowledgeReadingSection(CuratedKnowledge{ID: "sample", Title: "合成笔记", Content: "[正文](证据/正文.md)", Evidence: "[依据](证据/依据.md)", Applicability: "[条件](证据/条件.md)"}, map[string]string{"curated:sample": from}, to)
	for _, title := range []string{"正文", "依据", "条件"} {
		want := (&url.URL{Path: notebookLink(to, path.Join(path.Dir(from), "证据", title+".md"))}).String()
		if !strings.Contains(section, "["+title+"]("+want+")") {
			t.Fatal("reading section relocated only some of its Markdown fields", title, section)
		}
	}
	value := "# heading\n````md\n# code heading\n```\n# still code\n````\n## subheading"
	formatted := knowledgeReadingExcerpt(value, 500)
	if !strings.HasPrefix(formatted, "### heading\n") || !strings.Contains(formatted, "\n# code heading\n") || !strings.Contains(formatted, "\n# still code\n") || !strings.HasSuffix(formatted, "#### subheading") {
		t.Fatal("reading section broke nested fences or heading levels", formatted)
	}
	cut := knowledgeReadingExcerpt("```go\n"+strings.Repeat("code", 100), 20)
	if !strings.Contains(cut, "\n```\n\n（此处为正文摘录") {
		t.Fatal("excerpt left an unclosed fence", cut)
	}
}

func TestKnowledgeReadingCategoryAndTwoWaySyncUseSameCanonicalNotes(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	a.updating.Store(true)
	noLegacy := false
	c := VaultConfig{RootMode: "direct", HumanLayout: true, IncludeLegacy: &noLegacy, Enabled: true, Directory: t.TempDir()}
	organizationVault(t, a, c)
	v := curatedFixture(t, a, "project", "合成平台", "verified")
	before := v.Revision
	for pass := 0; pass < 2; pass++ {
		report := syncTestVault(t, a)
		if len(report.Conflicts) != 0 || len(report.Warnings) != 0 {
			t.Fatal("reading composition introduced sync conflicts", report)
		}
		result, err := a.store.searchLibrary(context.Background(), "", "", "", false, LibrarySearchOptions{Layer: "project"})
		if err != nil || len(result.Documents) < 3 || result.Documents[0].Kind != "index" {
			t.Fatal("default category still hides reading pages", result, err)
		}
		indexID := "curated-index:project:" + hash("合成平台")[:16]
		indexCount := 0
		for _, hit := range result.Documents {
			if hit.ID == indexID {
				indexCount++
			}
			if hit.Origin == "vault" && hit.Kind == "index" {
				t.Fatal("a reading mirror appeared as another category result", hit.ID, hit.Path)
			}
		}
		if indexCount != 1 {
			t.Fatal("reading view duplicated or disappeared after sync", result.Documents)
		}
		page, err := a.store.libraryDocument(context.Background(), indexID)
		if err != nil || !strings.Contains(page.Content, v.Content) || !strings.Contains(page.Content, v.Evidence) || !strings.Contains(page.Content, v.Applicability) || page.Layer != "project" {
			t.Fatal("category's reading view is not a faithful composition", page.Path, err)
		}
		raw := readTestFile(t, filepath.Join(c.Directory, filepath.FromSlash(page.Path)))
		parsed, err := parseVaultFile(page.Path, raw)
		if err != nil || parsed.Document.Kind != "index" || parsed.Document.ID != indexID {
			t.Fatal("reading sync turned a generated view into a new curated fact", err)
		}
		canonical, err := a.store.curatedKnowledge(context.Background(), v.ID)
		if err != nil || canonical.Revision != before || canonical.Status != "verified" || canonical.Content != v.Content {
			t.Fatal("reading composition mutated canonical knowledge", canonical, err)
		}
		byPath, err := a.store.libraryDocument(context.Background(), "document:"+path.Join(configuredKnowledgeFolder(c, "project"), "合成平台/README.md"))
		if err != nil || byPath.Content != page.Content || byPath.ID != indexID || byPath.Kind != "index" || !byPath.Generated {
			t.Fatal("handbook cannot be opened through its Markdown navigation", err)
		}
	}
}
