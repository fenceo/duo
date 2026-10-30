package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
)

const workspaceWorklogFilename = "任务记录.md"

var errWorkspaceDocumentConflict = errors.New("任务记录已被修改，请重新读取后合并；当前编辑未覆盖")
var errWorkspaceMemoryTooLarge = errors.New("工作区记忆超过 1 MiB")

func validWorkspaceMemoryRevision(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value
}

func workspaceMemorySnapshotMatches(expected string, existed bool, current string, readErr error) bool {
	if os.IsNotExist(readErr) {
		return !existed
	}
	return readErr == nil && existed && current == expected
}

type workspaceWorklogEntry struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Details bool   `json:"details,omitempty"`
}

func workspaceWorklogMarker(id string) string { return "<!-- duo:worklog:" + id + " -->" }

func workspaceWorklogDetailMarker(id string) string { return "<!-- duo:worklog-detail:" + id + " -->" }

func validWorkspaceWorklogID(id string) bool {
	parts := strings.Split(id, "/")
	return len(parts) == 2 && archiveIDPattern.MatchString(parts[0]) && archiveIDPattern.MatchString(parts[1])
}

// Append new sources without rewriting any existing byte, including human edits.
// The source marker belongs to Duo; deleting its whole block intentionally permits
// a later backfill to restore that source from the retained database record.
func appendWorkspaceWorklog(old string, exists bool, header string, entries []workspaceWorklogEntry) (string, bool, error) {
	if len(entries) > 2000 {
		return "", false, errors.New("任务记录来源超过 2000 条")
	}
	content := old
	if !exists {
		content = header
	}
	if len(content) > workspaceMemoryFileLimit {
		return "", false, errors.New("任务记录超过 1 MiB；已有内容未修改")
	}
	seen, detailed := map[string]bool{}, map[string]bool{}
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		const prefix = "<!-- duo:worklog:"
		if strings.HasPrefix(line, prefix) && strings.HasSuffix(line, " -->") {
			seen[strings.TrimSuffix(strings.TrimPrefix(line, prefix), " -->")] = true
		}
		const detailPrefix = "<!-- duo:worklog-detail:"
		if strings.HasPrefix(line, detailPrefix) && strings.HasSuffix(line, " -->") {
			detailed[strings.TrimSuffix(strings.TrimPrefix(line, detailPrefix), " -->")] = true
		}
	}
	for _, entry := range entries {
		if !validWorkspaceWorklogID(entry.ID) {
			return "", false, errors.New("任务记录来源 ID 无效")
		}
		marker := workspaceWorklogMarker(entry.ID)
		if seen[entry.ID] && (!entry.Details || detailed[entry.ID]) {
			continue
		}
		prefix := marker + "\n"
		if entry.Details {
			prefix += workspaceWorklogDetailMarker(entry.ID) + "\n"
			if seen[entry.ID] {
				// Old sparse entries and all manual edits stay intact. Append the
				// missing source detail once, rather than rewriting their blocks.
				prefix = workspaceWorklogDetailMarker(entry.ID) + "\n\n> 历史轮次资料补全：补充原需求、回复与执行摘录；此前记录和人工编辑保持原样。\n\n"
			}
		}
		addition := "\n\n" + prefix + strings.TrimSpace(entry.Content) + "\n"
		if len(content)+len(addition) > workspaceMemoryFileLimit {
			return content, true, nil
		}
		content += addition
		seen[entry.ID] = true
		detailed[entry.ID] = entry.Details
	}
	return content, false, nil
}

func workspaceWorklogText(value, workspace string, limit int) string {
	value, _ = clipContinuation(workspaceMemoryText(value, workspace), limit)
	// A model quote cannot create a source marker and suppress another entry.
	value = strings.ReplaceAll(value, "<!-- duo:", "&lt;!-- duo:")
	return strings.ReplaceAll(value, "<!-- /duo:", "&lt;!-- /duo:")
}

// Keep the final conclusion/error as well as the beginning of a long response.
// The complete bounded source copy remains separately linked from every turn.
func workspaceWorklogExcerpt(value, workspace string, limit int) (string, bool) {
	value = workspaceMemoryText(value, workspace)
	value = strings.ReplaceAll(strings.ReplaceAll(value, "<!-- duo:", "&lt;!-- duo:"), "<!-- /duo:", "&lt;!-- /duo:")
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes), false
	}
	head := limit * 2 / 3
	return string(runes[:head]) + "\n\n…（中间内容省略，保留开头与结尾；请打开本轮来源记录查阅）…\n\n" + string(runes[len(runes)-(limit-head):]), true
}

func workspaceWorklogQuote(text string) string {
	return "> " + strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\n> ") + "\n\n"
}

const workspaceWorklogNotes = "## 备注\n\n自动管理区域维护当前状态；人工编辑该区域会暂停自动替换。区域外正文保持原样，时间记录只追加新轮次。请保留来源标记以避免重复回填。\n\n## 进展记录\n\n以下按轮次时间持续追加本轮需求、助手结论、执行摘录及结构化进展。原始需求不自动作为任务目标，历史回复和自动整理不等于独立验证；失败记录保留失败状态。\n"

// v0.34.63 had no managed-state hash and mistook the first greeting for a goal.
// Only a byte-identical known default template is eligible for this correction.
// Any edited goal, plan or note disables it; the remaining document is retained.
func upgradeWorkspaceLegacyWorklogHeader(content string) (string, bool) {
	if strings.Count(content, workspaceTaskStateStart) != strings.Count(content, workspaceTaskStateEnd) || strings.Count(content, workspaceTaskStateStart) > 1 {
		return content, false
	}
	newline := "\n"
	if strings.Contains(content, "\r\n") {
		newline = "\r\n"
	}
	if !strings.HasPrefix(content, "---"+newline+"type: task_worklog"+newline+"task_id: ") {
		return content, false
	}
	for _, greeting := range []string{"你好", "您好", "hello", "Hello", "hi", "Hi", "你好，继续", "你好,继续", "好的", "继续"} {
		legacy := "## 任务目标\n\n" + greeting + "\n\n## 当前计划\n\n尚未手工填写。可在这里维护下一步计划。\n\n## 备注\n\n可直接编辑目标、计划、备注和已有记录；后续只追加新轮次。请保留来源标记以避免重复回填。\n\n## 进展记录\n\n以下按轮次时间持续追加。历史回复和自动整理不等于独立验证；失败记录保留失败状态。\n"
		expected := strings.ReplaceAll(legacy, "\n", newline)
		at := strings.Index(content, newline+"## 任务目标"+newline)
		if at < 0 {
			continue
		}
		at += len(newline)
		if len(content)-at < len(expected) || strings.Contains(content[:at], "<!-- duo:worklog:") {
			continue
		}
		if strings.Contains(content[:at], workspaceTaskStateStart) && !strings.Contains(content[:at], workspaceTaskStateEnd) {
			continue
		}
		// Compare the whole allowlisted template fingerprint, not a loose
		// search-and-replace that could alter a manually maintained document.
		if workspaceMemoryHash(content[at:at+len(expected)]) == workspaceMemoryHash(expected) {
			return content[:at] + strings.ReplaceAll(workspaceWorklogNotes, "\n", newline) + content[at+len(expected):], true
		}
	}
	return content, false
}

const workspaceTaskStateStart = "<!-- duo:task-state:"
const workspaceTaskStateEnd = "<!-- /duo:task-state -->"

func workspaceTaskStateBlock(body string) string {
	body = strings.TrimRight(strings.ReplaceAll(body, "\r\n", "\n"), "\n") + "\n"
	return workspaceTaskStateStart + workspaceMemoryHash(body) + " -->\n" + body + workspaceTaskStateEnd
}

func workspaceWorklogStateExcerpt(content string) string {
	start := strings.Index(content, workspaceTaskStateStart)
	if start < 0 {
		return ""
	}
	header := strings.Index(content[start:], " -->")
	end := strings.Index(content[start:], workspaceTaskStateEnd)
	if header < 0 || end < header {
		return ""
	}
	content = content[start+header+len(" -->") : start+end]
	content, _ = clipContinuation(strings.TrimSpace(content), 2400)
	return content
}

// Only the intact, explicitly managed state region may be replaced. Human text
// outside it is preserved byte-for-byte. A modified/malformed region is retained.
func updateWorkspaceWorklogState(content, body string) (string, bool) {
	if body == "" {
		return content, false
	}
	start := strings.Index(content, workspaceTaskStateStart)
	if start < 0 {
		if strings.Contains(content, workspaceTaskStateEnd) {
			return content, true
		}
		block := workspaceTaskStateBlock(body) + "\n\n"
		separator := "\n\n"
		if strings.Contains(content, "\r\n") {
			block = strings.ReplaceAll(block, "\n", "\r\n")
			separator = "\r\n\r\n"
		}
		// Keep frontmatter first and add the state before legacy section text.
		if at := strings.Index(content, "\n## "); at >= 0 {
			return content[:at+1] + block + content[at+1:], false
		}
		return content + separator + block, false
	}
	if strings.Count(content, workspaceTaskStateStart) != 1 || strings.Count(content, workspaceTaskStateEnd) != 1 {
		return content, true
	}
	headerEnd := strings.Index(content[start:], " -->")
	end := strings.Index(content[start:], workspaceTaskStateEnd)
	if headerEnd < 0 || end < headerEnd {
		return content, true
	}
	headerEnd += start
	end += start
	hash := content[start+len(workspaceTaskStateStart) : headerEnd]
	stored := content[headerEnd+len(" -->") : end]
	stored = strings.TrimPrefix(strings.ReplaceAll(stored, "\r\n", "\n"), "\n")
	if !validWorkspaceMemoryRevision(hash) || workspaceMemoryHash(stored) != hash {
		return content, true
	}
	replacement := workspaceTaskStateBlock(body)
	if strings.Contains(content[start:end], "\r\n") {
		replacement = strings.ReplaceAll(replacement, "\n", "\r\n")
	}
	return content[:start] + replacement + content[end+len(workspaceTaskStateEnd):], false
}

func workspaceWorklogHeader(task Task, goal string, tags ...string) string {
	title := workspaceMemoryText(task.Title, task.Workspace)
	quotedTitle, _ := json.Marshal(title)
	quotedTags, _ := json.Marshal(notebookTags(append([]string{"duo", "task-worklog"}, tags...), title))
	return "---\ntype: task_worklog\ntask_id: " + task.ID + "\ntitle: " + string(quotedTitle) + "\ntags: " + string(quotedTags) + "\n---\n\n# " + markdownTitle(title) + "\n\n" +
		"任务 ID：`" + task.ID + "` · [完整轮次与执行来源](README.md)\n\n" +
		workspaceTaskStateBlock(workspaceTaskStateContent(task, taskStatePatch{}, Run{}, "")) + "\n\n" +
		workspaceWorklogNotes
}

func workspaceWorklogEntryContent(task Task, run Run, memory *taskMemory, source string, process ...string) string {
	clean := func(value string, limit int) string { return workspaceWorklogText(value, task.Workspace, limit) }
	var body strings.Builder
	fmt.Fprintf(&body, "### %s · %s\n\n[来源记录：本轮对话与执行过程](%s) · 轮次 `%s`\n\n", memoryDate(run.Created), clean(run.Status, 40), source, run.ID)
	if run.Finished > run.Created {
		fmt.Fprintf(&body, "结束时间：%s\n\n", memoryDate(run.Finished))
	}
	request, _ := workspaceWorklogExcerpt(run.Input, task.Workspace, 2400)
	if request != "" {
		body.WriteString("#### 本轮需求（原始用户输入，非自动目标）\n\n" + workspaceWorklogQuote(request))
	}
	answer, _ := workspaceWorklogExcerpt(run.Result, task.Workspace, 8000)
	if answer != "" {
		body.WriteString("#### 助手结论与执行结果（来源回复，待核验）\n\n" + workspaceWorklogQuote(answer))
	} else {
		body.WriteString("#### 助手结论与执行结果\n\n本轮未取得可保存的助手回复；执行状态及错误见本轮记录，不推断任务已经完成。\n\n")
	}
	if len(process) > 0 && strings.TrimSpace(process[0]) != "" {
		excerpt, _ := workspaceWorklogExcerpt(process[0], task.Workspace, 2400)
		body.WriteString("#### 执行过程摘录（非独立验证）\n\n" + workspaceWorklogQuote(excerpt))
	}
	if memory != nil && (strings.TrimSpace(memory.Summary) != "" || memory.TaskState != nil) {
		if strings.TrimSpace(memory.Summary) != "" {
			body.WriteString("**进展（来源回复整理，待核验）**\n\n" + clean(memory.Summary, 1600) + "\n\n")
		}
		for _, insight := range memory.Insights {
			body.WriteString("- **" + clean(insight.Title, 120) + "**：" + clean(insight.Conclusion, 1000) + "\n  - 依据：“" + clean(insight.Evidence, 1000) + "”\n  - 适用条件：" + clean(insight.Applicability, 500) + "\n")
		}
		if len(memory.Decisions) > 0 {
			body.WriteString("\n**本轮决策与约定**\n\n")
			for _, item := range memory.Decisions {
				body.WriteString("- " + clean(item, 800) + "\n")
			}
		}
		if len(memory.OpenQuestions) > 0 {
			body.WriteString("\n**未解决事项**\n\n")
			for _, item := range memory.OpenQuestions {
				body.WriteString("- " + clean(item, 800) + "\n")
			}
		}
	} else {
		body.WriteString("**尚未整理为任务状态。** 上方保留了本轮需求和助手结果，未从提问推断目标、计划或完成情况。\n\n")
	}
	if memory != nil && memory.TaskState != nil {
		patch := memory.TaskState
		if patch.Goal != "" {
			body.WriteString("**目标更新**：" + clean(patch.Goal, 1500) + "\n\n")
		}
		if patch.GoalReason != "" {
			body.WriteString("变更原因：" + clean(patch.GoalReason, 1000) + "\n\n")
		}
		for _, criterion := range patch.Acceptance {
			body.WriteString("- 验收标准：" + clean(criterion, 1000) + "\n")
		}
		for _, step := range patch.Plan {
			body.WriteString("- 计划（" + clean(step.Status, 20) + "）：" + clean(step.Text, 1000) + "\n")
			if step.Evidence != "" {
				body.WriteString("  - 完成依据：“" + clean(step.Evidence, 1500) + "”\n")
			}
		}
		for _, item := range patch.Progress {
			body.WriteString("- 进展：" + clean(item, 1000) + "\n")
		}
		for _, item := range patch.Decisions {
			body.WriteString("- 决策：" + clean(item, 1000) + "\n")
		}
		for _, item := range patch.Evidence {
			body.WriteString("- 证据（" + clean(item.Status, 20) + "，来源报告）：" + clean(item.Text, 1000) + "；依据：“" + clean(item.Quote, 1500) + "”\n")
		}
		for _, item := range patch.Blockers {
			body.WriteString("- 阻塞：" + clean(item, 1000) + "\n")
		}
		for _, item := range patch.NextActions {
			body.WriteString("- 下一步：" + clean(item, 1000) + "\n")
		}
	}
	if run.Error != "" {
		body.WriteString("\n**执行错误（未解决）**：" + clean(run.Error, 1000) + "\n")
	}
	return body.String()
}

func workspaceWorklogPath(report WorkspaceMemoryReport) string {
	return path.Join(report.TaskDirectory, workspaceWorklogFilename)
}
