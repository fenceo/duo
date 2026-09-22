package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	modelProbeMaxModels  = 24
	modelProbeTimeout    = 25 * time.Second
	modelProbeRequestMax = 12 * time.Minute
)

const modelProbePrompt = "Reply with exactly OK. Do not read files, call tools, use the network, or modify anything."

type ModelProbeRequest struct {
	Engine    string   `json:"engine"`
	Workspace string   `json:"workspace"`
	Models    []string `json:"models"`
}

type ModelProbeResult struct {
	Model      string `json:"model"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	DurationMS int64  `json:"duration_ms"`
}

type ModelProbeResponse struct {
	Engine    string             `json:"engine"`
	Workspace string             `json:"workspace"`
	Results   []ModelProbeResult `json:"results"`
}

func normalizeProbeModels(models []string) ([]string, error) {
	out := make([]string, 0, len(models))
	seen := map[string]bool{}
	for _, raw := range models {
		model := strings.TrimSpace(raw)
		if model == "" {
			continue
		}
		if len(model) > 120 || strings.ContainsAny(model, "\x00\r\n") {
			return nil, errors.New("模型名称无效")
		}
		if seen[model] {
			continue
		}
		seen[model] = true
		out = append(out, model)
	}
	if len(out) > modelProbeMaxModels {
		return nil, fmt.Errorf("一次最多测试 %d 个模型", modelProbeMaxModels)
	}
	return out, nil
}

func probeWorkspace(e Environment, workspace string) (string, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" && len(e.Workspaces) > 0 {
		workspace = strings.TrimSpace(e.Workspaces[0])
	}
	valid := strings.HasPrefix(workspace, "/")
	if e.Type == "windows" {
		valid = filepath.IsAbs(workspace)
	}
	if !valid || len(workspace) > 4096 || strings.ContainsAny(workspace, "\x00\r\n") {
		return "", errors.New("请填写所选环境中的绝对工作目录")
	}
	return workspace, nil
}

func probeModels(ctx context.Context, c Config, e Environment, engine, workspace string, requested []string) (ModelProbeResponse, error) {
	if engine == "" {
		engine = "codex"
	}
	if !validEngine(engine) {
		return ModelProbeResponse{}, errors.New("AI 工具无效")
	}
	normalizedWorkspace, err := probeWorkspace(e, workspace)
	if err != nil {
		return ModelProbeResponse{}, err
	}
	workspace = normalizedWorkspace

	models, err := normalizeProbeModels(requested)
	if err != nil {
		return ModelProbeResponse{}, err
	}
	if len(models) == 0 {
		catalog, catalogErr := modelsForEngine(ctx, e, engine)
		if catalogErr != nil {
			return ModelProbeResponse{}, catalogErr
		}
		for _, model := range catalog.Models {
			models = append(models, model.ID)
		}
		if len(models) == 0 {
			defaultModel := e.Model
			if engine == "claude" {
				defaultModel = e.ClaudeModel
			}
			if strings.TrimSpace(defaultModel) != "" {
				models = []string{strings.TrimSpace(defaultModel)}
			}
		}
		if len(models) > modelProbeMaxModels {
			models = models[:modelProbeMaxModels]
		}
	}
	if len(models) == 0 {
		return ModelProbeResponse{}, errors.New("没有可测试的模型，请先读取模型列表或填写默认模型")
	}

	// Model probes must never inherit a task's hardware lease or write access.
	c.HardwareAI = nil
	response := ModelProbeResponse{Engine: engine, Workspace: workspace, Results: make([]ModelProbeResult, 0, len(models))}
	for _, model := range models {
		response.Results = append(response.Results, probeOneModel(ctx, c, engine, workspace, model))
		if ctx.Err() != nil {
			break
		}
	}
	return response, nil
}

func probeOneModel(parent context.Context, c Config, engine, workspace, model string) ModelProbeResult {
	started := time.Now()
	ctx, cancel := context.WithTimeout(parent, modelProbeTimeout)
	defer cancel()
	task := Task{
		Engine:    engine,
		Model:     model,
		Workspace: workspace,
		Mode:      &WorkMode{Permission: "read"},
	}
	_, result, err := (CodexRunner{}).Run(ctx, c, task, modelProbePrompt, func(string, string) {})
	duration := time.Since(started).Milliseconds()
	if duration < 1 {
		duration = 1
	}
	if err == nil && strings.TrimSpace(result) != "" {
		return ModelProbeResult{Model: model, Status: "available", Message: "调用成功", DurationMS: duration}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ModelProbeResult{Model: model, Status: "timeout", Message: "请求超时（25 秒）", DurationMS: duration}
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return ModelProbeResult{Model: model, Status: "unavailable", Message: "请求已取消", DurationMS: duration}
	}
	return ModelProbeResult{Model: model, Status: "unavailable", Message: publicProbeError(err), DurationMS: duration}
}

func publicProbeError(err error) string {
	if err == nil {
		return "调用失败"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "401"), strings.Contains(message, "403"),
		strings.Contains(message, "auth"), strings.Contains(message, "login"),
		strings.Contains(message, "permission"), strings.Contains(message, "unauthorized"):
		return "认证或访问权限被拒绝"
	case strings.Contains(message, "not found"), strings.Contains(message, "找不到"),
		strings.Contains(message, "executable"), strings.Contains(message, "cannot start"):
		return "找不到 AI 工具程序"
	case strings.Contains(message, "model"), strings.Contains(message, "unsupported"),
		strings.Contains(message, "invalid"):
		return "模型不受当前服务支持"
	default:
		return "调用失败，请检查该环境的登录状态和网络配置"
	}
}
