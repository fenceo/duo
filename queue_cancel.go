package main

import (
	"database/sql"
	"errors"
	"net/http"
)

var errRunNotQueued = errors.New("这条消息已开始执行或已结束，不能撤销排队")

const queueWithdrawn = "已撤销排队"

// Serialize with work's queued -> running transition. Never cancel a process.
func (a *App) cancelQueuedRun(task, id string) (Run, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rows, err := a.store.Query(conversationRunSelect+"WHERE r.task_id=? AND r.id=?", task, id)
	if err != nil {
		return Run{}, err
	}
	runs, err := readRuns(rows)
	if err != nil {
		return Run{}, err
	}
	if len(runs) == 0 {
		return Run{}, sql.ErrNoRows
	}
	run := runs[0]
	if run.Status == "interrupted" && run.Error == queueWithdrawn {
		return run, nil
	}
	if run.Status != "queued" {
		return Run{}, errRunNotQueued
	}
	tx, err := a.store.Begin()
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	run.Status, run.Error, run.Finished = "interrupted", queueWithdrawn, now()
	if _, err = tx.Exec("UPDATE runs SET status=?,error=?,finished=? WHERE id=?", run.Status, run.Error, run.Finished, id); err != nil {
		return Run{}, err
	}
	if _, err = tx.Exec(`UPDATE tasks SET updated=?,status=CASE
		WHEN EXISTS(SELECT 1 FROM runs WHERE task_id=? AND status='running') THEN 'running'
		WHEN EXISTS(SELECT 1 FROM runs WHERE task_id=? AND status='queued') THEN 'queued'
		ELSE COALESCE((SELECT status FROM runs WHERE task_id=? AND id<>? ORDER BY created DESC,id DESC LIMIT 1),'idle') END WHERE id=?`, now(), task, task, task, id, task); err != nil {
		return Run{}, err
	}
	if _, err = tx.Exec("INSERT INTO events(task_id,run_id,kind,text,created) VALUES(?,?,'status',?,?)", task, id, "interrupted "+queueWithdrawn, now()); err != nil {
		return Run{}, err
	}
	if err = tx.Commit(); err != nil {
		return Run{}, err
	}
	if worker := a.workers[task]; worker != nil && worker.nextRunID == id {
		worker.nextRunID = ""
	}
	a.changed()
	return run, nil
}

func (s *Server) cancelQueuedRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.app.cancelQueuedRun(r.PathValue("id"), r.PathValue("run"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		if errors.Is(err, errRunNotQueued) {
			status = http.StatusConflict
		}
		fail(w, status, err.Error())
		return
	}
	jsonOut(w, http.StatusOK, run)
}
