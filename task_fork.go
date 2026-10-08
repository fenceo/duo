package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

var errTaskForkBlocked = errors.New("无法分叉会话")

// forkTask takes a transactionally consistent local snapshot. Native sessions,
// pending work, approvals, devices and external chat bindings are not cloned.
func (a *App) forkTask(id string) (Task, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ctx.Err() != nil || a.updating.Load() {
		return Task{}, fmt.Errorf("%w：服务正在关闭或升级", errTaskForkBlocked)
	}
	source, err := a.store.task(id)
	if err != nil {
		return Task{}, err
	}
	var pending int
	if err = a.store.QueryRow("SELECT count(*) FROM runs WHERE task_id=? AND status IN ('running','queued')", id).Scan(&pending); err != nil {
		return Task{}, err
	}
	if source.Deleted || a.workers[id] != nil || pending > 0 || source.Status == "running" || source.Status == "queued" {
		return Task{}, fmt.Errorf("%w：请先停止执行并取消排队；已删除任务不能分叉", errTaskForkBlocked)
	}
	if source.Environment == nil {
		return Task{}, fmt.Errorf("%w：请先为原任务选择执行环境", errTaskForkBlocked)
	}
	target := source
	target.ID, target.Session, target.Status = uid(), "", "idle"
	target.Created, target.Updated = now(), now()
	target.Pinned, target.Archived, target.Deleted, target.Files = false, false, false, nil
	title := []rune(source.Title)
	if len(title) > 174 {
		title = title[:174]
	}
	target.Title = string(title) + " · 分叉"
	target.Binding = &TaskEngineBinding{Revision: uid(), HistoryID: uid()}
	tx, err := a.store.Begin()
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO tasks(id,title,workspace,model,session,status,created,updated) VALUES(?,?,?,?,?,?,?,?)", target.ID, target.Title, target.Workspace, target.Model, "", "idle", target.Created, target.Updated); err != nil {
		return Task{}, err
	}
	env, _ := json.Marshal(target.Environment)
	mode, _ := json.Marshal(target.Mode)
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO task_environments VALUES(?,?)", []any{target.ID, string(env)}},
		{"INSERT INTO task_execution(task_id,engine,reasoning_effort) VALUES(?,?,?)", []any{target.ID, target.Engine, target.ReasoningEffort}},
		{"INSERT INTO task_options(task_id,mode) VALUES(?,?)", []any{target.ID, string(mode)}},
		{"INSERT INTO notes(task_id,content,revision,updated) SELECT ?,content,revision,updated FROM notes WHERE task_id=?", []any{target.ID, id}},
	} {
		if _, err = tx.Exec(statement.query, statement.args...); err != nil {
			return Task{}, err
		}
	}
	runs, err := storedRuns(tx, id)
	if err != nil {
		return Task{}, err
	}
	runIDs, fileIDs := map[string]string{}, map[string]string{}
	for index, run := range runs {
		newID := fmt.Sprintf("%s-%08d", target.ID, index)
		runIDs[run.ID] = newID
		visibleResult, _ := visibleMemoryStream(run.Result, run.ID)
		for i := range run.Attachments {
			file := &run.Attachments[i]
			newFile, exists := fileIDs[file.ID]
			if !exists {
				newFile = uid()
				result, e := tx.Exec("INSERT INTO attachments(id,task_id,name,mime,data,created) SELECT ?,?,name,mime,data,created FROM attachments WHERE id=? AND task_id=?", newFile, target.ID, file.ID, id)
				if e != nil {
					return Task{}, e
				}
				count, e := result.RowsAffected()
				if e != nil || count != 1 {
					return Task{}, fmt.Errorf("%w：历史附件缺失，没有创建不完整的分叉", errTaskForkBlocked)
				}
				fileIDs[file.ID] = newFile
			}
			file.ID = newFile
		}
		files, _ := json.Marshal(run.Attachments)
		runMode, _ := json.Marshal(run.Mode)
		engine := run.Engine
		if engine == "" {
			engine = source.Engine
		}
		snapshot, _ := json.Marshal(RunExecution{Engine: engine, Model: run.Model})
		for _, statement := range []struct {
			query string
			args  []any
		}{
			{"INSERT INTO runs(id,task_id,input,kind,source,status,result,error,created,finished) VALUES(?,?,?,?,?,?,?,?,?,?)", []any{newID, target.ID, run.Input, run.Kind, "fork", run.Status, visibleResult, run.Error, run.Created, run.Finished}},
			{"INSERT INTO run_fork_sources VALUES(?,?,?)", []any{newID, id, run.ID}},
			{"INSERT INTO run_options(run_id,mode,attachments) VALUES(?,?,?)", []any{newID, string(runMode), string(files)}},
			{"INSERT INTO run_execution VALUES(?,?)", []any{newID, string(snapshot)}},
			{"INSERT INTO run_metrics SELECT ?,started,usage FROM run_metrics WHERE run_id=?", []any{newID, run.ID}},
			{"INSERT INTO events(task_id,run_id,kind,text,created) SELECT ?,?,kind,text,created FROM events WHERE task_id=? AND run_id=? AND kind IN ('user','assistant','command','tool','progress','usage','error','status') ORDER BY seq", []any{target.ID, newID, id, run.ID}},
			{"INSERT INTO task_memories SELECT ?,?,payload,updated FROM task_memories WHERE run_id=? AND task_id=?", []any{newID, target.ID, run.ID, id}},
		} {
			if _, err = tx.Exec(statement.query, statement.args...); err != nil {
				return Task{}, err
			}
		}
		if err = cleanForkAssistantEvents(tx, target.ID, newID, run.ID); err != nil {
			return Task{}, err
		}
	}
	// Read every entry, including stale knowledge, without the UI's page limit.
	rows, err := tx.Query("SELECT id,run_id FROM knowledge_entries WHERE task_id=?", id)
	if err != nil {
		return Task{}, err
	}
	type reference struct{ id, run string }
	var knowledge []reference
	for rows.Next() {
		var ref reference
		if err = rows.Scan(&ref.id, &ref.run); err != nil {
			break
		}
		knowledge = append(knowledge, ref)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return Task{}, err
	}
	for _, ref := range knowledge {
		if _, err = tx.Exec("INSERT INTO knowledge_entries SELECT ?,?,title,content,status,source,?,revision,created,updated FROM knowledge_entries WHERE id=? AND task_id=?", uid(), target.ID, runIDs[ref.run], ref.id, id); err != nil {
			return Task{}, err
		}
	}
	// The archive is built from the copied rows within this same transaction.
	// Later changes in the parent cannot alter the branch's initial context.
	preview, archive, err := a.buildContinuation(target, "full", tx)
	if err != nil {
		if errors.Is(err, errContinuationArchiveTooLarge) {
			return Task{}, fmt.Errorf("%w：%v", errTaskForkBlocked, err)
		}
		return Task{}, err
	}
	if _, err = tx.Exec("INSERT INTO task_handoff_context VALUES(?,?,?,?,?)", target.Binding.HistoryID, target.ID, preview.Context, archive, target.Created); err != nil {
		return Task{}, err
	}
	if _, err = tx.Exec("INSERT INTO task_continuations VALUES(?,?,?,?,?,?,?)", target.ID, source.ID, source.Engine, target.Engine, len(runs), len(knowledge), target.Created); err != nil {
		return Task{}, err
	}
	if err = saveEngineBinding(tx, target.ID, target.Binding); err != nil {
		return Task{}, err
	}
	if err = tx.Commit(); err != nil {
		return Task{}, err
	}
	a.changed()
	return target, nil
}

// Memory markers contain the original run ID. Keep only visible reply text
// when remapping history; structured memories are copied separately.
func cleanForkAssistantEvents(tx *sql.Tx, task, run, originalRun string) error {
	rows, err := tx.Query("SELECT seq,text FROM events WHERE task_id=? AND run_id=? AND kind='assistant' ORDER BY seq", task, run)
	if err != nil {
		return err
	}
	type replacement struct {
		seq  int64
		text string
	}
	var changes []replacement
	for rows.Next() {
		var seq int64
		var text string
		if err = rows.Scan(&seq, &text); err != nil {
			break
		}
		if visible, hidden := visibleMemoryStream(text, originalRun); hidden {
			changes = append(changes, replacement{seq, visible})
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, change := range changes {
		if _, err = tx.Exec("UPDATE events SET text=? WHERE seq=?", change.text, change.seq); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) forkTask(w http.ResponseWriter, r *http.Request) {
	var request struct{}
	if !body(w, r, &request) {
		return
	}
	task, err := s.app.forkTask(r.PathValue("id"))
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			fail(w, http.StatusNotFound, "任务不存在")
		case errors.Is(err, errTaskForkBlocked):
			fail(w, http.StatusConflict, err.Error())
		default:
			fail(w, http.StatusInternalServerError, "分叉失败，未改变原会话")
		}
		return
	}
	jsonOut(w, http.StatusCreated, task)
}
