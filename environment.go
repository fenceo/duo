package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Environment struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	Distro        string   `json:"distro"`
	User          string   `json:"user"`
	Host          string   `json:"host"`
	Port          int      `json:"port"`
	Identity      string   `json:"identity"`
	Codex         string   `json:"codex"`
	Claude        string   `json:"claude"`
	ClaudeModel   string   `json:"claude_model"`
	DefaultEngine string   `json:"default_engine"`
	Model         string   `json:"model"`
	ModelCache    string   `json:"model_cache"`
	Workspaces    []string `json:"workspaces"`
}

func legacyEnvironment(c Config) Environment {
	kind, name := "windows", "本机 Windows"
	if c.Distro != "" {
		kind = "wsl"
		name = "WSL · " + c.Distro
	}
	return Environment{ID: "default", Name: name, Type: kind, Distro: c.Distro, User: c.User, Codex: c.Codex, Model: c.Model, Workspaces: c.Workspaces}
}
func windowsEnvironment() Environment {
	home, _ := os.UserHomeDir()
	binary := filepath.Join(home, "AppData", "Roaming", "npm", "node_modules", "@openai", "codex", "node_modules", "@openai", "codex-win32-x64", "vendor", "x86_64-pc-windows-msvc", "bin", "codex.exe")
	if _, e := os.Stat(binary); e != nil {
		binary = "codex.exe"
	}
	return Environment{ID: "windows", Name: "本机 Windows", Type: "windows", Codex: binary, Workspaces: []string{filepath.Join(home, "Documents", "Codex")}}
}
func normalizeEnvironments(c *Config) error {
	if len(c.Environments) == 0 {
		c.Environments = []Environment{legacyEnvironment(*c)}
		c.DefaultEnvironment = "default"
		if c.Distro != "" {
			c.Environments = append(c.Environments, windowsEnvironment())
		}
	}
	if len(c.Environments) > 30 {
		return errors.New("最多配置 30 个执行环境")
	}
	seen := map[string]bool{}
	found := false
	for i := range c.Environments {
		e := &c.Environments[i]
		if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(e.ID) || seen[e.ID] {
			return errors.New("环境 ID 无效或重复")
		}
		seen[e.ID] = true
		if e.DefaultEngine == "" {
			e.DefaultEngine = "codex"
		}
		if !validEngine(e.DefaultEngine) {
			return errors.New("AI 工具必须是 Codex 或 Claude Code")
		}
		if e.Codex == "" {
			e.Codex = "codex"
			if e.Type == "windows" {
				e.Codex = "codex.exe"
			}
		}
		if e.Claude == "" {
			e.Claude = claudeBinary(e.Type)
		}
		if strings.TrimSpace(e.Name) == "" || strings.TrimSpace(e.Codex) == "" || len(e.Workspaces) == 0 {
			return errors.New("每个环境都需要名称、Codex 程序和工作目录")
		}
		switch e.Type {
		case "windows":
			e.Distro = ""
			e.Host = ""
		case "wsl":
			e.Host = ""
			if strings.TrimSpace(e.Distro) == "" {
				return errors.New("WSL 环境需要发行版名称")
			}
		case "ssh":
			e.Distro = ""
			if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:\[\]-]*$`).MatchString(e.Host) {
				return errors.New("SSH 主机名无效")
			}
			if e.User != "" && !regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$`).MatchString(e.User) {
				return errors.New("SSH 用户名无效")
			}
			if e.Port == 0 {
				e.Port = 22
			}
			if e.Port < 1 || e.Port > 65535 {
				return errors.New("SSH 端口无效")
			}
		default:
			return errors.New("环境类型必须是 windows、wsl 或 ssh")
		}
		for _, path := range e.Workspaces {
			if e.Type == "windows" {
				if !filepath.IsAbs(path) {
					return errors.New("Windows 工作目录需要绝对路径")
				}
			} else if !strings.HasPrefix(path, "/") {
				return errors.New("WSL/SSH 工作目录需要 Linux 绝对路径")
			}
		}
		if e.ID == c.DefaultEnvironment {
			found = true
			c.Distro = e.Distro
			c.User = e.User
			c.Codex = e.Codex
			c.Workspaces = append([]string{}, e.Workspaces...)
			c.Model = e.Model
		}
	}
	if !found {
		return errors.New("请选择存在的默认执行环境")
	}
	return nil
}
func (c Config) environment(id string) (Environment, error) {
	if id == "" {
		id = c.DefaultEnvironment
	}
	for _, e := range c.Environments {
		if e.ID == id {
			return e, nil
		}
	}
	return Environment{}, errors.New("执行环境不存在，请重新选择")
}
func runtimeConfig(c Config, e Environment) Config {
	c.Distro = e.Distro
	c.User = e.User
	c.Codex = e.Codex
	c.Claude = e.Claude
	if c.Claude == "" {
		c.Claude = claudeBinary(e.Type)
	}
	c.Model = e.Model
	c.Workspaces = e.Workspaces
	c.SSHHost = ""
	c.SSHPort = 0
	c.SSHKey = ""
	if e.Type == "ssh" {
		c.SSHHost = e.Host
		c.SSHPort = e.Port
		c.SSHKey = e.Identity
		c.Distro = ""
	}
	if e.Type == "windows" {
		c.Distro = ""
	}
	c.Feishu = FeishuConfig{}
	return c
}
func (s *Store) pinLegacyEnvironment(c Config) error {
	e, err := c.environment("")
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(e)
	_, err = s.Exec("INSERT INTO task_environments(task_id,environment) SELECT id,? FROM tasks WHERE id NOT IN (SELECT task_id FROM task_environments)", string(raw))
	return err
}
func posixQuote(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'" }
func sshCommand(c Config, args ...string) *exec.Cmd {
	base := []string{"-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=8", "-o", "ServerAliveInterval=10", "-o", "ServerAliveCountMax=2"}
	if c.SSHPort != 0 {
		base = append(base, "-p", fmt.Sprint(c.SSHPort))
	}
	if c.SSHKey != "" {
		base = append(base, "-i", c.SSHKey)
	}
	dest := c.SSHHost
	if c.User != "" {
		dest = c.User + "@" + dest
	}
	quoted := make([]string, len(args))
	for i, v := range args {
		quoted[i] = posixQuote(v)
	}
	return exec.Command("ssh.exe", append(base, "--", dest, "exec "+strings.Join(quoted, " "))...)
}

type ModelOption struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	ReasoningLevels  []string `json:"reasoning_levels"`
	DefaultReasoning string   `json:"default_reasoning"`
}

func validReasoning(value string) bool {
	switch value {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
		return true
	}
	return false
}

type ModelList struct {
	Models   []ModelOption `json:"models"`
	Source   string        `json:"source"`
	Modified int64         `json:"modified"`
	Message  string        `json:"message,omitempty"`
}

func parseModels(raw []byte) ([]ModelOption, error) {
	var cache struct {
		Models []struct {
			Slug       string `json:"slug"`
			Name       string `json:"display_name"`
			Visibility string `json:"visibility"`
			Reasoning  []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
			DefaultReasoning string `json:"default_reasoning_level"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &cache); err != nil {
		return nil, errors.New("模型缓存格式无效")
	}
	out := []ModelOption{}
	for _, m := range cache.Models {
		if m.Slug != "" && (m.Visibility == "list" || m.Visibility == "") {
			name := m.Name
			if name == "" {
				name = m.Slug
			}
			var levels []string
			if m.Reasoning != nil {
				levels = []string{}
			}
			for _, level := range m.Reasoning {
				if level.Effort != "" && validReasoning(level.Effort) {
					levels = append(levels, level.Effort)
				}
			}
			out = append(out, ModelOption{ID: m.Slug, Name: name, ReasoningLevels: levels, DefaultReasoning: m.DefaultReasoning})
		}
	}
	return out, nil
}

const readRemoteModels = `import pathlib,sys,json
import os,re
allowed={'none','minimal','low','medium','high','xhigh','max','ultra'}
root=pathlib.Path.home()/'.codex'
cands=[sys.argv[1]] if sys.argv[1] else []
try:
 cfg=root/'config.toml'
 if cfg.is_file():
  match=re.search(r'(?m)^\s*model_catalog_json\s*=\s*["\']([^"\']+)["\']',cfg.read_text(encoding='utf-8',errors='replace'))
  if match: cands.append(match.group(1) if os.path.isabs(match.group(1)) else str(root/match.group(1)))
except OSError: pass
cands+= [str(root/'models_cache.json'),str(root/'model_catalog.json'),str(root/'cockpit-model-catalog.json')]
models=[];used=[];seen=set();modified=0
for c in cands:
 p=pathlib.Path(c).expanduser()
 try:
  if not p.is_file() or p.stat().st_size>4194304: continue
  d=json.loads(p.read_text(encoding='utf-8'))
 except (OSError,ValueError): continue
 found=0
 for m in d.get('models',[]):
  slug=m.get('slug')
  if not slug or slug in seen or m.get('visibility','list') not in ('list',''): continue
  levels=m.get('supported_reasoning_levels')
  seen.add(slug);found+=1
  models.append({'id':slug,'name':m.get('display_name') or slug,'reasoning_levels':[r['effort'] for r in levels if isinstance(r,dict) and r.get('effort') in allowed] if isinstance(levels,list) else None,'default_reasoning':m.get('default_reasoning_level','')})
 if found: used.append(p.name)
 modified=max(modified,int(p.stat().st_mtime*1000))
out={'models':models,'modified':modified}
if models: out['source']='该环境的 Codex 模型目录 · '+' + '.join(used)
else: out['message']='此环境还没有模型缓存，请先登录并运行一次 Codex。'
print(json.dumps(out,ensure_ascii=False))
`

// Codex keeps its model list in models_cache.json, but a custom
// model_catalog_json (cockpit-style setups, third-party providers) replaces
// that file. Read every known location so those models show up too.
var modelCacheFileNames = []string{"models_cache.json", "model_catalog.json", "cockpit-model-catalog.json"}

func codexHome() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

func catalogFromConfig(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > 1024*1024 {
		return ""
	}
	match := regexp.MustCompile(`(?m)^\s*model_catalog_json\s*=\s*["']([^"']+)["']`).FindSubmatch(raw)
	if match == nil {
		return ""
	}
	return string(match[1])
}

func modelCacheCandidates(explicit string) []string {
	home := codexHome()
	out := []string{}
	add := func(path string) {
		if strings.TrimSpace(path) == "" {
			return
		}
		clean := filepath.Clean(path)
		for _, seen := range out {
			if seen == clean {
				return
			}
		}
		out = append(out, clean)
	}
	if catalog := catalogFromConfig(filepath.Join(home, "config.toml")); catalog != "" {
		if filepath.IsAbs(catalog) {
			add(catalog)
		} else {
			add(filepath.Join(home, catalog))
		}
	}
	add(explicit)
	for _, name := range modelCacheFileNames {
		add(filepath.Join(home, name))
	}
	return out
}

// readModelCaches merges every readable catalog. A model keeps the position and
// name it has in the file that lists it first.
func readModelCaches(paths []string) ([]ModelOption, []string, int64, string) {
	models, used := []ModelOption{}, []string{}
	seen := map[string]bool{}
	var modified int64
	note := ""
	for _, path := range paths {
		stat, err := os.Stat(path)
		if err != nil || stat.IsDir() {
			continue
		}
		if stat.Size() > 4*1024*1024 {
			note = "部分模型目录过大，已跳过"
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		list, err := parseModels(raw)
		if err != nil {
			continue
		}
		found := 0
		for _, m := range list {
			if seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			found++
			models = append(models, m)
		}
		if found > 0 {
			used = append(used, filepath.Base(path))
		}
		if stamp := stat.ModTime().UnixMilli(); stamp > modified {
			modified = stamp
		}
	}
	return models, used, modified, note
}

func modelsForEnvironment(ctx context.Context, e Environment) (ModelList, error) {
	out := ModelList{Models: []ModelOption{}, Source: "Codex 本地模型缓存"}
	if e.Type == "windows" {
		candidates := modelCacheCandidates(e.ModelCache)
		models, used, modified, note := readModelCaches(candidates)
		out.Models, out.Modified = models, modified
		if len(models) > 0 {
			out.Source = "Codex 模型目录 · " + strings.Join(used, " + ")
		} else {
			out.Message = "没有读到模型目录，请先在该环境登录并运行一次 Codex。" + note
		}
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := command(runtimeConfig(Config{}, e), "python3", "-c", readRemoteModels, e.ModelCache)
	bounded := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	hideCommand(bounded)
	raw, err := bounded.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("读取该环境模型失败：%s", strings.TrimSpace(string(raw)))
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, errors.New("环境返回的模型数据无效，请检查 Python 3 和 SSH 登录输出")
	}
	if out.Source == "" {
		out.Source = "该环境的 Codex 模型缓存"
	}
	return out, nil
}
