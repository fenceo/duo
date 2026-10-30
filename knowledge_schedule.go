package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type KnowledgeExtractionConfig struct {
	Enabled        bool   `json:"enabled"`
	Scheduled      bool   `json:"scheduled"`
	IntervalHours  int    `json:"interval_hours"`
	NeedsDirectory bool   `json:"needs_directory"`
	LastOrganized  int64  `json:"last_organized"`
	LastError      string `json:"last_error,omitempty"`
}
type knowledgeScheduleState struct {
	LastAttempt   int64  `json:"last_attempt"`
	LastOrganized int64  `json:"last_organized"`
	LastError     string `json:"last_error,omitempty"`
}

func (s *Store) knowledgeExtractionConfig() KnowledgeExtractionConfig {
	c := KnowledgeExtractionConfig{Enabled: true, IntervalHours: 24}
	if raw := s.setting("knowledge_extraction"); raw != "" {
		if json.Unmarshal([]byte(raw), &c) != nil {
			c.Enabled, c.Scheduled = false, false
		}
	}
	if c.IntervalHours < 1 || c.IntervalHours > 168 {
		c.IntervalHours = 24
	}
	var state knowledgeScheduleState
	_ = json.Unmarshal([]byte(s.setting("knowledge_extraction_schedule")), &state)
	c.NeedsDirectory = s.knowledgeExtractionNeedsDirectory()
	c.LastOrganized, c.LastError = state.LastOrganized, state.LastError
	return c
}

// The timer only harvests recorded candidates. It cannot issue model requests.
// Persist attempts too: a broken disk must not be retried every idle scan.
func (a *App) organizeKnowledgeDue(ctx context.Context, at int64) {
	c := a.store.knowledgeExtractionConfig()
	if !c.Scheduled || c.NeedsDirectory || !a.knowledgeOrganizeMu.TryLock() {
		return
	}
	defer a.knowledgeOrganizeMu.Unlock()
	var state knowledgeScheduleState
	_ = json.Unmarshal([]byte(a.store.setting("knowledge_extraction_schedule")), &state)
	if at-state.LastAttempt < int64(time.Duration(c.IntervalHours)*time.Hour/time.Millisecond) && state.LastAttempt != 0 {
		return
	}
	state.LastAttempt = at
	_, err := vaultRoot(a.store.vaultConfig())
	if err == nil {
		_, err = a.store.extractTaskKnowledge(ctx, "", "", nil)
	}
	if err == nil {
		state.LastOrganized, state.LastError = at, ""
	} else if !errors.Is(err, context.Canceled) {
		// Filesystem errors may contain personal absolute paths; keep the UI status portable.
		state.LastError = "定时整理未完成，请检查笔记路径或使用一键整理重试。"
	}
	raw, _ := json.Marshal(state)
	_ = a.store.set("knowledge_extraction_schedule", string(raw))
	a.changed()
}
