package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

const harnessWorkspaceMode = "harness:workspace"
const harnessPermissionMethod = "harness/requestPermission"

// Old work/custom snapshots deliberately retain their previous never policy.
// Only an explicit selection of the new official workspace preset enables ask.
func harnessInteractiveMode(mode *WorkMode) bool {
	return mode != nil && mode.ID == harnessWorkspaceMode && mode.Permission == "workspace" && mode.Approval == "request"
}

type harnessToolCall struct {
	ID       string          `json:"toolCallId"`
	Title    string          `json:"title"`
	RawInput json.RawMessage `json:"rawInput"`
}

type harnessPermissionRequest struct {
	SessionID string          `json:"sessionId"`
	ToolCall  harnessToolCall `json:"toolCall"`
	Options   []struct {
		ID   string `json:"optionId"`
		Kind string `json:"kind"`
	} `json:"options"`
}

func parseHarnessPermission(params json.RawMessage) (harnessPermissionRequest, error) {
	var request harnessPermissionRequest
	if len(params) > 256*1024 || json.Unmarshal(params, &request) != nil || request.SessionID == "" || request.ToolCall.ID == "" || strings.TrimSpace(request.ToolCall.Title) == "" || len(request.ToolCall.RawInput) == 0 || string(request.ToolCall.RawInput) == "null" || len(request.Options) == 0 || len(request.Options) > 16 {
		return request, errors.New("Harness 审批请求缺少会话、操作详情或有效选项")
	}
	seen, kinds := map[string]bool{}, map[string]bool{}
	for _, option := range request.Options {
		if option.ID == "" || len(option.ID) > 256 || seen[option.ID] {
			return request, errors.New("Harness 审批选项标识无效")
		}
		seen[option.ID] = true
		if option.Kind == "allow_once" || option.Kind == "reject_once" {
			if kinds[option.Kind] {
				return request, errors.New("Harness 单次审批选项不唯一")
			}
			kinds[option.Kind] = true
		}
	}
	return request, nil
}

func harnessAnswerPayload(params json.RawMessage, answer CodexAnswer) (json.RawMessage, error) {
	request, err := parseHarnessPermission(params)
	if err != nil {
		return nil, err
	}
	if len(answer.Answers) != 0 {
		return nil, errors.New("Harness 权限请求不接受问题答案")
	}
	if answer.Decision == "cancel" {
		return json.RawMessage(`{"outcome":{"outcome":"cancelled"}}`), nil
	}
	kind := ""
	switch answer.Decision {
	case "accept":
		kind = "allow_once"
	case "decline":
		kind = "reject_once"
	default:
		return nil, errors.New("Harness 只支持批准本次、拒绝本次或取消请求")
	}
	for _, option := range request.Options {
		if option.Kind == kind {
			return json.Marshal(map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": option.ID}})
		}
	}
	return nil, errors.New("Harness 未提供此单次审批选项")
}

type harnessPermissionReply struct {
	id     json.RawMessage
	result json.RawMessage
	callID string
}

// ACP sends only a tool ID in the permission RPC. Correlate it with the live
// same-session tool update before exposing a one-time grant to the user.
func (s *harnessACPTurn) permissionParams(raw json.RawMessage) (json.RawMessage, string, error) {
	var request harnessPermissionRequest
	if len(raw) > 256*1024 || json.Unmarshal(raw, &request) != nil || request.SessionID != s.session {
		return nil, "", errors.New("Harness 审批请求不属于当前会话")
	}
	tool, ok := s.tools[request.ToolCall.ID]
	if !ok {
		return nil, "", errors.New("Harness 审批请求没有对应的本轮工具详情")
	}
	// Ignore any replacement details supplied by the permission RPC itself.
	request.ToolCall = tool
	params, err := json.Marshal(request)
	if err == nil {
		_, err = parseHarnessPermission(params)
	}
	return params, tool.ID, err
}

func requestHarnessPermission(ctx context.Context, id json.RawMessage, callID string, params json.RawMessage) harnessPermissionReply {
	result, err := handleCodexInteraction(ctx, harnessPermissionMethod, params)
	if err != nil {
		result = json.RawMessage(`{"outcome":{"outcome":"cancelled"}}`)
	}
	return harnessPermissionReply{id: append(json.RawMessage(nil), id...), result: result, callID: callID}
}
