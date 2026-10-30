package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type KnowledgeRevision struct {
	Key           string   `json:"key" yaml:"key"`
	BaseHash      string   `json:"base_hash" yaml:"base_hash"`
	Content       string   `json:"content" yaml:"content"`
	Evidence      string   `json:"evidence" yaml:"evidence"`
	Applicability string   `json:"applicability" yaml:"applicability"`
	Tags          []string `json:"tags" yaml:"tags"`
	SourceID      string   `json:"source_id" yaml:"source_id"`
	SourceTaskID  string   `json:"source_task_id" yaml:"source_task_id"`
	Created       int64    `json:"created" yaml:"created"`
}

func validateKnowledgeRevisions(items []KnowledgeRevision) error {
	if len(items) > 8 {
		return errors.New("同一主题最多保留 8 条待合并修订，请先处理已有修订")
	}
	for _, p := range items {
		if len(p.Key) != 64 || strings.Trim(p.Key, "0123456789abcdef") != "" || len(p.BaseHash) != 64 || strings.Trim(p.BaseHash, "0123456789abcdef") != "" || p.Content == "" || p.Evidence == "" || p.Applicability == "" || len(p.Content)+len(p.Evidence)+len(p.Applicability) > 20000 || len(p.Tags) > 8 || len(p.SourceID) > 1024 || len(p.SourceTaskID) > 120 || !curatedSecretFree(p.Content+"\n"+p.Evidence+"\n"+p.Applicability+"\n"+p.SourceID+"\n"+p.SourceTaskID+"\n"+strings.Join(p.Tags, " ")) {
			return errors.New("知识修订内容或依据无效")
		}
	}
	return nil
}

func knowledgeTopic(layer, scope, title string) string {
	return layer + "\x00" + strings.Join(strings.Fields(scope), " ") + "\x00" + strings.Join(strings.Fields(title), " ")
}
func knowledgeBaseHash(v CuratedKnowledge) string {
	return hash(v.Content + "\x00" + v.Evidence + "\x00" + v.Applicability)
}

// Existing notes remain authoritative. Same-title candidates are proposals on
// that note, never an automatic rewrite or a second standalone knowledge file.
func (s *Store) queueKnowledgeRevision(ctx context.Context, id string, p KnowledgeRevision) (CuratedKnowledge, bool, error) {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return CuratedKnowledge{}, false, err
	}
	defer tx.Rollback()
	var raw string
	if err = tx.QueryRowContext(ctx, "SELECT document FROM curated_knowledge WHERE id=? AND deleted=0", id).Scan(&raw); err != nil {
		return CuratedKnowledge{}, false, err
	}
	var v CuratedKnowledge
	if err = json.Unmarshal([]byte(raw), &v); err != nil {
		return v, false, err
	}
	for _, key := range v.ReviewKeys {
		if key == p.Key {
			return v, true, tx.Commit()
		}
	}
	for _, prior := range v.PendingRevisions {
		if prior.Key == p.Key {
			return v, true, tx.Commit()
		}
	}
	p.BaseHash = knowledgeBaseHash(v)
	p.Created = now()
	v.PendingRevisions = append(v.PendingRevisions, p)
	if err = validateKnowledgeRevisions(v.PendingRevisions); err != nil {
		return v, false, err
	}
	if len(v.ReviewKeys) >= 4096 {
		return v, false, errors.New("此主题的修订记录已达到上限，请手动归档后继续整理")
	}
	v.ReviewKeys = append(v.ReviewKeys, p.Key)
	v.Revision++
	v.Updated = now()
	rawBytes, _ := json.Marshal(v)
	_, err = tx.ExecContext(ctx, "UPDATE curated_knowledge SET document=?,revision=?,updated=? WHERE id=? AND revision=? AND deleted=0", string(rawBytes), v.Revision, v.Updated, id, v.Revision-1)
	if err != nil {
		return v, false, err
	}
	return v, false, tx.Commit()
}

func (s *Store) resolveKnowledgeRevision(ctx context.Context, id, key string, revision int64, accept bool) (CuratedKnowledge, error) {
	id = strings.TrimPrefix(id, "curated:")
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return CuratedKnowledge{}, err
	}
	defer tx.Rollback()
	var raw string
	if err = tx.QueryRowContext(ctx, "SELECT document FROM curated_knowledge WHERE id=? AND revision=? AND deleted=0", id, revision).Scan(&raw); err != nil {
		return CuratedKnowledge{}, errConflict
	}
	var v CuratedKnowledge
	if err = json.Unmarshal([]byte(raw), &v); err != nil {
		return v, err
	}
	found := -1
	for i, p := range v.PendingRevisions {
		if p.Key == key {
			found = i
			break
		}
	}
	if found < 0 {
		return v, errConflict
	}
	p := v.PendingRevisions[found]
	if accept {
		if p.BaseHash != knowledgeBaseHash(v) {
			return v, errors.New("原正文已变化，请手动合并修订；已有内容未覆盖")
		}
		v.Content, v.Evidence, v.Applicability = p.Content, p.Evidence, p.Applicability
		v.Tags = notebookTags(append(v.Tags, p.Tags...), v.Title)
		v.Status = "observed"
		v.RevisionHistory = append(v.RevisionHistory, p)
		if len(v.RevisionHistory) > 8 {
			v.RevisionHistory = v.RevisionHistory[len(v.RevisionHistory)-8:]
		}
	}
	v.PendingRevisions = append(v.PendingRevisions[:found], v.PendingRevisions[found+1:]...)
	v.Revision++
	v.Updated = now()
	if err = prepareCurated(&v); err != nil {
		return v, err
	}
	b, _ := json.Marshal(v)
	if _, err = tx.ExecContext(ctx, "UPDATE curated_knowledge SET document=?,revision=?,updated=? WHERE id=? AND revision=? AND deleted=0", string(b), v.Revision, v.Updated, v.ID, revision); err != nil {
		return v, err
	}
	return v, tx.Commit()
}

func (s *Server) knowledgeRevisionRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/library/curated/{id}/revisions/{key}", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Revision int64 `json:"revision"`
			Accept   *bool `json:"accept"`
		}
		if !body(w, r, &v) {
			return
		}
		if v.Accept == nil {
			fail(w, 400, "请选择采用或忽略修订")
			return
		}
		out, err := s.app.store.resolveKnowledgeRevision(r.Context(), r.PathValue("id"), r.PathValue("key"), v.Revision, *v.Accept)
		if err != nil {
			fail(w, 409, err.Error())
			return
		}
		s.app.changed()
		jsonOut(w, 200, out)
	}))
}
