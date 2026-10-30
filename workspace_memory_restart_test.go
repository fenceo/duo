package main

import (
	"database/sql"
	"errors"
	"testing"
)

func workspaceRestartRun(t *testing.T, store *Store, status, kind string) (string, string) {
	t.Helper()
	task, run := uid(), uid()
	if _, err := store.Exec("INSERT INTO tasks(id,title,workspace,model,status,created,updated) VALUES(?,'Restart fixture','C:\\synthetic','synthetic',?,1,1)", task, status); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES(?,?,'Synthetic restart request',?,'web',?,1)", run, task, kind, status); err != nil {
		t.Fatal(err)
	}
	return task, run
}

func TestWorkspaceMemoryStartupQueuesOnlyNewlyInterruptedRuns(t *testing.T) {
	directory := t.TempDir()
	store, err := openStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	running, runningRun := workspaceRestartRun(t, store, "running", "chat")
	queued, queuedRun := workspaceRestartRun(t, store, "queued", "chat")
	knowledge, _ := workspaceRestartRun(t, store, "running", "knowledge")
	done, _ := workspaceRestartRun(t, store, "done", "chat")
	interrupted, _ := workspaceRestartRun(t, store, "interrupted", "chat")
	// Simulate an older database without the retry schema. The recovery receipt
	// must be created before startup converts active runs to interrupted.
	if _, err = store.Exec("DROP TABLE workspace_memory_sync_queue"); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = openStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range []string{running, queued, knowledge} {
		var next int64
		var attempts, paused int
		if err = store.QueryRow("SELECT next_at,attempts,paused FROM workspace_memory_sync_queue WHERE task_id=?", task).Scan(&next, &attempts, &paused); err != nil || next != 0 || attempts != 0 || paused != 0 {
			t.Fatal("newly interrupted work lost its pending export receipt", task, next, attempts, paused, err)
		}
	}
	for _, run := range []string{runningRun, queuedRun} {
		var status, failure string
		var finished int64
		if err = store.QueryRow("SELECT status,error,finished FROM runs WHERE id=?", run).Scan(&status, &failure, &finished); err != nil || status != "interrupted" || failure == "" || finished == 0 {
			t.Fatal("startup recovery did not preserve existing interrupted-run behavior", status, failure, finished, err)
		}
	}
	for _, task := range []string{done, interrupted} {
		var count int
		if err = store.QueryRow("SELECT count(*) FROM workspace_memory_sync_queue WHERE task_id=?", task).Scan(&count); err != nil || count != 0 {
			t.Fatal("old terminal history was incorrectly enqueued", task, count, err)
		}
	}
	if _, err = store.Exec("DELETE FROM workspace_memory_sync_queue"); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = openStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var count int
	if err = store.QueryRow("SELECT count(*) FROM workspace_memory_sync_queue").Scan(&count); err != nil || count != 0 {
		t.Fatal("reopening after export requeued old interrupted history", count, err)
	}
}

func TestWorkspaceMemoryStartupPreservesPausedRecoveryReceipt(t *testing.T) {
	directory := t.TempDir()
	store, err := openStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	task, _ := workspaceRestartRun(t, store, "running", "chat")
	if _, err = store.Exec("INSERT INTO workspace_memory_sync_queue(task_id,next_at,attempts,paused) VALUES(?,123456,4,1)", task); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = openStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var next int64
	var attempts, paused int
	if err = store.QueryRow("SELECT next_at,attempts,paused FROM workspace_memory_sync_queue WHERE task_id=?", task).Scan(&next, &attempts, &paused); err != nil || next != 123456 || attempts != 4 || paused != 1 {
		t.Fatal("startup recovery bypassed an existing conflict pause or disk backoff", next, attempts, paused, err)
	}
}

func TestWorkspaceMemorySuccessfulExportKeepsActiveChatReceipt(t *testing.T) {
	for _, status := range []string{"running", "queued"} {
		t.Run(status, func(t *testing.T) {
			store, err := openStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			task, run := workspaceRestartRun(t, store, status, "chat")
			app := &App{store: store}
			app.trackWorkspaceMemorySync(task, WorkspaceMemoryReport{Enabled: true}, nil)
			var attempts, paused int
			if err = store.QueryRow("SELECT attempts,paused FROM workspace_memory_sync_queue WHERE task_id=?", task).Scan(&attempts, &paused); err != nil || attempts != 0 || paused != 0 {
				t.Fatal("successful terminal-history export dropped the active chat receipt", attempts, paused, err)
			}
			app.trackWorkspaceMemorySync(task, WorkspaceMemoryReport{Enabled: true, Conflicts: []string{"synthetic conflict"}}, nil)
			if err = store.QueryRow("SELECT paused FROM workspace_memory_sync_queue WHERE task_id=?", task).Scan(&paused); err != nil || paused != 1 {
				t.Fatal("active chat bypassed a conflict pause", paused, err)
			}
			app.trackWorkspaceMemorySync(task, WorkspaceMemoryReport{Enabled: true}, errors.New("synthetic disk failure"))
			var next int64
			if err = store.QueryRow("SELECT next_at,attempts FROM workspace_memory_sync_queue WHERE task_id=?", task).Scan(&next, &attempts); err != nil || next <= now() || attempts != 1 {
				t.Fatal("active chat bypassed disk backoff", next, attempts, err)
			}
			if _, err = store.Exec("UPDATE runs SET status='interrupted',finished=2 WHERE id=?", run); err != nil {
				t.Fatal(err)
			}
			app.trackWorkspaceMemorySync(task, WorkspaceMemoryReport{Enabled: true}, nil)
			if err = store.QueryRow("SELECT task_id FROM workspace_memory_sync_queue WHERE task_id=?", task).Scan(&task); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("completed export left a receipt after the chat ended", err)
			}
		})
	}
}
