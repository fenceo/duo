package main

import (
	"errors"
	"fmt"
)

const harnessClosedSessionReason = "Harness 运行会话已结束，不能恢复原生上下文。请新建空白会话后继续；旧记录仍保留，不会自动带入 AI 上下文。"

var errHarnessSessionClosed = errors.New(harnessClosedSessionReason)
var errSessionResetBlocked = errors.New("无法新建空白会话")

// TaskRuntimeStatus is a read-only snapshot, not persisted task state. No
// environment, credential reference, process ID or native diagnostics escape.
type TaskRuntimeStatus struct {
	State       string `json:"state"`
	CanContinue bool   `json:"can_continue"`
	Reason      string `json:"reason"`
}

func harnessRuntimeStatus(task Task) *TaskRuntimeStatus {
	if task.Engine != "deepseek-harness" {
		return nil
	}
	if task.Session == "" {
		return &TaskRuntimeStatus{State: "new", CanContinue: true, Reason: "尚未建立 Harness 运行会话，首条消息将启动一个空白会话。"}
	}
	closed := &TaskRuntimeStatus{State: "closed", CanContinue: false, Reason: harnessClosedSessionReason}
	harnessRuntimes.Lock()
	defer harnessRuntimes.Unlock()
	w := harnessRuntimes.workers[task.Session]
	if w == nil {
		return closed
	}
	select {
	case <-w.done:
		return closed
	case <-w.halt:
		return closed
	default:
	}
	if w.stdoutClosed.Load() {
		return closed
	}
	w.mu.Lock()
	readFailed := w.readErr != nil
	w.mu.Unlock()
	if readFailed {
		return closed
	}
	if !w.lease.TryLock() {
		return &TaskRuntimeStatus{State: "busy", CanContinue: true, Reason: "Harness 正在执行；继续发送的消息将排队。"}
	}
	w.lease.Unlock()
	return &TaskRuntimeStatus{State: "live", CanContinue: true, Reason: "Harness 运行会话可继续；空闲 30 分钟后会关闭。"}
}

// resetSession deliberately clears only the native session identity. It never
// rebuilds context from retained events, runs, or knowledge. Serializing with
// submit/stop prevents an accepted queued message from losing its session.
func (a *App) resetSession(id string) (Task, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ctx.Err() != nil {
		return Task{}, fmt.Errorf("%w：服务正在关闭", errSessionResetBlocked)
	}
	task, err := a.store.task(id)
	if err != nil {
		return Task{}, err
	}
	if task.Archived || task.Deleted {
		return Task{}, fmt.Errorf("%w：任务已归档或删除，请先恢复任务", errSessionResetBlocked)
	}
	if a.workers[id] != nil || task.Status == "running" || task.Status == "queued" {
		return Task{}, fmt.Errorf("%w：任务正在执行或排队，请先停止任务", errSessionResetBlocked)
	}
	var pending int
	if err := a.store.QueryRow("SELECT count(*) FROM runs WHERE task_id=? AND status IN ('queued','running')", id).Scan(&pending); err != nil {
		return Task{}, err
	}
	if pending > 0 {
		return Task{}, fmt.Errorf("%w：任务仍有执行中或排队的消息，请先停止任务", errSessionResetBlocked)
	}
	if task.Engine == "deepseek-harness" {
		if err := closeIdleHarnessSession(task.Session); err != nil {
			return Task{}, fmt.Errorf("%w：%v", errSessionResetBlocked, err)
		}
	}
	stamp := now()
	if _, err := a.store.Exec("UPDATE tasks SET session='',updated=? WHERE id=?", stamp, id); err != nil {
		return Task{}, err
	}
	task.Session, task.Updated = "", stamp
	a.changed()
	return task, nil
}
