package main

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"sort"
	"strconv"
)

const conversationRunLimit = 5
const conversationEventLimit = 100

var errConversationCursor = errors.New("无效的对话分页位置")

type conversationRecords struct {
	Before   int64 `json:"before"`
	HasOlder bool  `json:"has_older"`
}

type conversationWindow struct {
	Before   string                         `json:"before"`
	HasOlder bool                           `json:"has_older"`
	Sequence int64                          `json:"sequence"`
	HasMore  bool                           `json:"has_more"`
	Records  map[string]conversationRecords `json:"records"`
}

const conversationRunSelect = `SELECT r.id,r.task_id,r.input,r.kind,r.source,r.status,r.result,r.error,r.created,r.finished,
COALESCE(m.started,0),COALESCE(m.usage,''),COALESCE(o.mode,''),COALESCE(o.attachments,'')
FROM runs r LEFT JOIN run_metrics m ON m.run_id=r.id LEFT JOIN run_options o ON o.run_id=r.id `

// Tool output is fetched in full only when its disclosure is opened. Neither
// display preferences nor these read-only projections alter the stored record.
const conversationEventSelect = `SELECT seq,run_id,kind,
CASE WHEN kind IN ('tool','log') THEN substr(text,1,240) ELSE text END,created,
kind IN ('tool','log') AND length(text)>240 FROM events `

func readConversationEvents(tx *sql.Tx, query string, args ...any) ([]Event, error) {
	rows, err := tx.Query(conversationEventSelect+query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var event Event
		if err = rows.Scan(&event.Seq, &event.RunID, &event.Kind, &event.Text, &event.Created, &event.Truncated); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func conversationCursor(q url.Values, key string) (int64, error) {
	if !q.Has(key) {
		return 0, nil
	}
	n, err := strconv.ParseInt(q.Get(key), 10, 64)
	if err != nil || n < 0 {
		return 0, errConversationCursor
	}
	return n, nil
}

// A consistent high-water mark separates the intentionally unloaded history
// from future events. Polling never walks the old log to reach the current turn.
func (s *Store) conversationPage(ctx context.Context, task string, q url.Values) ([]Run, []Event, conversationWindow, error) {
	window := conversationWindow{Records: map[string]conversationRecords{}}
	after, err := conversationCursor(q, "after")
	if err != nil {
		return nil, nil, window, err
	}
	beforeEvent, err := conversationCursor(q, "before_event")
	if err != nil {
		return nil, nil, window, err
	}
	if q.Has("after") && (q.Has("before") || q.Has("run")) {
		return nil, nil, window, errConversationCursor
	}
	tx, err := s.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, window, err
	}
	defer tx.Rollback()
	if err = tx.QueryRow("SELECT COALESCE(MAX(seq),0) FROM events WHERE task_id=?", task).Scan(&window.Sequence); err != nil {
		return nil, nil, window, err
	}
	var runs []Run
	if q.Has("run") {
		rows, e := tx.Query(conversationRunSelect+"WHERE r.task_id=? AND r.id=?", task, q.Get("run"))
		if e != nil {
			return nil, nil, window, e
		}
		runs, err = readRuns(rows)
	} else {
		where, args := "WHERE r.task_id=?", []any{task}
		if before := q.Get("before"); before != "" {
			var created int64
			if err = tx.QueryRow("SELECT created FROM runs WHERE task_id=? AND id=?", task, before).Scan(&created); err != nil {
				return nil, nil, window, err
			}
			where += " AND (r.created<? OR (r.created=? AND r.id<?))"
			args = append(args, created, created, before)
		}
		rows, e := tx.Query(conversationRunSelect+where+" ORDER BY r.created DESC,r.id DESC LIMIT 6", args...)
		if e != nil {
			return nil, nil, window, e
		}
		runs, err = readRuns(rows)
		if len(runs) > conversationRunLimit {
			window.HasOlder = true
			runs = runs[:conversationRunLimit]
		}
		if len(runs) > 0 {
			window.Before = runs[len(runs)-1].ID
		}
	}
	if err != nil {
		return nil, nil, window, err
	}
	events := []Event{}
	if q.Has("after") {
		events, err = readConversationEvents(tx, "WHERE task_id=? AND seq>? AND seq<=? ORDER BY seq LIMIT 201", task, after, window.Sequence)
		if err != nil {
			return nil, nil, window, err
		}
		if len(events) > 200 {
			events = events[:200]
			window.HasMore = true
			window.Sequence = events[len(events)-1].Seq
		}
		if window.Sequence < after {
			window.Sequence = after
		}
	}
	// Include all active runs, and a run changed by the delta even if its creation
	// date is outside the recent window. This preserves stop/approval/queue state.
	if !q.Has("before") && !q.Has("run") {
		rows, e := tx.Query(conversationRunSelect+"WHERE r.task_id=? AND r.status IN ('running','queued')", task)
		if e != nil {
			return nil, nil, window, e
		}
		active, e := readRuns(rows)
		if e != nil {
			return nil, nil, window, e
		}
		runs = append(runs, active...)
		seen := map[string]bool{}
		for _, run := range runs {
			seen[run.ID] = true
		}
		for _, event := range events {
			if event.RunID == "" || seen[event.RunID] {
				continue
			}
			seen[event.RunID] = true
			rows, e := tx.Query(conversationRunSelect+"WHERE r.task_id=? AND r.id=?", task, event.RunID)
			if e != nil {
				return nil, nil, window, e
			}
			changed, e := readRuns(rows)
			if e != nil {
				return nil, nil, window, e
			}
			runs = append(runs, changed...)
		}
	}
	unique := map[string]Run{}
	for _, run := range runs {
		unique[run.ID] = run
	}
	runs = []Run{}
	for _, run := range unique {
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].Created == runs[j].Created {
			return runs[i].ID < runs[j].ID
		}
		return runs[i].Created < runs[j].Created
	})
	if !q.Has("after") {
		ids := []string{}
		for _, run := range runs {
			ids = append(ids, run.ID)
		}
		if q.Has("run") {
			ids = []string{q.Get("run")}
		} else if !q.Has("before") {
			ids = append(ids, "")
		}
		for _, runID := range ids {
			query, args := "WHERE task_id=? AND run_id=? AND seq<=?", []any{task, runID, window.Sequence}
			if beforeEvent > 0 {
				query += " AND seq<?"
				args = append(args, beforeEvent)
			}
			page, e := readConversationEvents(tx, query+" ORDER BY seq DESC LIMIT 101", args...)
			if e != nil {
				return nil, nil, window, e
			}
			meta := conversationRecords{HasOlder: len(page) > conversationEventLimit}
			if meta.HasOlder {
				page = page[:conversationEventLimit]
			}
			if len(page) > 0 {
				meta.Before = page[len(page)-1].Seq
				window.Records[runID] = meta
			}
			events = append(events, page...)
			if beforeEvent > 0 {
				continue
			}
			// Keep the initial request and final answer visible even when a long
			// turn's earlier events are still on disk.
			pinned, e := readConversationEvents(tx, "WHERE task_id=? AND run_id=? AND kind='user' AND seq<=? ORDER BY seq LIMIT 1", task, runID, window.Sequence)
			if e != nil {
				return nil, nil, window, e
			}
			events = append(events, pinned...)
			if run, ok := unique[runID]; ok && run.Status == "done" && run.Result != "" {
				pinned, e = readConversationEvents(tx, "WHERE task_id=? AND run_id=? AND kind='assistant' AND seq<=? AND text=? ORDER BY seq DESC LIMIT 1", task, runID, window.Sequence, run.Result)
				if e != nil {
					return nil, nil, window, e
				}
				events = append(events, pinned...)
			}
		}
	}
	uniqueEvents := map[int64]Event{}
	for _, event := range events {
		uniqueEvents[event.Seq] = event
	}
	events = []Event{}
	for _, event := range uniqueEvents {
		events = append(events, event)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Seq < events[j].Seq })
	return runs, events, window, tx.Commit()
}
