package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Project identity is independent of mutable task titles, paths and engines.
// Existing tasks keep the conservative inferred fallback until explicitly set.
const knowledgeProjectSchema = `
CREATE TABLE IF NOT EXISTS knowledge_projects(id TEXT PRIMARY KEY,name TEXT NOT NULL,name_key TEXT NOT NULL UNIQUE,aliases TEXT NOT NULL,created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS task_knowledge_projects(task_id TEXT PRIMARY KEY REFERENCES tasks(id),project_id TEXT NOT NULL REFERENCES knowledge_projects(id));
CREATE INDEX IF NOT EXISTS task_knowledge_projects_project ON task_knowledge_projects(project_id);
`

type KnowledgeProject struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
}

type knowledgeProjectSelection struct {
	Project  *KnowledgeProject  `json:"project"`
	Projects []KnowledgeProject `json:"projects"`
}

func prepareKnowledgeProject(name string, aliases []string) (KnowledgeProject, error) {
	valid := func(value string) bool {
		return value != "" && utf8.RuneCountInString(value) <= 80 && !strings.ContainsFunc(value, unicode.IsControl) && curatedSecretFree(value) && !knowledgeGenericTerm(value)
	}
	name = strings.TrimSpace(name)
	if !valid(name) || len(aliases) > 8 {
		return KnowledgeProject{}, errors.New("项目名称最多 80 字，别名最多 8 个；请勿填写通用分类、密码或令牌")
	}
	v := KnowledgeProject{Name: name, Aliases: []string{}}
	seen := map[string]bool{knowledgeScopeAlias(name): true}
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if !valid(alias) {
			return KnowledgeProject{}, errors.New("项目别名无效：最多 80 字，请勿填写通用分类、密码或令牌")
		}
		key := knowledgeScopeAlias(alias)
		if !seen[key] {
			v.Aliases = append(v.Aliases, alias)
			seen[key] = true
		}
	}
	return v, nil
}

func (s *Store) knowledgeProjects(ctx context.Context) ([]KnowledgeProject, error) {
	rows, err := s.QueryContext(ctx, "SELECT id,name,aliases FROM knowledge_projects ORDER BY name_key,id LIMIT 501")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []KnowledgeProject{}
	for rows.Next() {
		var v KnowledgeProject
		var aliases string
		if err = rows.Scan(&v.ID, &v.Name, &aliases); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(aliases), &v.Aliases); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	if len(items) > 500 {
		return nil, errors.New("项目配置超过 500 个，请先整理项目配置")
	}
	return items, rows.Err()
}

func (s *Store) taskKnowledgeProject(ctx context.Context, task string) (*KnowledgeProject, error) {
	var v KnowledgeProject
	var aliases string
	err := s.QueryRowContext(ctx, "SELECT p.id,p.name,p.aliases FROM knowledge_projects p JOIN task_knowledge_projects t ON t.project_id=p.id WHERE t.task_id=?", task).Scan(&v.ID, &v.Name, &aliases)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err == nil {
		err = json.Unmarshal([]byte(aliases), &v.Aliases)
	}
	return &v, err
}

// setTaskKnowledgeProject changes only future recall/provenance. Historical run
// snapshots and the user's Markdown knowledge are never relabelled or rewritten.
func (s *Store) setTaskKnowledgeProject(ctx context.Context, task, projectID, name string, aliases []string) (*KnowledgeProject, error) {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var exists string
	if err = tx.QueryRowContext(ctx, "SELECT id FROM tasks WHERE id=?", task).Scan(&exists); err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) == "" && projectID == "" {
		if len(aliases) != 0 {
			return nil, errors.New("请先选择或创建项目")
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM task_knowledge_projects WHERE task_id=?", task); err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	var project KnowledgeProject
	var raw string
	if projectID != "" {
		if name != "" || len(aliases) != 0 {
			return nil, errors.New("选择已有项目时请勿同时填写新名称或别名")
		}
		err = tx.QueryRowContext(ctx, "SELECT id,name,aliases FROM knowledge_projects WHERE id=?", projectID).Scan(&project.ID, &project.Name, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("项目不存在，请重新选择")
		}
		if err == nil {
			err = json.Unmarshal([]byte(raw), &project.Aliases)
		}
	} else {
		project, err = prepareKnowledgeProject(name, aliases)
		if err != nil {
			return nil, err
		}
		// A same-name selection reuses its stable identity and existing aliases.
		err = tx.QueryRowContext(ctx, "SELECT id,name,aliases FROM knowledge_projects WHERE name_key=?", strings.ToLower(project.Name)).Scan(&project.ID, &project.Name, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			var count int
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM knowledge_projects").Scan(&count); err != nil {
				return nil, err
			}
			if count >= 500 {
				return nil, errors.New("项目最多 500 个，请选择已有项目")
			}
			rows, readErr := tx.QueryContext(ctx, "SELECT id,name,aliases FROM knowledge_projects")
			if readErr != nil {
				return nil, readErr
			}
			conflict := false
			for rows.Next() {
				var existing KnowledgeProject
				var encoded string
				if readErr = rows.Scan(&existing.ID, &existing.Name, &encoded); readErr != nil {
					break
				}
				if readErr = json.Unmarshal([]byte(encoded), &existing.Aliases); readErr != nil {
					break
				}
				for _, alias := range append([]string{project.Name}, project.Aliases...) {
					if knowledgeProjectMatchesScope(&existing, alias) {
						conflict = true
					}
				}
			}
			if readErr == nil {
				readErr = rows.Err()
			}
			rows.Close()
			if readErr != nil {
				return nil, readErr
			}
			if conflict {
				return nil, errors.New("项目名称或别名已属于已有项目，请选择该项目")
			}
			project.ID = uid()
			encoded, _ := json.Marshal(project.Aliases)
			_, err = tx.ExecContext(ctx, "INSERT INTO knowledge_projects(id,name,name_key,aliases,created) VALUES(?,?,?,?,?)", project.ID, project.Name, strings.ToLower(project.Name), string(encoded), now())
		} else if err == nil {
			err = json.Unmarshal([]byte(raw), &project.Aliases)
		}
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO task_knowledge_projects(task_id,project_id) VALUES(?,?) ON CONFLICT(task_id) DO UPDATE SET project_id=excluded.project_id", task, project.ID); err != nil {
		return nil, err
	}
	return &project, tx.Commit()
}

func knowledgeProjectMatchesScope(project *KnowledgeProject, scope string) bool {
	if project == nil {
		return false
	}
	for _, name := range append([]string{project.Name}, project.Aliases...) {
		if knowledgeScopeAlias(name) == knowledgeScopeAlias(scope) {
			return true
		}
	}
	return false
}

func (s *Server) knowledgeProjectRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/tasks/{id}/knowledge/project", s.secure(s.taskKnowledgeProject))
	m.HandleFunc("PUT /api/tasks/{id}/knowledge/project", s.secure(s.taskKnowledgeProject))
}

func (s *Server) taskKnowledgeProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if r.Method == http.MethodPut {
		var request struct {
			ProjectID string   `json:"project_id"`
			Name      string   `json:"name"`
			Aliases   []string `json:"aliases"`
		}
		if !body(w, r, &request) {
			return
		}
		s.app.mu.Lock()
		defer s.app.mu.Unlock()
		task, err := s.app.store.task(id)
		if err != nil {
			fail(w, http.StatusNotFound, "任务不存在")
			return
		}
		var pending int
		if err = s.app.store.QueryRowContext(r.Context(), "SELECT count(*) FROM runs WHERE task_id=? AND status IN ('running','queued')", id).Scan(&pending); err != nil {
			fail(w, http.StatusInternalServerError, "无法检查任务状态")
			return
		}
		if task.Deleted || task.Archived || task.Status == "running" || task.Status == "queued" || s.app.workers[id] != nil || pending > 0 {
			fail(w, http.StatusConflict, "请先停止任务并取消排队，再修改知识项目")
			return
		}
		if _, err = s.app.store.setTaskKnowledgeProject(r.Context(), id, request.ProjectID, request.Name, request.Aliases); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		s.app.changed()
	} else if _, err := s.app.store.task(id); err != nil {
		fail(w, http.StatusNotFound, "任务不存在")
		return
	}
	project, err := s.app.store.taskKnowledgeProject(r.Context(), id)
	if err != nil {
		fail(w, http.StatusInternalServerError, "无法读取任务知识项目")
		return
	}
	projects, err := s.app.store.knowledgeProjects(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, "无法读取知识项目列表")
		return
	}
	jsonOut(w, http.StatusOK, knowledgeProjectSelection{Project: project, Projects: projects})
}
