package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// An adopted full-topic merge retains the exact prior published body alongside
// its provenance. This works for manually authored notes with no source run.
type KnowledgeBaseline struct {
	Content       string   `json:"content" yaml:"content"`
	Evidence      string   `json:"evidence" yaml:"evidence"`
	Applicability string   `json:"applicability" yaml:"applicability"`
	Status        string   `json:"status" yaml:"status"`
	SourceID      string   `json:"source_id" yaml:"source_id"`
	SourceTaskID  string   `json:"source_task_id" yaml:"source_task_id"`
	SourceHash    string   `json:"source_hash" yaml:"source_hash"`
	Tags          []string `json:"tags" yaml:"tags"`
	Revision      int64    `json:"revision" yaml:"revision"`
}

type KnowledgeRevision struct {
	Key           string             `json:"key" yaml:"key"`
	BaseHash      string             `json:"base_hash" yaml:"base_hash"`
	BaseChanged   bool               `json:"base_changed,omitempty" yaml:"-"`
	Supersedes    []string           `json:"supersedes,omitempty" yaml:"supersedes,omitempty"`
	Previous      *KnowledgeBaseline `json:"previous,omitempty" yaml:"previous,omitempty"`
	Content       string             `json:"content" yaml:"content"`
	Evidence      string             `json:"evidence" yaml:"evidence"`
	Applicability string             `json:"applicability" yaml:"applicability"`
	Tags          []string           `json:"tags" yaml:"tags"`
	SourceID      string             `json:"source_id" yaml:"source_id"`
	SourceTaskID  string             `json:"source_task_id" yaml:"source_task_id"`
	Created       int64              `json:"created" yaml:"created"`
}

func validateKnowledgeRevisions(items []KnowledgeRevision) error {
	if len(items) > 64 {
		return errors.New("同一主题最多保留 64 条修订历史")
	}
	for _, p := range items {
		if len(p.Key) != 64 || strings.Trim(p.Key, "0123456789abcdef") != "" || len(p.BaseHash) != 64 || strings.Trim(p.BaseHash, "0123456789abcdef") != "" || p.Content == "" || p.Evidence == "" || p.Applicability == "" || len(p.Content)+len(p.Evidence)+len(p.Applicability) > 20000 || len(p.Tags) > 8 || len(p.SourceID) > 1024 || len(p.SourceTaskID) > 120 || !curatedSecretFree(p.Content+"\n"+p.Evidence+"\n"+p.Applicability+"\n"+p.SourceID+"\n"+p.SourceTaskID+"\n"+strings.Join(p.Tags, " ")) {
			return errors.New("知识修订内容或依据无效")
		}
		if len(p.Supersedes) > 8 {
			return errors.New("完整合并稿最多覆盖 8 条待修订")
		}
		seen := map[string]bool{}
		for _, key := range p.Supersedes {
			if len(key) != 64 || strings.Trim(key, "0123456789abcdef") != "" || key == p.Key || seen[key] {
				return errors.New("合并稿的修订来源标识无效")
			}
			seen[key] = true
		}
		if b := p.Previous; b != nil {
			if b.Content == "" || len(b.Content)+len(b.Evidence)+len(b.Applicability) > 12000 || len(b.Tags) > 8 || len(b.SourceID) > 1024 || len(b.SourceTaskID) > 120 || len(b.SourceHash) > 256 || b.Revision < 1 || knowledgeState(b.Status) != b.Status || !curatedSecretFree(b.Content+"\n"+b.Evidence+"\n"+b.Applicability+"\n"+b.SourceID+"\n"+b.SourceTaskID+"\n"+b.SourceHash+"\n"+strings.Join(b.Tags, " ")) {
				return errors.New("合并前知识正文快照或来源无效")
			}
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

// Conflict flags are presentation metadata, never part of portable identity or
// synchronization hashes. Always derive them from the currently saved body.
func knowledgeRevisionFlags(items []KnowledgeRevision, base string) []KnowledgeRevision {
	if len(items) == 0 {
		return nil
	}
	out := append([]KnowledgeRevision(nil), items...)
	for i := range out {
		out[i].BaseChanged = base != "" && out[i].BaseHash != base
		if len(out[i].Tags) == 0 {
			out[i].Tags = nil
		}
		if len(out[i].Supersedes) == 0 {
			out[i].Supersedes = nil
		}
		if out[i].Previous != nil {
			prior := *out[i].Previous
			if len(prior.Tags) == 0 {
				prior.Tags = nil
			}
			out[i].Previous = &prior
		}
	}
	return out
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
	for _, key := range p.Supersedes {
		found := false
		for _, prior := range v.PendingRevisions {
			found = found || prior.Key == key
		}
		if !found {
			return v, false, errors.New("合并稿包含已处理的修订，请重新整理当前主题")
		}
	}
	if len(v.PendingRevisions) >= 8 && (len(p.Supersedes) == 0 || len(v.PendingRevisions) >= 9) {
		return v, false, errors.New("同一主题最多保留 8 条待修订和 1 份完整合并稿，请先处理已有修订")
	}
	if p.BaseHash == "" {
		p.BaseHash = knowledgeBaseHash(v)
	}
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
	v.PendingRevisions = knowledgeRevisionFlags(v.PendingRevisions, knowledgeBaseHash(v))
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
		pendingKeys := map[string]bool{}
		for _, prior := range v.PendingRevisions {
			pendingKeys[prior.Key] = true
		}
		for _, key := range p.Supersedes {
			if !pendingKeys[key] {
				return v, errors.New("完整合并稿包含已处理的修订，请重新整理；已有内容未覆盖")
			}
		}
		if len(p.Supersedes) > 0 {
			p.Previous = &KnowledgeBaseline{Content: v.Content, Evidence: v.Evidence, Applicability: v.Applicability, Status: v.Status, SourceID: v.SourceID, SourceTaskID: v.SourceTaskID, SourceHash: v.SourceHash, Tags: append([]string(nil), v.Tags...), Revision: v.Revision}
		}
		v.Content, v.Evidence, v.Applicability = p.Content, p.Evidence, p.Applicability
		v.Tags = notebookTags(append(v.Tags, p.Tags...), v.Title)
		v.Status = "observed"
		covered := map[string]bool{}
		for _, key := range p.Supersedes {
			covered[key] = true
		}
		remaining := []KnowledgeRevision{}
		for _, prior := range v.PendingRevisions {
			if prior.Key == p.Key {
				v.RevisionHistory = append(v.RevisionHistory, p)
			} else if covered[prior.Key] {
				v.RevisionHistory = append(v.RevisionHistory, prior)
			} else {
				remaining = append(remaining, prior)
			}
		}
		v.PendingRevisions = remaining
		if len(v.RevisionHistory) > 64 {
			v.RevisionHistory = v.RevisionHistory[len(v.RevisionHistory)-64:]
		}
	} else {
		v.PendingRevisions = append(v.PendingRevisions[:found], v.PendingRevisions[found+1:]...)
	}
	v.Revision++
	v.Updated = now()
	if err = prepareCurated(&v); err != nil {
		return v, err
	}
	b, _ := json.Marshal(v)
	if _, err = tx.ExecContext(ctx, "UPDATE curated_knowledge SET document=?,revision=?,updated=? WHERE id=? AND revision=? AND deleted=0", string(b), v.Revision, v.Updated, v.ID, revision); err != nil {
		return v, err
	}
	v.PendingRevisions = knowledgeRevisionFlags(v.PendingRevisions, knowledgeBaseHash(v))
	return v, tx.Commit()
}

func (s *Server) knowledgeRevisionRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/library/curated/{id}/consolidate", s.secure(s.consolidateKnowledgeTopic))
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
