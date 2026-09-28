package main

import (
	"errors"
	"strings"
)

// Serialize model edits with submit/stop. A running or queued turn keeps its
// route; idle edits apply through ACP before the next prompt on the same log.
func (a *App) updateHarnessModel(id string, title, model, effort *string) (Task, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	task, err := a.store.task(id)
	if err != nil {
		return Task{}, err
	}
	if a.ctx.Err() != nil || a.updating.Load() {
		return Task{}, errors.New("服务正在关闭或更新，请稍后重试")
	}
	if task.Archived || task.Deleted {
		return Task{}, errors.New("请先恢复任务再切换模型")
	}
	var pending int
	if err := a.store.QueryRow("SELECT count(*) FROM runs WHERE task_id=? AND status IN ('running','queued')", id).Scan(&pending); err != nil {
		return Task{}, err
	}
	if a.workers[id] != nil || task.Status == "running" || task.Status == "queued" || pending > 0 {
		return Task{}, errors.New("请等待当前任务结束或停止任务后再切换模型")
	}
	if title != nil {
		task.Title = strings.TrimSpace(*title)
		if task.Title == "" || len([]rune(task.Title)) > 180 {
			return Task{}, errors.New("任务名称无效")
		}
	}
	if model != nil {
		selected := strings.TrimSpace(*model)
		if len(selected) > 120 || strings.ContainsAny(selected, "\r\n") {
			return Task{}, errors.New("模型名称无效")
		}
		if selected != task.Model {
			task.ReasoningEffort = ""
		}
		task.Model = selected
	}
	if effort != nil {
		task.ReasoningEffort = strings.TrimSpace(*effort)
		if !validEngineReasoning(task.Engine, task.ReasoningEffort) {
			return Task{}, errors.New("此 AI 工具不支持所选推理强度")
		}
	}
	tx, err := a.store.Begin()
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	task.Updated = now()
	if _, err := tx.Exec("UPDATE tasks SET title=?,model=?,updated=? WHERE id=?", task.Title, task.Model, task.Updated, id); err != nil {
		return Task{}, err
	}
	if _, err := tx.Exec("INSERT INTO task_execution(task_id,reasoning_effort,engine) VALUES(?,?,?) ON CONFLICT(task_id) DO UPDATE SET reasoning_effort=excluded.reasoning_effort", id, task.ReasoningEffort, task.Engine); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	a.changed()
	return task, nil
}
