package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"
)

func claudeSettingsCatalog(raw []byte) (ModelList, error) {
	v, _, err := engineAPIConfigView(Environment{}, "claude", "claude-api", map[string][]byte{"duo-api.json": raw})
	if err != nil {
		return ModelList{}, err
	}
	list := ModelList{Models: []ModelOption{}, DefaultModel: v.Model, APITarget: hash(string(raw)), APIEndpoint: v.BaseURL}
	ids := []string{v.Model}
	if v.ClaudeRoles != nil {
		ids = append(ids, v.ClaudeRoles.values()...)
	}
	for _, id := range ids {
		if validCatalogModelID(id) {
			list.Models = append(list.Models, ModelOption{ID: id, Engine: "claude", Origin: "configured"})
		}
	}
	return list, nil
}

const claudeScopedModelsScript = `import os,sys,json,pathlib,hashlib
p=pathlib.Path(sys.argv[1]);s=p.stat()
if not p.is_file() or s.st_size>1048576: raise ValueError('invalid config')
raw=p.read_bytes();d=json.loads(raw);env=d.get('env',{})
keys=['ANTHROPIC_MODEL','ANTHROPIC_DEFAULT_OPUS_MODEL','ANTHROPIC_DEFAULT_SONNET_MODEL','ANTHROPIC_DEFAULT_HAIKU_MODEL','ANTHROPIC_DEFAULT_FABLE_MODEL','CLAUDE_CODE_SUBAGENT_MODEL']
secrets=[env.get(k) for k in ['ANTHROPIC_AUTH_TOKEN','ANTHROPIC_API_KEY','CLAUDE_CODE_OAUTH_TOKEN'] if env.get(k)]
default=env.get('ANTHROPIC_MODEL',d.get('model',''));endpoint=env.get('ANTHROPIC_BASE_URL','')
for v in [default,endpoint]:
 if not isinstance(v,str) or any(x in v for x in secrets): raise ValueError('private routing')
models=[]
for k in keys:
 v=env.get(k,'')
 if not isinstance(v,str): raise ValueError('invalid model')
 if any(x in v for x in secrets): raise ValueError('private model')
 if v: models.append({'id':v,'engine':'claude','origin':'configured'})
print(json.dumps({'models':models,'default_model':default,'modified':int(s.st_mtime*1000),'api_target':hashlib.sha256(raw).hexdigest(),'api_endpoint':endpoint}))
`

func scopedClaudeCatalog(ctx context.Context, env Environment, path string) (ModelList, error) {
	list := ModelList{Models: []ModelOption{}}
	if env.Type == "windows" {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1048576 {
			return list, os.ErrInvalid
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return list, err
		}
		list, err = claudeSettingsCatalog(raw)
		if err != nil {
			return list, err
		}
		list.Modified = info.ModTime().UnixMilli()
	} else {
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		stdout, _, err := runModelEnvironmentCommand(bounded, env, "python3", "-c", claudeScopedModelsScript, path)
		if err != nil {
			return list, err
		}
		if err = json.Unmarshal(stdout, &list); err != nil {
			return list, err
		}
	}
	list = mergeEngineCatalog(list, env, "claude", list.DefaultModel)
	list.Source = "Duo 专用 Claude API / 模型角色配置"
	list.Status = "fallback"
	if len(list.Models) == 0 {
		list.Status = "empty"
	}
	list.Message = "仅读取 Duo 启动时加载的配置；未使用其他原生账号的模型列表，未调用模型验证权限。"
	return list, nil
}

func claudeScopedSettingsPath(env Environment, selected map[string]string) string {
	if p, exists := selected["DUO_CLAUDE_SETTINGS"]; exists {
		return strings.TrimSpace(p)
	}
	return env.ClaudeAPIConfig
}
