package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const previewLimit = 128 * 1024
const downloadLimit = 32 * 1024 * 1024

//go:embed workspace_reader.py
var workspaceReader string

type FileEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Size      int64  `json:"size"`
	Status    string `json:"status,omitempty"`
	Blocked   bool   `json:"blocked,omitempty"`
}
type FileResult struct {
	Path      string      `json:"path"`
	Items     []FileEntry `json:"items"`
	Content   string      `json:"content"`
	Size      int64       `json:"size"`
	Binary    bool        `json:"binary"`
	Truncated bool        `json:"truncated"`
	Message   string      `json:"message"`
	Data      string      `json:"data,omitempty"`
	Error     string      `json:"error,omitempty"`
}

func workspacePath(value string) (string, error) {
	if len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsAny(value, "\\:\x00\r\n") || strings.HasPrefix(value, "/") {
		return "", errors.New("请使用工作目录内的相对路径")
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." || strings.EqualFold(part, ".git") || (part != "." && strings.TrimRight(part, " .") != part) {
			return "", errors.New("不能读取工作目录外或 Git 内部文件")
		}
	}
	value = path.Clean(value)
	if value == "." {
		value = ""
	}
	return value, nil
}

type cappedOutput struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := max(0, b.limit-b.Len())
	if n > remaining {
		b.truncated = true
		p = p[:remaining]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}
func (b *cappedOutput) ReadFrom(r io.Reader) (int64, error) {
	var total int64
	buf := make([]byte, 32768)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			_, _ = b.Write(buf[:n])
			total += int64(n)
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}
func workspaceCommand(ctx context.Context, c Config, input string, limit int, args ...string) ([]byte, bool, error) {
	original := command(c, args...)
	cmd := commandWithContext(ctx, original)
	hideCommand(cmd)
	if original.Env == nil {
		cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	} else {
		cmd.Env = append(append([]string{}, original.Env...), "GIT_OPTIONAL_LOCKS=0")
	}
	cmd.WaitDelay = time.Second
	out := &cappedOutput{limit: limit}
	diagnostic := &cappedOutput{limit: 4096}
	cmd.Stdout = out
	cmd.Stderr = diagnostic
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	err := cmd.Run()
	if err != nil {
		return out.Bytes(), out.truncated, fmt.Errorf("执行环境读取失败：%s (%v)", strings.TrimSpace(diagnostic.String()), err)
	}
	return out.Bytes(), out.truncated, nil
}
func localGit(ctx context.Context, workspace string) ([]string, error) {
	args := []string{"git", "--no-pager", "--literal-pathspecs", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=", "-c", "core.untrackedCache=false", "-C", workspace}
	out, truncated, err := workspaceCommand(ctx, Config{}, "", 65536, append(args, "config", "--name-only", "--get-regexp", `^filter\..*\.(clean|process|required)$`)...)
	if truncated {
		return nil, errors.New("Git 过滤器配置超过读取限制")
	}
	// Exit 1 means no matching filters, not a broken repository.
	if err != nil && len(out) == 0 {
		if _, _, probeErr := workspaceCommand(ctx, Config{}, "", 4096, append(args, "rev-parse", "--git-dir")...); probeErr != nil {
			return nil, errors.New("当前目录不是 Git 仓库，或该环境未安装 Git；仍可浏览和下载文件")
		}
	}
	for _, key := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(key, "filter.") {
			i := strings.LastIndex(key, ".")
			driver := key[:i]
			args = append(args, "-c", driver+".clean=", "-c", driver+".process=", "-c", driver+".required=false")
		}
	}
	return args, nil
}
func readLocalWorkspace(ctx context.Context, workspace, action, relative string) (FileResult, error) {
	result := FileResult{Path: relative, Items: []FileEntry{}}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return result, fmt.Errorf("无法打开任务工作目录：%w", err)
	}
	defer root.Close()
	// Do not traverse symlinks/reparse points, even if they currently point inside.
	current := ""
	for _, part := range strings.Split(relative, "/") {
		if part == "" {
			continue
		}
		current = path.Join(current, part)
		info, e := root.Lstat(filepath.FromSlash(current))
		if e != nil {
			if action == "diff" && os.IsNotExist(e) {
				break
			}
			return result, e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return result, errors.New("符号链接不可在文件面板中打开")
		}
	}
	if action == "changes" || action == "diff" {
		args, e := localGit(ctx, workspace)
		if e != nil {
			result.Message = e.Error()
			return result, nil
		}
		if action == "changes" {
			prefixRaw, _, prefixErr := workspaceCommand(ctx, Config{}, "", 8192, append(append([]string{}, args...), "rev-parse", "--show-prefix")...)
			if prefixErr != nil {
				return result, prefixErr
			}
			prefix := strings.TrimSpace(string(prefixRaw))
			raw, truncated, e := workspaceCommand(ctx, Config{}, "", 1024*1024, append(args, "status", "--porcelain=v1", "-z", "--untracked-files=normal", "--ignore-submodules=all", "--", ".")...)
			if e != nil {
				return result, e
			}
			result.Truncated = truncated
			parts := bytes.Split(raw, []byte{0})
			for i := 0; i < len(parts); i++ {
				v := string(parts[i])
				if len(v) < 4 {
					continue
				}
				name := v[3:]
				status := v[:2]
				if strings.ContainsAny(status, "RC") {
					i++
				}
				if !strings.HasPrefix(name, prefix) {
					continue
				}
				name = strings.TrimPrefix(name, prefix)
				clean, e := workspacePath(name)
				if e != nil {
					continue
				}
				result.Items = append(result.Items, FileEntry{Name: path.Base(strings.TrimSuffix(name, "/")), Path: clean, Directory: strings.HasSuffix(name, "/"), Status: status})
				if len(result.Items) >= 500 {
					result.Truncated = true
					break
				}
			}
			result.Message = "工作目录当前 Git 状态，包含原有改动；并非本轮修改清单"
			return result, nil
		}
		for _, section := range []struct {
			label  string
			staged bool
		}{{"未暂存改动", false}, {"已暂存改动", true}} {
			tail := []string{"diff", "--no-ext-diff", "--no-textconv", "--ignore-submodules=all", "--no-color"}
			if section.staged {
				tail = append(tail, "--cached")
			}
			tail = append(tail, "--", relative)
			raw, truncated, e := workspaceCommand(ctx, Config{}, "", 512*1024, append(append([]string{}, args...), tail...)...)
			if e != nil {
				return result, e
			}
			result.Truncated = result.Truncated || truncated
			if len(raw) > 0 {
				result.Content += "── " + section.label + " ──\n" + strings.ToValidUTF8(string(raw), "�") + "\n"
			}
		}
		if result.Content == "" {
			result.Message = "没有已跟踪文件的差异。新文件可在“内容”中查看。"
		}
		return result, nil
	}
	name := filepath.FromSlash(relative)
	if name == "" {
		name = "."
	}
	file, err := root.Open(name)
	if err != nil {
		return result, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return result, err
	}
	result.Size = info.Size()
	if action == "list" {
		if !info.IsDir() {
			return result, errors.New("该路径不是目录")
		}
		entries, e := file.ReadDir(501)
		if e != nil && e != io.EOF {
			return result, e
		}
		if len(entries) > 500 {
			entries = entries[:500]
			result.Truncated = true
		}
		for _, entry := range entries {
			if strings.EqualFold(entry.Name(), ".git") {
				continue
			}
			p := path.Join(relative, entry.Name())
			if _, e := workspacePath(p); e != nil {
				continue
			}
			v := FileEntry{Name: entry.Name(), Path: p, Directory: entry.IsDir(), Blocked: entry.Type()&os.ModeSymlink != 0}
			if stat, e := entry.Info(); e == nil {
				v.Size = stat.Size()
				if !stat.Mode().IsRegular() && !stat.IsDir() {
					v.Blocked = true
				}
			}
			result.Items = append(result.Items, v)
		}
		sort.Slice(result.Items, func(i, j int) bool {
			a, b := result.Items[i], result.Items[j]
			if a.Directory != b.Directory {
				return a.Directory
			}
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		})
		return result, nil
	}
	if !info.Mode().IsRegular() {
		return result, errors.New("只支持普通文件")
	}
	limit := previewLimit
	if action == "download" {
		limit = downloadLimit
		if info.Size() > int64(limit) {
			return result, errors.New("下载文件不能超过 32 MiB")
		}
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return result, err
	}
	if len(raw) > limit {
		if action == "download" {
			return result, errors.New("下载文件不能超过 32 MiB")
		}
		result.Truncated = true
		raw = raw[:limit]
		for len(raw) > 0 && !utf8.Valid(raw) && !bytes.ContainsRune(raw, 0) {
			_, size := utf8.DecodeLastRune(raw)
			if size != 1 {
				break
			}
			raw = raw[:len(raw)-1]
			if len(raw) < limit-4 {
				break
			}
		}
	}
	if action == "download" {
		result.Data = base64.StdEncoding.EncodeToString(raw)
		return result, nil
	}
	result.Binary = bytes.ContainsRune(raw, 0) || !utf8.Valid(raw)
	if result.Binary {
		result.Message = "二进制或非 UTF-8 文件，请下载后查看"
	} else {
		result.Content = string(raw)
	}
	return result, nil
}
func readWorkspace(ctx context.Context, t Task, action, relative string) (FileResult, error) {
	if t.Environment == nil {
		return FileResult{}, errors.New("任务缺少执行环境")
	}
	if t.Environment.Type == "windows" {
		return readLocalWorkspace(ctx, t.Workspace, action, relative)
	}
	payload, _ := json.Marshal(map[string]string{"root": t.Workspace, "action": action, "path": relative})
	raw, truncated, err := workspaceCommand(ctx, runtimeConfig(Config{}, *t.Environment), string(payload), 46*1024*1024, "python3", "-c", workspaceReader)
	if err != nil {
		return FileResult{}, err
	}
	if truncated {
		return FileResult{}, errors.New("远端响应超过限制")
	}
	var result FileResult
	if err = json.Unmarshal(raw, &result); err != nil {
		return result, errors.New("远端返回无效内容，请检查 Python 3 环境")
	}
	if result.Error != "" {
		return result, errors.New(result.Error)
	}
	return result, nil
}
func (s *Server) workspaceRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/tasks/{id}/files", s.secure(func(w http.ResponseWriter, r *http.Request) {
		task, err := s.app.store.task(r.PathValue("id"))
		if err != nil {
			fail(w, 404, "任务不存在")
			return
		}
		relative, err := workspacePath(r.URL.Query().Get("path"))
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		action := r.URL.Query().Get("action")
		if action == "" {
			action = "list"
		}
		switch action {
		case "list", "read", "diff", "changes", "download":
		default:
			fail(w, 400, "无效文件操作")
			return
		}
		if (action == "read" || action == "download" || action == "diff") && relative == "" {
			fail(w, 400, "请选择文件")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		result, err := readWorkspace(ctx, task, action, relative)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		if action == "download" {
			data, e := base64.StdEncoding.DecodeString(result.Data)
			if e != nil {
				fail(w, 500, "下载内容无效")
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(relative)}))
			w.WriteHeader(200)
			_, _ = w.Write(data)
			return
		}
		jsonOut(w, 200, result)
	}))
}
