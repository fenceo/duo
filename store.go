package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"time"
)

func uid() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func now() int64 { return time.Now().UnixMilli() }

type Task struct {
	Mode            *WorkMode           `json:"mode,omitempty"`
	Deleted         bool                `json:"deleted"`
	Files           []RuntimeAttachment `json:"-"`
	Pinned          bool                `json:"pinned"`
	Archived        bool                `json:"archived"`
	Environment     *Environment        `json:"environment"`
	ID              string              `json:"id"`
	Title           string              `json:"title"`
	Workspace       string              `json:"workspace"`
	Model           string              `json:"model"`
	ReasoningEffort string              `json:"reasoning_effort"`
	Engine          string              `json:"engine"`
	Session         string              `json:"session"`
	Status          string              `json:"status"`
	Created         int64               `json:"created"`
	Updated         int64               `json:"updated"`
}
type Run struct {
	Started     int64        `json:"started"`
	Usage       *RunUsage    `json:"usage"`
	Mode        *WorkMode    `json:"mode,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	ID          string       `json:"id"`
	TaskID      string       `json:"task_id"`
	Input       string       `json:"input"`
	Kind        string       `json:"kind"`
	Source      string       `json:"source"`
	Status      string       `json:"status"`
	Result      string       `json:"result"`
	Error       string       `json:"error"`
	Created     int64        `json:"created"`
	Finished    int64        `json:"finished"`
}
type Event struct {
	Seq     int64  `json:"seq"`
	RunID   string `json:"run_id"`
	Kind    string `json:"kind"`
	Text    string `json:"text"`
	Created int64  `json:"created"`
}
type Note struct {
	Content  string `json:"content"`
	Revision int64  `json:"revision"`
	Updated  int64  `json:"updated"`
}
type Store struct{ *sql.DB }

func openStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "jianzuo.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS sessions(token TEXT PRIMARY KEY,csrf TEXT NOT NULL,expires INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS tasks(id TEXT PRIMARY KEY,title TEXT NOT NULL,workspace TEXT NOT NULL,model TEXT NOT NULL,session TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'idle',created INTEGER NOT NULL,updated INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS task_environments(task_id TEXT PRIMARY KEY REFERENCES tasks(id),environment TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS task_execution(task_id TEXT PRIMARY KEY REFERENCES tasks(id),reasoning_effort TEXT NOT NULL DEFAULT '',engine TEXT NOT NULL DEFAULT 'codex');
 CREATE TABLE IF NOT EXISTS task_continuations(target_task_id TEXT PRIMARY KEY REFERENCES tasks(id),source_task_id TEXT NOT NULL REFERENCES tasks(id),source_engine TEXT NOT NULL,target_engine TEXT NOT NULL,transferred_runs INTEGER NOT NULL DEFAULT 0,transferred_knowledge INTEGER NOT NULL DEFAULT 0,created INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS task_continuations_source ON task_continuations(source_task_id,created);
 CREATE TABLE IF NOT EXISTS task_preferences(task_id TEXT PRIMARY KEY REFERENCES tasks(id),pinned INTEGER NOT NULL DEFAULT 0,archived INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS runs(id TEXT PRIMARY KEY,task_id TEXT NOT NULL REFERENCES tasks(id),input TEXT NOT NULL,kind TEXT NOT NULL,source TEXT NOT NULL,status TEXT NOT NULL,result TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',created INTEGER NOT NULL,finished INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS events(seq INTEGER PRIMARY KEY AUTOINCREMENT,task_id TEXT NOT NULL REFERENCES tasks(id),run_id TEXT NOT NULL,kind TEXT NOT NULL,text TEXT NOT NULL,created INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS events_task ON events(task_id,seq);
 CREATE INDEX IF NOT EXISTS runs_task ON runs(task_id,created);
CREATE TABLE IF NOT EXISTS notes(task_id TEXT PRIMARY KEY REFERENCES tasks(id),content TEXT NOT NULL DEFAULT '',revision INTEGER NOT NULL DEFAULT 0,updated INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS scratch(id TEXT PRIMARY KEY,task_id TEXT NOT NULL REFERENCES tasks(id),content TEXT NOT NULL,revision INTEGER NOT NULL,updated INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS knowledge_entries(id TEXT PRIMARY KEY,task_id TEXT NOT NULL REFERENCES tasks(id),title TEXT NOT NULL DEFAULT '',content TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'observed',source TEXT NOT NULL DEFAULT 'manual',run_id TEXT NOT NULL DEFAULT '',revision INTEGER NOT NULL DEFAULT 1,created INTEGER NOT NULL DEFAULT 0,updated INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS knowledge_entries_task ON knowledge_entries(task_id,updated);
CREATE INDEX IF NOT EXISTS knowledge_entries_run ON knowledge_entries(task_id,run_id);
-- The single markdown note was the first knowledge store. Import it once so a
-- task keeps its history in the entry list instead of a hidden legacy table.
INSERT INTO knowledge_entries(id,task_id,title,content,status,source,run_id,revision,created,updated)
 SELECT lower(hex(randomblob(12))),task_id,'任务知识',content,'verified','migrated','',MAX(revision,1),updated,updated FROM notes
 WHERE content<>'' AND NOT EXISTS(SELECT 1 FROM settings WHERE key='knowledge_entries_v1');
INSERT OR IGNORE INTO settings VALUES('knowledge_entries_v1','1');
CREATE TABLE IF NOT EXISTS hardware(id TEXT PRIMARY KEY,task_id TEXT NOT NULL REFERENCES tasks(id),config TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS hardware_io(seq INTEGER PRIMARY KEY AUTOINCREMENT,hardware_id TEXT NOT NULL REFERENCES hardware(id) ON DELETE CASCADE,direction TEXT NOT NULL,data TEXT NOT NULL,created INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS hardware_io_device ON hardware_io(hardware_id,seq);
 CREATE TABLE IF NOT EXISTS task_hardware(task_id TEXT NOT NULL REFERENCES tasks(id),hardware_id TEXT NOT NULL REFERENCES hardware(id) ON DELETE CASCADE,PRIMARY KEY(task_id,hardware_id));
 CREATE TABLE IF NOT EXISTS hardware_ai_access(task_id TEXT NOT NULL,hardware_id TEXT NOT NULL,allow_read INTEGER NOT NULL DEFAULT 0,allow_write INTEGER NOT NULL DEFAULT 0,allow_power INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(task_id,hardware_id),FOREIGN KEY(task_id,hardware_id) REFERENCES task_hardware(task_id,hardware_id) ON DELETE CASCADE);
 CREATE TABLE IF NOT EXISTS hardware_io_tasks(seq INTEGER PRIMARY KEY REFERENCES hardware_io(seq) ON DELETE CASCADE,task_id TEXT NOT NULL REFERENCES tasks(id));
 INSERT OR IGNORE INTO task_hardware SELECT task_id,id FROM hardware WHERE NOT EXISTS(SELECT 1 FROM settings WHERE key='hardware_library_v1');
 INSERT OR IGNORE INTO hardware_io_tasks SELECT i.seq,h.task_id FROM hardware_io i JOIN hardware h ON h.id=i.hardware_id WHERE NOT EXISTS(SELECT 1 FROM settings WHERE key='hardware_library_v1');
 INSERT OR IGNORE INTO settings VALUES('hardware_library_v1','1');
 CREATE TABLE IF NOT EXISTS bindings(chat_id TEXT PRIMARY KEY,task_id TEXT NOT NULL REFERENCES tasks(id));
 CREATE TABLE IF NOT EXISTS seen(message_id TEXT PRIMARY KEY,created INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS outbox(id TEXT PRIMARY KEY,chat_id TEXT NOT NULL,text TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'pending',attempts INTEGER NOT NULL DEFAULT 0,next_at INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS outbox_cards(outbox_id TEXT PRIMARY KEY REFERENCES outbox(id),content TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS feishu_run_cards(id TEXT PRIMARY KEY,run_id TEXT NOT NULL REFERENCES runs(id),chat_id TEXT NOT NULL,app_id TEXT NOT NULL,owner TEXT NOT NULL,message_id TEXT NOT NULL DEFAULT '',last_hash TEXT NOT NULL DEFAULT '',state TEXT NOT NULL DEFAULT 'pending',attempts INTEGER NOT NULL DEFAULT 0,next_at INTEGER NOT NULL DEFAULT 0,first_attempt INTEGER NOT NULL DEFAULT 0,UNIQUE(run_id,chat_id));
 CREATE INDEX IF NOT EXISTS feishu_run_cards_due ON feishu_run_cards(state,next_at);
 CREATE INDEX IF NOT EXISTS events_run ON events(run_id,kind,seq);
 CREATE TABLE IF NOT EXISTS feishu_card_actions(id TEXT PRIMARY KEY,chat_id TEXT NOT NULL,task_id TEXT NOT NULL,action TEXT NOT NULL,query TEXT NOT NULL DEFAULT '',offset INTEGER NOT NULL DEFAULT 0,expires INTEGER NOT NULL,used INTEGER NOT NULL DEFAULT 0);
 DELETE FROM feishu_card_actions WHERE expires<unixepoch()*1000;
 UPDATE runs SET status='interrupted',error='服务重启，请检查后继续',finished=unixepoch()*1000 WHERE status IN ('running','queued');
 UPDATE tasks SET status='interrupted' WHERE status IN ('running','queued');`)
	if err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec(workbenchSchema); err != nil {
		db.Close()
		return nil, err
	}
	if err = ensureColumns(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
}

// SQLite has no "ADD COLUMN IF NOT EXISTS", so long-lived installs get the new
// sticky-note fields through an explicit column check.
func ensureColumns(db *sql.DB) error {
	for _, c := range []struct{ table, column, ddl string }{
		{"scratch", "title", "TEXT NOT NULL DEFAULT ''"},
		{"scratch", "status", "TEXT NOT NULL DEFAULT 'todo'"},
		{"scratch", "due", "TEXT NOT NULL DEFAULT ''"},
		{"scratch", "done_at", "INTEGER NOT NULL DEFAULT 0"},
	} {
		rows, err := db.Query("PRAGMA table_info(" + c.table + ")")
		if err != nil {
			return err
		}
		found := false
		for rows.Next() {
			var id, notNull, primary int
			var name, kind string
			var fallback sql.NullString
			if err = rows.Scan(&id, &name, &kind, &notNull, &fallback, &primary); err != nil {
				rows.Close()
				return err
			}
			if name == c.column {
				found = true
			}
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		if found {
			continue
		}
		if _, err = db.Exec("ALTER TABLE " + c.table + " ADD COLUMN " + c.column + " " + c.ddl); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) setting(key string) string {
	var v string
	_ = s.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&v)
	return v
}
func (s *Store) set(key, value string) error {
	_, e := s.Exec("INSERT INTO settings VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return e
}
func (s *Store) tasks(includeDeleted ...bool) ([]Task, error) {
	filter := " WHERE COALESCE(o.deleted,0)=0"
	if len(includeDeleted) > 0 && includeDeleted[0] {
		filter = ""
	}
	rows, e := s.Query("SELECT tasks.id,title,workspace,model,session,status,created,updated,COALESCE(environment,''),COALESCE(pinned,0),COALESCE(archived,0),COALESCE(reasoning_effort,''),COALESCE(engine,'codex'),COALESCE(o.mode,'{}'),COALESCE(o.deleted,0) FROM tasks LEFT JOIN task_environments ON tasks.id=task_environments.task_id LEFT JOIN task_preferences ON tasks.id=task_preferences.task_id LEFT JOIN task_execution ON tasks.id=task_execution.task_id LEFT JOIN task_options o ON tasks.id=o.task_id" + filter + " ORDER BY COALESCE(pinned,0) DESC,updated DESC,tasks.id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		var t Task
		var envJSON, modeJSON string
		if e = rows.Scan(&t.ID, &t.Title, &t.Workspace, &t.Model, &t.Session, &t.Status, &t.Created, &t.Updated, &envJSON, &t.Pinned, &t.Archived, &t.ReasoningEffort, &t.Engine, &modeJSON, &t.Deleted); e != nil {
			return nil, e
		}
		if envJSON != "" {
			if e = json.Unmarshal([]byte(envJSON), &t.Environment); e != nil {
				return nil, e
			}
		}
		_ = json.Unmarshal([]byte(modeJSON), &t.Mode)
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) task(id string) (Task, error) {
	var t Task
	var envJSON, modeJSON string
	e := s.QueryRow("SELECT tasks.id,title,workspace,model,session,status,created,updated,COALESCE(environment,''),COALESCE(pinned,0),COALESCE(archived,0),COALESCE(reasoning_effort,''),COALESCE(engine,'codex'),COALESCE(o.mode,'{}'),COALESCE(o.deleted,0) FROM tasks LEFT JOIN task_environments ON tasks.id=task_environments.task_id LEFT JOIN task_preferences ON tasks.id=task_preferences.task_id LEFT JOIN task_execution ON tasks.id=task_execution.task_id LEFT JOIN task_options o ON tasks.id=o.task_id WHERE tasks.id=?", id).Scan(&t.ID, &t.Title, &t.Workspace, &t.Model, &t.Session, &t.Status, &t.Created, &t.Updated, &envJSON, &t.Pinned, &t.Archived, &t.ReasoningEffort, &t.Engine, &modeJSON, &t.Deleted)
	if e == nil && envJSON != "" {
		e = json.Unmarshal([]byte(envJSON), &t.Environment)
	}
	_ = json.Unmarshal([]byte(modeJSON), &t.Mode)
	return t, e
}
func (s *Store) runs(id string) ([]Run, error) {
	rows, e := s.Query(`SELECT runs.id,runs.task_id,runs.input,runs.kind,runs.source,runs.status,runs.result,runs.error,runs.created,runs.finished,
COALESCE(run_metrics.started,0),COALESCE(run_metrics.usage,''),COALESCE(run_options.mode,''),COALESCE(run_options.attachments,'')
FROM runs
LEFT JOIN run_metrics ON run_metrics.run_id=runs.id
LEFT JOIN run_options ON run_options.run_id=runs.id
WHERE runs.task_id=?
ORDER BY runs.created,runs.id`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		var usageJSON, modeJSON, filesJSON string
		if e = rows.Scan(&r.ID, &r.TaskID, &r.Input, &r.Kind, &r.Source, &r.Status, &r.Result, &r.Error, &r.Created, &r.Finished, &r.Started, &usageJSON, &modeJSON, &filesJSON); e != nil {
			return nil, e
		}
		if usageJSON != "" {
			_ = json.Unmarshal([]byte(usageJSON), &r.Usage)
		}
		if modeJSON != "" {
			_ = json.Unmarshal([]byte(modeJSON), &r.Mode)
		}
		if filesJSON != "" {
			_ = json.Unmarshal([]byte(filesJSON), &r.Attachments)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) events(id string, after int64) ([]Event, error) {
	rows, e := s.Query("SELECT seq,run_id,kind,text,created FROM events WHERE task_id=? AND seq>? ORDER BY seq LIMIT 500", id, after)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var v Event
		if e = rows.Scan(&v.Seq, &v.RunID, &v.Kind, &v.Text, &v.Created); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) event(task, run, kind, text string) error {
	_, e := s.Exec("INSERT INTO events(task_id,run_id,kind,text,created) VALUES(?,?,?,?,?)", task, run, kind, text, now())
	return e
}
func (s *Store) note(id string) (Note, error) {
	var n Note
	e := s.QueryRow("SELECT content,revision,updated FROM notes WHERE task_id=?", id).Scan(&n.Content, &n.Revision, &n.Updated)
	if errors.Is(e, sql.ErrNoRows) {
		return n, nil
	}
	return n, e
}

var errConflict = errors.New("知识已被其他窗口修改，请保留当前草稿后重新加载")

func (s *Store) saveNote(id, content string, revision int64) (Note, error) {
	tx, e := s.Begin()
	if e != nil {
		return Note{}, e
	}
	defer tx.Rollback()
	var current int64
	e = tx.QueryRow("SELECT revision FROM notes WHERE task_id=?", id).Scan(&current)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return Note{}, e
	}
	if current != revision {
		return Note{}, errConflict
	}
	n := Note{content, current + 1, now()}
	_, e = tx.Exec("INSERT INTO notes VALUES(?,?,?,?) ON CONFLICT(task_id) DO UPDATE SET content=excluded.content,revision=excluded.revision,updated=excluded.updated", id, n.Content, n.Revision, n.Updated)
	if e != nil {
		return Note{}, e
	}
	return n, tx.Commit()
}
