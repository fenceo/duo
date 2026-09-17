package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Knowledge is what a task learned. Entries stay with the task, carry where they
// came from, and say how much they can be trusted.
type Knowledge struct {
	ID       string `json:"id"`
	TaskID   string `json:"task_id"`
	Title    string `json:"title"`
	Content  string `json:"content"`
	Status   string `json:"status"`
	Source   string `json:"source"`
	RunID    string `json:"run_id"`
	Revision int64  `json:"revision"`
	Created  int64  `json:"created"`
	Updated  int64  `json:"updated"`
}

const knowledgeMaxBytes = 200000
const knowledgeTruncatedNotice = "\n\n（内容过长，已截断，请在面板里补充）"

func knowledgeState(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "verified", "confirmed", "done", "adopted":
		return "verified"
	case "stale", "deprecated", "obsolete", "expired":
		return "stale"
	default:
		return "observed"
	}
}

func knowledgeOrigin(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "run", "feishu", "organize", "migrated":
		return strings.ToLower(strings.TrimSpace(s))
	default:
		return "manual"
	}
}

func knowledgeStateLabel(status string) string {
	switch knowledgeState(status) {
	case "verified":
		return "已验证"
	case "stale":
		return "已过时"
	default:
		return "待验证"
	}
}

func knowledgeSourceLabel(source string) string {
	switch knowledgeOrigin(source) {
	case "run":
		return "来自执行记录"
	case "feishu":
		return "来自飞书"
	case "organize":
		return "整理生成"
	case "migrated":
		return "历史任务知识"
	default:
		return "手工记录"
	}
}

// The knowledge run prompt is an instruction, not a title, so those entries are
// named by the moment they were produced.
func runKnowledgeTitle(kind, input string, created int64) string {
	stamp := time.UnixMilli(created).Format("01-02 15:04")
	if kind != "knowledge" {
		if head := scratchHeading(input); head != "" && utf8.RuneCountInString(head) <= 60 {
			return head
		}
	}
	return "执行总结 · " + stamp
}

func (s *Store) knowledgeList(task string) ([]Knowledge, error) {
	rows, err := s.Query("SELECT id,task_id,title,content,status,source,run_id,revision,created,updated FROM knowledge_entries WHERE task_id=? ORDER BY updated DESC,created DESC LIMIT 500", task)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Knowledge{}
	for rows.Next() {
		var k Knowledge
		if err = rows.Scan(&k.ID, &k.TaskID, &k.Title, &k.Content, &k.Status, &k.Source, &k.RunID, &k.Revision, &k.Created, &k.Updated); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) knowledgeForRun(taskID, runID string) (Knowledge, error) {
	var k Knowledge
	err := s.QueryRow("SELECT id,task_id,title,content,status,source,run_id,revision,created,updated FROM knowledge_entries WHERE task_id=? AND run_id=?", taskID, runID).
		Scan(&k.ID, &k.TaskID, &k.Title, &k.Content, &k.Status, &k.Source, &k.RunID, &k.Revision, &k.Created, &k.Updated)
	if errors.Is(err, sql.ErrNoRows) {
		return k, nil
	}
	return k, err
}

func prepareKnowledge(v *Knowledge) error {
	v.Title = strings.TrimSpace(v.Title)
	v.Content = strings.TrimSpace(v.Content)
	v.Status = knowledgeState(v.Status)
	v.Source = knowledgeOrigin(v.Source)
	if utf8.RuneCountInString(v.Title) > 120 {
		return errors.New("知识标题最多 120 字")
	}
	if len(v.Content) > knowledgeMaxBytes {
		return errors.New("单条知识最多 200 KiB，请拆成多条")
	}
	if v.Title == "" {
		v.Title = scratchHeading(v.Content)
	}
	if v.Title == "" {
		return errors.New("知识需要标题或内容")
	}
	return nil
}

func truncateKnowledgeContent(content string) string {
	if len(content) <= knowledgeMaxBytes {
		return content
	}
	limit := knowledgeMaxBytes - len(knowledgeTruncatedNotice)
	cut := 0
	for index := range content {
		if index > limit {
			break
		}
		cut = index
	}
	return content[:cut] + knowledgeTruncatedNotice
}

func (s *Store) writeKnowledge(v Knowledge, create bool) error {
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	v.Status = knowledgeState(v.Status)
	v.Source = knowledgeOrigin(v.Source)
	if create {
		_, err = tx.Exec("INSERT INTO knowledge_entries(id,task_id,title,content,status,source,run_id,revision,created,updated) VALUES(?,?,?,?,?,?,?,?,?,?)", v.ID, v.TaskID, v.Title, v.Content, v.Status, v.Source, v.RunID, v.Revision, v.Created, v.Updated)
	} else {
		var result sql.Result
		result, err = tx.Exec("UPDATE knowledge_entries SET title=?,content=?,status=?,revision=revision+1,updated=? WHERE id=? AND task_id=? AND revision=?", v.Title, v.Content, v.Status, now(), v.ID, v.TaskID, v.Revision)
		if err == nil {
			n, _ := result.RowsAffected()
			if n == 0 {
				return errConflict
			}
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Save a completed draft as a task knowledge entry. The revision guards an entry
// edited in another window while the engine was still writing the draft.
func (s *Store) adoptKnowledge(taskID, runID string, revision int64) (Knowledge, error) {
	tx, err := s.Begin()
	if err != nil {
		return Knowledge{}, err
	}
	defer tx.Rollback()
	var content, input, kind string
	var created int64
	err = tx.QueryRow("SELECT result,input,kind,created FROM runs WHERE id=? AND task_id=? AND kind='knowledge' AND status='done'", runID, taskID).Scan(&content, &input, &kind, &created)
	if err != nil || strings.TrimSpace(content) == "" {
		return Knowledge{}, errors.New("知识草稿尚未生成")
	}
	if len(content) > knowledgeMaxBytes {
		return Knowledge{}, errors.New("草稿过长，请拆分后再保存")
	}
	var k Knowledge
	err = tx.QueryRow("SELECT id,task_id,title,content,status,source,run_id,revision,created,updated FROM knowledge_entries WHERE task_id=? AND run_id=?", taskID, runID).
		Scan(&k.ID, &k.TaskID, &k.Title, &k.Content, &k.Status, &k.Source, &k.RunID, &k.Revision, &k.Created, &k.Updated)
	if err == nil {
		if k.Content == content {
			return k, tx.Commit()
		} // Repeated callbacks are harmless.
		if k.Revision != revision {
			return Knowledge{}, errConflict
		}
		k.Content = content
		k.Revision++
		k.Updated = now()
		if _, err = tx.Exec("UPDATE knowledge_entries SET content=?,revision=?,updated=? WHERE id=?", k.Content, k.Revision, k.Updated, k.ID); err != nil {
			return Knowledge{}, err
		}
		return k, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Knowledge{}, err
	}
	k = Knowledge{ID: uid(), TaskID: taskID, Title: runKnowledgeTitle(kind, input, created), Content: content, Status: "observed", Source: "run", RunID: runID, Revision: 1, Created: now(), Updated: now()}
	if _, err = tx.Exec("INSERT INTO knowledge_entries(id,task_id,title,content,status,source,run_id,revision,created,updated) VALUES(?,?,?,?,?,?,?,?,?,?)", k.ID, k.TaskID, k.Title, k.Content, k.Status, k.Source, k.RunID, k.Revision, k.Created, k.Updated); err != nil {
		return Knowledge{}, err
	}
	return k, tx.Commit()
}

// Settle any finished run into knowledge: a solved chat turn is reusable without
// running a separate summarize step.
func (s *Store) knowledgeFromRun(taskID, runID string) (Knowledge, error) {
	if k, err := s.knowledgeForRun(taskID, runID); err != nil || k.ID != "" {
		return k, err
	}
	var input, result, kind, status string
	var created int64
	if err := s.QueryRow("SELECT input,result,kind,status,created FROM runs WHERE id=? AND task_id=?", runID, taskID).Scan(&input, &result, &kind, &status, &created); err != nil {
		return Knowledge{}, errors.New("找不到这次执行记录")
	}
	if status != "done" || strings.TrimSpace(result) == "" {
		return Knowledge{}, errors.New("这次执行还没有可沉淀的结果")
	}
	result = truncateKnowledgeContent(result)
	k := Knowledge{ID: uid(), TaskID: taskID, Title: runKnowledgeTitle(kind, input, created), Content: result, Status: "observed", Source: "run", RunID: runID, Revision: 1, Created: now(), Updated: now()}
	if err := s.writeKnowledge(k, true); err != nil {
		return Knowledge{}, err
	}
	return k, nil
}

type knowledgeAction struct {
	Run      string `json:"run"`
	Revision int64  `json:"revision"`
}

func (f *Feishu) knowledgeControls(chat string, r Run) ([]any, error) {
	saved, err := f.app.store.knowledgeForRun(r.TaskID, r.ID)
	if err != nil {
		return nil, err
	}
	if saved.Content == r.Result && r.Result != "" {
		return []any{cardLine("已保存到任务知识")}, nil
	}
	query, _ := json.Marshal(knowledgeAction{Run: r.ID, Revision: saved.Revision})
	// Reuse the action token on delivery retries, keeping the rendered card stable.
	var token string
	_ = f.app.store.QueryRow("SELECT id FROM feishu_card_actions WHERE chat_id=? AND task_id=? AND action='save_knowledge' AND query=? AND expires>? ORDER BY rowid DESC LIMIT 1", chat, r.TaskID, string(query), now()).Scan(&token)
	var button map[string]any
	if token != "" {
		button = map[string]any{"tag": "button", "text": plainCardText("保存知识"), "type": "primary", "size": "small", "width": "default", "value": map[string]string{"jianzuo_action": token}}
	} else {
		button, err = f.cardButton(chat, r.TaskID, "save_knowledge", "保存知识", string(query), 0)
		if err != nil {
			return nil, err
		}
		button["type"] = "primary"
	}
	return []any{map[string]any{"tag": "action", "actions": []any{button}}}, nil
}

func (f *Feishu) knowledgeResultCard(chat, taskID, runID string) (map[string]any, error) {
	var d runCardDelivery
	d.chat = chat
	err := f.app.store.QueryRow("SELECT r.id,r.task_id,r.input,r.kind,r.status,r.result,r.error,r.created,t.title FROM runs r JOIN tasks t ON t.id=r.task_id WHERE r.id=? AND r.task_id=? AND r.kind='knowledge'", runID, taskID).Scan(&d.run.ID, &d.run.TaskID, &d.run.Input, &d.run.Kind, &d.run.Status, &d.run.Result, &d.run.Error, &d.run.Created, &d.title)
	if err != nil {
		return nil, err
	}
	content, err := f.runCardContent(d)
	if err != nil {
		return nil, err
	}
	var card map[string]any
	err = json.Unmarshal([]byte(content), &card)
	return card, err
}

func (s *Server) adoptKnowledge(w http.ResponseWriter, r *http.Request) {
	var v struct {
		RunID    string `json:"run_id"`
		Revision int64  `json:"revision"`
	}
	if !body(w, r, &v) {
		return
	}
	k, err := s.app.store.adoptKnowledge(r.PathValue("id"), v.RunID, v.Revision)
	if errors.Is(err, errConflict) {
		fail(w, 409, err.Error())
		return
	}
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.app.changed()
	jsonOut(w, 200, k)
}

func knowledgeMarkdown(task Task, items []Knowledge) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s · 任务知识\n\n> 由简作导出 · %s\n", task.Title, time.Now().Format("2006-01-02 15:04"))
	if len(items) == 0 {
		b.WriteString("\n还没有沉淀知识。\n")
		return b.String()
	}
	for _, k := range items {
		fmt.Fprintf(&b, "\n---\n\n## %s\n\n- 状态：%s\n- 来源：%s · %s\n\n%s\n", k.Title, knowledgeStateLabel(k.Status), knowledgeSourceLabel(k.Source), time.UnixMilli(k.Updated).Format("2006-01-02 15:04"), k.Content)
	}
	return b.String()
}

func (s *Server) knowledgeRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/tasks/{id}/knowledge", s.secure(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		task, err := s.app.store.task(id)
		if err != nil {
			fail(w, 404, "任务不存在")
			return
		}
		items, err := s.app.store.knowledgeList(id)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		if r.URL.Query().Get("download") == "1" {
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="task-knowledge.md"`)
			_, _ = w.Write([]byte(knowledgeMarkdown(task, items)))
			return
		}
		jsonOut(w, 200, items)
	}))
	write := func(w http.ResponseWriter, r *http.Request) {
		var v Knowledge
		if !body(w, r, &v) {
			return
		}
		if err := prepareKnowledge(&v); err != nil {
			fail(w, 400, err.Error())
			return
		}
		if _, err := s.app.store.task(r.PathValue("id")); err != nil {
			fail(w, 404, "任务不存在")
			return
		}
		v.ID = r.PathValue("kid")
		v.TaskID = r.PathValue("id")
		if v.ID == "" {
			v.ID = uid()
			v.Revision = 1
			v.Created = now()
			v.Updated = v.Created
			if err := s.app.store.writeKnowledge(v, true); err != nil {
				fail(w, 500, err.Error())
				return
			}
			s.app.changed()
			jsonOut(w, 201, v)
			return
		}
		if err := s.app.store.writeKnowledge(v, false); err != nil {
			fail(w, 409, err.Error())
			return
		}
		s.app.changed()
		jsonOut(w, 200, map[string]bool{"ok": true})
	}
	m.HandleFunc("POST /api/tasks/{id}/knowledge", s.secure(write))
	m.HandleFunc("PUT /api/tasks/{id}/knowledge/{kid}", s.secure(write))
	m.HandleFunc("DELETE /api/tasks/{id}/knowledge/{kid}", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Revision int64 `json:"revision"`
		}
		if !body(w, r, &v) {
			return
		}
		res, err := s.app.store.Exec("DELETE FROM knowledge_entries WHERE id=? AND task_id=? AND revision=?", r.PathValue("kid"), r.PathValue("id"), v.Revision)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			fail(w, 409, "这条知识已在其他窗口修改，请刷新后再删除")
			return
		}
		s.app.changed()
		jsonOut(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("POST /api/tasks/{id}/knowledge/from-run", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			RunID string `json:"run_id"`
		}
		if !body(w, r, &v) {
			return
		}
		k, err := s.app.store.knowledgeFromRun(r.PathValue("id"), v.RunID)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		s.app.changed()
		jsonOut(w, 200, k)
	}))
}
