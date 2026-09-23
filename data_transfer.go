package main

// The workspace archive deliberately contains only user work.  It is not a
// backup of the Duo installation: credentials, sessions, environment records,
// hardware state and Feishu tables never cross the archive boundary.

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	workspaceArchiveProtocol = 1
	workspaceArchiveKind     = "duo-workspace"
	workspaceZipMaxBytes     = int64(64 << 20)
	workspaceExpandedMax     = int64(96 << 20)
	workspaceJSONMax         = int64(32 << 20)
	workspaceMaxTasks        = 2000
	workspaceMaxRuns         = 20000
	workspaceMaxEvents       = 60000
	workspaceMaxKnowledge    = 10000
	workspaceMaxScratch      = 10000
)

type workspaceArchive struct {
	Protocol    int                          `json:"protocol"`
	Kind        string                       `json:"kind"`
	ExportedAt  int64                        `json:"exported_at"`
	Tasks       []workspaceArchiveTask       `json:"tasks"`
	Runs        []workspaceArchiveRun        `json:"runs"`
	Events      []workspaceArchiveEvent      `json:"events"`
	Knowledge   []workspaceArchiveKnowledge  `json:"knowledge"`
	Scratch     []workspaceArchiveScratch    `json:"scratch"`
	Notes       []workspaceArchiveNote       `json:"notes"`
	Attachments []workspaceArchiveAttachment `json:"attachments"`
}

type workspaceArchiveTask struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	Engine          string `json:"engine"`
	Status          string `json:"status"`
	Created         int64  `json:"created"`
	Updated         int64  `json:"updated"`
	Pinned          bool   `json:"pinned"`
	Archived        bool   `json:"archived"`
	Deleted         bool   `json:"deleted"`
	ModeID          string `json:"mode_id,omitempty"`
}

type workspaceArchiveRun struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"task_id"`
	Input       string    `json:"input"`
	Kind        string    `json:"kind"`
	Source      string    `json:"source"`
	Status      string    `json:"status"`
	Result      string    `json:"result"`
	Error       string    `json:"error"`
	Created     int64     `json:"created"`
	Finished    int64     `json:"finished"`
	Started     int64     `json:"started"`
	Usage       *RunUsage `json:"usage,omitempty"`
	ModeID      string    `json:"mode_id,omitempty"`
	Attachments []string  `json:"attachments,omitempty"`
}

type workspaceArchiveEvent struct {
	TaskID  string `json:"task_id"`
	RunID   string `json:"run_id"`
	Kind    string `json:"kind"`
	Text    string `json:"text"`
	Created int64  `json:"created"`
}

type workspaceArchiveKnowledge struct {
	ID       string `json:"id"`
	TaskID   string `json:"task_id"`
	Title    string `json:"title"`
	Content  string `json:"content"`
	Status   string `json:"status"`
	Source   string `json:"source"`
	RunID    string `json:"run_id,omitempty"`
	Revision int64  `json:"revision"`
	Created  int64  `json:"created"`
	Updated  int64  `json:"updated"`
}

type workspaceArchiveScratch struct {
	ID       string `json:"id"`
	TaskID   string `json:"task_id"`
	Title    string `json:"title"`
	Content  string `json:"content"`
	Status   string `json:"status"`
	Due      string `json:"due"`
	DoneAt   int64  `json:"done_at"`
	Revision int64  `json:"revision"`
	Updated  int64  `json:"updated"`
	Color    string `json:"color"`
}

type workspaceArchiveNote struct {
	TaskID   string `json:"task_id"`
	Content  string `json:"content"`
	Revision int64  `json:"revision"`
	Updated  int64  `json:"updated"`
}

type workspaceArchiveAttachment struct {
	ID     string `json:"id"`
	TaskID string `json:"task_id"`
	Name   string `json:"name"`
	Mime   string `json:"mime"`
	Size   int64  `json:"size"`
}

type workspaceZipData struct {
	Archive workspaceArchive
}

// workspaceTransferRoutes registers the sanitized workspace transfer
// endpoints. The normal Handler wrapper applies authentication and CSRF.
func (s *Server) workspaceTransferRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/workspace/export", s.secure(s.workspaceExport))
	m.HandleFunc("POST /api/workspace/import", s.secure(s.workspaceImport))
}

var (
	archiveIDPattern          = regexp.MustCompile(`^[A-Za-z0-9_-]{1,96}$`)
	archiveSecretPattern      = regexp.MustCompile(`(?i)(\b(?:api[_-]?key|secret|password|passwd|token|authorization|bearer|private[_-]?key)\b\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
	archiveKnownTokenPattern  = regexp.MustCompile(`(?i)\b(?:sk-[A-Za-z0-9_-]{12,}|gh[pousr]_[A-Za-z0-9_-]{12,}|github_pat_[A-Za-z0-9_]+|glpat-[A-Za-z0-9_-]{12,}|xox[baprs]-[A-Za-z0-9-]{12,}|AKIA[0-9A-Z]{16})\b`)
	archivePrivateKeyPattern  = regexp.MustCompile(`(?s)-----BEGIN [^-]+ PRIVATE KEY-----.*?-----END [^-]+ PRIVATE KEY-----`)
	archiveWindowsPathPattern = regexp.MustCompile(`(?i)(?:[A-Z]:[\\/]|\\\\)[^\s"'<>]+`)
	archiveUnixPathPattern    = regexp.MustCompile(`(?m)(^|[\s"'(])/(?:home|mnt|users|root|tmp|var|opt|workspace|work)/[^\s"'<>]+`)
)

func redactWorkspaceText(value string) string {
	value = archivePrivateKeyPattern.ReplaceAllString(value, "[私钥已脱敏]")
	value = archiveSecretPattern.ReplaceAllString(value, "$1[凭据已脱敏]")
	value = archiveKnownTokenPattern.ReplaceAllString(value, "[令牌已脱敏]")
	value = archiveWindowsPathPattern.ReplaceAllString(value, "[本机路径已脱敏]")
	value = archiveUnixPathPattern.ReplaceAllString(value, "$1[本机路径已脱敏]")
	return value
}

func archiveModeID(raw string) string {
	var mode struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(raw), &mode) != nil || !archiveIDPattern.MatchString(mode.ID) {
		return ""
	}
	return mode.ID
}

func (s *Store) workspaceModeID(taskID string) string {
	var raw string
	if s.QueryRow("SELECT mode FROM task_options WHERE task_id=?", taskID).Scan(&raw) != nil {
		return ""
	}
	return archiveModeID(raw)
}

func (s *Store) workspaceArchiveSnapshot() (workspaceArchive, map[string][]byte, error) {
	tasks, err := s.tasks(true)
	if err != nil {
		return workspaceArchive{}, nil, err
	}
	if len(tasks) > workspaceMaxTasks {
		return workspaceArchive{}, nil, errors.New("任务数量超过导出上限")
	}
	a := workspaceArchive{Protocol: workspaceArchiveProtocol, Kind: workspaceArchiveKind, ExportedAt: time.Now().UnixMilli()}
	for _, task := range tasks {
		a.Tasks = append(a.Tasks, workspaceArchiveTask{ID: task.ID, Title: redactWorkspaceText(task.Title), Model: redactWorkspaceText(task.Model), ReasoningEffort: task.ReasoningEffort, Engine: task.Engine, Status: task.Status, Created: task.Created, Updated: task.Updated, Pinned: task.Pinned, Archived: task.Archived, Deleted: task.Deleted, ModeID: s.workspaceModeID(task.ID)})
		runs, err := s.runs(task.ID)
		if err != nil {
			return workspaceArchive{}, nil, err
		}
		for _, run := range runs {
			r := workspaceArchiveRun{ID: run.ID, TaskID: task.ID, Input: redactWorkspaceText(run.Input), Kind: run.Kind, Source: run.Source, Status: run.Status, Result: redactWorkspaceText(run.Result), Error: redactWorkspaceText(run.Error), Created: run.Created, Finished: run.Finished, Started: run.Started, Usage: run.Usage, ModeID: ""}
			if run.Mode != nil {
				r.ModeID = archiveModeID(mustJSON(run.Mode))
			}
			a.Runs = append(a.Runs, r)
		}
	}
	if len(a.Runs) > workspaceMaxRuns {
		return workspaceArchive{}, nil, errors.New("对话轮次超过导出上限")
	}
	rows, err := s.Query("SELECT task_id,run_id,kind,text,created FROM events ORDER BY seq")
	if err != nil {
		return workspaceArchive{}, nil, err
	}
	for rows.Next() {
		var e workspaceArchiveEvent
		if err = rows.Scan(&e.TaskID, &e.RunID, &e.Kind, &e.Text, &e.Created); err != nil {
			rows.Close()
			return workspaceArchive{}, nil, err
		}
		e.Text = redactWorkspaceText(e.Text)
		a.Events = append(a.Events, e)
		if len(a.Events) > workspaceMaxEvents {
			rows.Close()
			return workspaceArchive{}, nil, errors.New("执行事件超过导出上限")
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return workspaceArchive{}, nil, err
	}
	rows.Close()
	rows, err = s.Query("SELECT id,task_id,title,content,status,source,run_id,revision,created,updated FROM knowledge_entries ORDER BY updated, id")
	if err != nil {
		return workspaceArchive{}, nil, err
	}
	for rows.Next() {
		var k workspaceArchiveKnowledge
		if err = rows.Scan(&k.ID, &k.TaskID, &k.Title, &k.Content, &k.Status, &k.Source, &k.RunID, &k.Revision, &k.Created, &k.Updated); err != nil {
			rows.Close()
			return workspaceArchive{}, nil, err
		}
		k.Title, k.Content = redactWorkspaceText(k.Title), redactWorkspaceText(k.Content)
		a.Knowledge = append(a.Knowledge, k)
		if len(a.Knowledge) > workspaceMaxKnowledge {
			rows.Close()
			return workspaceArchive{}, nil, errors.New("知识条目超过导出上限")
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return workspaceArchive{}, nil, err
	}
	rows.Close()
	rows, err = s.Query("SELECT s.id,s.task_id,s.title,s.content,s.status,s.due,s.done_at,s.revision,s.updated,COALESCE(c.color,'yellow') FROM scratch s LEFT JOIN scratch_colors c ON c.id=s.id ORDER BY s.updated,s.id")
	if err != nil {
		return workspaceArchive{}, nil, err
	}
	for rows.Next() {
		var v workspaceArchiveScratch
		if err = rows.Scan(&v.ID, &v.TaskID, &v.Title, &v.Content, &v.Status, &v.Due, &v.DoneAt, &v.Revision, &v.Updated, &v.Color); err != nil {
			rows.Close()
			return workspaceArchive{}, nil, err
		}
		v.Title, v.Content = redactWorkspaceText(v.Title), redactWorkspaceText(v.Content)
		a.Scratch = append(a.Scratch, v)
		if len(a.Scratch) > workspaceMaxScratch {
			rows.Close()
			return workspaceArchive{}, nil, errors.New("待办条目超过导出上限")
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return workspaceArchive{}, nil, err
	}
	rows.Close()
	rows, err = s.Query("SELECT task_id,content,revision,updated FROM notes ORDER BY task_id")
	if err != nil {
		return workspaceArchive{}, nil, err
	}
	for rows.Next() {
		var n workspaceArchiveNote
		if err = rows.Scan(&n.TaskID, &n.Content, &n.Revision, &n.Updated); err != nil {
			rows.Close()
			return workspaceArchive{}, nil, err
		}
		n.Content = redactWorkspaceText(n.Content)
		a.Notes = append(a.Notes, n)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return workspaceArchive{}, nil, err
	}
	rows.Close()
	// Attachments are intentionally excluded in v1.  Raw files are opaque and
	// may contain credentials or machine-specific data; users can add them
	// again after importing the sanitized workspace.
	return a, nil, nil
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func marshalWorkspaceArchive(a workspaceArchive) ([]byte, error) {
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > workspaceJSONMax {
		return nil, errors.New("工作区索引超过导出上限")
	}
	return b, nil
}

func buildWorkspaceZip(a workspaceArchive, attachments map[string][]byte) ([]byte, error) {
	if len(a.Attachments) != 0 || len(attachments) != 0 {
		return nil, errors.New("v1 工作区导出不支持附件，请单独上传")
	}
	index, err := marshalWorkspaceArchive(a)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	header := &zip.FileHeader{Name: "workspace.json", Method: zip.Deflate}
	header.SetModTime(time.Unix(0, 0))
	f, err := zw.CreateHeader(header)
	if err == nil {
		_, err = f.Write(index)
	}
	if err == nil {
		for _, item := range a.Attachments {
			data := attachments[item.ID]
			header = &zip.FileHeader{Name: "attachments/" + item.ID, Method: zip.Deflate}
			header.SetModTime(time.Unix(0, 0))
			f, err = zw.CreateHeader(header)
			if err == nil {
				_, err = f.Write(data)
			}
			if err != nil {
				break
			}
		}
	}
	if closeErr := zw.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if int64(out.Len()) > workspaceZipMaxBytes {
		return nil, errors.New("工作区导出包超过 64 MiB")
	}
	return out.Bytes(), nil
}

func readWorkspaceZip(raw []byte) (workspaceZipData, error) {
	if int64(len(raw)) > workspaceZipMaxBytes {
		return workspaceZipData{}, errors.New("导入包超过 64 MiB")
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return workspaceZipData{}, errors.New("工作区导入包不是有效 ZIP")
	}
	var index []byte
	seen := map[string]bool{}
	var expanded int64
	for _, file := range zr.File {
		name := filepath.ToSlash(file.Name)
		if name == "" || strings.HasSuffix(name, "/") || strings.Contains(name, "../") || strings.HasPrefix(name, "/") {
			return workspaceZipData{}, errors.New("导入包包含无效路径")
		}
		if file.UncompressedSize64 > uint64(workspaceExpandedMax) || file.CompressedSize64 == 0 && file.UncompressedSize64 > 0 || file.UncompressedSize64 > file.CompressedSize64*1000+1024 {
			return workspaceZipData{}, errors.New("导入包压缩比或展开大小异常")
		}
		if strings.HasPrefix(name, "attachments/") {
			return workspaceZipData{}, errors.New("v1 工作区导入包不支持附件，请导入后重新上传")
		}
		if name != "workspace.json" {
			return workspaceZipData{}, errors.New("导入包包含不支持的文件")
		}
		if seen[name] || name == "workspace.json" && index != nil {
			return workspaceZipData{}, errors.New("导入包包含重复文件")
		}
		seen[name] = true
		if expanded+int64(file.UncompressedSize64) > workspaceExpandedMax {
			return workspaceZipData{}, errors.New("导入包展开后过大")
		}
		reader, err := file.Open()
		if err != nil {
			return workspaceZipData{}, errors.New("无法读取导入包")
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, workspaceExpandedMax-expanded+1))
		reader.Close()
		if readErr != nil || uint64(len(data)) != file.UncompressedSize64 {
			return workspaceZipData{}, errors.New("导入包内容不完整")
		}
		expanded += int64(len(data))
		if name == "workspace.json" {
			if int64(len(data)) > workspaceJSONMax {
				return workspaceZipData{}, errors.New("工作区索引过大")
			}
			index = data
		}
	}
	if index == nil {
		return workspaceZipData{}, errors.New("导入包缺少 workspace.json")
	}
	var archive workspaceArchive
	decoder := json.NewDecoder(bytes.NewReader(index))
	if err := decoder.Decode(&archive); err != nil || archive.Protocol != workspaceArchiveProtocol || archive.Kind != workspaceArchiveKind {
		return workspaceZipData{}, errors.New("工作区索引版本不受支持")
	}
	if err := validateWorkspaceArchive(archive); err != nil {
		return workspaceZipData{}, err
	}
	return workspaceZipData{Archive: archive}, nil
}

func validArchiveText(value string, max int) bool {
	return len(value) <= max && !strings.ContainsAny(value, "\x00")
}

func validateWorkspaceArchive(a workspaceArchive) error {
	if len(a.Tasks) > workspaceMaxTasks || len(a.Runs) > workspaceMaxRuns || len(a.Events) > workspaceMaxEvents || len(a.Knowledge) > workspaceMaxKnowledge || len(a.Scratch) > workspaceMaxScratch {
		return errors.New("工作区内容超过导入上限")
	}
	if len(a.Attachments) != 0 {
		return errors.New("v1 工作区导入包不支持附件，请导入后重新上传")
	}
	tasks, runs := map[string]bool{}, map[string]bool{}
	for _, task := range a.Tasks {
		if !archiveIDPattern.MatchString(task.ID) || tasks[task.ID] || !validArchiveText(task.Title, 180) || !validArchiveText(task.Model, 120) || !validArchiveText(task.ReasoningEffort, 120) || task.Engine != "" && !validEngine(task.Engine) {
			return errors.New("导入任务字段无效")
		}
		tasks[task.ID] = true
	}
	for _, run := range a.Runs {
		if !archiveIDPattern.MatchString(run.ID) || runs[run.ID] || !tasks[run.TaskID] || !validArchiveText(run.Input, 200000) || !validArchiveText(run.Result, 2<<20) || !validArchiveText(run.Error, 1<<20) || len(run.Attachments) != 0 {
			return errors.New("导入对话字段无效")
		}
		runs[run.ID] = true
	}
	for _, e := range a.Events {
		if !tasks[e.TaskID] || !runs[e.RunID] || !validArchiveText(e.Text, 2<<20) {
			return errors.New("导入执行事件字段无效")
		}
	}
	for _, k := range a.Knowledge {
		if !archiveIDPattern.MatchString(k.ID) || !tasks[k.TaskID] || k.RunID != "" && !runs[k.RunID] || !validArchiveText(k.Title, 120) || !validArchiveText(k.Content, knowledgeMaxBytes) {
			return errors.New("导入知识字段无效")
		}
	}
	for _, v := range a.Scratch {
		if !archiveIDPattern.MatchString(v.ID) || !tasks[v.TaskID] || !validArchiveText(v.Title, 120) || !validArchiveText(v.Content, scratchMaxBytes) || !validScratchColor(v.Color) {
			return errors.New("导入待办字段无效")
		}
	}
	for _, n := range a.Notes {
		if !tasks[n.TaskID] || !validArchiveText(n.Content, 2<<20) {
			return errors.New("导入任务知识字段无效")
		}
	}
	return nil
}

func (s *Server) workspaceExport(w http.ResponseWriter, r *http.Request) {
	if !s.modelProbeMu.TryLock() {
		fail(w, http.StatusConflict, "模型读取或测试正在进行，请稍后再导出")
		return
	}
	defer s.modelProbeMu.Unlock()
	if !s.updateMu.TryLock() {
		fail(w, http.StatusConflict, errUpdateBusy.Error())
		return
	}
	defer s.updateMu.Unlock()
	release, err := s.app.beginUpdate()
	if err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	defer release()
	_, _ = s.app.store.Exec("PRAGMA wal_checkpoint(PASSIVE)")
	a, attachments, err := s.app.store.workspaceArchiveSnapshot()
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	archive, err := buildWorkspaceZip(a, attachments)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="duo-workspace-export.zip"`)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(archive)))
	_, _ = w.Write(archive)
}

func currentImportMode(catalog WorkCatalog, id string) string {
	if !archiveIDPattern.MatchString(id) {
		return "{}"
	}
	for _, mode := range catalog.Modes {
		if mode.ID == id {
			return mustJSON(mode)
		}
	}
	return "{}"
}

func (s *Server) workspaceImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, workspaceZipMaxBytes+1)
	raw, err := io.ReadAll(r.Body)
	if err != nil || int64(len(raw)) > workspaceZipMaxBytes {
		fail(w, http.StatusBadRequest, "导入包超过 64 MiB")
		return
	}
	data, err := readWorkspaceZip(raw)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.modelProbeMu.TryLock() {
		fail(w, http.StatusConflict, "模型读取或测试正在进行，请稍后再导入")
		return
	}
	defer s.modelProbeMu.Unlock()
	if !s.updateMu.TryLock() {
		fail(w, http.StatusConflict, errUpdateBusy.Error())
		return
	}
	defer s.updateMu.Unlock()
	release, err := s.app.beginUpdate()
	if err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	defer release()
	c := s.app.config.get()
	env, err := c.environment("")
	if err != nil || len(env.Workspaces) == 0 {
		fail(w, http.StatusConflict, "当前默认执行环境没有可用工作目录，未导入")
		return
	}
	modeCatalog := s.app.store.catalog()
	tx, err := s.app.store.Begin()
	if err != nil {
		fail(w, 500, "无法开始导入事务")
		return
	}
	rollback := func(message string) { _ = tx.Rollback(); fail(w, http.StatusBadRequest, message) }
	taskIDs, runIDs := map[string]string{}, map[string]string{}
	for _, task := range data.Archive.Tasks {
		id := uid()
		taskIDs[task.ID] = id
		status := "idle"
		created, updated := task.Created, task.Updated
		if created <= 0 {
			created = now()
		}
		if updated <= 0 {
			updated = created
		}
		if _, err = tx.Exec("INSERT INTO tasks(id,title,workspace,model,session,status,created,updated) VALUES(?,?,?,?,?,?,?,?)", id, task.Title, env.Workspaces[0], task.Model, "", status, created, updated); err != nil {
			rollback("导入任务失败")
			return
		}
		envRaw, _ := json.Marshal(env)
		if _, err = tx.Exec("INSERT INTO task_environments(task_id,environment) VALUES(?,?)", id, string(envRaw)); err != nil {
			rollback("导入执行环境失败")
			return
		}
		engine := task.Engine
		if engine == "" {
			engine = env.DefaultEngine
			if engine == "" {
				engine = "codex"
			}
		}
		if _, err = tx.Exec("INSERT INTO task_execution(task_id,reasoning_effort,engine) VALUES(?,?,?)", id, task.ReasoningEffort, engine); err != nil {
			rollback("导入任务设置失败")
			return
		}
		if _, err = tx.Exec("INSERT INTO task_preferences(task_id,pinned,archived) VALUES(?,?,?)", id, boolInt(task.Pinned), boolInt(task.Archived)); err != nil {
			rollback("导入任务状态失败")
			return
		}
		if _, err = tx.Exec("INSERT INTO task_options(task_id,mode,deleted) VALUES(?,?,?)", id, currentImportMode(modeCatalog, task.ModeID), boolInt(task.Deleted)); err != nil {
			rollback("导入任务模式失败")
			return
		}
	}
	for _, run := range data.Archive.Runs {
		id := uid()
		runIDs[run.ID] = id
		status := run.Status
		if status == "queued" || status == "running" {
			status = "interrupted"
		}
		if status == "" {
			status = "done"
		}
		finished := run.Finished
		if finished <= 0 && status == "interrupted" {
			finished = now()
		}
		if _, err = tx.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,error,created,finished) VALUES(?,?,?,?,?,?,?,?,?,?)", id, taskIDs[run.TaskID], run.Input, run.Kind, run.Source, status, run.Result, run.Error, run.Created, finished); err != nil {
			rollback("导入对话失败")
			return
		}
		modeRaw := currentImportMode(modeCatalog, run.ModeID)
		if _, err = tx.Exec("INSERT INTO run_options(run_id,mode,attachments) VALUES(?,?,?)", id, modeRaw, "[]"); err != nil {
			rollback("导入对话选项失败")
			return
		}
		usage := "null"
		if run.Usage != nil {
			usage = mustJSON(run.Usage)
		}
		if _, err = tx.Exec("INSERT INTO run_metrics(run_id,started,usage) VALUES(?,?,?)", id, run.Started, usage); err != nil {
			rollback("导入用量失败")
			return
		}
	}
	for _, event := range data.Archive.Events {
		if _, err = tx.Exec("INSERT INTO events(task_id,run_id,kind,text,created) VALUES(?,?,?,?,?)", taskIDs[event.TaskID], runIDs[event.RunID], event.Kind, event.Text, event.Created); err != nil {
			rollback("导入执行记录失败")
			return
		}
	}
	for _, k := range data.Archive.Knowledge {
		id := uid()
		runID := ""
		if k.RunID != "" {
			runID = runIDs[k.RunID]
		}
		if _, err = tx.Exec("INSERT INTO knowledge_entries(id,task_id,title,content,status,source,run_id,revision,created,updated) VALUES(?,?,?,?,?,?,?,?,?,?)", id, taskIDs[k.TaskID], k.Title, k.Content, knowledgeState(k.Status), knowledgeOrigin(k.Source), runID, maxInt64(k.Revision, 1), k.Created, k.Updated); err != nil {
			rollback("导入知识失败")
			return
		}
	}
	for _, v := range data.Archive.Scratch {
		id := uid()
		if _, err = tx.Exec("INSERT INTO scratch(id,task_id,title,content,status,due,done_at,revision,updated) VALUES(?,?,?,?,?,?,?,?,?)", id, taskIDs[v.TaskID], v.Title, v.Content, scratchState(v.Status), v.Due, v.DoneAt, maxInt64(v.Revision, 1), v.Updated); err != nil {
			rollback("导入待办失败")
			return
		}
		if _, err = tx.Exec("INSERT INTO scratch_colors(id,color) VALUES(?,?)", id, v.Color); err != nil {
			rollback("导入待办颜色失败")
			return
		}
	}
	for _, n := range data.Archive.Notes {
		if _, err = tx.Exec("INSERT INTO notes(task_id,content,revision,updated) VALUES(?,?,?,?)", taskIDs[n.TaskID], n.Content, maxInt64(n.Revision, 1), n.Updated); err != nil {
			rollback("导入任务知识失败")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		fail(w, 500, "导入事务提交失败")
		return
	}
	s.app.changed()
	jsonOut(w, http.StatusCreated, map[string]any{"ok": true, "tasks": len(data.Archive.Tasks), "runs": len(data.Archive.Runs), "events": len(data.Archive.Events), "knowledge": len(data.Archive.Knowledge), "scratch": len(data.Archive.Scratch), "attachments": len(data.Archive.Attachments)})
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func maxInt64(value, fallback int64) int64 {
	if value < fallback {
		return fallback
	}
	return value
}
