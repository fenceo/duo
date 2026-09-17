package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
)

type Scratch struct {
	Color     string `json:"color"`
	TaskID    string `json:"task_id"`
	TaskTitle string `json:"task_title"`
	ID        string `json:"id"`
	Content   string `json:"content"`
	Revision  int64  `json:"revision"`
	Updated   int64  `json:"updated"`
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
		if strings.TrimSpace(v.Content) == "" || len(v.Content) > 65536 || !validScratchColor(v.Color) {
			fail(w, 400, "便签需要内容，最多 64 KiB")
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
		if strings.TrimSpace(v.Content) == "" || len(v.Content) > 65536 || !validScratchColor(v.Color) {
			fail(w, 400, "便签需要内容，最多 64 KiB")
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
	query := `SELECT s.id,s.content,s.revision,s.updated,COALESCE(c.color,'yellow'),s.task_id,t.title FROM scratch s JOIN tasks t ON t.id=s.task_id LEFT JOIN scratch_colors c ON c.id=s.id LEFT JOIN task_options o ON o.task_id=t.id WHERE COALESCE(o.deleted,0)=0`
	args := []any{}
	if task != "" {
		query += " AND s.task_id=?"
		args = append(args, task)
	}
	query += " ORDER BY s.updated DESC LIMIT 500"
	rows, err := s.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Scratch{}
	for rows.Next() {
		var n Scratch
		if err = rows.Scan(&n.ID, &n.Content, &n.Revision, &n.Updated, &n.Color, &n.TaskID, &n.TaskTitle); err != nil {
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
	if create {
		if v.Color == "" {
			v.Color = "yellow"
		}
		_, err = tx.Exec("INSERT INTO scratch VALUES(?,?,?,?,?)", v.ID, v.TaskID, v.Content, v.Revision, v.Updated)
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
		var result sql.Result
		result, err = tx.Exec("UPDATE scratch SET content=?,revision=revision+1,updated=? WHERE id=? AND task_id=? AND revision=?", v.Content, now(), v.ID, v.TaskID, v.Revision)
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
