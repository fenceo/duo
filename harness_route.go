package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultHarnessProvider = "deepseek-official"
	defaultHarnessModel    = "deepseek-flash"
)

type harnessConfiguredRoute struct {
	Provider string
	Models   []ModelOption
}

type harnessSettings struct {
	Routes          []harnessConfiguredRoute
	Provider, Model string
}

func firstProfileEnv(values []map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

// Only project non-secret model metadata. Never return YAML decoder diagnostics:
// an invalid settings document can contain credentials in the failing scalar.
func readHarnessSettings(path string) (harnessSettings, error) {
	var out harnessSettings
	if path == "" {
		return out, nil
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, errors.New("无法读取此账号的 DSH settings.yaml")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 4*1024*1024+1))
	if err != nil || len(raw) > 4*1024*1024 {
		return out, errors.New("DSH settings.yaml 读取失败或文件过大")
	}
	var doc struct {
		Default struct {
			Provider string `yaml:"provider"`
			Model    string `yaml:"model"`
		} `yaml:"agent-default-model"`
		PI struct {
			Providers map[string]struct {
				Models []struct {
					ID      string    `yaml:"id"`
					Name    string    `yaml:"name"`
					Efforts yaml.Node `yaml:"reasoningEfforts"`
				} `yaml:"models"`
			} `yaml:"providers"`
		} `yaml:"llm-pi-ai"`
	}
	if yaml.Unmarshal(raw, &doc) != nil {
		return out, errors.New("DSH settings.yaml 格式无效，请在 Harness 中检查配置")
	}
	out.Provider, out.Model = doc.Default.Provider, doc.Default.Model
	names := make([]string, 0, len(doc.PI.Providers))
	for name := range doc.PI.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		route := harnessConfiguredRoute{Provider: name}
		seen := map[string]bool{}
		for _, m := range doc.PI.Providers[name].Models {
			id := strings.TrimSpace(m.ID)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			// Unknown metadata is not proof that every Harness effort is supported.
			// Omit the optional setting and let the native adapter use its default.
			levels := []string{}
			if m.Efforts.Kind == yaml.MappingNode {
				for i := 0; i+1 < len(m.Efforts.Content); i += 2 {
					level := m.Efforts.Content[i].Value
					if level != "" && validEngineReasoning("deepseek-harness", level) {
						levels = append(levels, level)
					}
				}
			}
			route.Models = append(route.Models, ModelOption{ID: id, Name: m.Name, Origin: "native", Engine: "deepseek-harness", ReasoningLevels: levels})
		}
		if len(route.Models) > 0 {
			out.Routes = append(out.Routes, route)
		}
	}
	return out, nil
}

func harnessSettingsPath(envType string, profileEnv map[string]string) string {
	if envType != "windows" {
		return ""
	}
	home, overridden := profileEnv["DSH_HOME"]
	if !overridden {
		home = os.Getenv("DSH_HOME")
	}
	if strings.TrimSpace(home) == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(userHome, ".dsh")
	}
	if home == "~" || strings.HasPrefix(home, "~/") || strings.HasPrefix(home, `~\`) {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(userHome, strings.TrimLeft(home[1:], `/\`))
	}
	return filepath.Join(home, "settings.yaml")
}

func harnessConfiguredDefaults(c Config) (provider, model string) {
	provider, model = strings.TrimSpace(c.HarnessProvider), strings.TrimSpace(c.HarnessModel)
	if provider == "" {
		provider = defaultHarnessProvider
	}
	if model == "" {
		model = defaultHarnessModel
	}
	return
}

func harnessAutoRoute(c Config) bool {
	provider, model := harnessConfiguredDefaults(c)
	return provider == defaultHarnessProvider && model == defaultHarnessModel
}

func harnessRouteSettings(c Config) (harnessSettings, error) {
	if c.Distro != "" || c.SSHHost != "" || !isNativeHarnessExecutable(c.Harness) {
		return harnessSettings{}, nil
	}
	return readHarnessSettings(harnessSettingsPath("windows", c.EngineEnv))
}

// Match the selected model, not just the legacy default. Otherwise picking a
// discovered model leaves the task attached to deepseek-official at runtime.
func harnessRouteForConfig(c Config, taskModel string) (provider, model string, err error) {
	provider, model = harnessConfiguredDefaults(c)
	if selected := strings.TrimSpace(taskModel); selected != "" {
		model = selected
	}
	if !harnessAutoRoute(c) {
		return
	}
	settings, err := harnessRouteSettings(c)
	if err != nil || len(settings.Routes) == 0 {
		return provider, model, err
	}
	if model == defaultHarnessModel && settings.Provider != "" && settings.Model != "" {
		for _, route := range settings.Routes {
			if route.Provider != settings.Provider {
				continue
			}
			for _, m := range route.Models {
				if m.ID == settings.Model {
					return route.Provider, m.ID, nil
				}
			}
		}
	}
	matched := ""
	for _, route := range settings.Routes {
		for _, m := range route.Models {
			if m.ID != model {
				continue
			}
			if matched != "" {
				return "", "", errors.New("此模型属于多个 Harness provider，请在环境设置中明确配置 provider 和默认模型")
			}
			matched = route.Provider
		}
	}
	if matched != "" {
		return matched, model, nil
	}
	if model == defaultHarnessModel && len(settings.Routes) == 1 && len(settings.Routes[0].Models) == 1 {
		return settings.Routes[0].Provider, settings.Routes[0].Models[0].ID, nil
	}
	return "", "", errors.New("未在此账号的 DSH 配置中找到所选模型，请重新读取模型列表并选择模型，或明确配置 Harness provider 和默认模型")
}

func configuredHarnessCatalog(env Environment, profileEnv map[string]string) (ModelList, error) {
	c := runtimeConfig(Config{}, env)
	c.EngineEnv = profileEnv
	provider, model := harnessConfiguredDefaults(c)
	list := ModelList{Models: []ModelOption{}, Source: "Duo 中该环境的 Harness 配置", Status: "fallback", Message: "仅列出本地配置；未调用模型，未知推理能力请使用工具默认。"}
	var settings harnessSettings
	var err error
	if env.Type == "windows" {
		settings, err = harnessRouteSettings(c)
	}
	if err != nil {
		return list, err
	}
	auto := harnessAutoRoute(c)
	for _, route := range settings.Routes {
		if !auto && route.Provider != provider {
			continue
		}
		list.Models = append(list.Models, route.Models...)
	}
	if len(list.Models) > 0 {
		list.Source = "DSH settings.yaml 模型配置"
		list.Status = "ready"
		list.Message = "已读取此账号的 provider/model；运行时按所选模型匹配 provider。推理强度只列出配置已声明的选项，其余使用工具默认。"
		if auto {
			model = ""
			if _, candidate, e := harnessRouteForConfig(c, ""); e == nil {
				model = candidate
			}
		}
	}
	// The generic catalog merge uses Codex's vocabulary, which excludes off.
	// Reapply the native Harness capabilities after merging model identifiers.
	capabilities := map[string][]string{}
	for _, m := range configuredEngineModels(env, "deepseek-harness") {
		levels := []string{}
		for _, level := range m.ReasoningLevels {
			if level != "" && validEngineReasoning("deepseek-harness", level) {
				levels = append(levels, level)
			}
		}
		capabilities[m.ID] = levels
	}
	for _, m := range list.Models {
		capabilities[m.ID] = m.ReasoningLevels
	}
	list = mergeEngineCatalog(list, env, "deepseek-harness", model)
	for i := range list.Models {
		levels, known := capabilities[list.Models[i].ID]
		if !known {
			levels = []string{}
		}
		list.Models[i].ReasoningLevels = levels
		validDefault := false
		for _, level := range levels {
			if level == list.Models[i].DefaultReasoning {
				validDefault = true
			}
		}
		if !validDefault {
			list.Models[i].DefaultReasoning = ""
		}
	}
	if len(list.Models) == 0 {
		list.Status = "empty"
	}
	return list, nil
}

func isNativeHarnessExecutable(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return true
	}
	base := strings.ToLower(filepath.Base(path))
	return base == "dsh" || base == "dsh.cmd" || base == "dsh.exe"
}
