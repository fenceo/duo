package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"mime"
	"net/http"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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
	Engine            string   `json:"engine"`
	Workspace         string   `json:"workspace"`
	Models            []string `json:"models"`
	ExpectedProfileID *string  `json:"expected_profile_id,omitempty"`
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

// Streaming is opt-in so existing JSON clients retain the response contract.
// Events contain public model IDs and classified errors, never raw CLI output.
type ModelProbeEvent struct {
	Type      string             `json:"type"`
	Engine    string             `json:"engine,omitempty"`
	Workspace string             `json:"workspace,omitempty"`
	Models    []string           `json:"models,omitempty"`
	Model     string             `json:"model,omitempty"`
	Result    *ModelProbeResult  `json:"result,omitempty"`
	Results   []ModelProbeResult `json:"results,omitempty"`
}

type modelProbePlan struct {
	config    Config
	engine    string
	workspace string
	models    []string
}

func normalizeProbeModels(models []string) ([]string, error) {
	out := make([]string, 0, len(models))
	seen := map[string]bool{}
	for _, raw := range models {
		model := strings.TrimSpace(raw)
		if model == "" {
			return nil, errors.New("请明确选择要测试的模型，模型 ID 不能为空")
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
	if len(out) == 0 {
		return nil, errors.New("请先读取模型列表并选择要测试的模型")
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

func prepareModelProbe(c Config, e Environment, engine, workspace string, requested []string) (modelProbePlan, error) {
	if engine == "" {
		engine = "codex"
	}
	if !validEngine(engine) {
		return modelProbePlan{}, errors.New("AI 工具无效")
	}
	normalizedWorkspace, err := probeWorkspace(e, workspace)
	if err != nil {
		return modelProbePlan{}, err
	}
	workspace = normalizedWorkspace

	models, err := normalizeProbeModels(requested)
	if err != nil {
		return modelProbePlan{}, err
	}

	// Snapshot the selected credentials for the whole batch. The caller admits
	// this plan under modelProbeMu and never rereads config between model calls.
	c.EngineEnv = maps.Clone(c.EngineEnv)
	c.Workspaces = append([]string(nil), c.Workspaces...)
	// Model probes must never inherit a task's hardware lease or write access.
	c.HardwareAI = nil
	return modelProbePlan{config: c, engine: engine, workspace: workspace, models: models}, nil
}

func probeModels(ctx context.Context, c Config, e Environment, engine, workspace string, requested []string) (ModelProbeResponse, error) {
	plan, err := prepareModelProbe(c, e, engine, workspace, requested)
	if err != nil {
		return ModelProbeResponse{}, err
	}
	return executeModelProbe(ctx, plan, probeOneModel, nil), nil
}

// emit returning an error (including a disconnected stream) stops the batch
// before any further model is invoked. Every real call owns a bounded context
// and uses the runner's existing process-tree cleanup path.
func executeModelProbe(ctx context.Context, plan modelProbePlan, run func(context.Context, Config, string, string, string) ModelProbeResult, emit func(ModelProbeEvent) error) ModelProbeResponse {
	response := ModelProbeResponse{Engine: plan.engine, Workspace: plan.workspace, Results: make([]ModelProbeResult, 0, len(plan.models))}
	send := func(event ModelProbeEvent) bool {
		return emit == nil || emit(event) == nil
	}
	if ctx.Err() != nil || !send(ModelProbeEvent{Type: "start", Engine: plan.engine, Workspace: plan.workspace, Models: append([]string(nil), plan.models...)}) {
		return response
	}
	for _, model := range plan.models {
		if ctx.Err() != nil || !send(ModelProbeEvent{Type: "model_start", Model: model}) {
			return response
		}
		if ctx.Err() != nil {
			return response
		}
		result := run(ctx, plan.config, plan.engine, plan.workspace, model)
		response.Results = append(response.Results, result)
		if ctx.Err() != nil {
			return response
		}
		if !send(ModelProbeEvent{Type: "result", Result: &result}) {
			return response
		}
	}
	_ = send(ModelProbeEvent{Type: "done", Engine: response.Engine, Workspace: response.Workspace, Results: response.Results})
	return response
}

func acceptsModelProbeStream(accept string) bool {
	for _, part := range strings.Split(accept, ",") {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil || mediaType != "application/x-ndjson" {
			continue
		}
		if raw, exists := params["q"]; exists {
			q, err := strconv.ParseFloat(raw, 64)
			if err != nil || !(q > 0 && q <= 1) {
				continue
			}
		}
		return true
	}
	return false
}

func (s *Server) testModels(w http.ResponseWriter, r *http.Request) {
	var request ModelProbeRequest
	if !body(w, r, &request) {
		return
	}
	// Reject empty/oversized lists before taking the lock or starting any CLI.
	if _, err := normalizeProbeModels(request.Models); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.modelProbeMu.TryLock() {
		fail(w, http.StatusConflict, "已有模型读取、测试或更新正在进行，请稍后重试")
		return
	}
	defer s.modelProbeMu.Unlock()
	if s.app.updating.Load() {
		fail(w, http.StatusConflict, errUpdateBusy.Error())
		return
	}
	c := s.app.config.get()
	env, err := c.environment(r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusNotFound, err.Error())
		return
	}
	if request.Engine == "" {
		request.Engine = "codex"
	}
	if !validEngine(request.Engine) {
		fail(w, http.StatusBadRequest, "AI 工具无效")
		return
	}
	profileID := s.app.store.activeEngineProfile(env.ID, request.Engine)
	if request.ExpectedProfileID != nil && *request.ExpectedProfileID != profileID {
		fail(w, http.StatusConflict, "账号/API 配置已变化，请重新读取模型列表并确认测试")
		return
	}
	runtime := runtimeConfig(c, env)
	if profileID != "" {
		found := false
		for _, profile := range s.app.store.engineProfiles() {
			if profile.ID == profileID && profile.Engine == request.Engine && profile.EnvironmentID == env.ID {
				runtime.EngineEnv = engineProfileEnv(profile)
				found = true
				break
			}
		}
		if !found {
			fail(w, http.StatusConflict, "账号/API 配置已失效，请重新选择并确认测试")
			return
		}
	}
	plan, err := prepareModelProbe(runtime, env, request.Engine, request.Workspace, request.Models)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), modelProbeRequestMax)
	defer cancel()
	if !acceptsModelProbeStream(r.Header.Get("Accept")) {
		jsonOut(w, http.StatusOK, executeModelProbe(ctx, plan, probeOneModel, nil))
		return
	}
	if _, ok := w.(http.Flusher); !ok {
		fail(w, http.StatusInternalServerError, "当前连接不支持流式模型测试")
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	writer := json.NewEncoder(w)
	controller := http.NewResponseController(w)
	executeModelProbe(ctx, plan, probeOneModel, func(event ModelProbeEvent) error {
		// A client that stops reading must not hold the shared admission lock
		// forever. This only bounds stream writes, not model generation time.
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := writer.Encode(event); err != nil {
			cancel()
			return err
		}
		if err := controller.Flush(); err != nil {
			cancel()
			return err
		}
		_ = controller.SetWriteDeadline(time.Time{})
		return nil
	})
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
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ModelProbeResult{Model: model, Status: "timeout", Message: "请求超时（25 秒）", DurationMS: duration}
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return ModelProbeResult{Model: model, Status: "unavailable", Message: "请求已取消", DurationMS: duration}
	}
	if err == nil && strings.TrimSpace(result) != "" {
		return ModelProbeResult{Model: model, Status: "available", Message: "调用成功", DurationMS: duration}
	}
	return ModelProbeResult{Model: model, Status: "unavailable", Message: publicProbeError(err), DurationMS: duration}
}

var unsupportedProbeModel = regexp.MustCompile("(?i)\\b(?:model_not_found|unsupported_model|invalid_model|model_not_supported)\\b|\\b(?:unsupported|unknown) model\\b|\\binvalid model (?:id|name)\\b|\\bmodel(?:\\s+['\"`][^'\"`\\r\\n]{1,120}['\"`])?\\s+(?:is\\s+)?(?:not found|not supported|does not exist)\\b")

func publicProbeError(err error) string {
	if err == nil {
		return "调用失败"
	}
	message := strings.ToLower(err.Error())
	var executableError *exec.Error
	switch {
	case strings.Contains(message, "missing_credential"), strings.Contains(message, "missing credential"),
		strings.Contains(message, "missing api key"), strings.Contains(message, "no api key"),
		strings.Contains(message, "api key not found"), strings.Contains(message, "api key is not set"),
		strings.Contains(message, "缺少凭据"):
		return "缺少 AI 凭据，请在对应执行环境配置该引擎的原生凭据后重试"
	case isHarnessUnsupportedReasoning(message):
		return harnessUnsupportedReasoningMessage
	case strings.Contains(message, "401"), strings.Contains(message, "403"),
		strings.Contains(message, "auth"), strings.Contains(message, "login"),
		strings.Contains(message, "permission"), strings.Contains(message, "unauthorized"),
		strings.Contains(message, "invalid api key"), strings.Contains(message, "invalid_api_key"),
		strings.Contains(message, "invalid credential"):
		return "认证或访问权限被拒绝"
	case unsupportedProbeModel.MatchString(message):
		return "模型不受当前服务支持"
	case errors.As(err, &executableError), strings.Contains(message, "executable file not found"),
		strings.Contains(message, "cannot find executable"), strings.Contains(message, "找不到 harness 程序"),
		strings.Contains(message, "找不到 ai 工具程序"):
		return "找不到 AI 工具程序"
	default:
		return "调用失败，请检查该环境的登录状态和网络配置"
	}
}
