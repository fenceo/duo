package main

import (
	"net/http"
	"strings"
)

// This explicit GET-models probe does not create a CLI session, save a key,
// modify a profile, or send inference. Validation errors contain no secrets.
func (s *Server) engineAPIModelsRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/environments/{id}/engine-api-models", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Engine   string      `json:"engine"`
			Protocol string      `json:"api_protocol"`
			BaseURL  string      `json:"base_url"`
			APIKey   string      `json:"api_key"`
			Target   Environment `json:"target"`
			AuthMode string      `json:"auth_mode,omitempty"`
		}
		if !body(w, r, &req) {
			return
		}
		env, err := s.app.config.get().environment(r.PathValue("id"))
		if err != nil || !sameAccountEnvironment(env, req.Target) {
			http.Error(w, "目标环境已变化，请重新打开配置", 409)
			return
		}
		if req.Engine != "claude" && req.Engine != "mimo" {
			http.Error(w, "此入口仅支持 Claude Code 或 MiMo Code", 400)
			return
		}
		validation := EngineSetupRequest{ID: "api-model-probe", Action: "account", Engine: req.Engine, Name: "API probe", Login: "apiKey", APIKey: req.APIKey, BaseURL: strings.TrimSpace(req.BaseURL), Model: "probe", APIProtocol: req.Protocol, AuthMode: req.AuthMode}
		if err = validateEngineSetup(validation, env); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		base, protocol := validation.BaseURL, req.Protocol
		if req.Engine == "claude" {
			protocol = "anthropic-messages"
			if base == "" {
				base = "https://api.anthropic.com"
			}
		}
		if protocol == "" {
			protocol = "openai-chat"
		}
		// Claude appends /v1 to its configured base. MiMo expects the full SDK base.
		if req.Engine == "claude" && !strings.HasSuffix(strings.TrimRight(base, "/"), "/v1") {
			base = strings.TrimRight(base, "/") + "/v1"
		}
		result := discoverHarnessModels(r.Context(), env, HarnessAPIRequest{API: protocol, BaseURL: base, APIKey: req.APIKey, ModelListBearer: req.Engine == "claude" && claudeUsesToken(validation)})
		for i := range result.Models {
			result.Models[i].Engine = req.Engine
		}
		jsonOut(w, http.StatusOK, result)
	}))
}
