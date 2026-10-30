package main

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Keep filesystem-relative paths separate from their Markdown URL form. URL
// path escaping protects literal fragment/query/percent characters; Markdown
// also needs parentheses escaped so its link destination remains unambiguous.
func knowledgeMarkdownURL(u *url.URL) string {
	return strings.ReplaceAll(strings.ReplaceAll(u.String(), "(", "%28"), ")", "%29")
}

func knowledgeMarkdownLink(from, to string) string {
	return knowledgeMarkdownURL(&url.URL{Path: notebookLink(from, to)})
}

func humanCuratedIndexes(items []CuratedKnowledge, tracked map[string]string, c VaultConfig) []LibraryDocument {
	docs := []LibraryDocument{}
	for _, v := range items {
		docs = append(docs, curatedDocument(v))
	}
	paths := curatedHumanPaths(docs, c)
	for id, p := range tracked {
		paths[id] = p
	}
	out := []LibraryDocument{}
	for _, layer := range []string{"consensus", "system", "project"} {
		folder := configuredKnowledgeFolder(c, layer)
		title := map[string]string{"consensus": "平台共识", "system": "系统知识", "project": "项目知识"}[layer]
		groups := map[string][]CuratedKnowledge{}
		var updated int64
		for _, v := range items {
			if v.Layer == layer {
				groups[v.ScopeKey] = append(groups[v.ScopeKey], v)
				updated = max(updated, v.Updated)
			}
		}
		if layer == "consensus" {
			group := groups[""]
			sort.Slice(group, func(i, j int) bool { return group[i].Title < group[j].Title })
			out = append(out, knowledgeReadingDocuments(group, paths, folder+"/README.md", "", "curated-index:consensus", title, layer, "", updated)...)
			continue
		}
		keys := []string{}
		for key := range groups {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var root strings.Builder
		fmt.Fprintf(&root, "# %s\n\n按主题阅读。自动整理的内容为待核对候选；采用修订也不会自动标记已验证。\n\n", title)
		for _, scope := range keys {
			group := groups[scope]
			sort.Slice(group, func(i, j int) bool { return group[i].Title < group[j].Title })
			// A topic's landing page has its own stable identity; unusual scope
			// spellings that sanitize alike get separate landing pages.
			sub := path.Join(folder, knowledgeFilename(scope), "README.md")
			for _, other := range keys {
				if other != scope && strings.EqualFold(knowledgeFilename(other), knowledgeFilename(scope)) {
					sub = path.Join(folder, knowledgeFilename(scope), "首页-"+hash(scope)[:8]+".md")
					break
				}
			}
			fmt.Fprintf(&root, "- [%s](%s) · %d 篇\n", markdownTitle(scope), knowledgeMarkdownLink(folder+"/README.md", sub), len(group))
			var groupUpdated int64
			for _, item := range group {
				groupUpdated = max(groupUpdated, item.Updated)
			}
			out = append(out, knowledgeReadingDocuments(group, paths, sub, folder+"/README.md", "curated-index:"+layer+":"+hash(scope)[:16], scope, layer, scope, groupUpdated)...)
		}
		if len(groups) == 0 {
			root.WriteString("尚无已整理知识。可以一键整理已有候选，或手工新建笔记。\n")
		}
		d := LibraryDocument{ID: "curated-index:" + layer, Kind: "index", Layer: layer, Generated: true, Title: title + " · 阅读索引", Content: root.String(), Status: "observed", Revision: 1, Updated: updated, Origin: "local", Path: folder + "/README.md"}
		d.Hash = documentHash(d)
		out = append(out, d)
	}
	return out
}

// Reading pages compose the existing notes rather than creating new claims.
// Small facts can be read in context; canonical notes retain identity/revisions.
// Bound items and bytes per page before Markdown's 2 MiB file limit.
func knowledgeReadingDocuments(items []CuratedKnowledge, paths map[string]string, landing, parent, id, title, layer, scope string, updated int64) []LibraryDocument {
	chunks := [][]CuratedKnowledge{{}}
	bytes := 0
	tocLine := func(v CuratedKnowledge, relative string) string {
		return fmt.Sprintf("- [%s](%s) · %s\n", markdownTitle(v.Title), knowledgeMarkdownLink(relative, paths["curated:"+v.ID]), knowledgeStateLabel(v.Status))
	}
	for _, v := range items {
		size := len(knowledgeReadingSection(v, paths, landing)) + len(tocLine(v, landing))
		last := len(chunks) - 1
		if len(chunks[last]) > 0 && (len(chunks[last]) >= 24 || bytes+size > 96<<10) {
			chunks = append(chunks, nil)
			last++
			bytes = 0
		}
		chunks[last] = append(chunks[last], v)
		bytes += size
	}
	pagePath := func(i int) string {
		if i == 0 {
			return landing
		}
		return path.Join(path.Dir(landing), fmt.Sprintf("阅读手册-%s-%02d.md", hash(id)[:8], i+1))
	}
	docs := []LibraryDocument{}
	for i, chunk := range chunks {
		rel := pagePath(i)
		var b strings.Builder
		fmt.Fprintf(&b, "# %s · 阅读手册\n\n本页汇集同一项目或主题的已有知识正文、适用条件和依据，不做未经验证的语义改写。历史来源报告不等于本轮复验；状态为待核对或过时的条目须先检查适用性。\n\n共 %d 条 · 第 %d/%d 页。\n\n", markdownTitle(title), len(items), i+1, len(chunks))
		if parent != "" {
			fmt.Fprintf(&b, "[返回分类首页](%s)\n\n", knowledgeMarkdownLink(rel, parent))
		}
		if len(chunks) > 1 {
			fmt.Fprintf(&b, "[手册首页](%s)", knowledgeMarkdownLink(rel, pagePath(0)))
			if i > 0 {
				fmt.Fprintf(&b, " · [上一页](%s)", knowledgeMarkdownLink(rel, pagePath(i-1)))
			}
			if i+1 < len(chunks) {
				fmt.Fprintf(&b, " · [下一页](%s)", knowledgeMarkdownLink(rel, pagePath(i+1)))
			}
			b.WriteString("\n\n")
		}
		b.WriteString("## 本页内容\n\n")
		for _, v := range chunk {
			b.WriteString(tocLine(v, rel))
		}
		if len(chunk) == 0 {
			b.WriteString("尚无已整理知识。\n")
		}
		b.WriteByte('\n')
		for _, v := range chunk {
			b.WriteString(knowledgeReadingSection(v, paths, rel))
		}
		pageID := id
		if i > 0 {
			pageID += fmt.Sprintf(":page:%d", i+1)
		}
		pageTitle := title + " · 阅读手册"
		if len(chunks) > 1 {
			pageTitle += fmt.Sprintf(" · 第 %d/%d 页", i+1, len(chunks))
		}
		d := LibraryDocument{ID: pageID, Kind: "index", Layer: layer, ScopeKey: scope, Generated: true, Title: pageTitle, Content: b.String(), Status: "observed", Revision: 1, Updated: updated, Origin: "local", Path: rel}
		d.Hash = documentHash(d)
		docs = append(docs, d)
	}
	return docs
}

func knowledgeReadingSection(v CuratedKnowledge, paths map[string]string, index string) string {
	var b strings.Builder
	p := paths["curated:"+v.ID]
	fmt.Fprintf(&b, "## %s\n\n状态：%s · [打开原笔记](%s)", markdownTitle(v.Title), knowledgeStateLabel(v.Status), knowledgeMarkdownLink(index, p))
	if len(v.PendingRevisions) > 0 {
		fmt.Fprintf(&b, " · %d 条待合并修订", len(v.PendingRevisions))
	}
	b.WriteString("\n\n" + knowledgeReadingExcerpt(knowledgeReadingLinks(v.Content, p, index), 5000) + "\n\n")
	b.WriteString("### 适用条件\n\n" + knowledgeReadingExcerpt(knowledgeReadingLinks(v.Applicability, p, index), 1200) + "\n\n")
	b.WriteString("### 来源依据\n\n" + knowledgeReadingExcerpt(knowledgeReadingLinks(v.Evidence, p, index), 1800) + "\n\n")
	if v.SourceID != "" {
		fmt.Fprintf(&b, "来源引用：`%s`。完整来源及修订见原笔记。\n\n", markdownTitle(v.SourceID))
	}
	return b.String()
}

var knowledgeReadingInlineLink = regexp.MustCompile(`(!?\[[^\]\n]*\]\()([^\n)]*)(\))`)
var knowledgeReadingReferenceLink = regexp.MustCompile(`^([ ]{0,3}\[[^\]\n]+\]:[ \t]*)(\S+)(.*)$`)

// A reading page lives beside, rather than inside, each original note. Keep
// ordinary Markdown provenance links relative to their original note. Code is
// never rewritten; unsupported Markdown constructs remain in the original.
func knowledgeReadingLinks(value, from, to string) string {
	if from == "" || from == to {
		return value
	}
	rebase := func(raw string) string {
		destination := strings.TrimSpace(raw)
		suffix := ""
		if strings.HasPrefix(destination, "<") {
			end := strings.Index(destination, ">")
			if end < 0 {
				return raw
			}
			suffix, destination = destination[end+1:], destination[1:end]
		} else if i := strings.Index(destination, ` "`); i >= 0 {
			suffix, destination = destination[i:], destination[:i]
		} else if i := strings.Index(destination, " '"); i >= 0 {
			suffix, destination = destination[i:], destination[:i]
		}
		if strings.ContainsAny(destination, "()") {
			// The simple inline matcher does not parse nested parentheses. Leave
			// those constructs intact rather than inventing a different path.
			return raw
		}
		u, err := url.Parse(destination)
		if err != nil || u.IsAbs() || u.Host != "" || strings.HasPrefix(u.Path, "/") || strings.ContainsAny(u.Path, "\\\x00:") {
			return raw
		}
		target := from
		if u.Path != "" {
			target = path.Clean(path.Join(path.Dir(from), u.Path))
		}
		if !validCuratedTrackedPath(target) {
			return raw
		}
		u.Path, u.RawPath = notebookLink(to, target), ""
		return knowledgeMarkdownURL(u) + suffix
	}
	lines := strings.Split(value, "\n")
	fence := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			kind, n := trimmed[0], 0
			for n < len(trimmed) && trimmed[n] == kind {
				n++
			}
			if fence == "" {
				fence = trimmed[:n]
			} else if kind == fence[0] && n >= len(fence) && strings.TrimSpace(trimmed[n:]) == "" {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		// Skip inline code spans as well as fenced command examples.
		var out strings.Builder
		start, delimiter := 0, 0
		for j := 0; j < len(line); {
			if line[j] != '`' {
				j++
				continue
			}
			end := j
			for end < len(line) && line[end] == '`' {
				end++
			}
			if delimiter == 0 {
				out.WriteString(knowledgeReadingInlineLink.ReplaceAllStringFunc(line[start:j], func(link string) string {
					m := knowledgeReadingInlineLink.FindStringSubmatch(link)
					return m[1] + rebase(m[2]) + m[3]
				}))
				start, delimiter = j, end-j
			} else if end-j == delimiter {
				out.WriteString(line[start:end])
				start, delimiter = end, 0
			}
			j = end
		}
		if delimiter == 0 {
			out.WriteString(knowledgeReadingInlineLink.ReplaceAllStringFunc(line[start:], func(link string) string {
				m := knowledgeReadingInlineLink.FindStringSubmatch(link)
				return m[1] + rebase(m[2]) + m[3]
			}))
		} else {
			out.WriteString(line[start:])
		}
		line = out.String()
		if m := knowledgeReadingReferenceLink.FindStringSubmatch(line); m != nil {
			line = m[1] + rebase(m[2]) + m[3]
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

func knowledgeReadingExcerpt(value string, limit int) string {
	runes := []rune(value)
	cut := len(runes) > limit
	if cut {
		value = string(runes[:limit])
	}
	lines := strings.Split(value, "\n")
	fence := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			kind := trimmed[0]
			n := 0
			for n < len(trimmed) && trimmed[n] == kind {
				n++
			}
			if fence == "" {
				fence = trimmed[:n]
			} else if kind == fence[0] && n >= len(fence) && strings.TrimSpace(trimmed[n:]) == "" {
				fence = ""
			}
			continue
		}
		if fence == "" && strings.HasPrefix(line, "#") {
			n := len(line) - len(strings.TrimLeft(line, "#"))
			if n <= 6 && n < len(line) && line[n] == ' ' {
				lines[i] = strings.Repeat("#", min(6, n+2)) + line[n:]
			}
		}
	}
	value = strings.Join(lines, "\n")
	if fence != "" {
		value += "\n" + fence
	}
	if cut {
		value += "\n\n（此处为正文摘录，完整内容请打开原笔记。）"
	}
	return value
}

// IDs belong in frontmatter. Only real filename collisions need a short suffix.
func knowledgeFilename(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune("<>:\"/\\|?*", r) {
			return '-'
		}
		return r
	}, value)
	value = strings.Trim(value, " .-")
	if utf8.RuneCountInString(value) > 70 {
		value = string([]rune(value)[:70])
	}
	value = strings.Trim(value, " .-")
	if value == "" {
		value = "未分类"
	}
	stem := strings.ToUpper(strings.SplitN(value, ".", 2)[0])
	if stem == "README" || strings.HasPrefix(stem, "README-") || strings.HasPrefix(value, "首页") || stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || (len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9') {
		value = "_" + value
	}
	return value
}

func curatedHumanPaths(docs []LibraryDocument, c VaultConfig) map[string]string {
	groups := map[string][]LibraryDocument{}
	for _, d := range docs {
		if d.Kind == "curated" {
			key := strings.ToLower(configuredCuratedPath(c, d))
			groups[key] = append(groups[key], d)
		}
	}
	out := map[string]string{}
	reserved := map[string]bool{}
	for key, group := range groups {
		if len(group) == 1 {
			out[group[0].ID] = configuredCuratedPath(c, group[0])
			reserved[key] = true
		}
	}
	duplicates := []LibraryDocument{}
	for _, group := range groups {
		if len(group) > 1 {
			duplicates = append(duplicates, group...)
		}
	}
	sort.Slice(duplicates, func(i, j int) bool { return duplicates[i].ID < duplicates[j].ID })
	for _, d := range duplicates {
		base := strings.TrimSuffix(configuredCuratedPath(c, d), ".md") + "-" + hash(d.ID)[:8]
		for n := 0; ; n++ {
			name := base + ".md"
			if n > 0 {
				name = fmt.Sprintf("%s-%d.md", base, n)
			}
			if !reserved[strings.ToLower(name)] {
				reserved[strings.ToLower(name)] = true
				out[d.ID] = name
				break
			}
		}
	}
	return out
}

func knowledgeHome(c VaultConfig) LibraryDocument {
	var b strings.Builder
	b.WriteString("# 知识库\n\n任务过程保存在工作区的任务记录中。本目录只整理可复用笔记；待核对候选与已确认知识分别显示，不把自动整理当作事实验证。\n\n")
	for _, layer := range []struct{ id, title string }{{"consensus", "平台共识"}, {"system", "系统知识"}, {"project", "项目知识"}} {
		b.WriteString("- [" + layer.title + "](" + knowledgeMarkdownLink("首页.md", path.Join(configuredKnowledgeFolder(c, layer.id), "README.md")) + ")\n")
	}
	d := LibraryDocument{ID: "curated-index:home", Kind: "index", Layer: "files", Generated: true, Title: "知识库首页", Content: b.String(), Status: "observed", Revision: 1, Origin: "local", Path: "首页.md"}
	d.Hash = documentHash(d)
	return d
}
