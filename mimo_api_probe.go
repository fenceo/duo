package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// This probes the configured API, without CLI sessions, project instructions,
// files, tools or conversation history. The UI explicitly confirms quota use.
func probeMimoAPI(parent context.Context, env Environment, api EngineAPIRequest, model string) ModelProbeResult {
	start := time.Now()
	result := ModelProbeResult{Model: model, Status: "unavailable"}
	ctx, cancel := context.WithTimeout(parent, modelProbeTimeout)
	defer cancel()
	id := strings.TrimPrefix(model, "duo-mimo/")
	body := map[string]any{"model": id, "messages": []any{map[string]string{"role": "user", "content": "Reply with OK."}}, "stream": false}
	suffix := "/chat/completions"
	if api.APIProtocol == "anthropic-messages" {
		suffix = "/messages"
		body["max_tokens"] = 32
	} else if strings.HasPrefix(strings.ToLower(id), "mimo-") {
		body["max_completion_tokens"] = 64
		body["thinking"] = map[string]string{"type": "disabled"}
	} else {
		body["max_tokens"] = 64
	}
	raw, _ := json.Marshal(body)
	response, err := requestHarnessModels(ctx, env, harnessDiscoveryHTTP{URL: strings.TrimRight(api.BaseURL, "/") + suffix, API: api.APIProtocol, APIKey: api.APIKey, Method: "POST", Payload: raw, TimeoutSeconds: 25})
	result.DurationMS = max(1, time.Since(start).Milliseconds())
	if ctx.Err() != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.Status = "timeout"
			result.Message = "API 请求超时（25 秒）"
		} else {
			result.Message = "API 请求已取消"
		}
		return result
	}
	if err != nil {
		result.Message = "目标环境无法访问 API，请检查网络、代理或证书"
		return result
	}
	switch response.Status {
	case 200:
	case 401, 403:
		result.Message = "API 认证或访问权限被拒绝"
		return result
	case 402:
		result.Message = "API 余额不足"
		return result
	case 429:
		result.Message = "API 请求被限流或套餐额度不足"
		return result
	case 400, 404:
		result.Message = "模型 ID 或 API 协议/地址不受当前服务支持"
		return result
	case 301, 302, 303, 307, 308:
		result.Message = "API 返回重定向，未转发 Key；请核对根地址"
		return result
	default:
		result.Message = "API 服务请求失败，请检查服务商状态"
		return result
	}
	var doc struct {
		Error   json.RawMessage
		Choices []struct {
			Message struct{ Content json.RawMessage }
		}
		Content []struct{ Type, Text string }
	}
	if json.Unmarshal(response.Body, &doc) != nil || len(doc.Error) > 0 && string(doc.Error) != "null" {
		result.Message = "API 返回无效结果；未回显原始响应"
		return result
	}
	text := ""
	if api.APIProtocol == "anthropic-messages" {
		for _, block := range doc.Content {
			if block.Type == "text" {
				text += block.Text
			}
		}
	} else if len(doc.Choices) > 0 {
		content := doc.Choices[0].Message.Content
		if json.Unmarshal(content, &text) != nil {
			var blocks []struct{ Type, Text string }
			if json.Unmarshal(content, &blocks) == nil {
				for _, block := range blocks {
					if block.Type == "text" {
						text += block.Text
					}
				}
			}
		}
	}
	if strings.TrimSpace(text) == "" {
		result.Message = "API 未返回文本回复；尚不能确认此模型可用于对话"
		return result
	}
	result.Status = "available"
	result.Message = "API 文本调用成功；CLI 工具与长会话能力未验证"
	return result
}
