package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type TaskPreferences struct {
	Pinned   *bool `json:"pinned"`
	Archived *bool `json:"archived"`
}

func (a *App) setTaskPreferences(id string, update TaskPreferences) (Task, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, err := a.store.task(id)
	if err != nil {
		return t, err
	}
	if t.Deleted {
		return t, errors.New("任务在回收站中，请先恢复")
	}
	if update.Pinned != nil {
		t.Pinned = *update.Pinned
	}
	if update.Archived != nil {
		if *update.Archived {
			a.terminals.mu.Lock()
			defer a.terminals.mu.Unlock()
			for _, link := range a.terminals.links {
				if link.Task == id {
					return t, errors.New("请先结束此任务的终端，再归档")
				}
			}
			var active int
			if err = a.store.QueryRow("SELECT count(*) FROM runs WHERE task_id=? AND status IN ('running','queued')", id).Scan(&active); err != nil {
				return t, err
			}
			if active > 0 || a.workers[id] != nil {
				return t, errors.New("任务正在执行，请结束后再归档")
			}
		}
		t.Archived = *update.Archived
	}
	_, err = a.store.Exec("INSERT INTO task_preferences VALUES(?,?,?) ON CONFLICT(task_id) DO UPDATE SET pinned=excluded.pinned,archived=excluded.archived", id, t.Pinned, t.Archived)
	return t, err
}

type SearchHit struct {
	TaskID    string `json:"task_id"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
	Snippet   string `json:"snippet"`
	Archived  bool   `json:"archived"`
}
type SearchResult struct {
	Hits      []SearchHit `json:"hits"`
	Truncated bool        `json:"truncated"`
}

func excerpt(content, query string) string {
	text := []rune(content)
	lower := []rune(strings.ToLower(content))
	needle := []rune(strings.ToLower(query))
	start := 0
	for i := 0; i+len(needle) <= len(lower); i++ {
		if string(lower[i:i+len(needle)]) == string(needle) {
			start = max(0, i-45)
			break
		}
	}
	end := min(len(text), start+220)
	out := string(text[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(text) {
		out += "…"
	}
	return out
}
func (s *Store) search(ctx context.Context, query string) (SearchResult, error) {
	result := SearchResult{Hits: []SearchHit{}}
	query = strings.TrimSpace(query)
	if query == "" {
		return result, nil
	}
	if !utf8.ValidString(query) || len([]rune(query)) > 160 {
		return result, errors.New("关键词最多 160 字")
	}
	// Parameters and instr make %, _, quotes and regex metacharacters literal.
	rows, err := s.QueryContext(ctx, `SELECT d.task_id,t.title,d.kind,d.reference,d.content,COALESCE(p.archived,0)
 FROM (
 SELECT id task_id,'task' kind,id reference,title content,updated stamp FROM tasks
 UNION ALL SELECT task_id,'note','',content,updated FROM notes
 UNION ALL SELECT task_id,'scratch',id,content,updated FROM scratch
 UNION ALL SELECT task_id,'message',CAST(seq AS TEXT),text,created FROM events WHERE kind IN ('user','assistant')
 ) d JOIN tasks t ON t.id=d.task_id LEFT JOIN task_preferences p ON p.task_id=t.id
 WHERE NOT EXISTS (SELECT 1 FROM task_options o WHERE o.task_id=t.id AND o.deleted=1)
 AND instr(lower(d.content),lower(?))>0 ORDER BY d.stamp DESC,d.task_id,d.kind,d.reference LIMIT 61`, query)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var hit SearchHit
		var content string
		if err = rows.Scan(&hit.TaskID, &hit.Title, &hit.Kind, &hit.Reference, &content, &hit.Archived); err != nil {
			return result, err
		}
		hit.Snippet = excerpt(content, query)
		result.Hits = append(result.Hits, hit)
	}
	if len(result.Hits) > 60 {
		result.Hits = result.Hits[:60]
		result.Truncated = true
	}
	return result, rows.Err()
}
func (s *Server) organizeRoutes(m *http.ServeMux) {
	m.HandleFunc("PATCH /api/tasks/{id}/preferences", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var update TaskPreferences
		if !body(w, r, &update) {
			return
		}
		if _, err := s.app.store.task(r.PathValue("id")); err != nil {
			fail(w, 404, "任务不存在")
			return
		}
		t, err := s.app.setTaskPreferences(r.PathValue("id"), update)
		if err != nil {
			fail(w, 409, err.Error())
			return
		}
		jsonOut(w, 200, t)
	}))
	m.HandleFunc("GET /api/search", s.secure(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		result, err := s.app.store.search(ctx, r.URL.Query().Get("q"))
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		jsonOut(w, 200, result)
	}))
}
