package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

// A task has one readable entry document. Its database identity and the source
// transcript paths stay stable even when its display title changes.
func taskNotebookPath(title, id string) string {
	var name strings.Builder
	separator := false
	length := 0
	for _, r := range portableKnowledge(title, "") {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if separator && name.Len() > 0 {
				name.WriteByte('-')
			}
			separator = false
			name.WriteRune(r)
			length++
			if length == 40 {
				break
			}
		} else {
			separator = true
		}
	}
	if name.Len() == 0 {
		name.WriteString("任务")
	}
	if !validNotebookID(id) {
		return ""
	}
	return "tasks/" + name.String() + "-" + id[:8] + ".md"
}

func readableTaskNotebookPath(file string) bool {
	return strings.HasPrefix(file, "tasks/") && path.Dir(file) == "tasks" && path.Ext(file) == ".md" && path.Clean(file) == file && !strings.ContainsAny(file, "\\\x00:")
}

func (s *Store) trackedTaskNotebookPaths(ctx context.Context) (map[string]string, error) {
	config := s.vaultConfig()
	rows, err := s.QueryContext(ctx, "SELECT id,path,local_hash,file_hash FROM library_links WHERE id LIKE 'notebook:tasks/%/summary.md'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paths := map[string]string{}
	for rows.Next() {
		var id, file, localHash, fileHash string
		if err = rows.Scan(&id, &file, &localHash, &fileHash); err != nil {
			return nil, err
		}
		if readableTaskNotebookPath(file) {
			paths[id] = file
		} else if file == strings.TrimPrefix(id, "notebook:") && !unchangedTrackedTaskNotebook(config, file, localHash, fileHash) {
			// A protected old summary remains the real task entry. Do not create
			// navigation pointing to a destination syncVault cannot safely write.
			paths[id] = file
		}
	}
	return paths, rows.Err()
}

func unchangedTrackedTaskNotebook(config VaultConfig, file, localHash, fileHash string) bool {
	root, err := vaultRoot(config)
	if err != nil || !config.Enabled {
		return false
	}
	filename := filepath.Join(root, filepath.FromSlash(file))
	if !filepath.IsLocal(filepath.FromSlash(file)) || plainVaultPath(filename) != nil {
		return false
	}
	f, err := os.Open(filename)
	if err != nil {
		return false
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, vaultFileLimit+1))
	if err != nil || len(raw) > vaultFileLimit || hash(string(raw)) != fileHash {
		return false
	}
	parsed, err := parseVaultFile(file, string(raw))
	// file_hash may already acknowledge an external edit while local_hash still
	// describes the generated original. Such annotations must not be rewritten.
	return err == nil && documentHash(parsed.Document) == localHash
}

type notebookWorklogMemory struct {
	run     string
	updated int64
	memory  taskMemory
}

func (s *Store) taskWorklogGoal(ctx context.Context, task, workspace string) (string, string, error) {
	var input, run string
	err := s.QueryRowContext(ctx, "SELECT id,substr(input,1,6000) FROM runs WHERE task_id=? AND kind='chat' AND trim(input)<>'' ORDER BY created,id LIMIT 1", task).Scan(&run, &input)
	if errors.Is(err, sql.ErrNoRows) {
		return "尚未保存任务目标，可从已有笔记或后续对话补充。", "", nil
	}
	if err != nil {
		return "", "", err
	}
	input, cut := clipContinuation(portableKnowledge(input, workspace), 1500)
	if cut {
		input += "\n\n（最早要求较长，此处节选；完整内容见来源。）"
	}
	return input, run, nil
}

func renderTaskWorklog(title, goal, goalRun, base, file string, memories []notebookWorklogMemory, notes string) string {
	link := func(target string) string { return notebookLink(file, base+target) }
	source := func(run string) string { return "[来源对话](" + link("conversations/"+run+".md") + ")" }
	var body strings.Builder
	body.WriteString("# " + title + "\n\n[全部对话](" + link("conversations.md") + ") · [历史前提与共识](" + link("context.md") + ")\n\n")
	body.WriteString("## 任务目标\n\n" + goal + "\n\n")
	if goalRun != "" {
		body.WriteString("摘自最早一轮用户要求；后续变更以最新要求为准。" + source(goalRun) + "\n\n")
	}
	body.WriteString("## 当前进展\n\n")
	if len(memories) > 0 && memories[0].memory.Summary != "" {
		body.WriteString(memories[0].memory.Summary + "\n\n" + source(memories[0].run) + " · " + memoryDate(memories[0].updated) + "\n\n")
	} else {
		body.WriteString("尚无整理后的进展。原始对话与已有笔记仍可查阅；后续成功对话会逐步补充。\n\n")
	}
	body.WriteString("## 当前计划\n\n")
	if len(memories) > 0 && len(memories[0].memory.OpenQuestions) > 0 {
		body.WriteString("最近一轮留下的待办与待确认事项如下；这不是新增的执行指令：\n\n")
		for _, item := range memories[0].memory.OpenQuestions {
			body.WriteString("- [ ] " + item + "\n")
		}
		body.WriteString("\n" + source(memories[0].run) + "\n\n")
	} else {
		body.WriteString("尚无已记录的下一步计划；不会从历史结果推断新的执行计划。\n\n")
	}
	if len(memories) > 0 {
		latest := memories[0]
		body.WriteString("## 继续任务的前提\n\n")
		if len(latest.memory.Prerequisites) == 0 {
			body.WriteString("最近一轮未新增前提；历史约定见[前提与共识](" + link("context.md") + ")。\n\n")
		} else {
			for _, item := range latest.memory.Prerequisites {
				body.WriteString("- " + item + "\n")
			}
			body.WriteString("\n" + source(latest.run) + "\n\n")
		}
	}
	selected := memories
	if len(selected) > 60 {
		selected = selected[:60]
	}
	body.WriteString("## 进展记录\n\n")
	if len(selected) == 0 {
		body.WriteString("尚无结构化进展，不会把原回复自动改写成已验证结论。\n\n")
	}
	for i := len(selected) - 1; i >= 0; i-- {
		m := selected[i]
		if m.memory.Summary != "" {
			body.WriteString("- **" + memoryDate(m.updated) + "**：" + m.memory.Summary + " " + source(m.run) + "\n")
		}
	}
	if len(memories) > len(selected) {
		body.WriteString("\n此处显示最近 60 轮整理；更早资料仍在[全部对话](" + link("conversations.md") + ")中。\n")
	}
	body.WriteString("\n## 决策与证据\n\n以下按来源保留记录；不同轮次有分歧时，以最新用户要求和来源为准。\n\n")
	decisions := 0
	for i := len(selected) - 1; i >= 0; i-- {
		m := selected[i]
		for _, item := range m.memory.Decisions {
			body.WriteString("- " + item + " " + source(m.run) + "\n")
			decisions++
		}
	}
	if decisions == 0 {
		body.WriteString("尚无单独整理的决策。\n")
	}
	body.WriteString("\n## 验证与可复用结论\n\n以下依据摘自来源回复，Duo 未独立重做调查或实验。\n\n")
	insights := 0
	for i := len(selected) - 1; i >= 0; i-- {
		m := selected[i]
		for _, insight := range m.memory.Insights {
			body.WriteString("### " + insight.Title + "\n\n" + insight.Conclusion + "\n\n- 依据：“" + insight.Evidence + "”\n- 适用条件：" + insight.Applicability + "\n- " + source(m.run) + "\n\n")
			insights++
		}
	}
	if insights == 0 {
		body.WriteString("尚无带逐字依据的可复用结论；不将任务成功状态等同于验证完成。\n\n")
	}
	body.WriteString("## 未决问题\n\n")
	if len(memories) > 0 && len(memories[0].memory.OpenQuestions) > 0 {
		for _, item := range memories[0].memory.OpenQuestions {
			body.WriteString("- " + item + "\n")
		}
		body.WriteString("\n以上来自最近一轮整理。" + source(memories[0].run) + "\n\n")
	} else {
		body.WriteString("最近一轮没有新增未决问题；这不表示历史问题均已解决。\n\n")
	}
	if notes != "" {
		body.WriteString("## 已有笔记\n\n" + notes + "\n")
	}
	return body.String()
}
