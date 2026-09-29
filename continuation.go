package main

import (
	"database/sql"
	"regexp"
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
	Fingerprint  string `json:"fingerprint"`
	ArchiveBytes int    `json:"archive_bytes"`
	SourceTaskID string `json:"source_task_id"`
	SourceEngine string `json:"source_engine"`
	SourceTitle  string `json:"source_title"`
	Runs         int    `json:"transferred_runs"`
	Knowledge    int    `json:"transferred_knowledge"`
	Context      string `json:"context"`
	Truncated    bool   `json:"context_truncated"`
}

var (
	continuationPrivateKey       = regexp.MustCompile(`(?is)-----BEGIN [^-\r\n]*PRIVATE KEY-----.*?-----END [^-\r\n]*PRIVATE KEY-----`)
	continuationCredential       = regexp.MustCompile(`(?im)(\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|auth(?:orization)?|password|secret|private[_ -]?key)\b\s*[:=]\s*)([^\s,;]+)`)
	continuationBearer           = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{12,}`)
	continuationToken            = regexp.MustCompile(`(?i)\b(?:sk-[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9_]{16,}|xox[baprs]-[A-Za-z0-9-]{16,})\b`)
	continuationQuotedCredential = regexp.MustCompile(`(?im)(["'](?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|auth(?:orization)?|password|secret|private[_ -]?key)["']\s*:\s*["'])([^"'\r\n]*)(["'])`)
)

// redactContinuation removes common credentials before a preview is shown or
// eventually handed to another engine. It is defence in depth, not a claim
// that arbitrary prose can be proven secret-free.
func redactContinuation(text string) string {
	text = continuationPrivateKey.ReplaceAllString(text, "[已隐藏的私钥]")
	text = continuationBearer.ReplaceAllString(text, "Bearer [已隐藏]")
	text = continuationToken.ReplaceAllString(text, "[已隐藏的令牌]")
	text = continuationQuotedCredential.ReplaceAllString(text, "$1[已隐藏]$3")
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
	mode := "full"
	if len(modes) > 0 {
		mode = modes[0]
	}
	preview, _, err := a.buildContinuation(task, mode)
	return preview, err
}
