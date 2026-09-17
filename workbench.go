package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

const workbenchSchema = `
CREATE TABLE IF NOT EXISTS task_options(task_id TEXT PRIMARY KEY REFERENCES tasks(id),mode TEXT NOT NULL DEFAULT '{}',deleted INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS run_options(run_id TEXT PRIMARY KEY REFERENCES runs(id),mode TEXT NOT NULL,attachments TEXT NOT NULL DEFAULT '[]');
CREATE TABLE IF NOT EXISTS run_metrics(run_id TEXT PRIMARY KEY REFERENCES runs(id),started INTEGER NOT NULL DEFAULT 0,usage TEXT NOT NULL DEFAULT 'null');
CREATE TABLE IF NOT EXISTS scratch_colors(id TEXT PRIMARY KEY REFERENCES scratch(id) ON DELETE CASCADE,color TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS attachments(id TEXT PRIMARY KEY,task_id TEXT NOT NULL REFERENCES tasks(id),name TEXT NOT NULL,mime TEXT NOT NULL,data BLOB NOT NULL,created INTEGER NOT NULL);
`

type WorkMode struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Permission string `json:"permission"`
	Prompt     string `json:"prompt"`
	Builtin    bool   `json:"builtin"`
}
type QuickCommand struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
}
type WorkCatalog struct {
	Modes    []WorkMode     `json:"modes"`
	Commands []QuickCommand `json:"commands"`
}

func builtinModes() []WorkMode {
	return []WorkMode{{ID: "work", Name: "直接执行", Permission: "workspace", Builtin: true}, {ID: "plan", Name: "分析规划", Permission: "read", Builtin: true}}
}
func (s *Store) catalog() WorkCatalog {
	var c WorkCatalog
	_ = json.Unmarshal([]byte(s.setting("workbench_catalog")), &c)
	c.Modes = append(builtinModes(), c.Modes...)
	if c.Commands == nil {
		c.Commands = []QuickCommand{}
	}
	return c
}
func validateCatalog(c *WorkCatalog) error {
	if len(c.Modes) > 20 || len(c.Commands) > 40 {
		return errors.New("最多 20 个自定义模式和 40 个指令")
	}
	seen := map[string]bool{"work": true, "plan": true}
	for i := range c.Modes {
		m := &c.Modes[i]
		m.Name = strings.TrimSpace(m.Name)
		m.Builtin = false
		if !safeWorkbenchID(m.ID) || seen[m.ID] || m.Name == "" || len([]rune(m.Name)) > 30 || len(m.Prompt) > 16000 || (m.Permission != "read" && m.Permission != "workspace") {
			return errors.New("模式名称、权限或内容无效")
		}
		seen[m.ID] = true
	}
	seen = map[string]bool{}
	for i := range c.Commands {
		v := &c.Commands[i]
		v.Name = strings.TrimSpace(v.Name)
		if !safeWorkbenchID(v.ID) || seen[v.ID] || v.Name == "" || len([]rune(v.Name)) > 40 || strings.TrimSpace(v.Content) == "" || len(v.Content) > 32000 {
			return errors.New("指令名称或内容无效")
		}
		seen[v.ID] = true
	}
	return nil
}
func safeWorkbenchID(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
func (s *Store) resolveMode(id string, fallback *WorkMode) (WorkMode, error) {
	if id == "" && fallback != nil && fallback.ID != "" {
		return *fallback, nil
	}
	if id == "" {
		id = "work"
	}
	for _, m := range s.catalog().Modes {
		if m.ID == id {
			return m, nil
		}
	}
	return WorkMode{}, errors.New("工作模式已被删除，请重新选择")
}

type RunUsage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	Cached     int64 `json:"cached"`
	CacheWrite int64 `json:"cache_write"`
	Total      int64 `json:"total"`
}

func parseUsage(engine string, raw json.RawMessage) *RunUsage {
	var values map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &values) != nil || values == nil {
		return nil
	}
	fields := map[string]int64{}
	for _, key := range []string{"input_tokens", "output_tokens", "cached_input_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"} {
		if value, ok := values[key]; ok {
			var n int64
			if string(value) == "null" || json.Unmarshal(value, &n) != nil {
				return nil
			}
			fields[key] = n
		}
	}
	in, iok := fields["input_tokens"]
	out, ook := fields["output_tokens"]
	if !iok && !ook {
		return nil
	}
	u := &RunUsage{Input: in, Output: out}
	if engine == "claude" {
		u.Cached = fields["cache_read_input_tokens"]
		u.CacheWrite = fields["cache_creation_input_tokens"]
		u.Total = in + out + u.Cached + u.CacheWrite
	} else {
		u.Cached = fields["cached_input_tokens"]
		u.Total = in + out
	}
	if u.Input < 0 || u.Output < 0 || u.Cached < 0 || u.CacheWrite < 0 {
		return nil
	}
	return u
}
func emitUsage(engine string, raw json.RawMessage, emit func(string, string)) {
	if u := parseUsage(engine, raw); u != nil {
		b, _ := json.Marshal(u)
		emit("usage", string(b))
	}
}
func (s *Store) hydrateRun(r *Run) error {
	var raw, mode, files string
	err := s.QueryRow("SELECT started,usage FROM run_metrics WHERE run_id=?", r.ID).Scan(&r.Started, &raw)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &r.Usage)
	}
	err = s.QueryRow("SELECT mode,attachments FROM run_options WHERE run_id=?", r.ID).Scan(&mode, &files)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if mode != "" {
		_ = json.Unmarshal([]byte(mode), &r.Mode)
	}
	if files != "" {
		_ = json.Unmarshal([]byte(files), &r.Attachments)
	}
	return nil
}
func (a *App) trashTask(id string, restore bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, err := a.store.task(id)
	if err != nil {
		return err
	}
	if a.workers[id] != nil {
		return errors.New("请先停止任务，再删除")
	}
	var active int
	if err = a.store.QueryRow("SELECT count(*) FROM runs WHERE task_id=? AND status IN ('queued','running')", id).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return errors.New("请先停止任务，再删除")
	}
	a.terminals.mu.Lock()
	defer a.terminals.mu.Unlock()
	for _, link := range a.terminals.links {
		if link.Task == id {
			return errors.New("请先关闭该任务的活动终端")
		}
	}
	a.hardware.mu.Lock()
	defer a.hardware.mu.Unlock()
	for _, link := range a.hardware.links {
		if link.controller == id {
			return errors.New("请先释放或断开该任务控制的硬件")
		}
	}
	tx, err := a.store.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec("INSERT INTO task_options(task_id,deleted) VALUES(?,?) ON CONFLICT(task_id) DO UPDATE SET deleted=excluded.deleted", id, !restore)
	if err != nil {
		return err
	}
	// Deleted sessions cannot receive new runs from old Feishu cards or bindings.
	_, err = tx.Exec("INSERT INTO task_preferences(task_id,pinned,archived) VALUES(?,?,1) ON CONFLICT(task_id) DO UPDATE SET archived=1", id, t.Pinned)
	if err != nil {
		return err
	}
	if !restore {
		_, err = tx.Exec("DELETE FROM bindings WHERE task_id=?", id)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Server) workbenchRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/workbench", s.secure(func(w http.ResponseWriter, r *http.Request) { jsonOut(w, 200, s.app.store.catalog()) }))
	m.HandleFunc("PUT /api/workbench", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var c WorkCatalog
		if !body(w, r, &c) {
			return
		}
		if err := validateCatalog(&c); err != nil {
			fail(w, 400, err.Error())
			return
		}
		s.app.mu.Lock()
		defer s.app.mu.Unlock()
		b, _ := json.Marshal(c)
		if err := s.app.store.set("workbench_catalog", string(b)); err != nil {
			fail(w, 500, err.Error())
			return
		}
		jsonOut(w, 200, s.app.store.catalog())
	}))
	m.HandleFunc("DELETE /api/tasks/{id}", s.secure(func(w http.ResponseWriter, r *http.Request) {
		if err := s.app.trashTask(r.PathValue("id"), false); err != nil {
			fail(w, 409, err.Error())
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("POST /api/tasks/{id}/restore", s.secure(func(w http.ResponseWriter, r *http.Request) {
		if err := s.app.trashTask(r.PathValue("id"), true); err != nil {
			fail(w, 409, err.Error())
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("GET /api/trash", s.secure(func(w http.ResponseWriter, r *http.Request) {
		all, err := s.app.store.tasks(true)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		out := []Task{}
		for _, t := range all {
			if t.Deleted {
				out = append(out, t)
			}
		}
		jsonOut(w, 200, out)
	}))
	s.directoryRoutes(m)
	s.attachmentRoutes(m)
}
