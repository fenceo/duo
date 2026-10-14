package main

import (
	"errors"
	"net/url"
	"strings"
)

func validateMimoAPI(v EngineAPIRequest) error {
	if v.Engine != "mimo" {
		return nil
	}
	if mimoSpeechModel(v.Model) {
		return errors.New("语音识别/合成模型不能用于 MiMo 代码会话，请选择文本模型")
	}
	if v.APIProtocol == "anthropic-messages" && strings.HasPrefix(strings.ToLower(v.Model), "mimo-") {
		return errors.New("MiMo CLI 对 MiMo 模型使用 OpenAI Chat Completions，请选择该协议及服务商提供的 /v1 根地址")
	}
	u, err := url.Parse(v.BaseURL)
	if err != nil {
		return nil
	} // General URL validation runs separately.
	host := strings.ToLower(u.Hostname())
	if host == "api.xiaomimimo.com" && (strings.HasPrefix(v.APIKey, "tp-") || strings.HasPrefix(v.APIKey, "ttp-")) {
		return errors.New("Token Plan Key 不能用于按量付费地址，请选择订阅区域预设，并使用套餐页面给出的根地址")
	}
	if strings.HasPrefix(host, "token-plan-") && strings.HasSuffix(host, ".xiaomimimo.com") && strings.HasPrefix(v.APIKey, "sk-") {
		return errors.New("按量付费 Key 不能用于 Token Plan 地址，请核对 Key 类型和服务根地址")
	}
	return nil
}
