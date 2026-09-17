package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type DirectoryChoice struct {
	Name string `json:"name"`
	Path string `json:"path"`
}
type DirectoryListing struct {
	Path      string            `json:"path"`
	Parent    string            `json:"parent"`
	Items     []DirectoryChoice `json:"items"`
	Truncated bool              `json:"truncated"`
}

const directoryScript = `import os,sys,json
p=sys.argv[1] or os.path.expanduser('~')
if not os.path.isabs(p): raise ValueError('absolute path required')
p=os.path.realpath(p)
if not os.path.isdir(p): raise ValueError('directory does not exist')
items=[];truncated=False
with os.scandir(p) as entries:
 for e in entries:
  if e.is_dir(follow_symlinks=False):
   if len(items)==500: truncated=True;break
   items.append({'name':e.name,'path':e.path})
print(json.dumps({'path':p,'parent':os.path.dirname(p),'items':sorted(items,key=lambda e:e['name'].lower()),'truncated':truncated}))
`

func browseDirectories(ctx context.Context, env Environment, p string) (DirectoryListing, error) {
	out := DirectoryListing{Items: []DirectoryChoice{}}
	if len(p) > 4096 || strings.ContainsAny(p, "\x00\r\n") {
		return out, errors.New("目录路径无效")
	}
	if env.Type != "windows" {
		if p != "" && !strings.HasPrefix(p, "/") {
			return out, errors.New("请使用 Linux 绝对路径")
		}
		ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		original := environmentProbeCommand(env, "python3", "-c", directoryScript, p)
		cmd := commandWithContext(ctx, original)
		hideCommand(cmd)
		cmd.WaitDelay = time.Second
		data := &cappedOutput{limit: 512 * 1024}
		diagnostic := &cappedOutput{limit: 2048}
		cmd.Stdout = data
		cmd.Stderr = diagnostic
		if err := cmd.Run(); err != nil {
			return out, errors.New("无法读取远端目录，请检查路径、连接及 Python 3：" + diagnostic.String())
		}
		if data.truncated {
			return out, errors.New("目录信息过大，请输入更具体的路径")
		}
		err := json.Unmarshal(data.Bytes(), &out)
		return out, err
	}
	if p == "" {
		for drive := 'A'; drive <= 'Z'; drive++ {
			path := string(drive) + ":\\"
			if info, e := os.Stat(path); e == nil && info.IsDir() {
				out.Items = append(out.Items, DirectoryChoice{Name: path, Path: path})
			}
		}
		return out, nil
	}
	if !filepath.IsAbs(p) {
		return out, errors.New("请使用 Windows 绝对路径")
	}
	p = filepath.Clean(p)
	entries, err := os.ReadDir(p)
	if err != nil {
		return out, err
	}
	out.Path = p
	out.Parent = filepath.Dir(p)
	if out.Parent == p {
		out.Parent = ""
	}
	for _, e := range entries {
		if e.IsDir() && e.Type()&os.ModeSymlink == 0 {
			if len(out.Items) == 500 {
				out.Truncated = true
				break
			}
			out.Items = append(out.Items, DirectoryChoice{Name: e.Name(), Path: filepath.Join(p, e.Name())})
		}
	}
	sort.Slice(out.Items, func(i, j int) bool { return strings.ToLower(out.Items[i].Name) < strings.ToLower(out.Items[j].Name) })
	return out, nil
}
func (s *Server) directoryRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/environments/{id}/directories", s.secure(func(w http.ResponseWriter, r *http.Request) {
		env, err := s.app.config.get().environment(r.PathValue("id"))
		if err != nil {
			fail(w, 404, err.Error())
			return
		}
		result, err := browseDirectories(r.Context(), env, r.URL.Query().Get("path"))
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		jsonOut(w, 200, result)
	}))
	m.HandleFunc("POST /api/environments/{id}/workspaces", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v struct{ Path string }
		if !body(w, r, &v) {
			return
		}
		env, err := s.app.config.get().environment(r.PathValue("id"))
		if err != nil {
			fail(w, 404, err.Error())
			return
		}
		if v.Path == "" {
			fail(w, 400, "请选择目录")
			return
		}
		listing, err := browseDirectories(r.Context(), env, v.Path)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		s.app.mu.Lock()
		defer s.app.mu.Unlock()
		config := s.app.config.get()
		found := false
		for i := range config.Environments {
			e := &config.Environments[i]
			if e.ID == env.ID {
				found = true
				exists := false
				for _, p := range e.Workspaces {
					if p == listing.Path {
						exists = true
					}
				}
				if !exists {
					if len(e.Workspaces) >= 50 {
						fail(w, 400, "常用工作区已达 50 个")
						return
					}
					e.Workspaces = append(e.Workspaces, listing.Path)
				}
			}
		}
		if !found {
			fail(w, 409, "执行环境已发生变化")
			return
		}
		if err = s.app.config.save(config); err != nil {
			fail(w, 400, err.Error())
			return
		}
		jsonOut(w, 200, map[string]string{"path": listing.Path})
	}))
}
