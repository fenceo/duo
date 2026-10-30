package main

import (
	"context"
	"errors"
	"testing"
)

func TestWorkspaceMemoryRetryReceiptPersistsBackoffAndConflictPause(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	a.updating.Store(true) // Keep the ordinary background loop out of this fixture.
	a.trackWorkspaceMemorySync(task.ID, WorkspaceMemoryReport{Enabled: true, SyncPending: true}, nil)
	other, err := openStore(a.store.directory)
	if err != nil {
		t.Fatal(err)
	}
	var attempts, paused int
	var next int64
	err = other.QueryRow("SELECT next_at,attempts,paused FROM workspace_memory_sync_queue WHERE task_id=?", task.ID).Scan(&next, &attempts, &paused)
	other.Close()
	if err != nil || next != 0 || attempts != 0 || paused != 0 {
		t.Fatal("pending receipt is not durable", next, attempts, paused, err)
	}
	a.trackWorkspaceMemorySync(task.ID, WorkspaceMemoryReport{Enabled: true}, errors.New("synthetic disk failure"))
	_ = a.store.QueryRow("SELECT next_at,attempts FROM workspace_memory_sync_queue WHERE task_id=?", task.ID).Scan(&next, &attempts)
	if next <= now() || attempts != 1 {
		t.Fatal("failed disk should back off", next, attempts)
	}
	first := next
	a.trackWorkspaceMemorySync(task.ID, WorkspaceMemoryReport{Enabled: true}, errors.New("synthetic retry"))
	_ = a.store.QueryRow("SELECT next_at,attempts FROM workspace_memory_sync_queue WHERE task_id=?", task.ID).Scan(&next, &attempts)
	if next <= first || attempts != 2 {
		t.Fatal("repeated failure must not spin", next, attempts)
	}
	a.trackWorkspaceMemorySync(task.ID, WorkspaceMemoryReport{Enabled: true, Conflicts: []string{"synthetic conflict"}}, nil)
	_ = a.store.QueryRow("SELECT paused FROM workspace_memory_sync_queue WHERE task_id=?", task.ID).Scan(&paused)
	if paused != 1 {
		t.Fatal("manual conflict should pause idle retry")
	}
	a.trackWorkspaceMemorySync(task.ID, WorkspaceMemoryReport{Enabled: true, SyncPending: true}, nil)
	_ = a.store.QueryRow("SELECT paused,attempts FROM workspace_memory_sync_queue WHERE task_id=?", task.ID).Scan(&paused, &attempts)
	if paused != 0 || attempts != 0 {
		t.Fatal("explicit successful retry did not reactivate backlog")
	}
	a.trackWorkspaceMemorySync(task.ID, WorkspaceMemoryReport{Enabled: true}, nil)
	var count int
	_ = a.store.QueryRow("SELECT count(*) FROM workspace_memory_sync_queue WHERE task_id=?", task.ID).Scan(&count)
	if count != 0 {
		t.Fatal("completed export left a retry receipt")
	}
}

func TestWorkspaceMemoryIdleRetryDrainsBacklogWithoutModelCalls(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	a.updating.Store(true)
	insertWorkspacePagingRuns(t, a, task, 135, "synthetic result", 0)
	report, err := a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil || !report.SyncPending {
		t.Fatal("first batch should leave durable backlog", report, err)
	}
	a.mu.Lock()
	a.workers[task.ID] = &worker{}
	a.mu.Unlock()
	a.updating.Store(false)
	a.retryWorkspaceMemoryDue(context.Background(), now())
	var count int
	_ = a.store.QueryRow("SELECT count(*) FROM workspace_memory_sync_queue WHERE task_id=?", task.ID).Scan(&count)
	if count != 1 {
		t.Fatal("busy task triggered an idle export")
	}
	a.mu.Lock()
	delete(a.workers, task.ID)
	a.mu.Unlock()
	a.retryWorkspaceMemoryDue(context.Background(), now())
	_ = a.store.QueryRow("SELECT count(*) FROM workspace_memory_sync_queue WHERE task_id=?", task.ID).Scan(&count)
	if count != 0 {
		t.Fatal("idle budget failed to drain small backlog")
	}
	runner := a.runner.(*fakeRunner)
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.inputs) != 0 {
		t.Fatal("offline retry called a model")
	}
}
