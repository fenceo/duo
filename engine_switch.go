package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
)

type handoffRequest struct {
	ExpectedBindingRevision string                   `json:"expected_binding_revision"`
	EnvironmentID           string                   `json:"environment_id"`
	Engine                  string                   `json:"engine"`
	Model                   string                   `json:"model"`
	ReasoningEffort         string                   `json:"reasoning_effort"`
	Workspace               string                   `json:"workspace"`
	ModeID                  string                   `json:"mode_id"`
	ProfileID               string                   `json:"profile_id"`
	ExpectedProfile         *EngineCredentialProfile `json:"expected_profile,omitempty"`
	ContextMode             string                   `json:"context_mode"`
	Fingerprint             string                   `json:"fingerprint"`
	Confirm                 bool                     `json:"confirm"`
	PreserveLegacySession   bool                     `json:"preserve_legacy_session"`
	ExpectedLegacySession   string                   `json:"expected_legacy_session,omitempty"`
}

type handoffResult struct {
	Task       Task `json:"task"`
	NewSession bool `json:"new_session"`
}

var errHandoffChanged = errors.New("任务或配置已改变，请重新读取预览后切换")

// Switching is a local atomic edit, not a model request. The next submitted
// message opens/resumes the selected engine. History stays in the same task.
func (a *App) switchTaskEngine(id string, v handoffRequest) (handoffResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !v.Confirm || v.Fingerprint == "" {
		return handoffResult{}, errors.New("请先检查接续预览并确认切换")
	}
	if a.updating.Load() || a.ctx.Err() != nil {
		return handoffResult{}, errors.New("服务正在更新或关闭")
	}
	task, err := a.store.task(id)
	if err != nil {
		return handoffResult{}, err
	}
	if task.Environment == nil {
		return handoffResult{}, errors.New("任务缺少执行环境，请先恢复环境配置")
	}
	rawRequest, _ := json.Marshal(v)
	opHash := sha256.Sum256(append([]byte(id), rawRequest...))
	operation := hex.EncodeToString(opHash[:])
	if task.Binding != nil && task.Binding.Revision == operation {
		return handoffResult{task, task.Session == ""}, nil
	}
	revision := "legacy"
	if task.Binding != nil {
		revision = task.Binding.Revision
	}
	if v.ExpectedBindingRevision != revision {
		return handoffResult{}, errHandoffChanged
	}
	if task.Archived || task.Deleted {
		return handoffResult{}, errors.New("请先恢复任务")
	}
	var pending int
	if err = a.store.QueryRow("SELECT count(*) FROM runs WHERE task_id=? AND status IN ('queued','running')", id).Scan(&pending); err != nil {
		return handoffResult{}, err
	}
	if a.workers[id] != nil || pending > 0 || task.Status == "running" || task.Status == "queued" {
		return handoffResult{}, errors.New("请等待当前执行结束，或先停止并取消排队，再切换 AI")
	}
	if !validEngine(v.Engine) || !validEngineReasoning(v.Engine, v.ReasoningEffort) {
		return handoffResult{}, errors.New("引擎或推理强度无效")
	}
	if len(v.Model) > 120 || strings.ContainsAny(v.Model, "\x00\r\n") {
		return handoffResult{}, errors.New("模型名称无效")
	}
	env, err := a.config.get().environment(v.EnvironmentID)
	if err != nil {
		return handoffResult{}, err
	}
	v.Workspace = strings.TrimSpace(v.Workspace)
	validPath := strings.HasPrefix(v.Workspace, "/")
	if env.Type == "windows" {
		validPath = filepath.IsAbs(v.Workspace)
	}
	if !validPath || len(v.Workspace) > 4096 || strings.ContainsAny(v.Workspace, "\x00\r\n") {
		return handoffResult{}, errors.New("请填写目标环境中的工作目录绝对路径")
	}
	var profile *EngineCredentialProfile
	if v.ProfileID == "__current__" {
		if task.Binding == nil || task.Engine != v.Engine || task.Environment.ID != env.ID {
			return handoffResult{}, errHandoffChanged
		}
		profile = task.Binding.Profile
	} else {
		profile, err = a.store.selectedEngineProfile(env.ID, v.Engine, v.ProfileID)
		if err != nil {
			return handoffResult{}, err
		}
		if !sameEngineProfile(profile, v.ExpectedProfile) || profile != nil && profile.Updated != v.ExpectedProfile.Updated {
			return handoffResult{}, errHandoffChanged
		}
	}
	var mode *WorkMode
	if v.ModeID == "__current__" {
		if task.Engine != v.Engine || task.Environment.ID != env.ID {
			return handoffResult{}, errHandoffChanged
		}
		mode = task.Mode // Includes the original nil/default permission semantics.
	} else {
		selected, modeErr := a.store.resolveMode(v.ModeID, nil)
		if modeErr != nil {
			return handoffResult{}, modeErr
		}
		mode = &selected
	}
	if mode != nil && (v.Engine != "codex" && mode.Approval == "auto" || v.Engine != "deepseek-harness" && mode.ID == "harness:read") {
		return handoffResult{}, errors.New("权限模式不适用于目标引擎")
	}
	if v.Engine == "deepseek-harness" {
		if _, err = harnessPolicy(Task{Mode: mode, Workspace: v.Workspace}); err != nil {
			return handoffResult{}, err
		}
	}
	preview, archive, err := a.buildContinuation(task, v.ContextMode)
	if err != nil {
		return handoffResult{}, err
	}
	if preview.Fingerprint != v.Fingerprint {
		return handoffResult{}, errHandoffChanged
	}
	newSession := task.Binding == nil || task.Engine != v.Engine || task.Workspace != v.Workspace || !reflect.DeepEqual(task.Environment, &env) || !sameEngineProfile(task.Binding.Profile, profile)
	if task.Engine == "deepseek-harness" && !reflect.DeepEqual(task.Mode, mode) {
		newSession = true
	}
	// Legacy rows cannot prove their original account. Only an explicit user
	// attestation may attach a profile without replacing the native session.
	// Never interpret merely opening the dialog or selecting a default as consent.
	if v.PreserveLegacySession {
		if task.Binding != nil || task.Session == "" || v.ExpectedLegacySession != task.Session {
			return handoffResult{}, errHandoffChanged
		}
		if task.Engine != v.Engine || task.Workspace != v.Workspace || !reflect.DeepEqual(task.Environment, &env) || !reflect.DeepEqual(task.Mode, mode) {
			return handoffResult{}, errors.New("保留旧会话时不能同时改变引擎、执行环境、工作目录或权限；请先确认原配置")
		}
		newSession = false
	}
	previous, _ := json.Marshal(task)
	if newSession && task.Engine == "deepseek-harness" {
		if err = closeIdleHarnessSession(task.Session); err != nil {
			return handoffResult{}, err
		}
	}
	binding := &TaskEngineBinding{Revision: operation, Profile: profile}
	if !newSession && task.Binding != nil {
		binding.HistoryID = task.Binding.HistoryID
	}
	tx, err := a.store.Begin()
	if err != nil {
		return handoffResult{}, err
	}
	defer tx.Rollback()
	legacy, _ := json.Marshal(RunExecution{Engine: task.Engine})
	if _, err = tx.Exec("INSERT INTO run_execution(run_id,snapshot) SELECT id,? FROM runs WHERE task_id=? AND NOT EXISTS(SELECT 1 FROM run_execution x WHERE x.run_id=runs.id)", string(legacy), id); err != nil {
		return handoffResult{}, err
	}
	if newSession {
		binding.HistoryID = uid()
		if _, err = tx.Exec("INSERT INTO task_handoff_context VALUES(?,?,?,?,?)", binding.HistoryID, id, preview.Context, archive, now()); err != nil {
			return handoffResult{}, err
		}
		task.Session = ""
	}
	task.Engine, task.Model, task.ReasoningEffort, task.Environment, task.Workspace, task.Mode, task.Binding, task.Updated = v.Engine, strings.TrimSpace(v.Model), v.ReasoningEffort, &env, v.Workspace, mode, binding, now()
	envJSON, _ := json.Marshal(env)
	modeJSON, _ := json.Marshal(mode)
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO task_engine_switches VALUES(?,?,?,?)", []any{operation, id, string(previous), now()}},
		{"UPDATE tasks SET workspace=?,model=?,session=?,updated=? WHERE id=?", []any{task.Workspace, task.Model, task.Session, task.Updated, id}},
		{"INSERT INTO task_execution(task_id,engine,reasoning_effort) VALUES(?,?,?) ON CONFLICT(task_id) DO UPDATE SET engine=excluded.engine,reasoning_effort=excluded.reasoning_effort", []any{id, task.Engine, task.ReasoningEffort}},
		{"INSERT INTO task_environments VALUES(?,?) ON CONFLICT(task_id) DO UPDATE SET environment=excluded.environment", []any{id, string(envJSON)}},
		{"INSERT INTO task_options(task_id,mode) VALUES(?,?) ON CONFLICT(task_id) DO UPDATE SET mode=excluded.mode", []any{id, string(modeJSON)}},
	} {
		if _, err = tx.Exec(statement.query, statement.args...); err != nil {
			return handoffResult{}, err
		}
	}
	if err = saveEngineBinding(tx, id, binding); err != nil {
		return handoffResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return handoffResult{}, err
	}
	a.changed()
	return handoffResult{task, newSession}, nil
}

func (s *Server) handoffRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/tasks/{id}/continuation/archive", s.secure(func(w http.ResponseWriter, r *http.Request) {
		task, err := s.app.store.task(r.PathValue("id"))
		if err != nil {
			fail(w, http.StatusNotFound, "任务不存在")
			return
		}
		_, archive, err := s.app.buildContinuation(task, r.URL.Query().Get("mode"))
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(archive))
	}))
	m.HandleFunc("POST /api/tasks/{id}/handoff", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v handoffRequest
		if !body(w, r, &v) {
			return
		}
		if !v.Confirm {
			fail(w, http.StatusPreconditionRequired, "请确认接续内容后切换")
			return
		}
		result, err := s.app.switchTaskEngine(r.PathValue("id"), v)
		if err != nil {
			fail(w, http.StatusConflict, err.Error())
			return
		}
		jsonOut(w, http.StatusOK, result)
	}))
}
