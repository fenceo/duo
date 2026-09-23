package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

var claudeModelEnvKeys = []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_CUSTOM_MODEL_OPTION"}

// Settings are only a configured-name catalog, not a replication of Claude's
// managed policy/account entitlement resolver. Never execute Claude -p to
// populate a menu, and never fabricate an available-model list from aliases.
func readClaudeConfiguredFiles(root, workspace string) ModelList {
	list := ModelList{Models: []ModelOption{}}
	files := []string{filepath.Join(root, "settings.json")}
	if workspace != "" {
		files = append(files, filepath.Join(workspace, ".claude", "settings.json"), filepath.Join(workspace, ".claude", "settings.local.json"))
	}
	add := func(id string) {
		if validCatalogModelID(id) {
			list.Models = append(list.Models, ModelOption{ID: id, Origin: "configured", Engine: "claude"})
		}
	}
	for _, path := range files {
		stat, err := os.Stat(path)
		if err != nil || !stat.Mode().IsRegular() || stat.Size() > 1024*1024 {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var value struct {
			Model  string            `json:"model"`
			Models []string          `json:"availableModels"`
			Env    map[string]string `json:"env"`
		}
		if json.Unmarshal(raw, &value) != nil {
			continue
		}
		if validCatalogModelID(value.Model) {
			list.DefaultModel = value.Model
			add(value.Model)
		}
		for _, model := range value.Models {
			add(model)
		}
		for _, key := range claudeModelEnvKeys {
			add(value.Env[key])
		}
		if stamp := stat.ModTime().UnixMilli(); stamp > list.Modified {
			list.Modified = stamp
		}
	}
	for _, key := range claudeModelEnvKeys {
		add(os.Getenv(key))
	}
	if value := os.Getenv("ANTHROPIC_MODEL"); validCatalogModelID(value) {
		list.DefaultModel = value
	}
	return list
}

const readRemoteClaudeConfiguredModels = `import os,sys,pathlib,json
root=pathlib.Path(os.environ.get('CLAUDE_CONFIG_DIR') or str(pathlib.Path.home()/'.claude')).expanduser()
files=[root/'settings.json']
if sys.argv[1]: files += [pathlib.Path(sys.argv[1])/'.claude'/'settings.json',pathlib.Path(sys.argv[1])/'.claude'/'settings.local.json']
keys=['ANTHROPIC_MODEL','ANTHROPIC_DEFAULT_MODEL','ANTHROPIC_DEFAULT_OPUS_MODEL','ANTHROPIC_DEFAULT_SONNET_MODEL','ANTHROPIC_DEFAULT_HAIKU_MODEL','ANTHROPIC_DEFAULT_FABLE_MODEL','ANTHROPIC_CUSTOM_MODEL_OPTION']
out={'models':[],'default_model':'','modified':0}
def add(value):
 if isinstance(value,str) and value.strip() and len(value)<=200 and not any(c in value for c in ['\x00','\r','\n']): out['models'].append({'id':value,'origin':'configured','engine':'claude'})
for p in files:
 try:
  if not p.is_file() or p.stat().st_size>1048576: continue
  d=json.loads(p.read_text(encoding='utf-8'))
  if not isinstance(d,dict): continue
  model=d.get('model')
  if isinstance(model,str): out['default_model']=model;add(model)
  models=d.get('availableModels')
  if isinstance(models,list):
   for model in models: add(model)
  env=d.get('env')
  if isinstance(env,dict):
   for key in keys: add(env.get(key))
  out['modified']=max(out['modified'],int(p.stat().st_mtime*1000))
 except (OSError,ValueError): pass
for key in keys: add(os.environ.get(key))
if os.environ.get('ANTHROPIC_MODEL'): out['default_model']=os.environ['ANTHROPIC_MODEL']
print(json.dumps(out,ensure_ascii=False))
`

func configuredClaudeCatalog(ctx context.Context, env Environment, profileEnv ...map[string]string) (ModelList, error) {
	var selected map[string]string
	if len(profileEnv) > 0 {
		selected = profileEnv[0]
	}
	workspace := ""
	if len(env.Workspaces) > 0 {
		workspace = env.Workspaces[0]
	}
	list := ModelList{Models: []ModelOption{}}
	if env.Type == "windows" {
		list = readClaudeConfiguredFiles(localEngineConfigRoot(selected, "CLAUDE_CONFIG_DIR", ".claude"), workspace)
	} else if env.Type == "wsl" || env.Type == "ssh" {
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		stdout, _, err := runModelEnvironmentCommand(bounded, env, withEngineEnv([]string{"python3", "-c", readRemoteClaudeConfiguredModels, workspace}, selected)...)
		if ctx.Err() != nil {
			return ModelList{}, ctx.Err()
		}
		if err != nil || json.Unmarshal(stdout, &list) != nil {
			list.Message = "未能读取目标用户的 Claude 配置；请检查环境和账号目录。 "
		}
	}
	defaultModel := env.ClaudeModel
	if defaultModel == "" {
		defaultModel = list.DefaultModel
	}
	list = mergeEngineCatalog(list, env, "claude", defaultModel)
	list.Source = "该环境的 Claude 配置模型"
	list.Status = "fallback"
	if len(list.Models) == 0 {
		list.Status = "empty"
	}
	list.Message += "仅列出该账号/目录及 Duo 中显式配置的模型，不代表已验证账号权限；组织策略和实际可用性由 Claude Code 决定。"
	return list, nil
}
