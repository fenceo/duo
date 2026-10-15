package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

var errMimoTranscriptUnavailable = errors.New("MiMo 原生结果无法核对")

func mimoTranscriptUnavailable(message string) error {
	return fmt.Errorf("%w：%s", errMimoTranscriptUnavailable, message)
}

// ACP 0.1.15 can finish before its text notifications reach the client, and
// returns end_turn even when the native message contains an API error. Read
// the CLI's durable result after stopping its writer; never replay it as input.
func mimoTranscript(ctx context.Context, c Config, t Task, variables map[string]string, session, input string) (string, *RunUsage, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	binary := c.Mimo
	if binary == "" {
		binary = "mimo"
	}
	var cmd *exec.Cmd
	if c.Distro != "" || c.SSHHost != "" {
		args := withEngineEnv([]string{binary, "export", session}, variables)
		quoted := []string{}
		for _, arg := range args {
			quoted = append(quoted, posixQuote(arg))
		}
		script := "cd -- " + posixQuote(t.Workspace) + " && exec " + strings.Join(quoted, " ")
		cmd = commandWithContext(ctx, nativeTargetCommand(c, "sh", "-lc", script))
	} else {
		cmd = exec.CommandContext(ctx, binary, "export", session)
		cmd.Dir = t.Workspace
		applyNativeEnv(cmd, variables)
	}
	if os.Getenv("DUO_TEST_MIMO_ACP") == "1" {
		cmd = exec.CommandContext(ctx, binary, "-test.run=^TestMimoProcessFixture$", "--", "export", session)
		cmd.Dir = t.Workspace
		applyNativeEnv(cmd, variables)
	}
	stdout, stderr := &cappedOutput{limit: 32 * 1024 * 1024}, &cappedOutput{limit: 4096}
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = stdout, stderr, time.Second
	hideCommand(cmd)
	if err := cmd.Run(); err != nil {
		return "", nil, mimoTranscriptUnavailable("原生记录暂不可读取")
	}
	return parseMimoTranscript(stdout.Bytes(), session, input, t.Model)
}

type mimoNativeMessage struct {
	Info struct {
		ID, ParentID, Role, ProviderID, ModelID string
		Error                                   *struct {
			Name string
			Data struct{ StatusCode int }
		}
		Tokens struct {
			Input, Output, Reasoning int64
			Cache                    struct{ Read, Write int64 }
		}
	}
	Parts []struct {
		Type, Text string
		Ignored    bool
	}
}

func parseMimoTranscript(raw []byte, session, input, model string) (string, *RunUsage, error) {
	var doc struct {
		Info     struct{ ID string }
		Messages []mimoNativeMessage
	}
	if json.Unmarshal(raw, &doc) != nil || doc.Info.ID != session {
		return "", nil, mimoTranscriptUnavailable("格式或会话不匹配")
	}
	user := -1
	for i, message := range doc.Messages {
		if message.Info.Role == "user" {
			user = i
		}
	}
	if user < 0 || doc.Messages[user].Info.ID == "" {
		return "", nil, mimoTranscriptUnavailable("缺少本轮输入")
	}
	text := ""
	for _, part := range doc.Messages[user].Parts {
		if part.Type == "text" && !part.Ignored {
			text += part.Text
		}
	}
	if text != input {
		return "", nil, mimoTranscriptUnavailable("记录不属于本轮输入，未采用旧回复")
	}
	var final *mimoNativeMessage
	usage := &RunUsage{}
	for i := user + 1; i < len(doc.Messages); i++ {
		message := &doc.Messages[i]
		if message.Info.Role != "assistant" || message.Info.ParentID != doc.Messages[user].Info.ID {
			continue
		}
		final = message
		tokens := message.Info.Tokens
		usage.Input += tokens.Input
		usage.Output += tokens.Output + tokens.Reasoning
		usage.Cached += tokens.Cache.Read
		usage.CacheWrite += tokens.Cache.Write
	}
	if final == nil {
		return "", nil, mimoTranscriptUnavailable("缺少本轮结果")
	}
	actual := final.Info.ProviderID + "/" + final.Info.ModelID
	if model != "" && actual != model {
		return "", nil, fmt.Errorf("MiMo 原生模型与所选模型不一致：所选 %s，实际 %s", model, actual)
	}
	if nativeErr := final.Info.Error; nativeErr != nil {
		// Native error payloads can contain headers and request bodies. Only
		// project known categories/statuses, never raw messages or responseBody.
		switch nativeErr.Data.StatusCode {
		case 401, 403:
			return "", nil, fmt.Errorf("MiMo 模型请求拒绝认证（HTTP %d），请检查所选服务的 Key、套餐地址和访问权限", nativeErr.Data.StatusCode)
		case 402:
			return "", nil, errors.New("MiMo 模型请求余额不足（HTTP 402）")
		case 429:
			return "", nil, errors.New("MiMo 模型请求达到速率或套餐额度限制（HTTP 429）")
		case 400, 404:
			return "", nil, fmt.Errorf("MiMo 模型或接口不兼容（HTTP %d），请检查模型 ID、接口协议和根地址", nativeErr.Data.StatusCode)
		default:
			return "", nil, errors.New("MiMo 原生模型请求失败，请检查目标 CLI 的原生诊断记录")
		}
	}
	text = ""
	for _, part := range final.Parts {
		if part.Type == "text" && !part.Ignored {
			text += part.Text
		}
	}
	usage.Total = usage.Input + usage.Output + usage.Cached + usage.CacheWrite
	if usage.Total == 0 {
		usage = nil
	}
	return strings.TrimSpace(text), usage, nil
}
