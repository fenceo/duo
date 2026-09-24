package main

import (
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// TaskContinuation records only lineage metadata; no native session or
// credential reference is copied between engines.
type TaskContinuation struct {
	TargetTaskID         string `json:"target_task_id"`
	SourceTaskID         string `json:"source_task_id"`
	SourceEngine         string `json:"source_engine"`
	TargetEngine         string `json:"target_engine"`
	TransferredRuns      int    `json:"transferred_runs"`
	TransferredKnowledge int    `json:"transferred_knowledge"`
	Created              int64  `json:"created"`
}

// taskContinuation returns lineage metadata when this task was created from
// another task. A missing row simply means it is an original task.
func (s *Store) taskContinuation(target string) (TaskContinuation, error) {
	var c TaskContinuation
	err := s.QueryRow(`SELECT target_task_id,source_task_id,source_engine,target_engine,transferred_runs,transferred_knowledge,created
FROM task_continuations WHERE target_task_id=?`, target).Scan(&c.TargetTaskID, &c.SourceTaskID, &c.SourceEngine, &c.TargetEngine, &c.TransferredRuns, &c.TransferredKnowledge, &c.Created)
	if err == sql.ErrNoRows {
		return TaskContinuation{}, sql.ErrNoRows
	}
	return c, err
}

// ContinuationPreview is a local, redacted snapshot. It is deliberately a
// preview object: generating it never calls an AI engine or sends data outside
// Duo. A later confirmation endpoint can submit this text after the user has
// reviewed it.
type ContinuationPreview struct {
	SourceTaskID         string `json:"source_task_id"`
	SourceEngine         string `json:"source_engine"`
	SourceTitle          string `json:"source_title"`
	Runs                 int    `json:"transferred_runs"`
	Knowledge            int    `json:"transferred_knowledge"`
	Context              string `json:"context"`
	Truncated             bool   `json:"context_truncated"`
}

var (
	continuationPrivateKey = regexp.MustCompile(`(?is)-----BEGIN [^-\r\n]*PRIVATE KEY-----.*?-----END [^-\r\n]*PRIVATE KEY-----`)
	continuationCredential = regexp.MustCompile(`(?im)(\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|auth(?:orization)?|password|secret|private[_ -]?key)\b\s*[:=]\s*)([^\s,;]+)`)
	continuationBearer     = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{12,}`)
	continuationToken      = regexp.MustCompile(`(?i)\b(?:sk-[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9_]{16,}|xox[baprs]-[A-Za-z0-9-]{16,})\b`)
)

// redactContinuation removes common credentials before a preview is shown or
// eventually handed to another engine. It is defence in depth, not a claim
// that arbitrary prose can be proven secret-free.
func redactContinuation(text string) string {
	text = continuationPrivateKey.ReplaceAllString(text, "[已隐藏的私钥]")
	text = continuationBearer.ReplaceAllString(text, "Bearer [已隐藏]")
	text = continuationToken.ReplaceAllString(text, "[已隐藏的令牌]")
	text = continuationCredential.ReplaceAllString(text, "$1[已隐藏]")
	return redactWorkspaceText(text)
}

func continuationEngineLabel(engine string) string {
	switch engine {
	case "claude":
		return "Claude Code"
	case "deepseek-harness":
		return "DeepSeek Harness"
	default:
		return "Codex"
	}
}

func clipContinuation(text string, maxRunes int) (string, bool) {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= maxRunes {
		return text, false
	}
	runes := []rune(text)
	return string(runes[:maxRunes]) + "\n…（上下文已截断，请先检查工作区）", true
}

// continuationPreview builds a bounded portable snapshot from existing local
// records. It never includes attachments or native session identifiers.
func (a *App) continuationPreview(id string, modes ...string) (ContinuationPreview, error) {
	task, err := a.store.task(id)
	if err != nil {
		return ContinuationPreview{}, err
	}
	runs, err := a.store.runs(id)
	if err != nil {
		return ContinuationPreview{}, err
	}
	knowledge, err := a.store.knowledgeList(id)
	if err != nil {
		return ContinuationPreview{}, err
	}
	mode := "recent"
	if len(modes) > 0 && modes[0] != "" {
		mode = modes[0]
	}
	if mode != "recent" && mode != "summary" && mode != "full" {
		return ContinuationPreview{}, fmt.Errorf("上下文范围无效")
	}
	completed := make([]Run, 0, len(runs))
	for _, run := range runs {
		if run.Status == "done" && run.Kind == "chat" {
			completed = append(completed, run)
		}
	}
	limit := 8
	if mode == "full" {
		limit = 20
	}
	if mode == "summary" {
		completed = nil
	} else if len(completed) > limit {
		completed = completed[len(completed)-limit:]
	}
	sort.SliceStable(knowledge, func(i, j int) bool {
		if knowledgeState(knowledge[i].Status) != knowledgeState(knowledge[j].Status) {
			return knowledgeState(knowledge[i].Status) == "verified"
		}
		return knowledge[i].Updated > knowledge[j].Updated
	})
	if len(knowledge) > 12 {
		knowledge = knowledge[:12]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "你正在接手 Duo 任务“%s”。原执行引擎：%s。\n", redactContinuation(task.Title), continuationEngineLabel(task.Engine))
	b.WriteString("以下内容是历史快照，可能不完整，也不代表目标引擎已经执行过其中的命令或修改。请先检查当前工作区状态，再继续任务。\n")
	if len(knowledge) > 0 {
		b.WriteString("\n## 已沉淀知识\n")
		for _, item := range knowledge {
			content, _ := clipContinuation(redactContinuation(item.Content), 2400)
			fmt.Fprintf(&b, "\n### %s（%s）\n%s\n", redactContinuation(item.Title), knowledgeStateLabel(item.Status), content)
		}
	}
	if len(completed) > 0 {
		b.WriteString("\n## 最近对话与执行结果\n")
		for _, run := range completed {
			input, _ := clipContinuation(redactContinuation(run.Input), 1800)
			result, _ := clipContinuation(redactContinuation(run.Result), 3000)
			if run.Error != "" {
				result = strings.TrimSpace(result + "\n错误：" + redactContinuation(run.Error))
			}
			fmt.Fprintf(&b, "\n### 用户要求\n%s\n\n### 原引擎结果（%s）\n%s\n", input, run.Status, result)
		}
	}
	context, truncated := clipContinuation(redactContinuation(b.String()), 24000)
	return ContinuationPreview{SourceTaskID: task.ID, SourceEngine: task.Engine, SourceTitle: task.Title, Runs: len(completed), Knowledge: len(knowledge), Context: context, Truncated: truncated}, nil
}
