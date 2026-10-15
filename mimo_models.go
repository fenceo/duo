package main

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var mimoModelIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:@+\-]+/[A-Za-z0-9_./:@+\-]+$`)

func parseMimoModels(stdout string) []ModelOption {
	models := []ModelOption{}
	seen := map[string]bool{}
	if len(stdout) > 2*1024*1024 {
		return models
	}
	for _, line := range strings.Split(strings.ReplaceAll(stdout, "\r\n", "\n"), "\n") {
		id := strings.TrimSpace(line)
		// MiMo 0.1.15 appends a human-readable context-window summary.
		id, _, _ = strings.Cut(id, " — window ")
		if len(id) <= 200 && mimoModelIDPattern.MatchString(id) && !seen[id] && !mimoSpeechModel(id) {
			seen[id] = true
			models = append(models, ModelOption{ID: id, Engine: "mimo", Origin: "native"})
		}
	}
	return models
}

func configuredMimoCatalog(ctx context.Context, env Environment, selected map[string]string) (ModelList, error) {
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := mimoModelsCommand(bounded, env, selected)
	stdout, stderr := &cappedOutput{limit: 2 * 1024 * 1024}, &cappedOutput{limit: 4096}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = time.Second
	hideCommand(cmd)
	err := cmd.Run()
	if ctx.Err() != nil {
		return ModelList{}, ctx.Err()
	}
	list := ModelList{Models: []ModelOption{}, Source: "MiMo 原生 CLI 模型目录", Status: "empty"}
	if err != nil {
		list.Message = "无法读取目标环境的 MiMo CLI 模型目录，请先安装当前官方 CLI 并配置 API。"
	} else {
		list.Models = parseMimoModels(string(stdout.Bytes()))
		if env.MimoConfig != "" || selected["MIMOCODE_CONFIG"] != "" {
			filtered := []ModelOption{}
			for _, model := range list.Models {
				if strings.HasPrefix(model.ID, "duo-mimo/") {
					filtered = append(filtered, model)
				}
			}
			list.Models = filtered
			list.Message = "使用所选环境的 Duo API 配置；未配置账号的内置服务不列入选项。"
		}
		if len(list.Models) > 0 {
			list.Status = "ready"
		}
	}
	list = mergeEngineCatalog(list, env, "mimo", env.MimoModel)
	chatModels := []ModelOption{}
	for _, model := range list.Models {
		if !mimoSpeechModel(model.ID) {
			chatModels = append(chatModels, model)
		}
	}
	list.Models = chatModels
	if mimoSpeechModel(list.DefaultModel) {
		list.DefaultModel = ""
	}
	if len(list.Models) > 0 && list.Status == "empty" {
		list.Status = "fallback"
	}
	list.Message += "目录来自原生 CLI 和显式配置；列出模型不代表已验证推理请求。"
	return list, nil
}

func mimoModelsCommand(ctx context.Context, env Environment, selected map[string]string) *exec.Cmd {
	path := env.Mimo
	if path == "" {
		path = "mimo"
	}
	variables := map[string]string{"MIMOCODE_DISABLE_CLAUDE_CODE": "1", "MIMOCODE_DISABLE_AUTOUPDATE": "1"}
	for k, v := range selected {
		variables[k] = v
	}
	workspace := ""
	if len(env.Workspaces) > 0 {
		workspace = env.Workspaces[0]
	}
	if env.Type == "windows" {
		cmd := exec.CommandContext(ctx, path, "models")
		cmd.Dir = workspace
		applyNativeEnv(cmd, variables)
		return cmd
	}
	args := withEngineEnv([]string{path, "models"}, variables)
	quoted := []string{}
	for _, arg := range args {
		quoted = append(quoted, posixQuote(arg))
	}
	script := "exec " + strings.Join(quoted, " ")
	if workspace != "" {
		script = "cd -- " + posixQuote(workspace) + " && " + script
	}
	return commandWithContext(ctx, environmentProbeCommand(env, "sh", "-lc", script))
}
