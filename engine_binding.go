package main

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// Profile records the account that owns the native session, even for followers.
// Resolve environment defaults once per run, never halfway through execution.
type TaskEngineBinding struct {
	Revision       string                   `json:"revision"`
	AccountMode    string                   `json:"account_mode,omitempty"`
	NativeRevision string                   `json:"native_revision,omitempty"`
	Profile        *EngineCredentialProfile `json:"profile,omitempty"`
	HistoryID      string                   `json:"history_id,omitempty"`
}

const engineBindingSchema = `
CREATE TABLE IF NOT EXISTS task_engine_bindings(task_id TEXT PRIMARY KEY REFERENCES tasks(id),binding TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS run_execution(run_id TEXT PRIMARY KEY REFERENCES runs(id),snapshot TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS task_handoff_context(id TEXT PRIMARY KEY,task_id TEXT NOT NULL REFERENCES tasks(id),context TEXT NOT NULL,archive TEXT NOT NULL,created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS task_engine_switches(id TEXT PRIMARY KEY,task_id TEXT NOT NULL REFERENCES tasks(id),previous TEXT NOT NULL,created INTEGER NOT NULL);
UPDATE task_engine_bindings SET binding=json_set(binding,'$.account_mode',
 CASE WHEN EXISTS(SELECT 1 FROM task_engine_switches s WHERE s.task_id=task_engine_bindings.task_id) THEN 'pinned' ELSE 'environment' END)
 WHERE json_type(binding)='object' AND json_extract(binding,'$.account_mode') IS NULL;
`

func (s *Store) selectedEngineProfile(environment, engine, id string) (*EngineCredentialProfile, error) {
	if id == "" {
		return nil, nil // Explicit native default, not the mutable Duo default.
	}
	for _, p := range s.engineProfiles() {
		if p.ID == id && p.EnvironmentID == environment && p.Engine == engine {
			if !validEngineCredentialKind(engine, p.Kind) || p.Kind == "env_file" {
				return nil, errors.New("此账号/API 引用尚不能用于执行")
			}
			return &p, nil
		}
	}
	return nil, errors.New("账号/API 配置不存在或不属于所选引擎和环境")
}

func (s *Store) defaultEngineBinding(task Task) (*TaskEngineBinding, error) {
	if task.Environment == nil {
		return nil, errors.New("任务缺少固定的执行环境，请先通过切换 AI 选择环境")
	}
	profile, err := s.selectedEngineProfile(task.Environment.ID, task.Engine, s.activeEngineProfile(task.Environment.ID, task.Engine))
	if err != nil {
		return nil, err
	}
	return &TaskEngineBinding{Revision: uid(), AccountMode: "environment", Profile: profile, NativeRevision: s.nativeAccountRevision(task.Environment.ID, task.Engine)}, nil
}

func (s *Store) nativeAccountRevision(environment, engine string) string {
	return s.setting("engine_native_revision:" + environment + ":" + engine)
}

// Metadata must describe the account used by the next turn, not the last turn.
// This copy does not alter session ownership or start a continuation.
func (s *Store) taskAccountForLookup(task Task) (Task, error) {
	if task.Binding != nil && task.Binding.AccountMode == "environment" {
		binding, err := s.defaultEngineBinding(task)
		if err != nil {
			return task, err
		}
		task.Binding = binding
	}
	return task, nil
}

func saveEngineBinding(tx *sql.Tx, task string, binding *TaskEngineBinding) error {
	raw, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO task_engine_bindings VALUES(?,?) ON CONFLICT(task_id) DO UPDATE SET binding=excluded.binding", task, string(raw))
	return err
}

func sameEngineProfile(a, b *EngineCredentialProfile) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.ID == b.ID && a.Engine == b.Engine && a.EnvironmentID == b.EnvironmentID && a.Kind == b.Kind && a.Reference == b.Reference
}

// Queued runs retain the model and account mode selected at submission. Pinned
// profiles stay fixed; followers resolve the environment when execution starts.
type RunExecution struct {
	Engine    string             `json:"engine"`
	Model     string             `json:"model"`
	Reasoning string             `json:"reasoning"`
	Binding   *TaskEngineBinding `json:"binding"`
}

func saveRunExecution(tx *sql.Tx, run string, task Task) error {
	raw, err := json.Marshal(RunExecution{task.Engine, task.Model, task.ReasoningEffort, task.Binding})
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO run_execution VALUES(?,?)", run, string(raw))
	return err
}

func (s *Store) applyRunExecution(run string, task *Task) error {
	var raw string
	err := s.QueryRow("SELECT snapshot FROM run_execution WHERE run_id=?", run).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var snapshot RunExecution
	if err = json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return err
	}
	if snapshot.Binding != nil && snapshot.Binding.AccountMode == "environment" && task.Binding != nil {
		// A preceding queued turn may have changed both account and history.
		snapshot.Binding = task.Binding
	}
	task.Engine, task.Model, task.ReasoningEffort, task.Binding = snapshot.Engine, snapshot.Model, snapshot.Reasoning, snapshot.Binding
	return nil
}
