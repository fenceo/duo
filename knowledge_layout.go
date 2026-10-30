package main

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

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
			if layer == "consensus" {
				root.WriteString(knowledgeIndexEntries(group, paths, folder+"/README.md"))
				continue
			}
			// A topic's landing page has its own stable identity; unusual scope
			// spellings that sanitize alike get separate landing pages.
			sub := path.Join(folder, knowledgeFilename(scope), "README.md")
			for _, other := range keys {
				if other != scope && strings.EqualFold(knowledgeFilename(other), knowledgeFilename(scope)) {
					sub = path.Join(folder, knowledgeFilename(scope), "首页-"+hash(scope)[:8]+".md")
					break
				}
			}
			fmt.Fprintf(&root, "- [%s](%s) · %d 篇\n", markdownTitle(scope), notebookLink(folder+"/README.md", sub), len(group))
			body := "# " + markdownTitle(scope) + "\n\n[返回分类首页](" + notebookLink(sub, folder+"/README.md") + ")\n\n" + knowledgeIndexEntries(group, paths, sub)
			d := LibraryDocument{ID: "curated-index:" + layer + ":" + hash(scope)[:16], Kind: "index", Layer: "files", Generated: true, Title: scope + " · 知识索引", Content: body, Status: "observed", Revision: 1, Updated: updated, Origin: "local", Path: sub}
			d.Hash = documentHash(d)
			out = append(out, d)
		}
		if len(groups) == 0 {
			root.WriteString("尚无已整理知识。可以一键整理已有候选，或手工新建笔记。\n")
		}
		d := LibraryDocument{ID: "curated-index:" + layer, Kind: "index", Layer: "files", Generated: true, Title: title + " · 阅读索引", Content: root.String(), Status: "observed", Revision: 1, Updated: updated, Origin: "local", Path: folder + "/README.md"}
		d.Hash = documentHash(d)
		out = append(out, d)
	}
	return out
}

func knowledgeIndexEntries(items []CuratedKnowledge, paths map[string]string, index string) string {
	var b strings.Builder
	for _, v := range items {
		p := paths["curated:"+v.ID]
		fmt.Fprintf(&b, "## [%s](%s)\n\n状态：%s", markdownTitle(v.Title), notebookLink(index, p), knowledgeStateLabel(v.Status))
		if len(v.PendingRevisions) > 0 {
			fmt.Fprintf(&b, " · %d 条待合并修订", len(v.PendingRevisions))
		}
		b.WriteString("\n\n")
		for _, line := range strings.Split(v.Content, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				summary, _ := clipContinuation(line, 160)
				b.WriteString(markdownTitle(summary) + "\n\n")
				break
			}
		}
	}
	return b.String()
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
		b.WriteString("- [" + layer.title + "](" + path.Join(configuredKnowledgeFolder(c, layer.id), "README.md") + ")\n")
	}
	d := LibraryDocument{ID: "curated-index:home", Kind: "index", Layer: "files", Generated: true, Title: "知识库首页", Content: b.String(), Status: "observed", Revision: 1, Origin: "local", Path: "首页.md"}
	d.Hash = documentHash(d)
	return d
}
