package main

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"
)

// A sticky note is a task todo: a short title, optional details, a state and an
// optional due date. Old rows only had content and read as pending todos.
type Scratch struct {
	Color     string `json:"color"`
	TaskID    string `json:"task_id"`
	TaskTitle string `json:"task_title"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Status    string `json:"status"`
	Due       string `json:"due"`
	DoneAt    int64  `json:"done_at"`
	Revision  int64  `json:"revision"`
	Updated   int64  `json:"updated"`
}

const scratchMaxBytes = 65536

var scratchDue = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func scratchState(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "doing", "running", "active", "in_progress":
		return "doing"
	case "done", "complete", "completed", "closed":
		return "done"
	default:
		return "todo"
	}
}

// A todo needs a handle even when the author only typed details.
func scratchHeading(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "#>-*• \t"))
		if line == "" {
			continue
		}
		if utf8.RuneCountInString(line) > 60 {
			line = string([]rune(line)[:60]) + "…"
		}
		return line
	}
	return ""
}

func prepareScratch(v *Scratch) error {
	v.Title = strings.TrimSpace(v.Title)
	v.Content = strings.TrimSpace(v.Content)
	v.Due = strings.TrimSpace(v.Due)
	v.Status = scratchState(v.Status)
	if v.Color != "" && !validScratchColor(v.Color) {
		return errors.New("便签颜色无效")
	}
	if v.Due != "" && !scratchDue.MatchString(v.Due) {
		return errors.New("截止日期请用 2026-09-20 这样的格式")
	}
	if utf8.RuneCountInString(v.Title) > 120 {
		return errors.New("便签标题最多 120 字")
	}
	if len(v.Content) > scratchMaxBytes {
		return errors.New("便签内容最多 64 KiB")
	}
	if v.Title == "" {
		v.Title = scratchHeading(v.Content)
	}
	if v.Title == "" {
		return errors.New("便签需要标题或内容")
	}
	return nil
}

func (s *Server) scratchRoutes(m *http.ServeMux) {
	get := func(w http.ResponseWriter, r *http.Request) {
		out, err := s.app.store.scratchList(r.PathValue("id"))
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		jsonOut(w, 200, out)
	}
	m.HandleFunc("GET /api/tasks/{id}/scratch", s.secure(get))
	m.HandleFunc("GET /api/scratch", s.secure(get))
	m.HandleFunc("POST /api/tasks/{id}/scratch", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v Scratch
		if !body(w, r, &v) {
			return
		}
		if err := prepareScratch(&v); err != nil {
			fail(w, 400, err.Error())
			return
		}
		if _, e := s.app.store.task(r.PathValue("id")); e != nil {
			fail(w, 404, "任务不存在")
			return
		}
		v.ID = uid()
		v.Revision = 1
		v.Updated = now()
		v.TaskID = r.PathValue("id")
		e := s.app.store.writeScratch(v, true)
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		jsonOut(w, 201, v)
	}))
	m.HandleFunc("PUT /api/tasks/{id}/scratch/{sid}", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v Scratch
		if !body(w, r, &v) {
			return
		}
		if err := prepareScratch(&v); err != nil {
			fail(w, 400, err.Error())
			return
		}
		v.ID = r.PathValue("sid")
		v.TaskID = r.PathValue("id")
		if e := s.app.store.writeScratch(v, false); e != nil {
			fail(w, 409, e.Error())
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("DELETE /api/tasks/{id}/scratch/{sid}", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v Scratch
		if !body(w, r, &v) {
			return
		}
		res, e := s.app.store.Exec("DELETE FROM scratch WHERE id=? AND task_id=? AND revision=?", r.PathValue("sid"), r.PathValue("id"), v.Revision)
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			fail(w, 409, "便签已有新版本，请刷新后再删除")
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}))
}

func validScratchColor(c string) bool {
	switch c {
	case "", "yellow", "green", "blue", "pink", "purple":
		return true
	}
	return false
}
func (s *Store) scratchList(task string) ([]Scratch, error) {
	query := `SELECT s.id,s.title,s.content,s.status,s.due,s.done_at,s.revision,s.updated,COALESCE(c.color,'yellow'),s.task_id,t.title FROM scratch s JOIN tasks t ON t.id=s.task_id LEFT JOIN scratch_colors c ON c.id=s.id LEFT JOIN task_options o ON o.task_id=t.id WHERE COALESCE(o.deleted,0)=0`
	args := []any{}
	if task != "" {
		query += " AND s.task_id=?"
		args = append(args, task)
	}
	// Open work first: doing, then todo ordered by due date, then finished work.
	query += " ORDER BY CASE s.status WHEN 'doing' THEN 0 WHEN 'todo' THEN 1 ELSE 2 END,CASE WHEN s.due='' THEN 1 ELSE 0 END,s.due,s.updated DESC LIMIT 500"
	rows, err := s.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Scratch{}
	for rows.Next() {
		var n Scratch
		if err = rows.Scan(&n.ID, &n.Title, &n.Content, &n.Status, &n.Due, &n.DoneAt, &n.Revision, &n.Updated, &n.Color, &n.TaskID, &n.TaskTitle); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func (s *Store) writeScratch(v Scratch, create bool) error {
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	v.Status = scratchState(v.Status)
	if create {
		if v.Color == "" {
			v.Color = "yellow"
		}
		// Callers that reach the store directly still get a usable todo handle.
		if v.Title == "" {
			v.Title = scratchHeading(v.Content)
		}
		if v.Status == "done" {
			v.DoneAt = now()
		}
		_, err = tx.Exec("INSERT INTO scratch(id,task_id,title,content,status,due,done_at,revision,updated) VALUES(?,?,?,?,?,?,?,?,?)", v.ID, v.TaskID, v.Title, v.Content, v.Status, v.Due, v.DoneAt, v.Revision, v.Updated)
	} else {
		if v.Color == "" {
			err = tx.QueryRow("SELECT color FROM scratch_colors WHERE id=?", v.ID).Scan(&v.Color)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			if v.Color == "" {
				v.Color = "yellow"
			}
		}
		var doneAt int64
		if err = tx.QueryRow("SELECT done_at FROM scratch WHERE id=? AND task_id=?", v.ID, v.TaskID).Scan(&doneAt); err != nil {
			if err == sql.ErrNoRows {
				return errors.New("便签不存在")
			}
			return err
		}
		switch {
		case v.Status != "done":
			v.DoneAt = 0
		case doneAt == 0:
			v.DoneAt = now()
		default:
			v.DoneAt = doneAt
		}
		var result sql.Result
		result, err = tx.Exec("UPDATE scratch SET title=?,content=?,status=?,due=?,done_at=?,revision=revision+1,updated=? WHERE id=? AND task_id=? AND revision=?", v.Title, v.Content, v.Status, v.Due, v.DoneAt, now(), v.ID, v.TaskID, v.Revision)
		if err == nil {
			n, _ := result.RowsAffected()
			if n == 0 {
				return errors.New("便签已在其他窗口修改，当前草稿保留，请重新打开")
			}
		}
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO scratch_colors VALUES(?,?) ON CONFLICT(id) DO UPDATE SET color=excluded.color", v.ID, v.Color)
	if err != nil {
		return err
	}
	return tx.Commit()
}
