package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	defaultHarnessProvider = "deepseek-official"
	defaultHarnessModel    = "deepseek-flash"
)

type harnessConfiguredRoute struct {
	Provider string
	Models   []ModelOption
}

func firstProfileEnv(values []map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

// harnessSettingsRoutes reads only provider and model identifiers from DSH's
// local settings file. It deliberately does not expose or parse credentials.
// DSH currently writes providers as a YAML mapping whose provider entries are
// followed by a brace and whose model records contain an id field; this parser
// accepts that format without adding a YAML dependency to the service.
func harnessSettingsRoutes(path string) []harnessConfiguredRoute {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 || len(raw) > 4*1024*1024 {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	providerLine := -1
	providerIndent := 0
	for i, line := range lines {
		if strings.TrimSpace(strings.TrimSuffix(line, "{")) == "providers:" {
			providerLine = i
			providerIndent = len(line) - len(strings.TrimLeft(line, " \t"))
			break
		}
	}
	if providerLine < 0 {
		return nil
	}
	providerRE := regexp.MustCompile(`^\s*([A-Za-z0-9][A-Za-z0-9_.:-]*)\s*:\s*$`)
	var providers []struct {
		name  string
		start int
	}
	for i := providerLine + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "{" || trimmed == "[" || trimmed == "}" || trimmed == "]" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent <= providerIndent && strings.Contains(trimmed, ":") {
			break
		}
		match := providerRE.FindStringSubmatch(line)
		if len(match) != 2 {
			continue
		}
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
			j++
		}
		if j < len(lines) && strings.TrimSpace(lines[j]) == "{" {
			providers = append(providers, struct {
				name  string
				start int
			}{name: match[1], start: i})
		}
	}
	if len(providers) == 0 {
		return nil
	}
	idRE := regexp.MustCompile(`\bid\s*:\s*([A-Za-z0-9][A-Za-z0-9._:/-]*)`)
	routes := make([]harnessConfiguredRoute, 0, len(providers))
	for i, provider := range providers {
		end := len(lines)
		if i+1 < len(providers) {
			end = providers[i+1].start
		}
		section := strings.Join(lines[provider.start:end], "\n")
		modelStart := strings.Index(section, "models:")
		if modelStart < 0 {
			continue
		}
		section = section[modelStart:]
		seen := map[string]bool{}
		models := make([]ModelOption, 0)
		for _, match := range idRE.FindAllStringSubmatch(section, -1) {
			if len(match) != 2 || seen[match[1]] {
				continue
			}
			seen[match[1]] = true
			models = append(models, ModelOption{ID: match[1], Name: match[1], Origin: "dsh-settings"})
		}
		if len(models) > 0 {
			routes = append(routes, harnessConfiguredRoute{Provider: provider.name, Models: models})
		}
	}
	return routes
}

func harnessSettingsPath(envType string, profileEnv map[string]string) string {
	if envType != "windows" {
		return ""
	}
	home := strings.TrimSpace(profileEnv["DSH_HOME"])
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(userHome, ".dsh")
	}
	return filepath.Join(home, "settings.yaml")
}

func harnessRouteForEnvironment(env Environment, profileEnv map[string]string) (provider, model string, settings []ModelOption) {
	provider = strings.TrimSpace(env.HarnessProvider)
	model = strings.TrimSpace(env.HarnessModel)
	if provider == "" {
		provider = defaultHarnessProvider
	}
	if model == "" {
		model = defaultHarnessModel
	}
	if provider != defaultHarnessProvider || model != defaultHarnessModel {
		return provider, model, nil
	}
	path := harnessSettingsPath(env.Type, profileEnv)
	if path == "" {
		return provider, model, nil
	}
	routes := harnessSettingsRoutes(path)
	if len(routes) == 0 {
		return provider, model, nil
	}
	// The built-in route remains valid when it is explicitly present in the
	// local catalog. Otherwise use the first configured provider with models;
	// this is what makes an existing Windows DSH setup work without copying its
	// API key into Duo.
	for _, route := range routes {
		if route.Provider == provider {
			for _, candidate := range route.Models {
				if candidate.ID == model {
					return provider, model, route.Models
				}
			}
		}
	}
	route := routes[0]
	return route.Provider, route.Models[0].ID, route.Models
}

func harnessRouteForConfig(c Config, taskModel string) (provider, model string) {
	provider = strings.TrimSpace(c.HarnessProvider)
	model = strings.TrimSpace(taskModel)
	if model == "" {
		model = strings.TrimSpace(c.HarnessModel)
	}
	if provider == "" {
		provider = defaultHarnessProvider
	}
	if model == "" {
		model = defaultHarnessModel
	}
	// A task created before route discovery can contain the old built-in model.
	// Resolve that exact fallback pair from the target's DSH settings, while
	// leaving any explicit provider/model choice untouched.
	if provider != defaultHarnessProvider || model != defaultHarnessModel || c.Distro != "" || c.SSHHost != "" || !isNativeHarnessExecutable(c.Harness) {
		return provider, model
	}
	env := Environment{Type: "windows", HarnessProvider: provider, HarnessModel: model}
	provider, model, _ = harnessRouteForEnvironment(env, c.EngineEnv)
	return provider, model
}

func isNativeHarnessExecutable(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return true
	}
	base := strings.ToLower(filepath.Base(path))
	return base == "dsh" || base == "dsh.cmd" || base == "dsh.exe"
}

func harnessSettingsModelCatalog(env Environment, profileEnv map[string]string) (provider, model string, models []ModelOption) {
	provider, model, models = harnessRouteForEnvironment(env, profileEnv)
	return provider, model, models
}
