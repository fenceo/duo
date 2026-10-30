package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const vaultFileLimit = 2 * 1024 * 1024

type VaultConfig struct {
	RootMode           string `json:"root_mode,omitempty"`
	HumanLayout        bool   `json:"human_layout,omitempty"`
	IncludeLegacy      *bool  `json:"include_legacy,omitempty"`
	TaskFolders        bool   `json:"task_folders"`
	IncludeAutomatic   bool   `json:"include_automatic"`
	Enabled            bool   `json:"enabled"`
	Directory          string `json:"directory"`
	IncludeRuns        bool   `json:"include_runs"`
	ConsensusDirectory string `json:"consensus_directory,omitempty"`
	SystemDirectory    string `json:"system_directory,omitempty"`
	ProjectDirectory   string `json:"project_directory,omitempty"`
}
type VaultReport struct {
	Updated   int64    `json:"updated"`
	Exported  int      `json:"exported"`
	Imported  int      `json:"imported"`
	Indexed   int      `json:"indexed"`
	Conflicts []string `json:"conflicts"`
	Warnings  []string `json:"warnings"`
	Error     string   `json:"error,omitempty"`
}
type vaultLink struct{ Path, Local, File string }
type vaultFile struct {
	Document   LibraryDocument
	Raw        string
	Properties map[string]any
}
type vaultCachedFile struct {
	Info fs.FileInfo
	File vaultFile
}

func (s *Store) vaultConfig() VaultConfig {
	c := VaultConfig{TaskFolders: true}
	raw := s.setting("knowledge_vault")
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &c)
	} else {
		c.Enabled, c.IncludeAutomatic, c.IncludeRuns = true, true, false
		c.RootMode, c.HumanLayout = "direct", true
		legacy := false
		c.IncludeLegacy = &legacy
	}
	if c.Directory == "" && (c.Enabled || raw == "") {
		c.Directory = filepath.Join(s.directory, "knowledge")
	}
	return c
}

// Report the actual managed subtree, not just the folder selected by the user.
// This is presentation metadata; vaultRoot still validates every write path.
func vaultDocumentDirectory(c VaultConfig) string {
	if !filepath.IsAbs(c.Directory) {
		return ""
	}
	if c.RootMode == "direct" {
		return filepath.Clean(c.Directory)
	}
	return filepath.Join(c.Directory, "Duo")
}

// Reject links and junctions at every component, including the vault root.
// The managed subtree must never point into another directory.
func plainVaultPath(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for p := abs; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return errors.New("知识库目录不能经过符号链接或目录联接")
			}
			real, e := filepath.EvalSymlinks(p)
			if e != nil {
				return e
			}
			if !strings.EqualFold(filepath.Clean(real), filepath.Clean(p)) {
				return errors.New("知识库目录不能经过重定向目录")
			}
		}
		next := filepath.Dir(p)
		if next == p {
			break
		}
	}
	return nil
}

func vaultRoot(c VaultConfig) (string, error) {
	if err := prepareKnowledgeFolders(&c); err != nil {
		return "", err
	}
	if !filepath.IsAbs(c.Directory) {
		return "", errors.New("请选择这台 Duo 服务所在电脑上的 Vault 绝对路径")
	}
	if err := plainVaultPath(c.Directory); err != nil {
		return "", err
	}
	info, err := os.Stat(c.Directory)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("Vault 必须是已有文件夹")
	}
	root := vaultDocumentDirectory(c)
	if err = plainVaultPath(root); err != nil {
		return "", err
	}
	return root, nil
}

func parseVaultFile(path, raw string) (vaultFile, error) {
	f := vaultFile{Raw: raw, Properties: map[string]any{}}
	text := strings.TrimPrefix(strings.ReplaceAll(raw, "\r\n", "\n"), "\ufeff")
	if !utf8.ValidString(text) {
		return f, errors.New("不是 UTF-8 Markdown")
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "<<<<<<< ") || strings.HasPrefix(line, ">>>>>>> ") {
			return f, errors.New("存在未解决的 Git 合并冲突")
		}
	}
	body := text
	if strings.HasPrefix(text, "---\n") {
		end := strings.Index(text[4:], "\n---\n")
		if end < 0 {
			return f, errors.New("YAML 属性区没有结束")
		}
		head := text[4 : 4+end]
		if err := yaml.Unmarshal([]byte(head), &f.Properties); err != nil {
			return f, fmt.Errorf("YAML 属性无效：%w", err)
		}
		if err := yaml.Unmarshal([]byte(head), &f.Document); err != nil {
			return f, fmt.Errorf("Duo 属性类型无效：%w", err)
		}
		body = text[4+end+5:]
	}
	d := &f.Document
	switch tags := f.Properties["tags"].(type) {
	case string:
		d.Tags = notebookTags(strings.Fields(strings.ReplaceAll(tags, ",", " ")), "")
	case []any:
		for _, tag := range tags {
			if value, ok := tag.(string); ok {
				d.Tags = append(d.Tags, value)
			}
		}
		d.Tags = notebookTags(d.Tags, "")
	}
	d.Content = strings.TrimSpace(body)
	d.Path = path
	d.Origin = "vault"
	if d.Title == "" {
		d.Title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if d.Kind != "knowledge" && d.Kind != "run" && d.Kind != "task" && d.Kind != "insight" && d.Kind != "topic" && d.Kind != "index" && d.Kind != "curated" {
		d.Kind = "note"
		d.Status = knowledgeState(d.Status)
	}
	if d.Kind == "knowledge" {
		d.Status = knowledgeState(d.Status)
	}
	if d.Kind == "curated" {
		candidate := curatedFromDocument(*d)
		candidate.Status = "observed"
		if err := prepareCurated(&candidate); err != nil {
			return f, fmt.Errorf("正式知识文件无效：%w", err)
		}
	}
	if d.ID == "" {
		d.ID = "note:" + hash(path)
	}
	d.Hash = documentHash(*d)
	return f, nil
}

func renderVaultFile(d LibraryDocument, properties map[string]any) (string, error) {
	if properties == nil {
		properties = map[string]any{}
	}
	for _, key := range []string{"duo_pending_revisions", "duo_revision_history", "duo_review_keys"} {
		delete(properties, key)
	}
	// Preserve Obsidian tags and all unknown user properties on local updates.
	raw, err := yaml.Marshal(d)
	if err != nil {
		return "", err
	}
	var own map[string]any
	if err = yaml.Unmarshal(raw, &own); err != nil {
		return "", err
	}
	if len(d.Tags) > 0 {
		own["tags"] = d.Tags
	}
	for k, v := range own {
		if k == "tags" {
			if _, ok := properties[k]; ok {
				continue
			}
		}
		properties[k] = v
	}
	properties["duo_format"] = 1
	raw, err = yaml.Marshal(properties)
	if err != nil {
		return "", err
	}
	return "---\n" + string(raw) + "---\n\n" + strings.TrimSpace(d.Content) + "\n", nil
}

func scanVault(ctx context.Context, root string) (map[string]vaultFile, []string, error) {
	return scanVaultCached(ctx, root, nil)
}

func scanVaultCached(ctx context.Context, root string, cache map[string]vaultCachedFile) (map[string]vaultFile, []string, error) {
	files := map[string]vaultFile{}
	warnings := []string{}
	total := int64(0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if err := plainVaultPath(path); err != nil {
			warnings = append(warnings, path+"："+err.Error())
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if info.Size() > vaultFileLimit {
			warnings = append(warnings, path+"：超过 2 MiB，未索引")
			return nil
		}
		total += info.Size()
		if total > 128*1024*1024 || len(files) >= 10000 {
			return errors.New("Duo 文档超过 128 MiB 或 10000 个文件，请拆分资料目录")
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if prior, ok := cache[rel]; ok && prior.Info.Size() == info.Size() && prior.Info.ModTime().Equal(info.ModTime()) && os.SameFile(prior.Info, info) {
			files[rel] = prior.File
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(file, vaultFileLimit+1))
		file.Close()
		if err != nil {
			return err
		}
		if len(b) > vaultFileLimit {
			return errors.New("读取时文件增大，请重试")
		}
		f, err := parseVaultFile(rel, string(b))
		if err != nil {
			warnings = append(warnings, rel+"："+err.Error())
			return nil
		}
		if f.Document.Updated == 0 {
			f.Document.Updated = info.ModTime().UnixMilli()
		}
		files[rel] = f
		if cache != nil {
			cache[rel] = vaultCachedFile{Info: info, File: f}
		}
		return nil
	})
	for rel := range cache {
		if _, ok := files[rel]; !ok {
			delete(cache, rel)
		}
	}
	return files, warnings, err
}

func writeVaultFile(root, rel, expected, content string) error {
	path := filepath.Join(root, filepath.FromSlash(rel))
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return errors.New("知识文档路径越界")
	}
	if err := plainVaultPath(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := plainVaultPath(path); err != nil {
		return err
	}
	check := func() error {
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) && expected == "" {
			return nil
		}
		if err != nil {
			return err
		}
		if expected == "" || hash(string(b)) != expected {
			return errors.New("文件已被其他程序修改，保留两端内容，请重新同步")
		}
		return nil
	}
	if err := check(); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".duo-write-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.WriteString(content); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = plainVaultPath(path); err != nil {
		return err
	}
	if err = check(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// A tracked import can have additional human-maintained topic folders. Those
// folders are not a request to flatten its layout. Distinguish them from paths
// produced by either Duo layout using the previous file's title and scope, so
// renaming a managed note or explicitly changing its layer root still uses the
// existing safe-move path. Suffix comparison also recognizes a prior root that
// became a child of the newly configured root.
func curatedCustomTrackedPath(c VaultConfig, previous LibraryDocument, tracked string) bool {
	if !curatedLayer(previous.Layer) || !validCuratedTrackedPath(tracked) {
		return false
	}
	currentFolder := configuredKnowledgeFolder(c, previous.Layer)
	if !strings.HasPrefix(tracked, currentFolder+"/") {
		return false
	}
	for _, human := range []bool{false, true} {
		layout := c
		layout.HumanLayout = human
		canonical := configuredCuratedPath(layout, previous)
		folder := configuredKnowledgeFolder(layout, previous.Layer)
		if canonical == "" || !strings.HasPrefix(canonical, folder+"/") {
			continue
		}
		suffix := strings.TrimPrefix(canonical, folder+"/")
		if strings.HasSuffix(tracked, "/"+suffix) {
			return false
		}
		if human {
			// Human layout adds a stable suffix only for actual collisions.
			base := strings.TrimSuffix(suffix, ".md") + "-" + hash(previous.ID)[:8]
			if strings.HasSuffix(tracked, "/"+base+".md") {
				return false
			}
			prefix := path.Base(base) + "-"
			name := strings.TrimSuffix(path.Base(tracked), ".md")
			ordinal := strings.TrimPrefix(name, prefix)
			if strings.HasPrefix(name, prefix) && ordinal != "" && strings.Trim(ordinal, "0123456789") == "" && strings.HasSuffix(path.Dir(tracked), "/"+path.Dir(suffix)) {
				return false
			}
		}
	}
	return true
}

// The human's root landing page is not necessarily a Duo-generated document.
// Pick a stable alternative without replacing it (including unparseable files).
func availableKnowledgeHomePath(root string, files map[string]vaultFile, wanted string) (string, error) {
	available := func(relative string) bool {
		if file, exists := files[relative]; exists {
			return file.Document.ID == "curated-index:home" && file.Document.Kind == "index"
		}
		_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
		return os.IsNotExist(err)
	}
	if available(wanted) {
		return wanted, nil
	}
	for i := 0; i < 1000; i++ {
		name := "Duo知识库索引.md"
		if i > 0 {
			name = "Duo知识库索引-" + hash("curated-index:home")[:8]
			if i > 1 {
				name += fmt.Sprintf("-%d", i)
			}
			name += ".md"
		}
		if available(name) {
			return name, nil
		}
	}
	return "", errors.New("知识库首页文件名冲突过多，请保留用户文件并另选目录")
}

func (a *App) syncVault(ctx context.Context, background ...bool) (report VaultReport, err error) {
	a.vaultMu.Lock()
	defer a.vaultMu.Unlock()
	report = VaultReport{Updated: now(), Conflicts: []string{}, Warnings: []string{}}
	defer func() {
		if err != nil {
			report.Error = err.Error()
		}
		a.vaultReport = report
	}()
	c := a.store.vaultConfig()
	if !c.Enabled {
		return report, nil
	}
	if filepath.Clean(c.Directory) == filepath.Clean(filepath.Join(a.store.directory, "knowledge")) {
		if err = plainVaultPath(c.Directory); err != nil {
			return report, err
		}
		if err = os.MkdirAll(c.Directory, 0700); err != nil {
			return report, err
		}
	}
	root, err := vaultRoot(c)
	if err != nil {
		return report, err
	}
	if err = ensureKnowledgeLayerFolders(root, c); err != nil {
		return report, err
	}
	var cache map[string]vaultCachedFile
	if len(background) > 0 && background[0] {
		if a.vaultCacheRoot != root {
			a.vaultCacheRoot = root
			a.vaultCache = map[string]vaultCachedFile{}
		}
		cache = a.vaultCache
	} else {
		a.vaultCacheRoot = ""
		a.vaultCache = nil
	}
	files, warnings, err := scanVaultCached(ctx, root, cache)
	report.Warnings = warnings
	if err != nil {
		return report, err
	}
	curatedWarnings, imported, err := a.store.importNewCuratedFiles(ctx, files)
	if err != nil {
		return report, err
	}
	report.Warnings = append(report.Warnings, curatedWarnings...)
	report.Imported += imported
	docs, err := a.store.localLibrary(ctx)
	if err != nil {
		return report, err
	}
	humanPaths := curatedHumanPaths(docs, c)
	if c.TaskFolders && c.IncludeAutomatic {
		notebooks, e := a.store.notebookDocuments(ctx)
		if e != nil {
			return report, e
		}
		docs = append(docs, notebooks...)
	}
	for _, d := range docs {
		if err = ctx.Err(); err != nil {
			return report, err
		}
		if c.IncludeLegacy != nil && !*c.IncludeLegacy && d.Kind != "curated" && !strings.HasPrefix(d.ID, "curated-index:") {
			continue
		}
		if strings.HasPrefix(d.ID, "curated-index:") {
			// Earlier entries in this pass may have imported external edits and
			// downgraded verification; the reading index must show that state now.
			d, err = a.store.libraryDocument(ctx, d.ID)
			if err != nil {
				return report, err
			}
		}
		if d.Kind == "run" && !c.IncludeRuns {
			continue
		}
		if d.Automatic && d.Kind != "curated" && !c.IncludeAutomatic {
			continue
		}
		if c.TaskFolders && d.Kind == "knowledge" && d.Automatic {
			// New conversations already have a full transcript; do not export a second
			// automatic excerpt. Preserve and relocate automatic files tracked by v1.
			var tracked int
			if err = a.store.QueryRow("SELECT count(*) FROM library_links WHERE id=?", d.ID).Scan(&tracked); err != nil {
				return report, err
			}
			if tracked == 0 {
				continue
			}
		}
		localHash := documentHash(d)
		if d.Kind == "curated" || strings.HasPrefix(d.ID, "curated-index:") {
			// Explicit user-maintained procedures may include meaningful device
			// paths (/srv/nfs, /mnt, flash targets). Credential checks happen on save.
		} else if c.TaskFolders {
			d.Title = portableKnowledge(d.Title, "")
			d.TaskTitle = portableKnowledge(d.TaskTitle, "")
			d.Content = portableKnowledge(d.Content, "")
			decorateLibraryDocument(&d)
		} else {
			d.Title = redactContinuation(d.Title)
			d.TaskTitle = redactContinuation(d.TaskTitle)
			d.Content = redactContinuation(d.Content)
		}
		if len(d.Content) > vaultFileLimit-8192 {
			report.Warnings = append(report.Warnings, d.ID+"：内容过大，保留本地原文，未导出")
			continue
		}
		name := strings.TrimPrefix(d.ID, d.Kind+":")
		// IDs come from the database, but imported data must not form paths.
		if !d.Generated && !validNotebookID(name) {
			report.Warnings = append(report.Warnings, "无法导出非标准 ID："+d.ID)
			continue
		}
		rel := d.Kind + "/" + name + ".md"
		if strings.HasPrefix(d.ID, "curated-index:") {
			rel = d.Path
		} else if d.Kind == "curated" {
			rel = configuredCuratedPath(c, d)
			if c.HumanLayout {
				rel = humanPaths[d.ID]
			}
			if rel == "" {
				continue
			}
		} else if c.TaskFolders {
			rel = notebookPath(d)
			if rel == "" {
				continue
			}
		}
		var link vaultLink
		e := a.store.QueryRow("SELECT path,local_hash,file_hash FROM library_links WHERE id=?", d.ID).Scan(&link.Path, &link.Local, &link.File)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return report, e
		}
		if d.ID == "curated-index:home" {
			if e == nil && validCuratedTrackedPath(link.Path) {
				rel = link.Path
			}
			chosen, chooseErr := availableKnowledgeHomePath(root, files, rel)
			if chooseErr != nil {
				return report, chooseErr
			}
			if chosen != rel {
				// An existing human document is never moved or treated as the
				// generated page's baseline. Track only the new independent index.
				rel, link, e = chosen, vaultLink{}, sql.ErrNoRows
			}
		}
		layerDirectoryChanged := false
		if e == nil && d.Kind == "curated" {
			previousLayer := d.Layer
			if previous, ok := files[link.Path]; ok && curatedLayer(previous.Document.Layer) {
				previousLayer = previous.Document.Layer
				if previousLayer == d.Layer && curatedCustomTrackedPath(c, previous.Document, link.Path) {
					// Keep imported project/topic hierarchies and their relative
					// source/index links intact. TaskFolders only normalizes paths
					// generated by Duo, not arbitrary user-maintained subfolders.
					rel = link.Path
				}
			}
			layerDirectoryChanged = rel != link.Path && curatedTrackedFolder(previousLayer, link.Path) != configuredKnowledgeFolder(c, d.Layer)
		}
		// Upgrade old tracked paths only when the exact file is unchanged. Rename
		// preserves user properties and never overwrites another document.
		if e == nil && (c.TaskFolders || layerDirectoryChanged || strings.HasPrefix(d.ID, "curated-index:")) && rel != link.Path {
			old, ok := files[link.Path]
			if ok && hash(old.Raw) == link.File {
				target := filepath.Join(root, filepath.FromSlash(rel))
				if _, check := os.Lstat(target); os.IsNotExist(check) {
					if moveErr := writeVaultFile(root, rel, "", old.Raw); moveErr == nil {
						source := filepath.Join(root, filepath.FromSlash(link.Path))
						if b, readErr := os.ReadFile(source); readErr == nil && hash(string(b)) == link.File && plainVaultPath(source) == nil {
							if removeErr := os.Remove(source); removeErr == nil {
								delete(files, link.Path)
								old.Document.Path = rel
								files[rel] = old
								link.Path = rel
							}
						}
					}
				}
			}
		}
		if e == nil {
			rel = link.Path
		}
		file, exists := files[rel]
		if !exists {
			if e == nil {
				report.Warnings = append(report.Warnings, rel+"：文件已移除、改名或无法解析，不自动重建")
				continue
			}
			// A conflicting/invalid existing file is never considered empty.
			if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); !os.IsNotExist(statErr) {
				report.Conflicts = append(report.Conflicts, rel)
				continue
			}
		}
		fileHash := ""
		if exists {
			fileHash = hash(file.Raw)
		}
		if exists && (file.Document.ID != d.ID || file.Document.Kind != d.Kind || file.Document.TaskID != d.TaskID) {
			report.Conflicts = append(report.Conflicts, rel+"：来源标识改变")
			continue
		}
		if exists && e != nil && documentHash(file.Document) != documentHash(d) {
			report.Conflicts = append(report.Conflicts, rel+"：没有共同同步基线，请手工合并")
			continue
		}
		if exists && e == nil && d.Generated && localHash != link.Local && (fileHash != link.File || documentHash(file.Document) != link.Local) {
			// An idle sync may already have acknowledged the external file hash.
			// Its body still differs from the generated baseline, so a later task
			// update must preserve that edit rather than silently overwrite it.
			report.Conflicts = append(report.Conflicts, rel+"：自动文档与外部编辑都已变化，保留外部文件")
			continue
		}
		if exists && e == nil && fileHash != link.File {
			// Auto-maintained summaries/indexes are not allowed to erase edits in an
			// external editor. Keep the edited document visible and report the conflict.
			if d.Generated && localHash != link.Local {
				report.Conflicts = append(report.Conflicts, rel+"：自动文档与外部编辑都已变化，保留外部文件")
				continue
			}
			if localHash != link.Local && ((d.Kind == "curated" && !sameCuratedContent(d, file.Document)) || (d.Kind != "curated" && (strings.TrimSpace(file.Document.Content) != strings.TrimSpace(d.Content) || file.Document.Title != d.Title || file.Document.Status != d.Status))) {
				report.Conflicts = append(report.Conflicts, rel+"：Duo 和 Obsidian 都已修改")
				continue
			}
			if d.Kind == "curated" {
				if e = validateCuratedIdentity(d, file.Document); e != nil {
					report.Conflicts = append(report.Conflicts, rel+"："+e.Error())
					continue
				}
				d, e = a.store.importCuratedFile(ctx, d, file.Document)
				if e != nil {
					report.Conflicts = append(report.Conflicts, rel+"："+e.Error())
					continue
				}
				localHash = documentHash(d)
				report.Imported++
				// Show the same verification state in Obsidian after an external
				// substantive edit, retaining unknown user properties and the body.
				if refreshed, renderErr := renderVaultFile(d, file.Properties); renderErr == nil && refreshed != file.Raw {
					if writeErr := writeVaultFile(root, rel, fileHash, refreshed); writeErr != nil {
						report.Conflicts = append(report.Conflicts, rel+"："+writeErr.Error())
						continue
					}
					fileHash = hash(refreshed)
					report.Exported++
				}
			} else if d.Kind == "knowledge" {
				// Redacted exports cannot safely replace private local originals.
				original := d
				original.Hash = ""
				if documentHash(original) != localHash {
					report.Conflicts = append(report.Conflicts, rel+"：原文包含脱敏内容，请手工合并")
					continue
				}
				v := Knowledge{ID: name, TaskID: d.TaskID, Title: file.Document.Title, Content: file.Document.Content, Status: file.Document.Status, Revision: d.Revision}
				if e = prepareKnowledge(&v); e != nil {
					report.Conflicts = append(report.Conflicts, rel+"："+e.Error())
					continue
				}
				if e = a.store.writeKnowledge(v, false); e != nil {
					report.Conflicts = append(report.Conflicts, rel+"：本地条目已变化")
					continue
				}
				d.Title, d.Content, d.Status = v.Title, v.Content, v.Status
				if e = a.store.QueryRow("SELECT revision,updated FROM knowledge_entries WHERE id=?", name).Scan(&d.Revision, &d.Updated); e != nil {
					return report, e
				}
				localHash = documentHash(d)
				report.Imported++
			}
			// Record edits remain portable annotations; original runs are immutable.
		} else if !exists || e != nil || localHash != link.Local {
			raw, e := renderVaultFile(d, file.Properties)
			if e != nil {
				return report, e
			}
			if !exists || raw != file.Raw {
				if e = writeVaultFile(root, rel, fileHash, raw); e != nil {
					report.Conflicts = append(report.Conflicts, rel+"："+e.Error())
					continue
				}
				fileHash = hash(raw)
				report.Exported++
			}
		}
		if _, err = a.store.Exec("INSERT INTO library_links VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET path=excluded.path,local_hash=excluded.local_hash,file_hash=excluded.file_hash WHERE library_links.path<>excluded.path OR library_links.local_hash<>excluded.local_hash OR library_links.file_hash<>excluded.file_hash", d.ID, rel, localHash, fileHash); err != nil {
			return report, err
		}
	}
	// Re-read after writes; removals and unparseable/conflicted Git files disappear
	// from the derived index instead of returning old cached content.
	if report.Exported > 0 {
		files, warnings, err = scanVault(ctx, root)
	}
	for _, warning := range warnings {
		found := false
		for _, prior := range report.Warnings {
			if prior == warning {
				found = true
				break
			}
		}
		if !found {
			report.Warnings = append(report.Warnings, warning)
		}
	}
	if err != nil {
		return report, err
	}
	tx, err := a.store.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	// Idle scans no longer rewrite the entire SQLite index.
	indexedRows, e := tx.Query("SELECT path FROM library_files")
	if e != nil {
		return report, e
	}
	removed := []string{}
	for indexedRows.Next() {
		var path string
		if e = indexedRows.Scan(&path); e != nil {
			break
		}
		if _, ok := files[path]; !ok {
			removed = append(removed, path)
		}
	}
	if e == nil {
		e = indexedRows.Err()
	}
	indexedRows.Close()
	if e != nil {
		return report, e
	}
	for _, path := range removed {
		if _, err = tx.Exec("DELETE FROM library_files WHERE path=?", path); err != nil {
			return report, err
		}
	}
	for path, f := range files {
		b, e := json.Marshal(f.Document)
		if e != nil {
			return report, e
		}
		if _, err = tx.Exec("INSERT INTO library_files VALUES(?,?) ON CONFLICT(path) DO UPDATE SET document=excluded.document WHERE library_files.document<>excluded.document", path, string(b)); err != nil {
			return report, err
		}
		report.Indexed++
	}
	err = tx.Commit()
	return report, err
}

func (a *App) vaultLoop() {
	defer a.wg.Done()
	for {
		if !a.updating.Load() {
			ctx, cancel := context.WithTimeout(a.ctx, 25*time.Second)
			a.retryWorkspaceMemoryDue(ctx, now())
			cancel()
		}
		if !a.updating.Load() {
			ctx, cancel := context.WithTimeout(a.ctx, 35*time.Second)
			a.organizeKnowledgeDue(ctx, now())
			cancel()
		}
		if a.store.vaultConfig().Enabled && !a.updating.Load() {
			ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
			_, _ = a.syncVault(ctx, true)
			cancel()
		}
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(60 * time.Second):
		}
	}
}

func (s *Server) vaultRoutes(m *http.ServeMux) {
	s.vaultMigrationRoutes(m)
	m.HandleFunc("GET /api/library/vault", s.secure(func(w http.ResponseWriter, r *http.Request) {
		s.app.vaultMu.Lock()
		defer s.app.vaultMu.Unlock()
		c := s.app.store.vaultConfig()
		jsonOut(w, 200, map[string]any{"config": c, "report": s.app.vaultReport, "document_directory": vaultDocumentDirectory(c)})
	}))
	m.HandleFunc("PUT /api/library/vault", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var c VaultConfig
		if !body(w, r, &c) {
			return
		}
		c.Directory = strings.TrimSpace(c.Directory)
		if err := prepareKnowledgeFolders(&c); err != nil {
			fail(w, 400, err.Error())
			return
		}
		if c.Directory == "" {
			c.Directory = filepath.Join(s.app.store.directory, "knowledge")
		}
		if c.Enabled {
			if filepath.Clean(c.Directory) == filepath.Clean(filepath.Join(s.app.store.directory, "knowledge")) {
				if err := plainVaultPath(c.Directory); err == nil {
					_ = os.MkdirAll(c.Directory, 0700)
				}
			}
			root, err := vaultRoot(c)
			if err != nil {
				fail(w, 400, err.Error())
				return
			}
			if err = ensureKnowledgeLayerFolders(root, c); err != nil {
				fail(w, 400, err.Error())
				return
			}
		}
		s.app.vaultMu.Lock()
		defer s.app.vaultMu.Unlock()
		old := s.app.store.vaultConfig()
		tx, err := s.app.store.Begin()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		defer tx.Rollback()
		if vaultDocumentDirectory(old) != vaultDocumentDirectory(c) || !c.Enabled {
			if _, err = tx.Exec("DELETE FROM library_files; DELETE FROM library_links"); err != nil {
				fail(w, 500, err.Error())
				return
			}
		}
		b, _ := json.Marshal(c)
		if _, err = tx.Exec("INSERT INTO settings VALUES('knowledge_vault',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(b)); err == nil {
			err = tx.Commit()
		}
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		s.app.vaultReport = VaultReport{}
		jsonOut(w, 200, struct {
			VaultConfig
			DocumentDirectory string `json:"document_directory"`
		}{c, vaultDocumentDirectory(c)})
	}))
	m.HandleFunc("POST /api/library/vault/refresh", s.secure(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		report, err := s.app.syncVault(ctx)
		if err != nil {
			report.Error = err.Error()
		}
		jsonOut(w, 200, report)
	}))
}
