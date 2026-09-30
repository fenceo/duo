package main

import (
	"encoding/json"
	"errors"
)

// Called under App.mu. Persist ownership, continuation and the actual run
// snapshot together. A failed archive or transaction must never launch a CLI
// with the old account's native session.
func (a *App) resolveRunAccount(run string, task *Task) error {
	if task.Binding == nil || task.Environment == nil {
		return nil
	}
	previous := *task.Binding
	binding := previous
	if binding.AccountMode == "environment" {
		resolved, err := a.store.defaultEngineBinding(*task)
		if err != nil {
			return err
		}
		binding.Profile, binding.NativeRevision = resolved.Profile, resolved.NativeRevision
	} else if binding.Profile == nil || binding.Profile.Kind == "native" {
		binding.NativeRevision = a.store.nativeAccountRevision(task.Environment.ID, task.Engine)
	}
	nativeChanged := (binding.Profile == nil || binding.Profile.Kind == "native") && binding.NativeRevision != previous.NativeRevision
	changed := !sameEngineProfile(previous.Profile, binding.Profile) || nativeChanged
	if !changed {
		snapshot, err := json.Marshal(RunExecution{task.Engine, task.Model, task.ReasoningEffort, task.Binding})
		if err != nil {
			return err
		}
		_, err = a.store.Exec("INSERT INTO run_execution VALUES(?,?) ON CONFLICT(run_id) DO UPDATE SET snapshot=excluded.snapshot", run, string(snapshot))
		return err
	}
	// Respect an explicit blank-session reset; a never-started task also has
	// no native conversation to transfer. Existing handoff archives still move.
	needsHistory := task.Session != "" || previous.HistoryID != ""
	var preview ContinuationPreview
	var archive string
	var err error
	if needsHistory {
		preview, archive, err = a.buildContinuation(*task, "full")
		if err != nil {
			return err
		}
	}
	if task.Engine == "deepseek-harness" {
		if err = closeIdleHarnessSession(task.Session); err != nil {
			return err
		}
	}
	next := *task
	binding.Revision = uid()
	if needsHistory {
		binding.HistoryID = uid()
	}
	next.Binding, next.Session = &binding, ""
	snapshot, err := json.Marshal(RunExecution{next.Engine, next.Model, next.ReasoningEffort, next.Binding})
	if err != nil {
		return err
	}
	tx, err := a.store.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if needsHistory {
		if _, err = tx.Exec("INSERT INTO task_handoff_context VALUES(?,?,?,?,?)", binding.HistoryID, task.ID, preview.Context, archive, now()); err != nil {
			return err
		}
	}
	oldTask, err := json.Marshal(task)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO task_engine_switches VALUES(?,?,?,?)", binding.Revision, task.ID, string(oldTask), now()); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE tasks SET session='',updated=? WHERE id=?", now(), task.ID); err != nil {
		return err
	}
	if err = saveEngineBinding(tx, task.ID, next.Binding); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO run_execution VALUES(?,?) ON CONFLICT(run_id) DO UPDATE SET snapshot=excluded.snapshot", run, string(snapshot)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	*task = next
	return nil
}

// Native files are shared by external CLIs. Only write while Duo has no work
// in this environment/engine; holding App.mu also excludes new submissions.
func (a *App) environmentAccountIdle(environment, engine string) error {
	var pending int
	err := a.store.QueryRow(`SELECT count(*) FROM runs r JOIN task_environments e ON e.task_id=r.task_id
 LEFT JOIN task_execution x ON x.task_id=r.task_id
 WHERE r.status IN ('running','queued') AND json_extract(e.environment,'$.id')=? AND COALESCE(x.engine,'codex')=?`, environment, engine).Scan(&pending)
	if err != nil {
		return err
	}
	if pending > 0 {
		return errors.New("目标环境的此引擎仍有运行或排队任务，请结束后再同步原生登录；可先切换 Duo 环境账号，在下一轮生效")
	}
	return nil
}

func (s *Store) useSyncedNativeAccount(environment, engine string) error {
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range map[string]string{engineProfileKey(environment, engine): "", "engine_native_revision:" + environment + ":" + engine: uid()} {
		if _, err = tx.Exec("INSERT INTO settings VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}
