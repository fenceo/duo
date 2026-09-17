package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
)

// Save a specific completed draft. Both the revision and the generation time
// protect knowledge edited in another window while Codex was generating it.
func (s *Store) adoptKnowledge(taskID, runID string, revision int64) (Note, error) {
	tx, err := s.Begin()
	if err != nil {
		return Note{}, err
	}
	defer tx.Rollback()
	var content string
	var created int64
	err = tx.QueryRow("SELECT result,created FROM runs WHERE id=? AND task_id=? AND kind='knowledge' AND status='done'", runID, taskID).Scan(&content, &created)
	if err != nil || content == "" {
		return Note{}, errors.New("知识草稿尚未生成")
	}
	if len(content) > 500000 {
		return Note{}, errors.New("草稿过长，请在网页编辑后保存")
	}
	var n Note
	err = tx.QueryRow("SELECT content,revision,updated FROM notes WHERE task_id=?", taskID).Scan(&n.Content, &n.Revision, &n.Updated)
	if err != nil && err != sql.ErrNoRows {
		return Note{}, err
	}
	if n.Content == content {
		return n, nil
	} // Repeated callbacks are harmless.
	if n.Revision != revision || n.Updated > created {
		return Note{}, errConflict
	}
	n = Note{Content: content, Revision: n.Revision + 1, Updated: now()}
	_, err = tx.Exec("INSERT INTO notes VALUES(?,?,?,?) ON CONFLICT(task_id) DO UPDATE SET content=excluded.content,revision=excluded.revision,updated=excluded.updated", taskID, n.Content, n.Revision, n.Updated)
	if err != nil {
		return Note{}, err
	}
	return n, tx.Commit()
}

type knowledgeAction struct {
	Run      string `json:"run"`
	Revision int64  `json:"revision"`
}

func (f *Feishu) knowledgeControls(chat string, r Run) ([]any, error) {
	n, err := f.app.store.note(r.TaskID)
	if err != nil {
		return nil, err
	}
	if n.Content == r.Result && r.Result != "" {
		return []any{cardLine("已保存到任务知识")}, nil
	}
	if n.Updated > r.Created {
		return []any{cardLine("知识已更新，请在网页合并草稿。")}, nil
	}
	query, _ := json.Marshal(knowledgeAction{Run: r.ID, Revision: n.Revision})
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
	n, err := s.app.store.adoptKnowledge(r.PathValue("id"), v.RunID, v.Revision)
	if errors.Is(err, errConflict) {
		fail(w, 409, err.Error())
		return
	}
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.app.changed()
	jsonOut(w, 200, n)
}
