package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Migration is deliberately additive. A failed attempt never changes the active
// root or deletes a source/target file; an identical copied file can be resumed.
const vaultMigrationBytes = 256 << 20

type VaultMigrationRequest struct {
	Directory   string `json:"directory"`
	RootMode    string `json:"root_mode"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Confirm     bool   `json:"confirm,omitempty"`
}

type VaultMigrationFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Action string `json:"action"`
	Hash   string `json:"hash"`
}

type VaultMigrationPreview struct {
	SourceDirectory string               `json:"source_directory"`
	TargetDirectory string               `json:"target_directory"`
	Fingerprint     string               `json:"fingerprint"`
	Files           []VaultMigrationFile `json:"files"`
	FileCount       int                  `json:"file_count"`
	TotalBytes      int64                `json:"total_bytes"`
	CopyCount       int                  `json:"copy_count"`
	IdenticalCount  int                  `json:"identical_count"`
	Conflicts       []string             `json:"conflicts"`
	Warnings        []string             `json:"warnings"`
	SourceDirty     bool                 `json:"source_dirty"`
	Ready           bool                 `json:"ready"`
	Completed       bool                 `json:"completed"`
	Copied          int                  `json:"copied"`
	Config          *VaultConfig         `json:"config,omitempty"`
	state           string
	configuration   VaultConfig
}

type vaultMigrationQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// Include the identities, revisions, provenance and inputs of generated reading
// pages. A transaction's final check prevents committing against a newer DB.
func vaultMigrationState(ctx context.Context, q vaultMigrationQuery) (string, error) {
	h := sha256.New()
	for _, query := range []string{
		"SELECT key,value FROM settings WHERE key='knowledge_vault' ORDER BY key",
		"SELECT * FROM library_links ORDER BY id", "SELECT * FROM library_files ORDER BY path",
		"SELECT * FROM curated_knowledge ORDER BY id", "SELECT * FROM knowledge_entries ORDER BY id",
		"SELECT * FROM task_memories ORDER BY run_id", "SELECT * FROM tasks ORDER BY id",
		"SELECT * FROM task_options ORDER BY task_id",
		"SELECT id,task_id,input,status,result,error,finished FROM runs WHERE status IN ('done','failed','interrupted') ORDER BY id",
		"SELECT run_id,seq,text FROM events WHERE kind='user' ORDER BY run_id,seq",
	} {
		io.WriteString(h, query+"\n")
		rows, err := q.QueryContext(ctx, query)
		if err != nil {
			return "", err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return "", err
		}
		for rows.Next() {
			values, refs := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				refs[i] = &values[i]
			}
			if err = rows.Scan(refs...); err != nil {
				break
			}
			b, _ := json.Marshal(values)
			h.Write(b)
			h.Write([]byte{'\n'})
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func vaultMigrationOverlap(a, b string) bool {
	a, b = strings.ToLower(filepath.Clean(a)), strings.ToLower(filepath.Clean(b))
	prefix := func(value string) string {
		return strings.TrimRight(value, string(filepath.Separator)) + string(filepath.Separator)
	}
	return a == b || strings.HasPrefix(a, prefix(b)) || strings.HasPrefix(b, prefix(a))
}

func vaultMigrationHash(ctx context.Context, name string) (string, int64, error) {
	if err := plainVaultPath(name); err != nil {
		return "", 0, err
	}
	f, err := os.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", 0, errors.New("迁移只接受普通文件")
	}
	if info.Size() > 32<<20 {
		return "", 0, errors.New("单个迁移文件超过 32 MiB，请先拆分或在文件管理器处理")
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var size int64
	for {
		if err = ctx.Err(); err != nil {
			return "", 0, err
		}
		n, readErr := f.Read(buf)
		size += int64(n)
		if size > 32<<20 {
			return "", 0, errors.New("读取期间迁移文件增大，请重新预览")
		}
		h.Write(buf[:n])
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", 0, readErr
		}
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

func (a *App) vaultMigrationPreview(ctx context.Context, request VaultMigrationRequest) (VaultMigrationPreview, error) {
	a.vaultMu.Lock()
	defer a.vaultMu.Unlock()
	return a.vaultMigrationPreviewLocked(ctx, request)
}

func (a *App) vaultMigrationPreviewLocked(ctx context.Context, request VaultMigrationRequest) (VaultMigrationPreview, error) {
	p := VaultMigrationPreview{Files: []VaultMigrationFile{}, Conflicts: []string{}, Warnings: []string{}}
	c := a.store.vaultConfig()
	if !c.Enabled {
		return p, errors.New("请先启用知识库同步，再迁移已有知识根目录")
	}
	source, err := vaultRoot(c)
	if err != nil {
		return p, err
	}
	targetConfig := c
	targetConfig.Directory, targetConfig.RootMode = strings.TrimSpace(request.Directory), request.RootMode
	if targetConfig.RootMode == "" {
		targetConfig.RootMode = "direct"
	}
	target, err := vaultRoot(targetConfig)
	if err != nil {
		return p, err
	}
	if vaultMigrationOverlap(source, target) {
		return p, errors.New("新旧实际知识根目录不能相同或互相包含；请选择独立目录")
	}
	p.SourceDirectory, p.TargetDirectory, p.configuration = source, target, targetConfig
	p.state, err = vaultMigrationState(ctx, a.store)
	if err != nil {
		return p, err
	}
	err = filepath.WalkDir(source, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == source {
			return nil
		}
		rel, err := filepath.Rel(source, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.EqualFold(entry.Name(), ".git") || strings.EqualFold(entry.Name(), ".obsidian") || strings.EqualFold(entry.Name(), ".trash") {
			p.Warnings = append(p.Warnings, rel+"：应用配置或版本控制目录不迁移，原目录保留")
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if err = plainVaultPath(name); err != nil {
			return fmt.Errorf("%s：%w", rel, err)
		}
		if entry.IsDir() {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".duo-write-") || strings.HasPrefix(entry.Name(), ".duo-migrate-") {
			p.Warnings = append(p.Warnings, rel+"：未完成写入临时文件不迁移，原文件保留")
			return nil
		}
		hash, size, err := vaultMigrationHash(ctx, name)
		if err != nil {
			return fmt.Errorf("%s：%w", rel, err)
		}
		p.TotalBytes += size
		if p.TotalBytes > vaultMigrationBytes || len(p.Files) >= 10000 {
			return errors.New("迁移资料超过 256 MiB 或 10000 个文件，请拆分知识根目录")
		}
		file := VaultMigrationFile{Path: rel, Size: size, Hash: hash, Action: "copy"}
		destination := filepath.Join(target, filepath.FromSlash(rel))
		if err = plainVaultPath(destination); err != nil {
			return fmt.Errorf("%s：%w", rel, err)
		}
		if _, err = os.Lstat(destination); err == nil {
			targetHash, _, hashErr := vaultMigrationHash(ctx, destination)
			if hashErr == nil && targetHash == hash {
				file.Action = "identical"
				p.IdenticalCount++
			} else {
				file.Action = "conflict"
				p.Conflicts = append(p.Conflicts, rel+"：目标已有不同内容或不是普通文件；两端均保留")
			}
		} else if os.IsNotExist(err) {
			p.CopyCount++
		} else {
			return err
		}
		p.Files = append(p.Files, file)
		return nil
	})
	if err != nil {
		return p, err
	}
	sort.Slice(p.Files, func(i, j int) bool { return p.Files[i].Path < p.Files[j].Path })
	p.FileCount = len(p.Files)
	// A new root must be empty or contain only identical files from an earlier
	// attempt. Merging an unrelated Vault could import colliding portable IDs or
	// establish new baselines before the user has reviewed that separate action.
	sourcePaths := map[string]bool{}
	for _, file := range p.Files {
		sourcePaths[strings.ToLower(file.Path)] = true
	}
	if _, statErr := os.Stat(target); statErr == nil {
		err = filepath.WalkDir(target, func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if name == target {
				return nil
			}
			if strings.EqualFold(entry.Name(), ".git") || strings.EqualFold(entry.Name(), ".obsidian") || strings.EqualFold(entry.Name(), ".trash") {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if err := plainVaultPath(name); err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(target, name)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if !sourcePaths[strings.ToLower(rel)] {
				p.Conflicts = append(p.Conflicts, rel+"：目标存在清单外文件；请选择空目录或上次迁移的目标目录")
			}
			if len(p.Conflicts) > 10000 {
				return errors.New("目标文件过多，请使用空目录迁移")
			}
			return nil
		})
		if err != nil {
			return p, err
		}
	} else if !os.IsNotExist(statErr) {
		return p, statErr
	}
	if err = a.vaultMigrationDirty(ctx, c, &p); err != nil {
		return p, err
	}
	state, err := vaultMigrationState(ctx, a.store)
	if err != nil {
		return p, err
	}
	if state != p.state {
		return p, errors.New("预览期间任务或知识已变化，请空闲后重新预览")
	}
	p.Ready = !p.SourceDirty && len(p.Conflicts) == 0
	b, _ := json.Marshal(struct {
		Source, Target, State string
		Config                VaultConfig
		Files                 []VaultMigrationFile
	}{source, target, p.state, targetConfig, p.Files})
	p.Fingerprint = hash(string(b))
	return p, nil
}

// A copy never establishes a new synchronization baseline. Require a clean
// common baseline first so a root change cannot make an old export authoritative.
func (a *App) vaultMigrationDirty(ctx context.Context, c VaultConfig, p *VaultMigrationPreview) error {
	files := map[string]VaultMigrationFile{}
	for _, file := range p.Files {
		files[file.Path] = file
	}
	links := map[string]vaultLink{}
	rows, err := a.store.QueryContext(ctx, "SELECT id,path,local_hash,file_hash FROM library_links")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var link vaultLink
		if err = rows.Scan(&id, &link.Path, &link.Local, &link.File); err != nil {
			break
		}
		links[id] = link
		if file, ok := files[link.Path]; !ok || file.Hash != link.File {
			p.SourceDirty = true
			p.Warnings = append(p.Warnings, link.Path+"：源文件与同步基线不同，请先立即同步并解决冲突")
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	docs, err := a.store.localLibrary(ctx)
	if err != nil {
		return err
	}
	if c.TaskFolders && c.IncludeAutomatic {
		notebooks, e := a.store.notebookDocuments(ctx)
		if e != nil {
			return e
		}
		docs = append(docs, notebooks...)
	}
	for _, d := range docs {
		if c.IncludeLegacy != nil && !*c.IncludeLegacy && d.Kind != "curated" && !strings.HasPrefix(d.ID, "curated-index:") {
			continue
		}
		if d.Kind == "run" && !c.IncludeRuns || d.Automatic && d.Kind != "curated" && !c.IncludeAutomatic {
			continue
		}
		link, exists := links[d.ID]
		if c.TaskFolders && d.Kind == "knowledge" && d.Automatic && !exists {
			continue
		}
		if !exists || link.Local != documentHash(d) {
			p.SourceDirty = true
			p.Warnings = append(p.Warnings, d.Title+"：Duo 侧存在待导出的修改，请先立即同步")
		}
	}
	for _, file := range p.Files {
		if !strings.EqualFold(filepath.Ext(file.Path), ".md") {
			continue
		}
		// A portable curated note not yet known locally must first go through the
		// regular import validation and observed-state downgrade at the old root.
		b, readErr := os.ReadFile(filepath.Join(p.SourceDirectory, filepath.FromSlash(file.Path)))
		if readErr != nil {
			return readErr
		}
		if parsed, parseErr := parseVaultFile(file.Path, string(b)); parseErr == nil && parsed.Document.Kind == "curated" {
			if _, exists := links[parsed.Document.ID]; !exists {
				p.SourceDirty = true
				p.Warnings = append(p.Warnings, file.Path+"：来源笔记尚未导入，请先立即同步")
			}
		}
	}
	return nil
}

// A staged, fsynced file is installed without replacing an existing name.
// Ordinary os.Rename would overwrite a concurrently created user file.
func copyVaultMigrationFile(ctx context.Context, source, target string, item VaultMigrationFile) error {
	from := filepath.Join(source, filepath.FromSlash(item.Path))
	to := filepath.Join(target, filepath.FromSlash(item.Path))
	if !filepath.IsLocal(filepath.FromSlash(item.Path)) {
		return errors.New("迁移文件路径越界")
	}
	if err := plainVaultPath(from); err != nil {
		return err
	}
	if err := plainVaultPath(to); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0700); err != nil {
		return err
	}
	if err := plainVaultPath(to); err != nil {
		return err
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(to), ".duo-migrate-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var size int64
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		n, readErr := in.Read(buf)
		size += int64(n)
		if size > item.Size {
			return errors.New("源文件增大，请重新预览")
		}
		if _, err = tmp.Write(buf[:n]); err != nil {
			return err
		}
		h.Write(buf[:n])
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if size != item.Size || hex.EncodeToString(h.Sum(nil)) != item.Hash {
		return errors.New("源文件已变化，请重新预览")
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = plainVaultPath(to); err != nil {
		return err
	}
	if err = installVaultMigrationFile(tmp.Name(), to); err != nil {
		return fmt.Errorf("无法安全创建目标文件（不会覆盖已有文件）：%w", err)
	}
	return nil
}

func (a *App) migrateVault(ctx context.Context, request VaultMigrationRequest) (VaultMigrationPreview, error) {
	a.vaultMu.Lock()
	defer a.vaultMu.Unlock()
	p, err := a.vaultMigrationPreviewLocked(ctx, request)
	if err != nil {
		return p, err
	}
	if !request.Confirm || request.Fingerprint == "" {
		return p, errors.New("请先预览迁移清单，并明确确认复制后切换知识根目录")
	}
	if !p.Ready || p.Fingerprint != request.Fingerprint {
		return p, errors.New("迁移清单已变化或存在待同步/冲突内容；原根目录仍生效，请处理后重新预览")
	}
	for _, file := range p.Files {
		if file.Action == "copy" {
			if err = copyVaultMigrationFile(ctx, p.SourceDirectory, p.TargetDirectory, file); err != nil {
				return p, fmt.Errorf("%s：%w；原根目录仍生效，已复制文件保留，可重新预览续传", file.Path, err)
			}
			p.Copied++
		}
	}
	// Rehash source and target after copying, including the source inventory. New
	// files, edits, removals and target changes all prevent the final configuration.
	check, err := a.vaultMigrationPreviewLocked(ctx, request)
	if err != nil {
		return p, err
	}
	if !check.Ready || check.state != p.state || check.CopyCount != 0 || len(check.Files) != len(p.Files) {
		return p, errors.New("复制期间源、目标或任务知识已变化；原根目录仍生效，请重新预览续传")
	}
	for i, file := range p.Files {
		if file.Path != check.Files[i].Path || file.Hash != check.Files[i].Hash {
			return p, errors.New("复制期间源文件已变化；原根目录仍生效，请重新预览")
		}
	}
	tx, err := a.store.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	state, err := vaultMigrationState(ctx, tx)
	if err != nil {
		return p, err
	}
	if state != p.state {
		return p, errors.New("提交前任务或知识已变化；原根目录仍生效，请重新预览")
	}
	b, _ := json.Marshal(p.configuration)
	if _, err = tx.ExecContext(ctx, "INSERT INTO settings VALUES('knowledge_vault',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(b)); err != nil {
		return p, err
	}
	if err = tx.Commit(); err != nil {
		return p, err
	}
	a.vaultCacheRoot, a.vaultCache = "", nil
	a.vaultReport = VaultReport{}
	p.Config, p.Completed = &p.configuration, true
	return p, nil
}

func (s *Server) vaultMigrationRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/library/vault/migration/preview", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var request VaultMigrationRequest
		if !body(w, r, &request) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		p, err := s.app.vaultMigrationPreview(ctx, request)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		jsonOut(w, 200, p)
	}))
	m.HandleFunc("POST /api/library/vault/migration", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var request VaultMigrationRequest
		if !body(w, r, &request) {
			return
		}
		if !request.Confirm || request.Fingerprint == "" {
			fail(w, 400, "请先预览迁移清单并明确确认")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		defer cancel()
		p, err := s.app.migrateVault(ctx, request)
		if err != nil {
			fail(w, 409, err.Error())
			return
		}
		jsonOut(w, 200, p)
	}))
}
