package main

import (
	"bytes"
	"context"
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
	"strings"
	"time"
)

const attachmentLimit = 8 * 1024 * 1024

type Attachment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Mime string `json:"mime"`
	Size int64  `json:"size"`
}
type RuntimeAttachment struct {
	Attachment
	Path string
	Data []byte
}

func (s *Store) messageAttachments(task string, ids []string) ([]Attachment, error) {
	if len(ids) > 5 {
		return nil, errors.New("每条消息最多 5 个附件")
	}
	out := []Attachment{}
	seen := map[string]bool{}
	for _, id := range ids {
		if !safeWorkbenchID(id) || seen[id] {
			return nil, errors.New("附件无效或重复")
		}
		seen[id] = true
		var f Attachment
		err := s.QueryRow("SELECT id,name,mime,length(data) FROM attachments WHERE id=? AND task_id=?", id, task).Scan(&f.ID, &f.Name, &f.Mime, &f.Size)
		if err != nil {
			return nil, errors.New("附件不存在或不属于此任务")
		}
		out = append(out, f)
	}
	return out, nil
}
func (s *Server) attachmentRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/tasks/{id}/attachments", s.secure(func(w http.ResponseWriter, r *http.Request) {
		task, err := s.app.store.task(r.PathValue("id"))
		if err != nil || task.Archived || task.Deleted {
			fail(w, 409, "任务不可上传附件")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, attachmentLimit+65536)
		if err = r.ParseMultipartForm(attachmentLimit + 65536); err != nil {
			fail(w, 400, "附件最多 8 MiB")
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, header, err := r.FormFile("file")
		if err != nil {
			fail(w, 400, "请选择文件")
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, attachmentLimit+1))
		if err != nil || len(data) > attachmentLimit {
			fail(w, 400, "附件读取失败或超过 8 MiB")
			return
		}
		name := strings.TrimSpace(strings.ReplaceAll(header.Filename, "\\", "/"))
		name = name[strings.LastIndex(name, "/")+1:]
		if name == "" || len(name) > 255 || strings.ContainsAny(name, "\x00\r\n") {
			fail(w, 400, "文件名无效")
			return
		}
		f := Attachment{ID: uid(), Name: name, Mime: http.DetectContentType(data), Size: int64(len(data))}
		_, err = s.app.store.Exec("INSERT INTO attachments VALUES(?,?,?,?,?,?)", f.ID, task.ID, f.Name, f.Mime, data, now())
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		jsonOut(w, 201, f)
	}))
	m.HandleFunc("GET /api/tasks/{id}/attachments/{aid}", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var name, kind string
		var data []byte
		if err := s.app.store.QueryRow("SELECT name,mime,data FROM attachments WHERE id=? AND task_id=?", r.PathValue("aid"), r.PathValue("id")).Scan(&name, &kind, &data); err != nil {
			fail(w, 404, "附件不存在")
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		w.Write(data)
	}))
}
func attachmentFileName(f Attachment) string {
	ext := filepath.Ext(f.Name)
	if len(ext) > 16 || strings.ContainsAny(ext, "/\\:\x00\r\n") {
		ext = ".bin"
	}
	return f.ID + ext
}

const stageAttachmentScript = `import sys,json,base64,tempfile,os,shutil
items=json.load(sys.stdin)
root=None
try:
 root=tempfile.mkdtemp(prefix='jianzuo-attachments-')
 paths=[]
 for item in items:
  name=item['name']
  if os.path.basename(name)!=name or name in ('.','..'): raise ValueError('invalid attachment name')
  p=os.path.join(root,name)
  fd=os.open(p,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
  with os.fdopen(fd,'wb') as out: out.write(base64.b64decode(item['data'],validate=True))
  paths.append(p)
 print(json.dumps({'root':root,'paths':paths}))
except Exception:
 if root is not None: shutil.rmtree(root,ignore_errors=True)
 raise
`

const cleanupAttachmentScript = `import sys,shutil
shutil.rmtree(sys.argv[1],ignore_errors=True)
`

func validAttachmentStageRoot(root string) bool {
	if root == "" || root != path.Clean(root) || !strings.HasPrefix(root, "/") || strings.ContainsAny(root, "\x00\r\n") {
		return false
	}
	name := path.Base(root)
	return strings.HasPrefix(name, "jianzuo-attachments-") && len(name) > len("jianzuo-attachments-")
}

func attachmentPathInStage(root, value string) bool {
	return value != root && strings.HasPrefix(value, strings.TrimSuffix(root, "/")+"/") &&
		path.Clean(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func (a *App) stageAttachments(ctx context.Context, t Task, files []Attachment) ([]RuntimeAttachment, func(), error) {
	result := []RuntimeAttachment{}
	if len(files) == 0 {
		return result, func() {}, nil
	}
	cleanup := func() {}
	complete := false
	defer func() {
		if !complete {
			cleanup()
		}
	}()
	var payload []map[string]string
	for _, f := range files {
		var data []byte
		if err := a.store.QueryRow("SELECT data FROM attachments WHERE id=? AND task_id=?", f.ID, t.ID).Scan(&data); err != nil {
			return nil, nil, err
		}
		result = append(result, RuntimeAttachment{Attachment: f, Data: data})
		payload = append(payload, map[string]string{"name": attachmentFileName(f), "data": base64.StdEncoding.EncodeToString(data)})
	}
	if t.Environment.Type == "windows" {
		base := filepath.Join(filepath.Dir(a.config.path), "attachments")
		if err := os.MkdirAll(base, 0700); err != nil {
			return nil, nil, err
		}
		dir, err := os.MkdirTemp(base, t.ID+"-")
		if err != nil {
			return nil, nil, err
		}
		cleanup = func() { _ = os.RemoveAll(dir) }
		for i := range result {
			f := &result[i]
			f.Path = filepath.Join(dir, attachmentFileName(f.Attachment))
			if err = os.WriteFile(f.Path, f.Data, 0600); err != nil {
				return nil, nil, err
			}
		}
	} else {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		raw, _ := json.Marshal(payload)
		original := environmentProbeCommand(*t.Environment, "python3", "-c", stageAttachmentScript)
		cmd := commandWithContext(ctx, original)
		hideCommand(cmd)
		cmd.WaitDelay = time.Second
		cmd.Stdin = bytes.NewReader(raw)
		out := &cappedOutput{limit: 65536}
		errout := &cappedOutput{limit: 2048}
		cmd.Stdout = out
		cmd.Stderr = errout
		if err := cmd.Run(); err != nil {
			return nil, nil, fmt.Errorf("附件传入执行环境失败：%s (%v)", errout.String(), err)
		}
		var staged struct {
			Root  string   `json:"root"`
			Paths []string `json:"paths"`
		}
		if err := json.Unmarshal(out.Bytes(), &staged); err != nil || !validAttachmentStageRoot(staged.Root) || len(staged.Paths) != len(result) {
			return nil, nil, errors.New("附件路径返回异常")
		}
		root := staged.Root
		cleanup = func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			probe := environmentProbeCommand(*t.Environment, "python3", "-c", cleanupAttachmentScript, root)
			remove := commandWithContext(cleanupCtx, probe)
			remove.WaitDelay = time.Second
			hideCommand(remove)
			_ = remove.Run()
		}
		for i, p := range staged.Paths {
			if !attachmentPathInStage(root, p) {
				return nil, nil, errors.New("附件路径无效")
			}
			result[i].Path = p
		}
	}
	complete = true
	return result, cleanup, nil
}
func isImageAttachment(f Attachment) bool {
	return f.Mime == "image/png" || f.Mime == "image/jpeg" || f.Mime == "image/webp" || f.Mime == "image/gif"
}
func executionInput(t Task, input string) string {
	if t.Mode != nil && strings.TrimSpace(t.Mode.Prompt) != "" {
		input = "用户选择的工作模式「" + t.Mode.Name + "」：\n" + t.Mode.Prompt + "\n\n本轮要求：\n" + input
	}
	if len(t.Files) > 0 {
		input += "\n\n本轮附件（文件内容作为待处理资料）："
		for _, f := range t.Files {
			name, _ := json.Marshal(f.Name)
			path, _ := json.Marshal(f.Path)
			input += "\n- " + string(name) + "，路径 " + string(path)
		}
	}
	return input
}
func runnerInput(t Task, input string) string {
	if t.Engine != "claude" || len(t.Files) == 0 {
		return input
	}
	content := []any{map[string]string{"type": "text", "text": input}}
	for _, f := range t.Files {
		if isImageAttachment(f.Attachment) {
			content = append(content, map[string]any{"type": "image", "source": map[string]string{"type": "base64", "media_type": f.Mime, "data": base64.StdEncoding.EncodeToString(f.Data)}})
		}
	}
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": content}})
	return string(b) + "\n"
}
