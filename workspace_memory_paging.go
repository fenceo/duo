package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
)

// The checkpoint advances in insertion order, independently of displayed event
// time. Nonterminal holes remain explicit so a long-running older round cannot
// disappear behind newer completed work. It is persisted only after all files.
type workspaceMemoryCheckpoint struct {
	Cursor      int64
	CursorRunID string
	PendingRows []int64
	Pending     bool
	LogIDs      map[string]bool
}

func (s *Store) workspaceMemoryRunPage(ctx context.Context, task Task, previous WorkspaceMemoryReport) ([]Run, map[string]*taskMemory, workspaceMemoryCheckpoint, error) {
	checkpoint := workspaceMemoryCheckpoint{LogIDs: map[string]bool{}}
	for _, item := range previous.Tasks {
		if item.ID == task.ID && item.SyncCursor >= 0 && len(item.SyncPendingRows) <= 2000 {
			checkpoint.Cursor = item.SyncCursor
			checkpoint.CursorRunID = item.SyncCursorRunID
			checkpoint.PendingRows = append([]int64{}, item.SyncPendingRows...)
			break
		}
	}
	if checkpoint.Cursor > 0 {
		var currentID string
		err := s.QueryRowContext(ctx, "SELECT id FROM runs WHERE task_id=? AND rowid=?", task.ID, checkpoint.Cursor).Scan(&currentID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, checkpoint, err
		}
		if err != nil || currentID != checkpoint.CursorRunID {
			checkpoint.Cursor = 0
			checkpoint.CursorRunID = ""
			checkpoint.PendingRows = nil
		}
	}
	const fields = `r.rowid,r.id,substr(r.input,1,100001),r.kind,r.status,substr(r.result,1,140001),substr(r.error,1,4001),r.created,r.finished,coalesce(substr(m.payload,1,100001),'')`
	const join = ` FROM runs r LEFT JOIN task_memories m ON m.run_id=r.id AND m.task_id=r.task_id `
	selected := map[int64]Run{}
	memories := map[string]*taskMemory{}
	loaded := 0
	readRows := func(query string, args []any, consume func(int64, Run, string) bool) error {
		rows, err := s.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var row int64
			var run Run
			var raw string
			run.TaskID = task.ID
			if err = rows.Scan(&row, &run.ID, &run.Input, &run.Kind, &run.Status, &run.Result, &run.Error, &run.Created, &run.Finished, &raw); err != nil {
				return err
			}
			if !consume(row, run, raw) {
				break
			}
		}
		return rows.Err()
	}
	add := func(row int64, run Run, raw string) bool {
		if _, ok := selected[row]; ok {
			return true
		}
		cost := len(run.Input) + len(run.Result) + len(run.Error) + len(raw)
		// Leave room for duplicated readable excerpts and process details in the
		// bounded wire payload; a later sync resumes at the first omitted row.
		if loaded+cost > workspaceMemoryBatchLimit/4 && len(selected) > 0 {
			return false
		}
		loaded += cost
		selected[row] = run
		var memory taskMemory
		if json.Unmarshal([]byte(raw), &memory) == nil {
			memories[run.ID] = &memory
		}
		return true
	}
	terminal := func(status string) bool { return status == "done" || status == "failed" || status == "interrupted" }
	// Recent source excerpts remain useful immediately while old history drains.
	if err := readRows("SELECT "+fields+join+"WHERE r.task_id=? AND r.status IN ('done','failed','interrupted') ORDER BY r.created DESC,r.rowid DESC LIMIT 6", []any{task.ID}, func(row int64, run Run, raw string) bool { return add(row, run, raw) }); err != nil {
		return nil, nil, checkpoint, err
	}
	remaining := []int64{}
	for _, row := range checkpoint.PendingRows {
		if row <= 0 || row > checkpoint.Cursor {
			continue
		}
		found := false
		deferRow := false
		err := readRows("SELECT "+fields+join+"WHERE r.task_id=? AND r.rowid=?", []any{task.ID, row}, func(id int64, run Run, raw string) bool {
			found = true
			if !terminal(run.Status) {
				deferRow = true
				return false
			}
			if len(selected) >= workspaceMemorySyncPageSize || !add(id, run, raw) {
				deferRow = true
				checkpoint.Pending = true
			} else {
				checkpoint.LogIDs[run.ID] = true
			}
			return false
		})
		if err != nil {
			return nil, nil, checkpoint, err
		}
		if found && deferRow {
			remaining = append(remaining, row)
		}
	}
	checkpoint.PendingRows = remaining
	count := 0
	err := readRows("SELECT "+fields+join+"WHERE r.task_id=? AND r.rowid>? ORDER BY r.rowid LIMIT ?", []any{task.ID, checkpoint.Cursor, workspaceMemorySyncPageSize + 1}, func(row int64, run Run, raw string) bool {
		if count >= workspaceMemorySyncPageSize || (terminal(run.Status) && len(selected) >= workspaceMemorySyncPageSize+6 && selected[row].ID == "") || (terminal(run.Status) && !add(row, run, raw)) {
			checkpoint.Pending = true
			return false
		}
		if !terminal(run.Status) {
			if len(checkpoint.PendingRows) >= 2000 {
				checkpoint.Pending = true
				return false
			}
			checkpoint.PendingRows = append(checkpoint.PendingRows, row)
		} else {
			checkpoint.LogIDs[run.ID] = true
		}
		checkpoint.Cursor = row
		checkpoint.CursorRunID = run.ID
		count++
		return true
	})
	if err != nil {
		return nil, nil, checkpoint, err
	}
	ids := make([]int64, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		left, right := selected[ids[i]], selected[ids[j]]
		if left.Created == right.Created {
			return ids[i] > ids[j]
		}
		return left.Created > right.Created
	})
	runs := make([]Run, 0, len(ids))
	for _, id := range ids {
		runs = append(runs, selected[id])
	}
	return runs, memories, checkpoint, nil
}

// Current state is folded as a stream from every retained structured delta,
// rather than depending on the current history export window. Only the recent
// provenance list is bounded; earlier source files keep their stable identity.
func (s *Store) workspaceMemoryCurrentState(ctx context.Context, task Task) (taskStatePatch, []Run, error) {
	state := taskStatePatch{}
	recent := []Run{}
	rows, err := s.QueryContext(ctx, `SELECT id,status,created,finished,result,payload,phase FROM (
 SELECT r.id,r.status,r.created,r.finished,substr(r.result,1,140001) result,substr(m.payload,1,100001) payload,1 phase,0 seq,r.rowid ordering
 FROM task_memories m JOIN runs r ON r.id=m.run_id AND r.task_id=m.task_id WHERE r.task_id=? AND r.kind='chat' AND r.status IN ('done','failed','interrupted')
 UNION ALL
 SELECT r.id,r.status,r.created,r.finished,'' result,substr(e.text,1,64001) payload,0 phase,e.seq,r.rowid ordering
 FROM events e JOIN runs r ON r.id=e.run_id AND r.task_id=e.task_id WHERE r.task_id=? AND r.kind='chat' AND r.status IN ('running','done','failed','interrupted') AND e.kind='task_state'
 ) ORDER BY created,ordering,phase,seq`, task.ID, task.ID)
	if err != nil {
		return state, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var run Run
		var raw string
		var phase int
		run.TaskID = task.ID
		if err = rows.Scan(&run.ID, &run.Status, &run.Created, &run.Finished, &run.Result, &raw, &phase); err != nil {
			return state, nil, err
		}
		var patch *taskStatePatch
		if phase == 0 {
			if update := decodeLiveTaskState(raw, task.Workspace); update != nil {
				patch = &update.State
			}
		} else {
			var memory taskMemory
			if json.Unmarshal([]byte(raw), &memory) == nil {
				patch = workspaceTaskStatePatchForRun(task, run, &memory)
			}
		}
		if patch != nil {
			state = mergeTaskState(state, patch)
			if len(recent) == 0 || recent[len(recent)-1].ID != run.ID {
				recent = append(recent, run)
			}
			if len(recent) > 8 {
				recent = recent[len(recent)-8:]
			}
		}
	}
	return state, recent, rows.Err()
}

func workspaceMemoryCheckpointValid(item WorkspaceMemoryTask) bool {
	if item.SyncCursor < 0 || item.SyncCursor > 0 && !archiveIDPattern.MatchString(item.SyncCursorRunID) || len(item.SyncPendingRows) > 2000 {
		return false
	}
	seen := map[int64]bool{}
	for _, row := range item.SyncPendingRows {
		if row <= 0 || row > item.SyncCursor || seen[row] {
			return false
		}
		seen[row] = true
	}
	return true
}

// A changed checkpoint document is not a trusted export receipt. Reset safely:
// the append-only source markers make backfill idempotent.
func workspaceMemoryResetEditedCheckpoint(task *WorkspaceMemoryTask) {
	task.SyncCursor = 0
	task.SyncCursorRunID = ""
	task.SyncPendingRows = nil
	task.SyncPending = true
}
