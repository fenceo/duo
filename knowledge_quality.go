package main

import (
	"context"
	"net/http"
	"sort"
	"strings"
)

const (
	knowledgeQualityScanLimit  = 2000
	knowledgeQualityByteLimit  = 32 << 20
	knowledgeQualityIssueLimit = 40
	knowledgeQualityGroupLimit = 8
)

type KnowledgeQualityItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Layer    string `json:"layer"`
	ScopeKey string `json:"scope_key"`
	Status   string `json:"status"`
}

type KnowledgeQualityDuplicate struct {
	BodyHash       string                 `json:"body_hash"`
	Layer          string                 `json:"layer"`
	ScopeKey       string                 `json:"scope_key"`
	Count          int                    `json:"count"`
	Items          []KnowledgeQualityItem `json:"items"`
	ItemsTruncated bool                   `json:"items_truncated"`
}

type KnowledgeQualityReport struct {
	Scanned             int                         `json:"scanned"`
	ScanLimit           int                         `json:"scan_limit"`
	ScanTruncated       bool                        `json:"scan_truncated"`
	EmptyCount          int                         `json:"empty_count"`
	DuplicateGroupCount int                         `json:"duplicate_group_count"`
	IssuesTruncated     bool                        `json:"issues_truncated"`
	Empty               []KnowledgeQualityItem      `json:"empty"`
	DuplicateGroups     []KnowledgeQualityDuplicate `json:"duplicate_groups"`
	Notice              string                      `json:"notice"`
}

// This is a bounded, read-only inspection of active curated database entries.
// It neither walks the vault nor diagnoses archived files or semantic quality.
func (s *Store) knowledgeQuality(ctx context.Context) (KnowledgeQualityReport, error) {
	return s.knowledgeQualityWithLimits(ctx, knowledgeQualityScanLimit, knowledgeQualityByteLimit)
}

func (s *Store) knowledgeQualityWithLimits(ctx context.Context, scanLimit, byteLimit int) (KnowledgeQualityReport, error) {
	report := KnowledgeQualityReport{
		ScanLimit: scanLimit, Empty: []KnowledgeQualityItem{}, DuplicateGroups: []KnowledgeQualityDuplicate{},
		Notice: "仅检查当前正式知识条目，不扫描笔记目录、归档或逐轮记录。空正文指仅有标题、空白或注释；重复仅指同分类与范围内正文一致，依据和适用条件仍可能不同，请打开原文核对，不会自动合并或删除。计数仅涵盖本次有界扫描，不代表语义质量或事实验证。",
	}
	// Select only the current body and small metadata, not revision histories.
	rows, err := s.QueryContext(ctx, `SELECT id,layer,scope_key,
		COALESCE(json_extract(document,'$.title'),''),
		COALESCE(json_extract(document,'$.status'),''),
		COALESCE(json_extract(document,'$.content'),'')
		FROM curated_knowledge WHERE deleted=0 ORDER BY id LIMIT ?`, scanLimit+1)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	groups := map[string]*KnowledgeQualityDuplicate{}
	bytes := 0
	for rows.Next() {
		var item KnowledgeQualityItem
		var content string
		if err := rows.Scan(&item.ID, &item.Layer, &item.ScopeKey, &item.Title, &item.Status, &content); err != nil {
			return report, err
		}
		bytes += len(content) + len(item.ID) + len(item.Title) + len(item.Layer) + len(item.ScopeKey) + len(item.Status)
		if report.Scanned >= scanLimit || bytes > byteLimit {
			report.ScanTruncated = true
			break
		}
		report.Scanned++
		item.ID = "curated:" + item.ID
		body := normalizeKnowledgeQualityBody(content)
		if knowledgeQualityEmpty(body, item.Title) {
			report.EmptyCount++
			if len(report.Empty) < knowledgeQualityIssueLimit {
				report.Empty = append(report.Empty, item)
			} else {
				report.IssuesTruncated = true
			}
			continue
		}
		// Preserve case, code indentation, evidence, and applicability. Equal
		// bodies are a review hint, never permission to merge two documents.
		bodyHash := hash(body)
		key := item.Layer + "\x00" + item.ScopeKey + "\x00" + bodyHash
		group := groups[key]
		if group == nil {
			group = &KnowledgeQualityDuplicate{BodyHash: bodyHash, Layer: item.Layer, ScopeKey: item.ScopeKey, Items: []KnowledgeQualityItem{}}
			groups[key] = group
		}
		group.Count++
		if len(group.Items) < knowledgeQualityGroupLimit {
			group.Items = append(group.Items, item)
		} else {
			group.ItemsTruncated = true
		}
	}
	if err := rows.Err(); err != nil {
		return report, err
	}
	keys := make([]string, 0, len(groups))
	for key, group := range groups {
		if group.Count > 1 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	report.DuplicateGroupCount = len(keys)
	for _, key := range keys {
		group := groups[key]
		if len(report.DuplicateGroups) >= knowledgeQualityIssueLimit {
			report.IssuesTruncated = true
			break
		}
		report.DuplicateGroups = append(report.DuplicateGroups, *group)
		report.IssuesTruncated = report.IssuesTruncated || group.ItemsTruncated
	}
	return report, nil
}

func normalizeKnowledgeQualityBody(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(strings.ReplaceAll(content, "\r", "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	// Strip only surrounding blank lines. Leading spaces on the first actual
	// line can be Markdown code indentation and must remain meaningful.
	return strings.Join(lines, "\n")
}

func knowledgeQualityEmpty(body, title string) bool {
	// HTML comments and heading-only outlines carry no knowledge body.
	// Keep fenced/indented code, links, tables, and brief valid notes intact.
	lines := []string{}
	inComment := false
	for _, raw := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(raw)
		if !inComment && trimmed != "" && (strings.HasPrefix(raw, "    ") || strings.HasPrefix(raw, "\t") || strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")) {
			return false
		}
		var clean strings.Builder
		for raw != "" {
			if inComment {
				end := strings.Index(raw, "-->")
				if end < 0 {
					break
				}
				raw, inComment = raw[end+3:], false
				continue
			}
			start := strings.Index(raw, "<!--")
			if start < 0 {
				clean.WriteString(raw)
				break
			}
			clean.WriteString(raw[:start])
			raw, inComment = raw[start+4:], true
		}
		lines = append(lines, clean.String())
	}
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if line != strings.TrimLeft(line, " \t") && (strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "    ")) {
			return false
		}
		if strings.HasPrefix(trimmed, "#") {
			level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
			if level <= 6 && (len(trimmed) == level || trimmed[level] == ' ' || trimmed[level] == '\t') {
				continue
			}
		}
		// A Setext heading is also an outline, not substantive text.
		if index+1 < len(lines) {
			next := strings.TrimSpace(lines[index+1])
			if len(next) >= 3 && (strings.Trim(next, "=") == "" || strings.Trim(next, "-") == "") {
				lines[index+1] = ""
				continue
			}
		}
		if strings.EqualFold(trimmed, strings.TrimSpace(title)) {
			continue
		}
		return false
	}
	return true
}

func (s *Server) knowledgeQuality(w http.ResponseWriter, r *http.Request) {
	report, err := s.app.store.knowledgeQuality(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取知识质量检查失败")
		return
	}
	jsonOut(w, http.StatusOK, report)
}
