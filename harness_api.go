package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const harnessAPIProvider = "duo-api"
const harnessAPIKeyRef = "DUO_HARNESS_API_KEY"

type HarnessAPIRequest struct {
	Target  string        `json:"target"`
	API     string        `json:"api"`
	BaseURL string        `json:"base_url"`
	Model   string        `json:"model"`
	APIKey  string        `json:"api_key"`
	Input   []string      `json:"input,omitempty"`
	Models  []ModelOption `json:"-"`
}

// Only this projection crosses HTTP. Neither native files nor a masked key
// are returned; a boolean is sufficient for editing without exposing a key.
type HarnessAPIView struct {
	EnvironmentID   string              `json:"environment_id"`
	Target          string              `json:"target"`
	API             string              `json:"api"`
	BaseURL         string              `json:"base_url"`
	Model           string              `json:"model"`
	KeyConfigured   bool                `json:"key_configured"`
	Message         string              `json:"message,omitempty"`
	Models          []ModelOption       `json:"models"`
	ModelInputs     map[string][]string `json:"model_inputs,omitempty"`
	DiscoveryStatus string              `json:"discovery_status,omitempty"`
}

func harnessAPITarget(env Environment) string {
	raw, _ := json.Marshal(env) // Environment contains no credential values.
	return hash(string(raw))
}

func validateHarnessAPIInput(input []string) bool {
	if len(input) < 1 || len(input) > 2 {
		return false
	}
	seen := map[string]bool{}
	for _, value := range input {
		if seen[value] || value != "text" && value != "image" {
			return false
		}
		seen[value] = true
	}
	return seen["text"]
}

func validateHarnessAPI(v HarnessAPIRequest) error {
	if v.Input != nil && !validateHarnessAPIInput(v.Input) {
		return errors.New("模型输入能力仅支持文字或文字和图片")
	}
	if v.API != "openai-completions" && v.API != "openai-responses" && v.API != "anthropic-messages" {
		return errors.New("请选择支持的 API 协议")
	}
	if len(v.BaseURL) > 2000 || strings.ContainsAny(v.BaseURL, "\r\n\x00") {
		return errors.New("API 地址无效")
	}
	u, err := url.Parse(v.BaseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))) {
		return errors.New("API 地址需要 HTTPS；本机服务可使用回环 HTTP 地址，请填写 API 根地址")
	}
	if strings.TrimSpace(v.Model) == "" || len(v.Model) > 120 || strings.ContainsAny(v.Model, "\r\n\x00") {
		return errors.New("请填写有效的模型 ID")
	}
	if len(v.APIKey) > 16000 || strings.ContainsAny(v.APIKey, "\r\n\x00") || v.APIKey != strings.TrimSpace(v.APIKey) {
		return errors.New("API key 无效，请检查空格或换行")
	}
	return nil
}

func harnessYAML(raw []byte, kind yaml.Kind) (*yaml.Node, error) {
	invalid := errors.New("Harness 原生配置格式无效；未覆盖文件，请先在目标环境检查配置")
	if len(raw) > maxCodexAuthBytes {
		return nil, invalid
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return &yaml.Node{Kind: kind}, nil
	}
	var doc yaml.Node
	d := yaml.NewDecoder(bytes.NewReader(raw))
	if d.Decode(&doc) != nil || len(doc.Content) != 1 || doc.Content[0].Kind != kind {
		return nil, invalid
	}
	var extra yaml.Node
	if d.Decode(&extra) != io.EOF {
		return nil, invalid
	}
	var check func(*yaml.Node) bool
	check = func(n *yaml.Node) bool {
		if n.Kind == yaml.MappingNode {
			seen := map[string]bool{}
			for i := 0; i+1 < len(n.Content); i += 2 {
				key := n.Content[i]
				if key.Kind != yaml.ScalarNode || seen[key.Value] {
					return false
				}
				seen[key.Value] = true
			}
		}
		for _, c := range n.Content {
			if !check(c) {
				return false
			}
		}
		return true
	}
	if !check(doc.Content[0]) {
		return nil, invalid
	}
	return doc.Content[0], nil
}

func harnessField(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func harnessSet(n *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			old := n.Content[i+1]
			value.HeadComment, value.LineComment, value.FootComment = old.HeadComment, old.LineComment, old.FootComment
			n.Content[i+1] = value
			return
		}
	}
	n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func harnessString(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}
func harnessValue(n *yaml.Node) string {
	if n != nil && n.Kind == yaml.ScalarNode && n.Tag == "!!str" {
		return n.Value
	}
	return ""
}

func harnessMap(n *yaml.Node, key string) (*yaml.Node, error) {
	v := harnessField(n, key)
	if v == nil {
		v = &yaml.Node{Kind: yaml.MappingNode}
		harnessSet(n, key, v)
	}
	if v.Kind != yaml.MappingNode || harnessField(v, "<<") != nil {
		return nil, errors.New("Harness 配置使用了无法安全合并的结构，未覆盖原文件")
	}
	return v, nil
}

func harnessPatchRow(patch *yaml.Node, id string, create bool) (*yaml.Node, error) {
	var found *yaml.Node
	for _, row := range patch.Content {
		if harnessValue(harnessField(row, "id")) != id {
			continue
		}
		if found != nil {
			return nil, errors.New("Harness 配置含有重复条目，未覆盖原文件")
		}
		found = row
	}
	if found == nil && create {
		found = &yaml.Node{Kind: yaml.MappingNode}
		harnessSet(found, "id", harnessString(id))
		patch.Content = append(patch.Content, found)
	}
	return found, nil
}

func harnessAPIDocuments(files map[string][]byte) (*yaml.Node, *yaml.Node, error) {
	patch, err := harnessYAML(files["cordis.patch.yml"], yaml.SequenceNode)
	if err != nil {
		return nil, nil, err
	}
	for _, row := range patch.Content {
		if row.Kind != yaml.MappingNode {
			return nil, nil, errors.New("Harness 原生补丁格式无效，未覆盖原文件")
		}
	}
	store, err := harnessPatchRow(patch, "credentials-local", false)
	if err != nil {
		return nil, nil, err
	}
	if store != nil {
		cfg := harnessField(store, "config")
		if disabled := harnessField(store, "disabled"); disabled != nil && disabled.Value == "true" || harnessField(cfg, "path") != nil || harnessField(cfg, "dshHome") != nil {
			return nil, nil, errors.New("Harness 使用了自定义凭据存储，请在 Harness 中配置该存储；此入口只管理目标用户的默认原生目录")
		}
	}
	creds, err := harnessYAML(files[".credentials.yaml"], yaml.MappingNode)
	if err != nil {
		return nil, nil, err
	}
	if len(creds.Content) > 0 {
		version := harnessField(creds, "version")
		if version == nil || version.Tag != "!!int" || version.Value != "1" {
			return nil, nil, errors.New("Harness 凭据文件不是 version 1，未覆盖文件；请先升级并打开一次 Harness")
		}
		for i := 0; i+1 < len(creds.Content); i += 2 {
			if k := creds.Content[i].Value; k != "version" && k != "refs" && k != "records" {
				return nil, nil, errors.New("Harness 凭据文件包含未知结构，未覆盖原文件")
			}
		}
	}
	refs := harnessField(creds, "refs")
	if refs != nil && (refs.Kind != yaml.MappingNode || harnessField(refs, "<<") != nil) {
		return nil, nil, errors.New("Harness 凭据引用格式无效，未覆盖原文件")
	}
	return patch, creds, nil
}

func harnessAPIView(env Environment, files map[string][]byte) (HarnessAPIView, error) {
	v := HarnessAPIView{EnvironmentID: env.ID, Target: harnessAPITarget(env), API: "openai-completions", BaseURL: "https://api.deepseek.com", Model: "deepseek-chat"}
	patch, creds, err := harnessAPIDocuments(files)
	if err != nil {
		return v, err
	}
	row, err := harnessPatchRow(patch, "llm-pi-ai", false)
	if err != nil {
		return v, err
	}
	provider := harnessField(harnessField(harnessField(row, "config"), "providers"), harnessAPIProvider)
	if provider != nil {
		v.API = harnessValue(harnessField(provider, "api"))
		v.BaseURL = harnessValue(harnessField(provider, "baseURL"))
		if models := harnessField(provider, "models"); models != nil && models.Kind == yaml.SequenceNode && len(models.Content) > 0 {
			v.Model = harnessValue(harnessField(models.Content[0], "id"))
			v.ModelInputs = map[string][]string{}
			for _, model := range models.Content {
				input := harnessField(model, "input")
				if input == nil || input.Kind != yaml.SequenceNode {
					continue
				}
				var values []string
				if input.Decode(&values) == nil && validateHarnessAPIInput(values) {
					v.ModelInputs[harnessValue(harnessField(model, "id"))] = values
				}
			}
		}
		def, _ := harnessPatchRow(patch, "agent-default-model", false)
		if cfg := harnessField(def, "config"); harnessValue(harnessField(cfg, "provider")) == harnessAPIProvider {
			v.Model = harnessValue(harnessField(cfg, "model"))
		}
		if err := validateHarnessAPI(HarnessAPIRequest{API: v.API, BaseURL: v.BaseURL, Model: v.Model}); err != nil {
			return HarnessAPIView{}, errors.New("已保存的 Duo API 配置无效，请先在 Harness 中检查配置")
		}
	}
	v.KeyConfigured = harnessValue(harnessField(harnessField(creds, "refs"), harnessAPIKeyRef)) != ""
	v.Models = []ModelOption{}
	if settings, e := parseHarnessModelFiles(map[string][]byte{"cordis.patch.yml": files["cordis.patch.yml"]}); e == nil {
		for _, route := range settings.Routes {
			if route.Provider == harnessAPIProvider {
				v.Models = route.Models
			}
		}
	}
	return v, nil
}

func mergeHarnessAPI(files map[string][]byte, v HarnessAPIRequest) (map[string][]byte, error) {
	if err := validateHarnessAPI(v); err != nil {
		return nil, err
	}
	patch, creds, err := harnessAPIDocuments(files)
	if err != nil {
		return nil, err
	}
	refs, err := harnessMap(creds, "refs")
	if err != nil {
		return nil, err
	}
	if v.APIKey == "" && harnessValue(harnessField(refs, harnessAPIKeyRef)) == "" {
		return nil, errors.New("首次配置请填写 API key")
	}
	if v.APIKey != "" {
		harnessSet(refs, harnessAPIKeyRef, harnessString(v.APIKey))
	}
	harnessSet(creds, "version", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"})
	row, err := harnessPatchRow(patch, "llm-pi-ai", true)
	if err != nil {
		return nil, err
	}
	cfg, err := harnessMap(row, "config")
	if err != nil {
		return nil, err
	}
	providers, err := harnessMap(cfg, "providers")
	if err != nil {
		return nil, err
	}
	provider, err := harnessMap(providers, harnessAPIProvider)
	if err != nil {
		return nil, err
	}
	// Refresh only Duo's provider. Preserve metadata for retained IDs, and never
	// carry models from an old API endpoint into a different service.
	oldModels := harnessField(provider, "models")
	endpointChanged := harnessValue(harnessField(provider, "baseURL")) != strings.TrimRight(v.BaseURL, "/") || harnessValue(harnessField(provider, "api")) != v.API
	if v.Models != nil || endpointChanged {
		nextModels := &yaml.Node{Kind: yaml.SequenceNode}
		for _, m := range v.Models {
			var node *yaml.Node
			if !endpointChanged && oldModels != nil && oldModels.Kind == yaml.SequenceNode {
				for _, old := range oldModels.Content {
					if harnessValue(harnessField(old, "id")) == m.ID {
						node = old
						break
					}
				}
			}
			if node == nil {
				node = &yaml.Node{Kind: yaml.MappingNode}
				harnessSet(node, "id", harnessString(m.ID))
			}
			harnessSet(node, "name", harnessString(m.Name))
			nextModels.Content = append(nextModels.Content, node)
		}
		harnessSet(provider, "models", nextModels)
	}
	harnessSet(provider, "api", harnessString(v.API))
	harnessSet(provider, "baseURL", harnessString(strings.TrimRight(v.BaseURL, "/")))
	harnessSet(provider, "apiKeyEnv", harnessString(harnessAPIKeyRef))
	harnessSet(provider, "displayName", harnessString("Duo API"))
	models := harnessField(provider, "models")
	if models == nil {
		models = &yaml.Node{Kind: yaml.SequenceNode}
		harnessSet(provider, "models", models)
	}
	if models.Kind != yaml.SequenceNode {
		return nil, errors.New("Harness 模型列表结构无效，未覆盖原文件")
	}
	var selected *yaml.Node
	for _, m := range models.Content {
		if harnessValue(harnessField(m, "id")) == v.Model {
			selected = m
		}
	}
	if selected == nil {
		m := &yaml.Node{Kind: yaml.MappingNode}
		harnessSet(m, "id", harnessString(v.Model))
		models.Content = append(models.Content, m)
		selected = m
	}
	// Apply only to the selected model. Older clients omit this field, retaining
	// native declarations and other model metadata at the same endpoint.
	if v.Input != nil {
		input := &yaml.Node{Kind: yaml.SequenceNode}
		for _, value := range v.Input {
			input.Content = append(input.Content, harnessString(value))
		}
		harnessSet(selected, "input", input)
	}
	harnessSet(row, "disabled", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"})
	def, err := harnessPatchRow(patch, "agent-default-model", true)
	if err != nil {
		return nil, err
	}
	defaultCfg, err := harnessMap(def, "config")
	if err != nil {
		return nil, err
	}
	harnessSet(defaultCfg, "provider", harnessString(harnessAPIProvider))
	harnessSet(defaultCfg, "model", harnessString(v.Model))
	encodedPatch, err := yaml.Marshal(patch)
	if err != nil {
		return nil, errors.New("无法生成 Harness 配置")
	}
	encodedCreds, err := yaml.Marshal(creds)
	if err != nil {
		return nil, errors.New("无法生成 Harness 凭据文件")
	}
	return map[string][]byte{"cordis.patch.yml": encodedPatch, ".credentials.yaml": encodedCreds}, nil
}

func (s *Server) harnessAPIRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/environments/{id}/harness-api/models", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v HarnessAPIRequest
		if !body(w, r, &v) {
			return
		}
		// Listing does not require choosing a model beforehand.
		v.Model = "model-list-only"
		if err := validateHarnessAPI(v); err != nil {
			fail(w, 400, err.Error())
			return
		}
		a := s.app
		env, err := a.config.get().environment(r.PathValue("id"))
		if err != nil || v.Target != harnessAPITarget(env) {
			fail(w, 409, "环境配置已变化，请关闭窗口后重新打开")
			return
		}
		if !s.admitModelCatalog(r.Context()) {
			fail(w, 409, "已有模型读取、测试或更新正在进行，请稍后重试")
			return
		}
		defer s.modelProbeMu.Unlock()
		if a.updating.Load() {
			fail(w, 409, errUpdateBusy.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if v.APIKey == "" {
			files, e := a.engineSetup.harnessRead(ctx, env, "", "deepseek-harness")
			if e != nil {
				fail(w, 400, "无法读取目标环境已保存的 Key，请检查连接和文件权限")
				return
			}
			v.APIKey, err = harnessRequestKey(v, files)
			if err != nil {
				fail(w, 400, err.Error())
				return
			}
		}
		if err := validateHarnessAPI(v); err != nil {
			fail(w, 400, err.Error())
			return
		}
		out := a.engineSetup.harnessDiscover(ctx, env, v)
		current, e := a.config.get().environment(env.ID)
		if e != nil || harnessAPITarget(current) != v.Target {
			fail(w, 409, "环境配置已变化，请重新打开后读取模型")
			return
		}
		jsonOut(w, 200, out)
	}))
	m.HandleFunc("GET /api/environments/{id}/harness-api", s.secure(func(w http.ResponseWriter, r *http.Request) {
		env, err := s.app.config.get().environment(r.PathValue("id"))
		if err != nil {
			fail(w, 404, "执行环境不存在")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		files, err := s.app.engineSetup.harnessRead(ctx, env, "", "deepseek-harness")
		if err != nil {
			fail(w, 400, "无法读取目标环境的 Harness 配置，请检查连接、Python 3 和文件权限")
			return
		}
		v, err := harnessAPIView(env, files)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		jsonOut(w, 200, v)
	}))
	m.HandleFunc("PUT /api/environments/{id}/harness-api", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v HarnessAPIRequest
		if !body(w, r, &v) {
			return
		}
		if err := validateHarnessAPI(v); err != nil {
			fail(w, 400, err.Error())
			return
		}
		a := s.app
		a.mu.Lock()
		if a.updating.Load() || a.ctx.Err() != nil {
			a.mu.Unlock()
			fail(w, 409, errUpdateBusy.Error())
			return
		}
		env, err := a.config.get().environment(r.PathValue("id"))
		if err != nil || v.Target != harnessAPITarget(env) {
			a.mu.Unlock()
			fail(w, 409, "环境配置已变化，请关闭窗口后重新打开")
			return
		}
		manager := a.engineSetup
		manager.mu.Lock()
		if manager.active {
			manager.mu.Unlock()
			a.mu.Unlock()
			fail(w, 409, "其他安装、登录或配置操作正在进行，请稍后重试")
			return
		}
		manager.active = true
		manager.mu.Unlock()
		a.wg.Add(1)
		a.mu.Unlock()
		defer a.wg.Done()
		defer func() { manager.mu.Lock(); manager.active = false; manager.mu.Unlock() }()
		// Once admitted, finish independently of a browser disconnect. The
		// maintenance gate protects the file/config transaction until completion.
		ctx, cancel := context.WithTimeout(a.ctx, 50*time.Second)
		defer cancel()
		old, err := manager.harnessRead(ctx, env, "", "deepseek-harness")
		if err != nil {
			fail(w, 400, "无法读取目标环境的 Harness 配置，请检查连接、Python 3 和文件权限")
			return
		}
		key, err := harnessRequestKey(v, old)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		probe := v
		probe.APIKey = key
		if err := validateHarnessAPI(probe); err != nil {
			fail(w, 400, err.Error())
			return
		}
		if !s.admitModelCatalog(ctx) {
			fail(w, 409, "已有模型读取或测试正在进行，请稍后重试")
			return
		}
		discovery := manager.harnessDiscover(ctx, env, probe)
		s.modelProbeMu.Unlock()
		probe.APIKey = ""
		if discovery.Status == "failed" {
			fail(w, 400, discovery.Message+"；配置未保存")
			return
		}
		if discovery.Status == "ready" {
			v.Models = discovery.Models
			listed := false
			for _, model := range discovery.Models {
				if model.ID == v.Model {
					listed = true
					break
				}
			}
			if !listed {
				discovery.Message += " 所填默认模型未出现在列表中，作为手动模型保留；其可用性尚未验证。"
			}
		}
		next, err := mergeHarnessAPI(old, v)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		c := a.config.get()
		current, err := c.environment(env.ID)
		if err != nil || harnessAPITarget(current) != v.Target {
			fail(w, 409, "环境配置已变化，未写入旧目标，请重新打开配置窗口")
			return
		}
		if err := manager.harnessWrite(ctx, env, "", "deepseek-harness", old, next); err != nil {
			fail(w, 400, "Harness 配置保存失败，请检查目标用户权限、连接或原生配置是否正在修改；请重新打开核对，备份保留在目标配置目录")
			return
		}
		for i := range c.Environments {
			if c.Environments[i].ID == env.ID {
				c.Environments[i].HarnessProvider = harnessAPIProvider
				c.Environments[i].HarnessModel = v.Model
			}
		}
		if err := a.config.save(c); err != nil {
			rollbackCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
			defer stop()
			if manager.harnessWrite(rollbackCtx, env, "", "deepseek-harness", next, old) != nil {
				fail(w, 500, "原生 API 配置已写入，但 Duo 环境保存失败且回退未确认；请检查目标目录备份后重新配置")
				return
			}
			fail(w, 500, "Duo 环境保存失败，已恢复原生配置，请重试")
			return
		}
		current, _ = c.environment(env.ID)
		out, _ := harnessAPIView(current, next)
		out.DiscoveryStatus = discovery.Status
		out.Message = "API 配置已保存，原文件已备份。" + discovery.Message + " 新建任务可读取该环境已保存的模型；已运行的 Harness 可能需要重新打开。"
		a.changed()
		jsonOut(w, 200, out)
	}))
}
