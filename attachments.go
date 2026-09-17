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
	"os/exec"
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

const stageAttachmentScript = `import sys,json,base64,tempfile,os
items=json.load(sys.stdin)
root=tempfile.mkdtemp(prefix='jianzuo-attachments-')
paths=[]
for item in items:
 name=item['name']
 if os.path.basename(name)!=name or name in ('.','..'): raise ValueError('invalid attachment name')
 p=os.path.join(root,name)
 fd=os.open(p,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
 with os.fdopen(fd,'wb') as out: out.write(base64.b64decode(item['data'],validate=True))
 paths.append(p)
print(json.dumps(paths))
`

func (a *App) stageAttachments(ctx context.Context, t Task, files []Attachment) ([]RuntimeAttachment, error) {
	result := []RuntimeAttachment{}
	if len(files) == 0 {
		return result, nil
	}
	var payload []map[string]string
	for _, f := range files {
		var data []byte
		if err := a.store.QueryRow("SELECT data FROM attachments WHERE id=? AND task_id=?", f.ID, t.ID).Scan(&data); err != nil {
			return nil, err
		}
		result = append(result, RuntimeAttachment{Attachment: f, Data: data})
		payload = append(payload, map[string]string{"name": attachmentFileName(f), "data": base64.StdEncoding.EncodeToString(data)})
	}
	if t.Environment.Type == "windows" {
		base := filepath.Join(filepath.Dir(a.config.path), "attachments")
		if err := os.MkdirAll(base, 0700); err != nil {
			return nil, err
		}
		dir, err := os.MkdirTemp(base, t.ID+"-")
		if err != nil {
			return nil, err
		}
		for i := range result {
			f := &result[i]
			f.Path = filepath.Join(dir, attachmentFileName(f.Attachment))
			if err = os.WriteFile(f.Path, f.Data, 0600); err != nil {
				return nil, err
			}
		}
	} else {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		raw, _ := json.Marshal(payload)
		original := environmentProbeCommand(*t.Environment, "python3", "-c", stageAttachmentScript)
		cmd := exec.CommandContext(ctx, original.Path, original.Args[1:]...)
		hideCommand(cmd)
		cmd.WaitDelay = time.Second
		cmd.Stdin = bytes.NewReader(raw)
		out := &cappedOutput{limit: 65536}
		errout := &cappedOutput{limit: 2048}
		cmd.Stdout = out
		cmd.Stderr = errout
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("附件传入执行环境失败：%s (%v)", errout.String(), err)
		}
		var paths []string
		if err := json.Unmarshal(out.Bytes(), &paths); err != nil || len(paths) != len(result) {
			return nil, errors.New("附件路径返回异常")
		}
		for i, p := range paths {
			if !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\x00\r\n") {
				return nil, errors.New("附件路径无效")
			}
			result[i].Path = p
		}
	}
	return result, nil
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
