package main

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const workspaceMemoryRetrySchema = `
CREATE TABLE IF NOT EXISTS workspace_memory_sync_queue(task_id TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,next_at INTEGER NOT NULL,attempts INTEGER NOT NULL DEFAULT 0,paused INTEGER NOT NULL DEFAULT 0);
`

// The database receipt survives application exits. It contains no paths or
// transcript copies. Manual sync reactivates a conflict-paused item explicitly.
func (a *App) trackWorkspaceMemorySync(task string, report WorkspaceMemoryReport, syncErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if !report.Enabled {
		_, _ = a.store.ExecContext(ctx, "DELETE FROM workspace_memory_sync_queue WHERE task_id=?", task)
		return
	}
	if len(report.Conflicts) > 0 {
		_, _ = a.store.ExecContext(ctx, `INSERT INTO workspace_memory_sync_queue(task_id,next_at,paused) VALUES(?,0,1)
ON CONFLICT(task_id) DO UPDATE SET paused=1`, task)
		return
	}
	if syncErr != nil || report.Error != "" {
		var attempts int
		_ = a.store.QueryRowContext(ctx, "SELECT attempts FROM workspace_memory_sync_queue WHERE task_id=?", task).Scan(&attempts)
		delay := time.Minute * time.Duration(1<<min(attempts, 6))
		delay = min(delay, time.Hour)
		_, _ = a.store.ExecContext(ctx, `INSERT INTO workspace_memory_sync_queue(task_id,next_at,attempts,paused) VALUES(?,?,1,0)
ON CONFLICT(task_id) DO UPDATE SET next_at=excluded.next_at,attempts=min(7,workspace_memory_sync_queue.attempts+1),paused=0`, task, now()+delay.Milliseconds())
		return
	}
	// A normal history export excludes unfinished rounds. Keep its durable
	// receipt until the live chat ends, even when all terminal history is done.
	// The transaction makes the active-run check and receipt update indivisible
	// with respect to another run's startup/finalization on the same store.
	tx, err := a.store.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	var active bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM runs WHERE task_id=? AND kind='chat' AND status IN ('running','queued'))", task).Scan(&active); err != nil {
		return
	}
	if report.SyncPending || active {
		_, err = tx.ExecContext(ctx, `INSERT INTO workspace_memory_sync_queue(task_id,next_at,attempts,paused) VALUES(?,0,0,0)
ON CONFLICT(task_id) DO UPDATE SET next_at=0,attempts=0,paused=0`, task)
	} else {
		_, err = tx.ExecContext(ctx, "DELETE FROM workspace_memory_sync_queue WHERE task_id=?", task)
	}
	if err == nil {
		_ = tx.Commit()
	}
}

// Drain at most four batches on an idle minute. A failed disk backs off; a
// generated-file conflict requires explicit user retry. No model is invoked.
func (a *App) retryWorkspaceMemoryDue(ctx context.Context, at int64) {
	for batch := 0; batch < 4 && ctx.Err() == nil && !a.updating.Load(); batch++ {
		a.mu.Lock()
		busy := len(a.workers) != 0
		a.mu.Unlock()
		if busy {
			return
		}
		var task string
		err := a.store.QueryRowContext(ctx, `SELECT q.task_id FROM workspace_memory_sync_queue q
JOIN tasks t ON t.id=q.task_id WHERE q.paused=0 AND q.next_at<=?
AND NOT EXISTS(SELECT 1 FROM task_options o WHERE o.task_id=t.id AND o.deleted=1)
ORDER BY q.next_at,t.updated,q.task_id LIMIT 1`, at).Scan(&task)
		if errors.Is(err, sql.ErrNoRows) || err != nil {
			return
		}
		report, err := a.syncWorkspaceMemory(ctx, task)
		if err != nil || len(report.Conflicts) > 0 {
			return
		}
	}
}
