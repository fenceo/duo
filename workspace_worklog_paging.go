package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const workspaceWorklogPageTarget = 256 * 1024
const workspaceWorklogNavigationStart = "<!-- duo:history-navigation:"
const workspaceWorklogNavigationEnd = "<!-- /duo:history-navigation -->"

var workspaceWorklogMonth = regexp.MustCompile(`^(?:[0-9]{4}-(?:0[1-9]|1[0-2])|undated)$`)
var workspaceWorklogPage = regexp.MustCompile(`^([0-9]{4}-(?:0[1-9]|1[0-2])|undated)-([0-9]{4})\.md$`)

func workspaceWorklogHistoryDocument(name string) bool {
	return name == "README.md" || strings.HasSuffix(name, ".md") && workspaceWorklogMonth.MatchString(strings.TrimSuffix(name, ".md")) || workspaceWorklogPage.MatchString(name)
}

func workspaceWorklogMarkers(content string, seen, detailed map[string]bool) {
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		for prefix, target := range map[string]map[string]bool{"<!-- duo:worklog:": seen, "<!-- duo:worklog-detail:": detailed} {
			if strings.HasPrefix(line, prefix) && strings.HasSuffix(line, " -->") {
				target[strings.TrimSuffix(strings.TrimPrefix(line, prefix), " -->")] = true
			}
		}
	}
}

// Oversized human files stay unreadable through the bounded editor API. Only
// short source-marker lines are streamed here; arbitrarily long human lines
// are discarded without allocating their body or ever modifying the file.
func workspaceWorklogScanMarkers(ctx context.Context, reader io.Reader) (map[string]bool, map[string]bool, error) {
	seen, detailed := map[string]bool{}, map[string]bool{}
	buffer := bufio.NewReaderSize(reader, 4096)
	skip := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		line, err := buffer.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			skip = true
			continue
		}
		if !skip && len(line) <= 512 {
			workspaceWorklogMarkers(strings.TrimRight(string(line), "\r\n"), seen, detailed)
		}
		skip = false
		if err == io.EOF {
			return seen, detailed, nil
		}
		if err != nil {
			return nil, nil, err
		}
	}
}

func workspaceWorklogAddition(entry workspaceWorklogEntry, seen bool) string {
	prefix := workspaceWorklogMarker(entry.ID) + "\n"
	if entry.Details {
		prefix += workspaceWorklogDetailMarker(entry.ID) + "\n"
		if seen {
			prefix = workspaceWorklogDetailMarker(entry.ID) + "\n\n> 历史轮次资料补全：补充原需求、回复与执行摘录；此前记录和人工编辑保持原样。\n\n"
		}
	}
	return "\n\n" + prefix + strings.TrimSpace(entry.Content) + "\n"
}

func updateWorkspaceWorklogNavigation(content, body string) (string, bool) {
	if !strings.Contains(content, workspaceWorklogNavigationStart) {
		if strings.Contains(content, workspaceWorklogNavigationEnd) {
			return content, true
		}
		block := strings.ReplaceAll(strings.ReplaceAll(workspaceTaskStateBlock(body), workspaceTaskStateStart, workspaceWorklogNavigationStart), workspaceTaskStateEnd, workspaceWorklogNavigationEnd)
		separator := "\n\n"
		if strings.Contains(content, "\r\n") {
			block = strings.ReplaceAll(block, "\n", "\r\n")
			separator = "\r\n\r\n"
		}
		if at := strings.Index(content, workspaceTaskStateEnd); at >= 0 {
			at += len(workspaceTaskStateEnd)
			return content[:at] + separator + block + content[at:], false
		}
		return content + separator + block + separator, false
	}
	// Reuse the same hash/CRLF preservation contract with disjoint markers.
	if strings.Contains(content, workspaceTaskStateStart) || strings.Contains(content, workspaceTaskStateEnd) {
		// Existing task state is protected from this marker substitution.
		placeholderStart, placeholderEnd := "<!-- duo-navigation-placeholder-start:", "<!-- duo-navigation-placeholder-end -->"
		if strings.Contains(content, placeholderStart) || strings.Contains(content, placeholderEnd) {
			return content, true
		}
		content = strings.ReplaceAll(strings.ReplaceAll(content, workspaceTaskStateStart, placeholderStart), workspaceTaskStateEnd, placeholderEnd)
		updated, conflict := updateWorkspaceWorklogState(strings.ReplaceAll(strings.ReplaceAll(content, workspaceWorklogNavigationStart, workspaceTaskStateStart), workspaceWorklogNavigationEnd, workspaceTaskStateEnd), body)
		updated = strings.ReplaceAll(strings.ReplaceAll(updated, workspaceTaskStateStart, workspaceWorklogNavigationStart), workspaceTaskStateEnd, workspaceWorklogNavigationEnd)
		return strings.ReplaceAll(strings.ReplaceAll(updated, placeholderStart, workspaceTaskStateStart), placeholderEnd, workspaceTaskStateEnd), conflict
	}
	updated, conflict := updateWorkspaceWorklogState(strings.ReplaceAll(strings.ReplaceAll(content, workspaceWorklogNavigationStart, workspaceTaskStateStart), workspaceWorklogNavigationEnd, workspaceTaskStateEnd), body)
	return strings.ReplaceAll(strings.ReplaceAll(updated, workspaceTaskStateStart, workspaceWorklogNavigationStart), workspaceTaskStateEnd, workspaceWorklogNavigationEnd), conflict
}

func appendWorkspaceWorklogPages(ctx context.Context, task Task, file workspaceMemoryFile, read func(string) (string, error), list func(string) ([]string, error), scan func(string) (map[string]bool, map[string]bool, error), plain func(string, string, string, bool) error, warn func(string)) error {
	directory := path.Dir(file.Path)
	if path.Base(file.Path) != workspaceWorklogFilename || !workspaceMemoryTaskDirectory(task.ID, directory) {
		return errors.New("任务记录追加路径无效")
	}
	if len(file.Entries) > 2000 {
		return errors.New("任务记录来源超过 2000 条")
	}
	old, err := read(file.Path)
	exists := err == nil
	oversized := errors.Is(err, errWorkspaceMemoryTooLarge)
	if err != nil && !os.IsNotExist(err) && !oversized {
		return err
	}
	main := old
	if !exists {
		main = file.Content
	}
	if oversized {
		main = ""
		warn("任务记录超过 1 MiB；保留人工原文，新轮次仍写入历史分页，请从任务来源索引打开历史记录。")
	}
	if !oversized {
		var upgraded bool
		main, upgraded = upgradeWorkspaceLegacyWorklogHeader(main)
		if upgraded {
			warn("已按已知自动模板修正旧版问候目标；历史记录和人工内容保留，当前目标仍需有效任务状态确认。")
		}
	}
	seen, detailed := map[string]bool{}, map[string]bool{}
	workspaceWorklogMarkers(main, seen, detailed)
	if oversized {
		seen, detailed, err = scan(file.Path)
		if err != nil {
			return err
		}
	}
	names, err := list(path.Join(directory, "history"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	pages := map[string][]string{}
	for _, name := range names {
		if match := workspaceWorklogPage.FindStringSubmatch(name); match != nil && match[2] != "0000" {
			pages[match[1]] = append(pages[match[1]], name)
		}
	}
	months := map[string]bool{}
	for _, entry := range file.Entries {
		month := entry.Month
		if month == "" {
			month = "undated"
		}
		if !workspaceWorklogMonth.MatchString(month) || !validWorkspaceWorklogID(entry.ID) {
			return errors.New("任务记录来源或月份无效")
		}
		if entry.Source != "" && (!workspaceMemoryDocumentPath(entry.Source) || path.Base(path.Dir(entry.Source)) != "runs") {
			return errors.New("任务来源链接无效")
		}
		months[month] = true
	}
	// Check all source identities, even when a historical timestamp/month was
	// corrected. Retain bodies only for touched months, keeping memory bounded.
	contents, originals := map[string]string{}, map[string]string{}
	blocked := map[string]bool{}
	for month := range pages {
		sort.Strings(pages[month])
		for _, name := range pages[month] {
			if err = ctx.Err(); err != nil {
				return err
			}
			body, e := read(path.Join(directory, "history", name))
			if errors.Is(e, errWorkspaceMemoryTooLarge) {
				markers, details, scanErr := scan(path.Join(directory, "history", name))
				if scanErr != nil {
					return scanErr
				}
				for id := range markers {
					seen[id] = true
				}
				for id := range details {
					detailed[id] = true
				}
				blocked[name] = true
				warn("历史分页超过 1 MiB，保留原文；后续记录使用新分页：" + name)
				continue
			}
			if e != nil {
				return e
			}
			if months[month] {
				contents[name] = body
				originals[name] = body
			}
			workspaceWorklogMarkers(body, seen, detailed)
		}
	}
	for _, entry := range file.Entries {
		if seen[entry.ID] && (!entry.Details || detailed[entry.ID]) {
			continue
		}
		addition := workspaceWorklogAddition(entry, seen[entry.ID])
		if !oversized && len(main)+len(addition) <= workspaceWorklogPageTarget {
			main += addition
		} else {
			month := entry.Month
			if month == "" {
				month = "undated"
			}
			name := ""
			if len(pages[month]) > 0 {
				name = pages[month][len(pages[month])-1]
			}
			if name == "" || blocked[name] || len(contents[name])+len(addition) > workspaceWorklogPageTarget {
				number := 1
				if name != "" {
					match := workspaceWorklogPage.FindStringSubmatch(name)
					number, _ = strconv.Atoi(match[2])
					number++
				}
				if number > 9999 {
					return errors.New("任务单月历史已达到 9999 分页；保留原文，未推进同步检查点")
				}
				name = fmt.Sprintf("%s-%04d.md", month, number)
				pages[month] = append(pages[month], name)
				contents[name] = "# " + month + " · 任务进展记录\n\n[返回任务当前状态](../" + workspaceWorklogFilename + ") · [历史月份索引](README.md)\n\n按来源追加保存任务需求、结论、决策与证据；历史回填保留原发生时间，不推断为已验证知识。\n"
			}
			if entry.Source != "" {
				addition = strings.Replace(addition, "]("+workspaceMemoryLink(file.Path, entry.Source)+")", "]("+workspaceMemoryLink(path.Join(directory, "history", name), entry.Source)+")", 1)
			}
			if len(contents[name])+len(addition) > workspaceMemoryFileLimit {
				return errors.New("单条任务记录超过历史分页上限；保留原文，未推进同步检查点")
			}
			contents[name] += addition
		}
		seen[entry.ID] = true
		if entry.Details {
			detailed[entry.ID] = true
		}
	}
	// Source pages are staged before the main navigation and the final cursor.
	changed := []string{}
	for name, body := range contents {
		if body != originals[name] {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	for _, name := range changed {
		oldPage, existed := originals[name]
		if err = plain(path.Join(directory, "history", name), contents[name], oldPage, existed); err != nil {
			return err
		}
	}
	monthNames := []string{}
	for month := range pages {
		monthNames = append(monthNames, month)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(monthNames)))
	wantNavigation := len(monthNames) > 0 || file.HistoryPending || strings.Contains(main, workspaceWorklogNavigationStart)
	if wantNavigation {
		var index strings.Builder
		index.WriteString("# 任务历史记录\n\n[返回任务当前状态](../" + workspaceWorklogFilename + ")\n\n按发生月份查找；每页保留来源与执行状态，人工修改不被重写。\n\n")
		for _, month := range monthNames {
			sort.Strings(pages[month])
			fmt.Fprintf(&index, "- [%s](%s.md) · %d 页\n", month, month, len(pages[month]))
			if !months[month] {
				continue
			}
			var monthIndex strings.Builder
			fmt.Fprintf(&monthIndex, "# %s · 任务记录分页\n\n[全部月份](README.md) · [任务当前状态](../%s)\n\n", month, workspaceWorklogFilename)
			for _, name := range pages[month] {
				fmt.Fprintf(&monthIndex, "- [%s](%s)\n", name, name)
			}
			if err = plainHistoryIndex(path.Join(directory, "history", month+".md"), monthIndex.String(), read, plain, warn); err != nil {
				return err
			}
		}
		if err = plainHistoryIndex(path.Join(directory, "history", "README.md"), index.String(), read, plain, warn); err != nil {
			return err
		}
	}
	if oversized {
		return nil
	}
	var executionConflict bool
	executionContent := file.ExecutionContent
	if executionContent != nil && *executionContent == "" && strings.Contains(main, workspaceExecutionCheckpointStart) {
		// A long history may be exported in several batches. A stale live
		// snapshot must survive until its own terminal source is durably in the
		// existing history or staged earlier in this successful append.
		if _, conflict := updateWorkspaceExecutionCheckpoint(main, executionContent); !conflict {
			id := workspaceExecutionCheckpointSource(main)
			if !strings.HasPrefix(id, task.ID+"/") || !seen[id] || !detailed[id] {
				executionContent = nil
				warn("执行快照对应的终态来源尚未写入时间记录，已保留快照；后续历史同步保存该来源后再自动清理。")
			}
		}
	}
	main, executionConflict = updateWorkspaceExecutionCheckpoint(main, executionContent)
	if executionConflict {
		warn("执行快照区域已被人工修改或标记不完整，已保留原文；任务状态及终态时间记录仍独立保存。")
	}
	if file.StateIncomplete {
		warn("任务状态历史不完整，已保留自动状态区；新进展仍写入时间记录。")
	} else {
		var conflict bool
		main, conflict = updateWorkspaceWorklogState(main, file.StateContent)
		if conflict {
			warn("任务状态自动管理区域已被人工修改或标记不完整，已保留原文；新进展仍追加到时间记录。")
		}
	}
	if wantNavigation {
		body := "## 历史与最近进展\n\n[按月份查看完整任务记录](history/README.md) · [完整轮次来源](README.md)\n\n主文档保留当前状态与既有人工内容；新增长历史自动分页，不会在达到 1 MiB 后停止记录。\n"
		recent := file.RecentEntries
		if len(recent) == 0 {
			recent = file.Entries
		}
		start := len(recent) - 4
		if start < 0 {
			start = 0
		}
		for _, entry := range recent[start:] {
			snippet, _ := workspaceWorklogExcerpt(entry.Content, task.Workspace, 700)
			body += "\n" + snippet + "\n"
		}
		var conflict bool
		main, conflict = updateWorkspaceWorklogNavigation(main, body)
		if conflict {
			warn("历史导航区域已被人工修改，已保留原文；分页可从任务来源索引打开。")
		}
	}
	if len(main) > workspaceMemoryFileLimit {
		warn("更新任务状态或导航将超过 1 MiB，保留主文档；新轮次已保存到历史分页。")
		return nil
	}
	return plain(file.Path, main, old, exists)
}

func plainHistoryIndex(relative, body string, read func(string) (string, error), plain func(string, string, string, bool) error, warn func(string)) error {
	old, err := read(relative)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && !workspaceMemoryUnmodified(old) {
		warn("历史索引已被人工修改，保留原文：" + relative)
		return nil
	}
	return plain(relative, workspaceMemoryManaged(body), old, err == nil)
}
