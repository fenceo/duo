package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

//go:embed harness_discovery.py
var harnessDiscoveryScript string

// Some Anthropic-compatible relays (for example Z.ai/GLM) answer with HTTP 200
// and a provider error envelope such as
// {"code":1001,"msg":"Authentication parameter not received","success":false}
// instead of an HTTP error status. Report that as a failed validation with only
// the numeric provider code - never the raw payload or credential - and keep the
// save blocked because no model list was verified.
func modelListRejection(body []byte) string {
	var envelope struct {
		Success *bool           `json:"success"`
		Code    json.RawMessage `json:"code"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		failed, code := envelope.Success != nil && !*envelope.Success, ""
		if len(envelope.Code) > 0 {
			var number float64
			var text string
			switch {
			case json.Unmarshal(envelope.Code, &number) == nil && number != 0:
				failed, code = true, strconv.FormatFloat(number, 'f', -1, 64)
			case json.Unmarshal(envelope.Code, &text) == nil && text != "" && len(text) <= 12 && strings.Trim(text, "0123456789") == "":
				failed, code = true, text
			}
		}
		if failed {
			if code != "" {
				return "服务商以成功状态返回了错误（错误码 " + code + "），模型列表未读取，配置未保存；请检查 API Key、账号权限与 API 根地址"
			}
			return "服务商以成功状态返回了错误，模型列表未读取，配置未保存；请检查 API Key、账号权限与 API 根地址"
		}
	}
	return "服务商返回了无法识别的模型列表，请检查 API 根地址"
}

type HarnessDiscovery struct {
	Status  string        `json:"status"`
	Models  []ModelOption `json:"models"`
	Message string        `json:"message"`
}

type harnessDiscoveryHTTP struct {
	URL            string          `json:"url"`
	API            string          `json:"api"`
	APIKey         string          `json:"api_key"`
	Bearer         bool            `json:"bearer_auth,omitempty"`
	Method         string          `json:"method,omitempty"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	TimeoutSeconds int             `json:"timeout_seconds,omitempty"`
}
type harnessDiscoveryResponse struct {
	Status int    `json:"status"`
	Body   []byte `json:"body"`
}

func requestHarnessModels(ctx context.Context, env Environment, req harnessDiscoveryHTTP) (harnessDiscoveryResponse, error) {
	if env.Type == "wsl" || env.Type == "ssh" {
		raw, _ := json.Marshal(req)
		base := environmentProbeCommand(env, "sh", "-lc", "exec python3 -c "+posixQuote(harnessDiscoveryScript))
		cmd := commandWithContext(ctx, base)
		cmd.Stdin = bytes.NewReader(raw)
		out := &limitedBuffer{limit: 6 * 1024 * 1024}
		cmd.Stdout, cmd.Stderr, cmd.WaitDelay = out, io.Discard, 2*time.Second
		hideCommand(cmd)
		var result harnessDiscoveryResponse
		if cmd.Run() != nil || json.Unmarshal(out.Bytes(), &result) != nil {
			return result, errors.New("目标环境无法访问模型接口，请检查连接、Python 3、代理或证书")
		}
		return result, nil
	}
	method := req.Method
	if method == "" {
		method = "GET"
	}
	if method != "GET" && method != "POST" {
		return harnessDiscoveryResponse{}, errors.New("API 请求方法无效")
	}
	request, err := http.NewRequestWithContext(ctx, method, req.URL, bytes.NewReader(req.Payload))
	if err != nil {
		return harnessDiscoveryResponse{}, errors.New("模型列表地址无效")
	}
	request.Header.Set("Accept", "application/json")
	if method == "POST" {
		request.Header.Set("Content-Type", "application/json")
	}
	if req.API == "anthropic-messages" {
		if req.Bearer {
			request.Header.Set("Authorization", "Bearer "+req.APIKey)
		} else {
			request.Header.Set("x-api-key", req.APIKey)
		}
		request.Header.Set("anthropic-version", "2023-06-01")
	} else {
		request.Header.Set("Authorization", "Bearer "+req.APIKey)
	}
	timeout := 12 * time.Second
	if req.TimeoutSeconds > 0 && req.TimeoutSeconds <= 25 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}
	client := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return harnessDiscoveryResponse{}, errors.New("无法访问模型接口，请检查地址、网络、代理或证书")
	}
	defer response.Body.Close()
	result := harnessDiscoveryResponse{Status: response.StatusCode}
	if result.Status == 200 {
		result.Body, err = io.ReadAll(io.LimitReader(response.Body, maxCodexAuthBytes+1))
		if err != nil || len(result.Body) > maxCodexAuthBytes {
			return result, errors.New("模型列表读取失败或响应过大")
		}
	}
	return result, nil
}

func discoverHarnessModels(ctx context.Context, env Environment, v HarnessAPIRequest) HarnessDiscovery {
	out := HarnessDiscovery{Status: "failed", Models: []ModelOption{}}
	if v.APIKey == "" {
		out.Message = "请填写 API key，或先保存一个 Key"
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	root := strings.TrimRight(v.BaseURL, "/")
	if v.API == "anthropic-messages" {
		u, _ := url.Parse(root)
		if u.Path == "" {
			root += "/v1"
		}
	}
	endpoint := root + "/models"
	seen, cursors := map[string]bool{}, map[string]bool{}
	for page := 0; page < 10; page++ {
		response, err := requestHarnessModels(ctx, env, harnessDiscoveryHTTP{URL: endpoint, API: v.API, APIKey: v.APIKey, Bearer: v.ModelListBearer})
		if err != nil {
			out.Message = err.Error()
			return out
		}
		switch response.Status {
		case 200:
		case 404, 405, 501:
			out.Status, out.Message = "unsupported", "服务商未提供标准模型列表接口；可手动填写模型 ID 保存。接口与模型调用尚未验证。"
			return out
		case 401, 403:
			out.Message = "模型接口拒绝认证，请检查 API key 和访问权限"
			return out
		case 429:
			out.Message = "模型列表请求被限流，请稍后重试"
			return out
		default:
			out.Message = fmt.Sprintf("模型列表请求失败（HTTP %d），请检查 API 根地址和服务商状态", response.Status)
			return out
		}
		var doc struct {
			Data []struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				DisplayName string `json:"display_name"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if json.Unmarshal(response.Body, &doc) != nil || doc.Data == nil {
			out.Message = modelListRejection(response.Body)
			return out
		}
		for _, m := range doc.Data {
			id := strings.TrimSpace(m.ID)
			if id == "" || len(id) > 120 || strings.ContainsAny(id, "\r\n\x00") || strings.Contains(id, v.APIKey) {
				out.Message = "服务商模型列表包含无效条目"
				return out
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			name := strings.TrimSpace(m.DisplayName)
			if name == "" {
				name = strings.TrimSpace(m.Name)
			}
			if name == "" || len(name) > 240 || strings.ContainsAny(name, "\r\n\x00") || strings.Contains(name, v.APIKey) {
				name = id
			}
			out.Models = append(out.Models, ModelOption{ID: id, Name: name, Engine: "deepseek-harness", Origin: "native", ReasoningLevels: []string{}})
			if len(out.Models) > 1000 {
				out.Message = "服务商模型列表过大，请使用范围更小的 API 入口"
				return out
			}
		}
		if !doc.HasMore {
			if len(out.Models) == 0 {
				out.Message = "认证请求成功，但服务商返回了空模型列表，请检查账号的模型权限"
				return out
			}
			out.Status, out.Message = "ready", fmt.Sprintf("接口认证通过，已读取 %d 个模型；未发送推理请求，实际模型调用尚未验证。", len(out.Models))
			return out
		}
		if v.API != "anthropic-messages" || doc.LastID == "" || len(doc.LastID) > 120 || cursors[doc.LastID] {
			out.Message = "模型列表分页无效，未保存不完整列表"
			return out
		}
		cursors[doc.LastID] = true
		endpoint = root + "/models?after_id=" + url.QueryEscape(doc.LastID)
	}
	out.Message = "模型列表分页过多，未保存不完整列表"
	return out
}

func harnessRequestKey(v HarnessAPIRequest, files map[string][]byte) (string, error) {
	if v.APIKey != "" {
		return v.APIKey, nil
	}
	_, creds, err := harnessAPIDocuments(files)
	if err != nil {
		return "", err
	}
	key := harnessValue(harnessField(harnessField(creds, "refs"), harnessAPIKeyRef))
	if key == "" {
		return "", errors.New("首次配置请填写 API key")
	}
	return key, nil
}
