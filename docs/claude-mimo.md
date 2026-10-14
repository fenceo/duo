# Claude Code 与 MiMo 配置

在“环境与引擎”中先检测并安装对应环境的 CLI，再点击该行“配置 API”。它直接打开所选环境的独立配置窗口，不进入账号管理。两个工具都支持官方服务或自定义兼容服务；引擎决定执行方式，API 服务决定实际调用哪个模型。

## Claude Code

需要 Anthropic Messages 兼容服务。官方 Anthropic API 可选择官方预设并留空地址，使用 `x-api-key`。第三方地址通常使用 Bearer Token；服务若要求 `x-api-key`，在请求头选项中选择 API Key。MiMo 按量付费预设使用 `https://api.xiaomimimo.com/anthropic`；Token Plan 的区域地址与 Key 以订阅后台为准。

填写模型 ID 后，第三方配置会同时设置默认模型、Opus/Sonnet/Haiku 映射及子任务默认模型，避免继续请求旧的 Claude 模型名称。点击“保存并读取模型”后，配置直接写入所选环境的原生 Claude 目录并生效，原文件留有备份。保留目标工具权限、MCP、工作区信任和其他偏好，不更改任务会话。API 配置会替换当前订阅登录；原登录在备份中保留，账号库存不删除。

## MiMo Code

选择官方 MiMo Code，引擎通过 `mimo acp` 执行。当前官方独立发行与旧 npm alpha 包的接口和配置格式不同；一键安装使用当前官方发行。

MiMo 模型选择 OpenAI Chat Completions，根地址通常包含 `/v1`。当前 MiMo CLI 会把 MiMo 模型固定到 Chat Completions SDK；不能为这些模型选择 Anthropic Messages 根地址。其他兼容服务的非 MiMo 模型仍可使用 Anthropic Messages，填写包含版本路径的根地址。地址按服务商给出的值填写，不把 Responses 接口当作 Chat Completions 接口。Token Plan Key 和按量付费 Key 不能混用；订阅套餐使用套餐页面给出的区域根地址。

点击“保存并读取模型”后直接用于所选环境，无需添加账号或再激活。新配置保存为目标原生配置目录中的 `duo-api.json`，Duo 通过 `MIMOCODE_CONFIG` 引用，保留其他原生配置。MiMo 会话仍保存在目标用户的原生数据目录，切换 API 不移动历史。若已激活 v0.34.18 的 MiMo 库存配置，窗口会读取并更新它，不创建重复库存或删除旧引用。配置模型在原生目录中显示为 `duo-mimo/<上游模型 ID>`；上游 ID 中的 `/` 会保留。

使用 Duo API 配置时，任务列表选择 `duo-mimo/` 路由的模型，避免误用未配置 Key 的内置提供商。官方模型目录中的专用 ASR/TTS 型号用于转写/合成，不能执行代码会话，因而不列入此选择器；并未卸载这些模型或禁用语音服务。重新保存旧配置可刷新原生目录和 MiMo 思考/工具续接元数据。

每轮结束后，Duo 停止该轮原生写入进程，再读取 CLI 保存的本轮结果，核对会话、输入和所选模型；补回 ACP 未发出的短回复并投影认证、额度或接口错误。只采用当前输入对应的最终消息，不把旧回复或工具前言当作最终回复。原生记录读取失败时保留流式记录并提示，不清空历史或新建替代会话。过程显示合并连续思考片段和累积正文快照，保留工具步骤；轮次底部显示所选模型和原生用量。模型自述或默认配置文件不代表本轮实际路由。

任务提供 Build、Plan 和原生 allow 权限。Plan 遵循 MiMo 的原生规划代理规则；允许联网，不能把它当作操作系统只读沙箱。需要人工决定的原生工具请求可以单次批准、拒绝或取消；原生明确的 ask/deny 规则保留。所选代理和权限在下次消息发送时生效，当前执行不变。

## 验证与模型列表

“验证并读取模型”从所选 Windows/WSL/SSH 环境访问模型目录接口，只发 GET，不消耗推理额度；保存时也会自动验证。成功说明模型目录认证通过，具体模型调用仍未验证。服务若没有标准模型列表接口，会明确提示并允许手动填写模型 ID。Key 不回显；留空保留同一服务已保存的 Key。改变地址、协议或认证方式需要重新填写 Key。保存配置前需结束该环境对应引擎的执行或排队任务。

新建任务的模型列表读取该环境实际 CLI 或显式配置。MiMo 的独立无工具推理测试、独立知识整理及 Duo 桌面/硬件工具尚未接入，相应入口不可用；不以普通聊天权限代替。

官方依据：[MiMo Code](https://github.com/XiaomiMiMo/MiMo-Code)、[MiMo 在 Claude Code 中使用](https://mimo.mi.com/docs/zh-CN/tokenplan/integration/claudecode)、[Claude Code 网关配置](https://code.claude.com/docs/en/llm-gateway-connect)。
