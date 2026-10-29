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
}

func workspaceWorklogMarker(id string) string { return "<!-- duo:worklog:" + id + " -->" }

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
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		const prefix = "<!-- duo:worklog:"
		if strings.HasPrefix(line, prefix) && strings.HasSuffix(line, " -->") {
			seen[strings.TrimSuffix(strings.TrimPrefix(line, prefix), " -->")] = true
		}
	}
	for _, entry := range entries {
		if !validWorkspaceWorklogID(entry.ID) {
			return "", false, errors.New("任务记录来源 ID 无效")
		}
		marker := workspaceWorklogMarker(entry.ID)
		if seen[entry.ID] {
			continue
		}
		addition := "\n\n" + marker + "\n" + strings.TrimSpace(entry.Content) + "\n"
		if len(content)+len(addition) > workspaceMemoryFileLimit {
			return content, true, nil
		}
		content += addition
		seen[entry.ID] = true
	}
	return content, false, nil
}

func workspaceWorklogText(value, workspace string, limit int) string {
	value, _ = clipContinuation(workspaceMemoryText(value, workspace), limit)
	// A model quote cannot create a source marker and suppress another entry.
	return strings.ReplaceAll(value, "<!-- duo:worklog:", "&lt;!-- duo:worklog:")
}

func workspaceWorklogHeader(task Task, goal string, tags ...string) string {
	title := workspaceMemoryText(task.Title, task.Workspace)
	quotedTitle, _ := json.Marshal(title)
	quotedTags, _ := json.Marshal(notebookTags(append([]string{"duo", "task-worklog"}, tags...), title))
	return "---\ntype: task_worklog\ntask_id: " + task.ID + "\ntitle: " + string(quotedTitle) + "\ntags: " + string(quotedTags) + "\n---\n\n# " + markdownTitle(title) + "\n\n" +
		"任务 ID：`" + task.ID + "`\n\n" +
		"## 任务目标\n\n" + goal + "\n\n" +
		"## 当前计划\n\n尚未手工填写。可在这里维护下一步计划。\n\n" +
		"## 备注\n\n可直接编辑目标、计划、备注和已有记录；后续只追加新轮次。请保留来源标记以避免重复回填。\n\n" +
		"## 进展记录\n\n以下按轮次时间持续追加。历史回复和自动整理不等于独立验证；失败记录保留失败状态。\n"
}

func workspaceWorklogEntryContent(task Task, run Run, memory *taskMemory, source string) string {
	clean := func(value string, limit int) string { return workspaceWorklogText(value, task.Workspace, limit) }
	var body strings.Builder
	fmt.Fprintf(&body, "### %s · %s\n\n[来源记录](%s)\n\n", memoryDate(run.Created), clean(run.Status, 40), source)
	if memory != nil && strings.TrimSpace(memory.Summary) != "" {
		body.WriteString("**进展（来源回复整理，待核验）**\n\n" + clean(memory.Summary, 1600) + "\n\n")
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
		body.WriteString("**尚未整理；以下为原始对话摘录，不代表独立验证。**\n\n")
		body.WriteString("要求：" + clean(run.Input, 600) + "\n\n")
		if strings.TrimSpace(run.Result) != "" {
			body.WriteString("回复摘录：" + clean(run.Result, 1600) + "\n\n")
		} else {
			body.WriteString("本轮未取得最终回复。\n\n")
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
