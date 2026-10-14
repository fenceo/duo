package main

import (
	"context"
	"errors"
	"strings"
)

var errMimoAPIUnconfigured = errors.New("MiMo 独立 API 配置不可用，请在所选环境配置 API")

func mimoAPIRevision(v EngineAPIRequest) string {
	return hash(v.BaseURL + "\x00" + v.APIProtocol + "\x00" + v.APIKey)
}

// Use the same config reference as task execution. Credentials stay inside
// the service; catalog/probe responses never contain native files or keys.
func (a *App) readMimoAPI(ctx context.Context, env Environment, selected map[string]string) (EngineAPIRequest, error) {
	if path := selected["MIMOCODE_CONFIG"]; path != "" {
		env.MimoConfig = path
	}
	if env.MimoConfig == "" {
		return EngineAPIRequest{}, errMimoAPIUnconfigured
	}
	dir, kind, err := a.engineAPILocation(ctx, env, "mimo")
	if err != nil {
		return EngineAPIRequest{}, errMimoAPIUnconfigured
	}
	files, err := a.engineSetup.harnessRead(ctx, env, dir, kind)
	if err != nil {
		return EngineAPIRequest{}, errors.New("无法读取所选环境的 MiMo API 配置")
	}
	view, key, err := engineAPIConfigView(env, "mimo", kind, files)
	if err != nil || key == "" || view.Model == "" {
		return EngineAPIRequest{}, errMimoAPIUnconfigured
	}
	v := view.EngineAPIRequest
	v.APIKey = key
	v.AuthMode = "" // MiMo's SDK uses its protocol's native key header.
	if err = validateMimoAPI(v); err != nil {
		return EngineAPIRequest{}, err
	}
	if err = validateEngineSetup(engineAPISetup(v), env); err != nil {
		return EngineAPIRequest{}, errors.New("MiMo API 配置无效，请重新保存所选环境的配置")
	}
	return v, nil
}

func (a *App) mimoAPICatalog(ctx context.Context, env Environment, selected map[string]string) (ModelList, error) {
	v, err := a.readMimoAPI(ctx, env, selected)
	if errors.Is(err, errMimoAPIUnconfigured) {
		return configuredMimoCatalog(ctx, env, selected)
	}
	if err != nil {
		return ModelList{}, err
	}
	discovery := engineAPIDiscover(ctx, a.engineSetup, env, v)
	if discovery.Status == "unsupported" {
		cached, err := configuredMimoCatalog(ctx, env, selected)
		cached.Status = "fallback"
		cached.APITarget, cached.APIEndpoint = mimoAPIRevision(v), v.BaseURL
		cached.Message = discovery.Message + " 仅显示已有原生配置；未更新配置或调用模型。"
		return cached, err
	}
	if discovery.Status != "ready" {
		return ModelList{}, errors.New(discovery.Message)
	}
	list := ModelList{Models: []ModelOption{}, Status: "ready", Source: "MiMo API 实时模型目录", DefaultModel: "duo-mimo/" + v.Model}
	list.APITarget, list.APIEndpoint = mimoAPIRevision(v), v.BaseURL
	for _, model := range discovery.Models {
		model.ID = "duo-mimo/" + model.ID
		list.Models = append(list.Models, model)
	}
	list = mergeEngineCatalog(list, env, "mimo", list.DefaultModel)
	chat := []ModelOption{}
	for _, model := range list.Models {
		if !mimoSpeechModel(model.ID) {
			chat = append(chat, model)
		}
	}
	list.Models = chat
	list.Message = strings.TrimSpace(discovery.Message) + "；已查询所选服务当前目录，未修改配置或发送推理请求。"
	return list, nil
}
