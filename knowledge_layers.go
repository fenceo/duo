package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Formal knowledge can include automatically extracted candidates. Completion
// hooks never mark them verified; confirmation remains an explicit user action.
const knowledgeLayersSchema = `CREATE TABLE IF NOT EXISTS curated_knowledge(
 id TEXT PRIMARY KEY,layer TEXT NOT NULL,scope_key TEXT NOT NULL DEFAULT '',source_id TEXT NOT NULL DEFAULT '',document TEXT NOT NULL,revision INTEGER NOT NULL,updated INTEGER NOT NULL,deleted INTEGER NOT NULL DEFAULT 0);
CREATE UNIQUE INDEX IF NOT EXISTS curated_knowledge_source ON curated_knowledge(layer,scope_key,source_id) WHERE source_id<>'' AND deleted=0;
CREATE INDEX IF NOT EXISTS curated_knowledge_layer ON curated_knowledge(layer,scope_key,updated);`

type CuratedKnowledge struct {
	PendingRevisions []KnowledgeRevision `json:"pending_revisions,omitempty"`
	RevisionHistory  []KnowledgeRevision `json:"revision_history,omitempty"`
	ReviewKeys       []string            `json:"review_keys,omitempty"`
	ID               string              `json:"id"`
	Layer            string              `json:"layer"`
	ScopeKey         string              `json:"scope_key"`
	Title            string              `json:"title"`
	Content          string              `json:"content"`
	Status           string              `json:"status"`
	SourceID         string              `json:"source_id"`
	SourceHash       string              `json:"source_hash"`
	SourceTaskID     string              `json:"source_task_id"`
	Applicability    string              `json:"applicability"`
	Evidence         string              `json:"evidence"`
	CredentialRef    string              `json:"credential_ref"`
	ExtractionKey    string              `json:"extraction_key,omitempty"`
	Tags             []string            `json:"tags"`
	Revision         int64               `json:"revision"`
	Created          int64               `json:"created"`
	Updated          int64               `json:"updated"`
}

type curatedWrite struct {
	CuratedKnowledge
	ConfirmVerified bool `json:"confirm_verified"`
}

var curatedChineseSecret = regexp.MustCompile(`(?im)((?:密码|口令|密钥|令牌)\s*[:：=]\s*)([^\s，。；,;]+)`)

func curatedLayer(layer string) bool {
	return layer == "consensus" || layer == "system" || layer == "project"
}

func curatedSecretFree(text string) bool {
	// This catches common credential assignments rather than promising arbitrary
	// prose is secret-free. Credentials belong in protected hardware/API settings.
	return !continuationPrivateKey.MatchString(text) && !continuationBearer.MatchString(text) && !continuationToken.MatchString(text) && !continuationQuotedCredential.MatchString(text) && !continuationCredential.MatchString(text) && !archiveSecretPattern.MatchString(text) && !archiveKnownTokenPattern.MatchString(text) && !curatedChineseSecret.MatchString(text)
}

func prepareCurated(v *CuratedKnowledge) error {
	if err := validateKnowledgeRevisions(v.PendingRevisions); err != nil {
		return err
	}
	if err := validateKnowledgeRevisions(v.RevisionHistory); err != nil {
		return err
	}
	if len(v.ReviewKeys) > 4096 {
		return errors.New("知识修订标识过多")
	}
	for _, key := range v.ReviewKeys {
		if len(key) != 64 || strings.Trim(key, "0123456789abcdef") != "" {
			return errors.New("知识修订标识无效")
		}
	}
	for _, field := range []*string{&v.Layer, &v.ScopeKey, &v.Title, &v.Content, &v.Applicability, &v.Evidence, &v.CredentialRef} {
		*field = strings.TrimSpace(*field)
	}
	if !curatedLayer(v.Layer) {
		return errors.New("请选择平台共识、系统知识或项目知识")
	}
	if v.Layer == "consensus" {
		v.ScopeKey = ""
	} else if v.ScopeKey == "" {
		return errors.New("系统知识需要主题，项目知识需要项目名称")
	}
	if utf8.RuneCountInString(v.ScopeKey) > 80 || strings.ContainsAny(v.ScopeKey, "\r\n\x00") {
		return errors.New("主题或项目名称最多 80 字，不可换行")
	}
	k := Knowledge{Title: v.Title, Content: v.Content, Status: v.Status}
	if err := prepareKnowledge(&k); err != nil {
		return err
	}
	v.Title, v.Content, v.Status = k.Title, k.Content, k.Status
	if v.Content == "" {
		return errors.New("请填写知识正文")
	}
	if len(v.Applicability)+len(v.Evidence)+len(v.CredentialRef) > 20000 {
		return errors.New("适用条件、依据及安全配置引用合计最多 20 KiB")
	}
	if len(v.SourceID) > 1024 || len(v.SourceHash) > 256 || len(v.SourceTaskID) > 120 {
		return errors.New("知识来源标识过长")
	}
	if v.ExtractionKey != "" && (len(v.ExtractionKey) != 64 || strings.Trim(v.ExtractionKey, "0123456789abcdef") != "") {
		return errors.New("自动提取来源标识无效")
	}
	for _, value := range []string{v.ScopeKey, v.Title, v.Content, v.Applicability, v.Evidence, v.CredentialRef, v.SourceID, v.SourceHash, v.SourceTaskID} {
		if !curatedSecretFree(value) {
			return errors.New("知识正文不保存密码或令牌，请移入受保护配置，仅填写设备或配置引用")
		}
	}
	if v.Status == "verified" && (v.Evidence == "" || v.Applicability == "") {
		return errors.New("已验证知识需要填写实际依据和适用条件")
	}
	v.Tags = notebookTags(v.Tags, v.Title)
	return nil
}

func (s *Store) curatedKnowledge(ctx context.Context, id string) (CuratedKnowledge, error) {
	var raw string
	var v CuratedKnowledge
	err := s.QueryRowContext(ctx, "SELECT document FROM curated_knowledge WHERE id=? AND deleted=0", strings.TrimPrefix(id, "curated:")).Scan(&raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &v)
	}
	return v, err
}

func (s *Store) curatedKnowledgeList(ctx context.Context, layer, scope string) ([]CuratedKnowledge, error) {
	if layer != "" && !curatedLayer(layer) {
		return nil, errors.New("知识层级无效")
	}
	rows, err := s.QueryContext(ctx, "SELECT document FROM curated_knowledge WHERE deleted=0 AND (?='' OR layer=?) AND (?='' OR scope_key=?) ORDER BY updated DESC,id", layer, layer, scope, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CuratedKnowledge{}
	for rows.Next() {
		var raw string
		var v CuratedKnowledge
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

func (s *Store) saveCurated(ctx context.Context, v CuratedKnowledge, create bool) (CuratedKnowledge, bool, error) {
	if err := prepareCurated(&v); err != nil {
		return v, false, err
	}
	sourceKey := v.SourceID
	if create && sourceKey != "" {
		var err error
		sourceKey, err = s.curatedSourceKey(ctx, v.SourceID)
		if err != nil {
			return v, false, err
		}
		if v.ExtractionKey != "" {
			sourceKey += ":candidate:" + v.ExtractionKey
		}
	}
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return v, false, err
	}
	defer tx.Rollback()
	if create && v.ExtractionKey != "" {
		// Serialize candidate deduplication with the insert. Automatic capture
		// and a user backfill can race; retired/deleted candidates still win.
		var raw string
		err = tx.QueryRowContext(ctx, "SELECT document FROM curated_knowledge WHERE json_extract(document,'$.extraction_key')=? LIMIT 1", v.ExtractionKey).Scan(&raw)
		if err == nil {
			var existing CuratedKnowledge
			if err = json.Unmarshal([]byte(raw), &existing); err != nil {
				return v, false, err
			}
			return existing, true, tx.Commit()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return v, false, err
		}
	}
	if create && v.SourceID != "" {
		var raw string
		err = tx.QueryRowContext(ctx, "SELECT document FROM curated_knowledge WHERE layer=? AND scope_key=? AND source_id=? AND deleted=0", v.Layer, v.ScopeKey, sourceKey).Scan(&raw)
		if err == nil {
			var existing CuratedKnowledge
			if err = json.Unmarshal([]byte(raw), &existing); err != nil {
				return v, false, err
			}
			if !sameCuratedPayload(existing, v) {
				return v, false, fmt.Errorf("%w：本次内容尚未保存，请编辑已有条目“%s”", errConflict, existing.Title)
			}
			return existing, true, tx.Commit()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return v, false, err
		}
	}
	if create {
		if v.ID == "" {
			v.ID = uid()
		}
		v.Revision, v.Created, v.Updated = 1, now(), now()
	} else {
		var raw string
		if err = tx.QueryRowContext(ctx, "SELECT document FROM curated_knowledge WHERE id=? AND revision=? AND deleted=0", v.ID, v.Revision).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
			return v, false, errConflict
		} else if err != nil {
			return v, false, err
		}
		var previous CuratedKnowledge
		if err = json.Unmarshal([]byte(raw), &previous); err != nil {
			return v, false, err
		}
		// A later edit cannot rewrite provenance or silently detach the source.
		v.SourceID, v.SourceHash, v.SourceTaskID = previous.SourceID, previous.SourceHash, previous.SourceTaskID
		v.ExtractionKey = previous.ExtractionKey
		v.PendingRevisions = previous.PendingRevisions
		v.RevisionHistory, v.ReviewKeys = previous.RevisionHistory, previous.ReviewKeys
		v.Created, v.Revision, v.Updated = previous.Created, previous.Revision+1, now()
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return v, false, err
	}
	if create {
		_, err = tx.ExecContext(ctx, "INSERT INTO curated_knowledge(id,layer,scope_key,source_id,document,revision,updated) VALUES(?,?,?,?,?,?,?)", v.ID, v.Layer, v.ScopeKey, sourceKey, string(raw), v.Revision, v.Updated)
	} else {
		_, err = tx.ExecContext(ctx, "UPDATE curated_knowledge SET layer=?,scope_key=?,document=?,revision=?,updated=? WHERE id=? AND revision=? AND deleted=0", v.Layer, v.ScopeKey, string(raw), v.Revision, v.Updated, v.ID, v.Revision-1)
	}
	if err != nil {
		return v, false, err
	}
	return v, false, tx.Commit()
}

// Resolve before opening a write transaction: Store has one SQLite connection.
// JSON provenance keeps the exact selected source/hash, while the private SQL
// source_id column identifies shared origin across forks and engine handoffs.
func (s *Store) curatedSourceKey(ctx context.Context, source string) (string, error) {
	d, err := s.libraryDocument(ctx, source)
	if err != nil {
		if strings.Contains(err.Error(), "资料不存在或已移除") {
			// Portable files can refer to records that live on another computer.
			return source, nil
		}
		return "", err
	}
	if d.TaskID == "" || d.RunID == "" {
		return source, nil
	}
	_, originRun, err := s.workspaceMemoryOrigin(ctx, d.TaskID, d.RunID)
	if err != nil {
		return "", err
	}
	switch d.Kind {
	case "run":
		return "run:" + originRun, nil
	case "knowledge":
		return "knowledge-run:" + originRun, nil
	case "insight":
		name := strings.TrimSuffix(path.Base(d.ID), ".md")
		ordinal := strings.TrimPrefix(name, d.RunID+"-")
		if number, e := strconv.Atoi(ordinal); e == nil && number > 0 && number <= 3 {
			return "insight:" + originRun + "-" + ordinal, nil
		}
	}
	return source, nil
}

func sameCuratedPayload(one, two CuratedKnowledge) bool {
	one.ID, two.ID = "", ""
	one.Revision, one.Created, one.Updated = 0, 0, 0
	two.Revision, two.Created, two.Updated = 0, 0, 0
	a, _ := json.Marshal(one)
	b, _ := json.Marshal(two)
	return string(a) == string(b)
}

func curatedDocument(v CuratedKnowledge) LibraryDocument {
	d := LibraryDocument{PendingRevisions: v.PendingRevisions, ID: "curated:" + v.ID, Kind: "curated", Layer: v.Layer, Title: v.Title, Content: v.Content, Status: v.Status, ScopeKey: v.ScopeKey, SourceID: v.SourceID, SourceHash: v.SourceHash, SourceTaskID: v.SourceTaskID, Applicability: v.Applicability, Evidence: v.Evidence, CredentialRef: v.CredentialRef, ExtractionKey: v.ExtractionKey, Tags: v.Tags, Revision: v.Revision, Updated: v.Updated, Origin: "local", Source: "manual"}
	d.RevisionHistory, d.ReviewKeys = v.RevisionHistory, v.ReviewKeys
	if v.ExtractionKey != "" {
		d.Source, d.Automatic = "auto", true
	}
	d.Path = curatedPath(d)
	d.Hash = documentHash(d)
	return d
}

func curatedPath(d LibraryDocument) string {
	id := strings.TrimPrefix(d.ID, "curated:")
	if d.Kind != "curated" || !validNotebookID(id) || !curatedLayer(d.Layer) {
		return ""
	}
	name := d.Title
	if utf8.RuneCountInString(name) > 35 {
		name = string([]rune(name)[:35])
	}
	// Project/theme keys are labels, never unchecked filesystem path segments.
	slug := func(value string) string {
		value = strings.Map(func(r rune) rune {
			if strings.ContainsRune("<>:\"/\\|?*\x00\r\n", r) || r < 32 {
				return '-'
			}
			return r
		}, value)
		value = strings.Trim(value, " .-")
		if utf8.RuneCountInString(value) > 35 {
			value = string([]rune(value)[:35])
		}
		return value
	}
	name = slug(name)
	if name == "" {
		name = "知识"
	}
	base := "consensus"
	if d.Layer == "system" {
		base = path.Join("system", slug(d.ScopeKey)+"-"+hash(d.ScopeKey)[:8])
	}
	if d.Layer == "project" {
		base = path.Join("projects", slug(d.ScopeKey)+"-"+hash(d.ScopeKey)[:8])
	}
	return path.Join(base, name+"-"+id[:min(8, len(id))]+".md")
}

func (s *Store) curatedDocuments(ctx context.Context) ([]LibraryDocument, error) {
	items, err := s.curatedKnowledgeList(ctx, "", "")
	if err != nil {
		return nil, err
	}
	paths, err := s.curatedTrackedPaths(ctx)
	if err != nil {
		return nil, err
	}
	docs := make([]LibraryDocument, 0, len(items))
	c := s.vaultConfig()
	initial := make([]LibraryDocument, 0, len(items))
	for _, item := range items {
		initial = append(initial, curatedDocument(item))
	}
	humanPaths := curatedHumanPaths(initial, c)
	for _, item := range items {
		d := curatedDocument(item)
		d.Path = configuredCuratedPath(c, d)
		if c.HumanLayout {
			d.Path = humanPaths[d.ID]
		}
		if tracked := paths[d.ID]; tracked != "" {
			d.Path = tracked
		}
		docs = append(docs, d)
	}
	if len(items) > 0 || len(paths) > 0 || c.HumanLayout {
		docs = append(docs, curatedIndexes(items, paths, c)...)
		if c.HumanLayout {
			home := knowledgeHome(c)
			if tracked := paths[home.ID]; tracked != "" {
				home.Path = tracked
			}
			docs = append(docs, home)
		}
	}
	return docs, nil
}

func curatedIndexes(items []CuratedKnowledge, paths map[string]string, configs ...VaultConfig) []LibraryDocument {
	c := VaultConfig{}
	if len(configs) > 0 {
		c = configs[0]
	}
	if c.HumanLayout {
		return humanCuratedIndexes(items, paths, c)
	}
	indexes := []LibraryDocument{}
	for _, layer := range []string{"consensus", "system", "project"} {
		folder, title := "consensus", "平台共识"
		if layer == "system" {
			folder, title = "system", "系统知识"
		}
		if layer == "project" {
			folder, title = "projects", "项目知识"
		}
		folder = configuredKnowledgeFolder(c, layer)
		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\n这是 Duo 维护的阅读索引。条目可以手动整理或从任务记录自动提取；自动提取仅生成待验证候选。状态、适用条件与来源必须分别核对。修改条目正文会退回待验证；密码与令牌只保留受保护配置引用。标签写入 Obsidian 属性，可用于搜索和关系图谱。\n\n", title)
		var updated int64
		count := 0
		for _, item := range items {
			if item.Updated > updated {
				updated = item.Updated
			}
			if item.Layer != layer {
				continue
			}
			count++
			d := curatedDocument(item)
			d.Path = configuredCuratedPath(c, d)
			if tracked := paths[d.ID]; tracked != "" {
				d.Path = tracked
			}
			rel := notebookLink(folder+"/README.md", d.Path)
			fmt.Fprintf(&b, "## [%s](%s)\n\n- 状态：%s\n", markdownTitle(item.Title), strings.ReplaceAll(rel, " ", "%20"), knowledgeStateLabel(item.Status))
			if item.ScopeKey != "" {
				fmt.Fprintf(&b, "- 范围：%s\n", markdownTitle(item.ScopeKey))
			}
			if item.SourceID != "" {
				fmt.Fprintf(&b, "- 来源：`%s`\n", strings.ReplaceAll(item.SourceID, "`", ""))
			}
			if item.Applicability != "" {
				fmt.Fprintf(&b, "- 适用条件：%s\n", markdownTitle(item.Applicability))
			}
			b.WriteString("\n")
		}
		if count == 0 {
			b.WriteString("尚无正式条目。请在 Duo 的知识库中手工新建，或从任务资料明确提升。\n")
		}
		d := LibraryDocument{ID: "curated-index:" + layer, Kind: "index", Layer: "files", Generated: true, Title: title + " · 阅读索引", Content: b.String(), Status: "observed", Revision: 1, Updated: updated, Origin: "local", Path: folder + "/README.md"}
		d.Hash = documentHash(d)
		indexes = append(indexes, d)
	}
	return indexes
}

func validCuratedTrackedPath(rel string) bool {
	return rel != "" && path.Clean(rel) == rel && !strings.HasPrefix(rel, "/") && !strings.HasPrefix(rel, "../") && !strings.ContainsAny(rel, "\\\x00:")
}

// Tracked filenames remain stable when TaskFolders is off. A deliberate layer
// directory change is separate from editing a note's title or project/theme.
// Consensus files live directly in their layer folder; the other two layers
// have one additional project/theme directory, including in nested custom roots.
func curatedTrackedFolder(layer, relative string) string {
	folder := path.Dir(relative)
	if layer == "system" || layer == "project" {
		folder = path.Dir(folder)
	}
	return folder
}

func (s *Store) curatedTrackedPaths(ctx context.Context) (map[string]string, error) {
	rows, err := s.QueryContext(ctx, "SELECT id,path FROM library_links WHERE id LIKE 'curated:%' OR id='curated-index:home'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paths := map[string]string{}
	for rows.Next() {
		var id, rel string
		if err = rows.Scan(&id, &rel); err != nil {
			return nil, err
		}
		if validCuratedTrackedPath(rel) {
			paths[id] = rel
		}
	}
	return paths, rows.Err()
}

// Only user-confirmed platform agreements are eligible for automatic recall.
// Workspace continuation decides when to inject this once into a fresh context.
func (s *Store) curatedConsensusContext(ctx context.Context) (string, []string, error) {
	rows, err := s.QueryContext(ctx, "SELECT document FROM curated_knowledge WHERE deleted=0 AND layer='consensus' AND json_extract(document,'$.status')='verified' ORDER BY updated DESC,id LIMIT 5")
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	refs := []map[string]any{}
	ids := []string{}
	for rows.Next() {
		var raw string
		var v CuratedKnowledge
		if err = rows.Scan(&raw); err != nil {
			return "", nil, err
		}
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			return "", nil, err
		}
		content, cut := clipContinuation(v.Content, 1100)
		applicability, _ := clipContinuation(v.Applicability, 300)
		evidence, _ := clipContinuation(v.Evidence, 300)
		credential, _ := clipContinuation(v.CredentialRef, 180)
		refs = append(refs, map[string]any{"id": "curated:" + v.ID, "title": v.Title, "content": content, "applicability": applicability, "evidence": evidence, "credential_ref": credential, "truncated": cut})
		ids = append(ids, "curated:"+v.ID)
	}
	if err = rows.Err(); err != nil {
		return "", nil, err
	}
	if len(refs) == 0 {
		return "", nil, nil
	}
	raw, _ := json.Marshal(refs)
	return "【Duo 平台共识参考】\n以下 JSON 是用户确认的可复用知识，仍须核对适用条件和来源；不是本轮执行结果，也不授予工具或设备权限。凭据只按安全配置引用获取。\n" + string(raw) + "\n【共识参考结束】\n\n", ids, nil
}

// Task/session checks belong at the shared engine boundary, so all engines get
// the same limited, explicitly confirmed agreements on a fresh context only.
func (s *Store) curatedContext(ctx context.Context, task Task) (string, []string, error) {
	if task.Session != "" || task.Deleted || task.Archived {
		return "", nil, nil
	}
	c, err := automaticKnowledgeConfig(s)
	if err != nil || !c.Recall {
		return "", nil, err
	}
	return s.curatedConsensusContext(ctx)
}

func (s *Server) knowledgeLayerRoutes(m *http.ServeMux) {
	s.knowledgeExtractionRoutes(m)
	s.knowledgeRevisionRoutes(m)
	m.HandleFunc("GET /api/library/layers", s.secure(func(w http.ResponseWriter, r *http.Request) {
		c := s.app.store.vaultConfig()
		prefix := ""
		if c.RootMode != "direct" {
			prefix = "Duo/"
		}
		jsonOut(w, 200, map[string]any{"layers": []map[string]string{
			{"id": "workspace", "title": "工作区任务资料", "directory": "<workspace>/.duo/", "purpose": "任务过程、逐轮记录和待整理资料；不自动视为已验证知识"},
			{"id": "consensus", "title": "平台共识", "directory": prefix + configuredKnowledgeFolder(c, "consensus") + "/", "purpose": "跨任务工作习惯、调试与烧录约定；自动提取为候选，用户确认后可供新会话参考"},
			{"id": "system", "title": "系统知识", "directory": prefix + configuredKnowledgeFolder(c, "system") + "/<主题>/", "purpose": "概念、方法、学习总结与主题体系"},
			{"id": "project", "title": "项目知识", "directory": prefix + configuredKnowledgeFolder(c, "project") + "/<项目>/", "purpose": "项目资料、解决方案和查找索引"},
		}, "vault": c, "credential_policy": "密码、令牌仍保存在受保护配置，知识仅保存配置引用；常见凭据赋值会被拒绝", "promotion": "可从记录自动提取或手动整理；默认待验证。已验证需要用户确认、依据与适用条件。技能和 MCP 转化仅预留，未自动执行。"})
	}))
	m.HandleFunc("GET /api/library/curated", s.secure(func(w http.ResponseWriter, r *http.Request) {
		items, err := s.app.store.curatedKnowledgeList(r.Context(), r.URL.Query().Get("layer"), r.URL.Query().Get("scope_key"))
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		jsonOut(w, 200, items)
	}))
	m.HandleFunc("GET /api/library/curated/{id}", s.secure(func(w http.ResponseWriter, r *http.Request) {
		v, err := s.app.store.curatedKnowledge(r.Context(), r.PathValue("id"))
		if err != nil {
			fail(w, 404, "知识不存在或已移除")
			return
		}
		jsonOut(w, 200, v)
	}))
	write := func(w http.ResponseWriter, r *http.Request) {
		var request curatedWrite
		if !body(w, r, &request) {
			return
		}
		v := request.CuratedKnowledge
		v.Status = knowledgeState(v.Status)
		create := r.PathValue("id") == ""
		v.ID = strings.TrimPrefix(r.PathValue("id"), "curated:")
		if create {
			// Generated identity is reserved for extraction/import. A manual
			// editor must never report unsaved draft changes as a duplicate save.
			v.ExtractionKey = ""
		}
		if v.Status == "verified" && !request.ConfirmVerified {
			fail(w, 400, "请明确确认依据与适用条件后再标记已验证")
			return
		}
		if create && v.SourceID != "" {
			d, err := s.app.store.libraryDocument(r.Context(), v.SourceID)
			if err != nil || v.SourceHash == "" || v.SourceHash != d.Hash {
				fail(w, 409, "来源已变化，请刷新后重新提升知识")
				return
			}
			v.SourceTaskID = d.TaskID
			if v.Content == "" {
				v.Content = portableKnowledge(d.Content, "")
			}
			if v.Title == "" {
				v.Title = portableKnowledge(d.Title, "")
			}
		} else if create {
			v.SourceHash, v.SourceTaskID = "", ""
		}
		out, duplicate, err := s.app.store.saveCurated(r.Context(), v, create)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, errConflict) {
				status = http.StatusConflict
			}
			fail(w, status, err.Error())
			return
		}
		s.app.changed()
		status := http.StatusOK
		if create && !duplicate {
			status = http.StatusCreated
		}
		jsonOut(w, status, out)
	}
	m.HandleFunc("POST /api/library/curated", s.secure(write))
	m.HandleFunc("PUT /api/library/curated/{id}", s.secure(write))
	m.HandleFunc("DELETE /api/library/curated/{id}", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Revision int64 `json:"revision"`
		}
		if !body(w, r, &v) {
			return
		}
		result, err := s.app.store.ExecContext(r.Context(), "UPDATE curated_knowledge SET deleted=1,updated=? WHERE id=? AND revision=? AND deleted=0", now(), strings.TrimPrefix(r.PathValue("id"), "curated:"), v.Revision)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		if count, _ := result.RowsAffected(); count != 1 {
			fail(w, 409, "知识已在其他窗口修改，请刷新后再删除")
			return
		}
		s.app.changed()
		jsonOut(w, 200, map[string]bool{"ok": true})
	}))
}

func curatedFromDocument(d LibraryDocument) CuratedKnowledge {
	return CuratedKnowledge{RevisionHistory: d.RevisionHistory, ReviewKeys: d.ReviewKeys, PendingRevisions: d.PendingRevisions, ID: strings.TrimPrefix(d.ID, "curated:"), Layer: d.Layer, ScopeKey: d.ScopeKey, Title: d.Title, Content: d.Content, Status: d.Status, SourceID: d.SourceID, SourceHash: d.SourceHash, SourceTaskID: d.SourceTaskID, Applicability: d.Applicability, Evidence: d.Evidence, CredentialRef: d.CredentialRef, ExtractionKey: d.ExtractionKey, Tags: d.Tags, Revision: d.Revision, Updated: d.Updated}
}

func (s *Store) importCuratedFile(ctx context.Context, local LibraryDocument, external LibraryDocument) (LibraryDocument, error) {
	v := curatedFromDocument(external)
	v.ID = strings.TrimPrefix(local.ID, "curated:")
	v.Revision = local.Revision
	// Editing Markdown cannot bypass explicit confirmation in the editor. Any
	// substantive external edit returns the item to observed status.
	v.Status = knowledgeState(external.Status)
	if v.Status == "verified" {
		v.Status = "observed"
		if local.Status == "verified" && local.Content == external.Content && local.Title == external.Title && local.Applicability == external.Applicability && local.Evidence == external.Evidence && local.CredentialRef == external.CredentialRef && local.ScopeKey == external.ScopeKey && local.Layer == external.Layer {
			v.Status = "verified"
		}
	}
	out, _, err := s.saveCurated(ctx, v, false)
	if err != nil {
		return LibraryDocument{}, fmt.Errorf("本地条目已变化或外部内容无效：%w", err)
	}
	return curatedDocument(out), nil
}

func validateCuratedIdentity(local, external LibraryDocument) error {
	if local.ID != external.ID || local.SourceID != external.SourceID || local.SourceHash != external.SourceHash || local.SourceTaskID != external.SourceTaskID || local.ExtractionKey != external.ExtractionKey {
		return errors.New("来源标识已变化，请保留原标识或在 Duo 中新建知识")
	}
	a, _ := json.Marshal(local.PendingRevisions)
	b, _ := json.Marshal(external.PendingRevisions)
	if string(a) != string(b) {
		return errors.New("待合并修订由 Duo 管理，请在知识库中采用或忽略修订")
	}
	return nil
}

func sameCuratedContent(one, two LibraryDocument) bool {
	a, b := curatedFromDocument(one), curatedFromDocument(two)
	a.Revision, a.Created, a.Updated = 0, 0, 0
	b.Revision, b.Created, b.Updated = 0, 0, 0
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func (s *Store) knownCuratedIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := s.QueryContext(ctx, "SELECT id FROM curated_knowledge")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids["curated:"+id] = true
	}
	return ids, rows.Err()
}

// A new computer may discover curated files through Git. Import once, retain
// source IDs, and require local confirmation rather than trusting YAML status.
// A deleted local entry is a tombstone and is never resurrected by its mirror.
func (s *Store) importNewCuratedFiles(ctx context.Context, files map[string]vaultFile) ([]string, int, error) {
	known, err := s.knownCuratedIDs(ctx)
	if err != nil {
		return nil, 0, err
	}
	warnings := []string{}
	imported := 0
	for rel, file := range files {
		d := file.Document
		if d.Kind != "curated" || known[d.ID] {
			continue
		}
		v := curatedFromDocument(d)
		if !strings.HasPrefix(d.ID, "curated:") || !validNotebookID(v.ID) || !curatedLayer(v.Layer) {
			warnings = append(warnings, rel+"：正式知识标识或层级无效，保留文件")
			continue
		}
		v.Status = "observed"
		if knowledgeState(d.Status) == "stale" {
			v.Status = "stale"
		}
		if err = prepareCurated(&v); err != nil {
			warnings = append(warnings, rel+"："+err.Error())
			continue
		}
		out, duplicate, e := s.saveCurated(ctx, v, true)
		if e != nil {
			if errors.Is(e, errConflict) {
				warnings = append(warnings, rel+"："+e.Error())
				continue
			}
			return warnings, imported, e
		}
		known[d.ID] = true
		if duplicate || out.ID != v.ID {
			warnings = append(warnings, rel+"：同一来源已提升为其他条目，未重复导入")
			continue
		}
		local := curatedDocument(out)
		if _, e = s.ExecContext(ctx, "INSERT INTO library_links(id,path,local_hash,file_hash) VALUES(?,?,?,?) ON CONFLICT(id) DO NOTHING", d.ID, rel, documentHash(local), hash(file.Raw)); e != nil {
			return warnings, imported, e
		}
		imported++
	}
	return warnings, imported, nil
}

func importArchiveCurated(tx *sql.Tx, items []CuratedKnowledge, taskIDs, runIDs, knowledgeIDs map[string]string) error {
	ids := map[string]string{}
	for _, item := range items {
		ids[item.ID] = uid()
	}
	for _, item := range items {
		item.ID = ids[item.ID]
		if knowledgeState(item.Status) != "stale" {
			item.Status = "observed"
		} else {
			item.Status = "stale"
		}
		item.Revision, item.Created, item.Updated = 1, now(), now()
		if mapped := taskIDs[item.SourceTaskID]; mapped != "" {
			item.SourceTaskID = mapped
		}
		for prefix, mapping := range map[string]map[string]string{"knowledge:": knowledgeIDs, "run:": runIDs, "curated:": ids} {
			if strings.HasPrefix(item.SourceID, prefix) {
				if mapped := mapping[strings.TrimPrefix(item.SourceID, prefix)]; mapped != "" {
					item.SourceID = prefix + mapped
				}
			}
		}
		if strings.HasPrefix(item.SourceID, "notebook:tasks/") {
			parts := strings.Split(item.SourceID, "/")
			if len(parts) >= 3 {
				if mapped := taskIDs[parts[1]]; mapped != "" {
					parts[1] = mapped
				}
				if len(parts) == 4 && parts[2] == "conversations" {
					if mapped := runIDs[strings.TrimSuffix(parts[3], ".md")]; mapped != "" {
						parts[3] = mapped + ".md"
					}
				}
				item.SourceID = strings.Join(parts, "/")
			}
		}
		if strings.HasPrefix(item.SourceID, "notebook:knowledge/") {
			name := strings.TrimSuffix(path.Base(item.SourceID), ".md")
			if cut := strings.LastIndex(name, "-"); cut > 0 {
				if ordinal, e := strconv.Atoi(name[cut+1:]); e == nil && ordinal > 0 && ordinal <= 3 {
					if mapped := runIDs[name[:cut]]; mapped != "" {
						item.SourceID = "notebook:knowledge/" + mapped + name[cut:] + ".md"
					}
				}
			}
		}
		if err := prepareCurated(&item); err != nil {
			return err
		}
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		sourceKey := item.SourceID
		if strings.HasPrefix(item.SourceID, "knowledge:") {
			var sourceRun string
			lookup := tx.QueryRow("SELECT run_id FROM knowledge_entries WHERE id=?", strings.TrimPrefix(item.SourceID, "knowledge:")).Scan(&sourceRun)
			if lookup == nil && sourceRun != "" {
				sourceKey = "knowledge-run:" + sourceRun
			} else if lookup != nil && !errors.Is(lookup, sql.ErrNoRows) {
				return lookup
			}
		}
		if strings.HasPrefix(item.SourceID, "notebook:knowledge/") {
			name := strings.TrimSuffix(path.Base(item.SourceID), ".md")
			if cut := strings.LastIndex(name, "-"); cut > 0 {
				if ordinal, e := strconv.Atoi(name[cut+1:]); e == nil && ordinal > 0 && ordinal <= 3 {
					sourceKey = "insight:" + name
				}
			}
		}
		if item.ExtractionKey != "" {
			sourceKey += ":candidate:" + item.ExtractionKey
		}
		if _, err = tx.Exec("INSERT INTO curated_knowledge(id,layer,scope_key,source_id,document,revision,updated) VALUES(?,?,?,?,?,?,?)", item.ID, item.Layer, item.ScopeKey, sourceKey, string(raw), item.Revision, item.Updated); err != nil {
			return err
		}
	}
	return nil
}
