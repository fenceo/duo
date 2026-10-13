# Claude Code 与 MiMo 配置

在“环境与引擎”中先检测并安装对应环境的 CLI，再点击该行“配置 API”。表单会打开账号管理并选中环境和工具。两个工具都支持官方服务或自定义兼容服务；引擎决定执行方式，API 服务决定实际调用哪个模型。

## Claude Code

需要 Anthropic Messages 兼容服务。官方 Anthropic API 可选择官方预设并留空地址，使用 `x-api-key`。第三方地址通常使用 Bearer Token；服务若要求 `x-api-key`，在请求头选项中选择 API Key。MiMo 按量付费预设使用 `https://api.xiaomimimo.com/anthropic`；Token Plan 的区域地址与 Key 以订阅后台为准。

填写模型 ID 后，第三方配置会同时设置默认模型、Opus/Sonnet/Haiku 映射及子任务默认模型，避免继续请求旧的 Claude 模型名称。配置作为独立账号库存保存；在账号卡片点击“切换到环境”后，目标原生 Claude 配置生效。切换保留目标工具权限、MCP、工作区信任和其他偏好，不更改任务会话。

## MiMo Code

选择官方 MiMo Code，引擎通过 `mimo acp` 执行。当前官方独立发行与旧 npm alpha 包的接口和配置格式不同；一键安装使用当前官方发行。

选择 OpenAI Chat Completions 或 Anthropic Messages 兼容协议，填写相应版本根地址、Key 和模型 ID。OpenAI 地址通常包含 `/v1`；Anthropic SDK 需要包含版本路径的根地址。地址按服务商给出的值填写，不把 Responses 接口当作 Chat Completions 接口。MiMo、Anthropic 官方预设和 Token Plan 地址均可编辑。

配置保存后，在账号卡片选择“用于此环境”。Duo 通过 `MIMOCODE_CONFIG` 引用该库存文件；MiMo 会话仍保存在目标用户的原生数据目录，切换 API 不移动历史。配置模型在原生目录中显示为 `duo-mimo/<上游模型 ID>`；上游 ID 中的 `/` 会保留。也可继承目标环境已有的原生配置。

任务提供 Build、Plan 和原生 allow 权限。Plan 遵循 MiMo 的原生规划代理规则；允许联网，不能把它当作操作系统只读沙箱。需要人工决定的原生工具请求可以单次批准、拒绝或取消；原生明确的 ask/deny 规则保留。所选代理和权限在下次消息发送时生效，当前执行不变。

## 验证与模型列表

“验证认证并读取模型”从所选 Windows/WSL/SSH 环境访问模型目录接口，只发 GET，不消耗推理额度。成功说明模型目录认证通过，具体模型调用仍未验证。服务若没有标准模型列表接口，会明确提示并允许手动填写模型 ID。不要把库存保存成功当作配置已激活。

新建任务的模型列表读取该环境实际 CLI 或显式配置。MiMo 的独立无工具推理测试、独立知识整理及 Duo 桌面/硬件工具尚未接入，相应入口不可用；不以普通聊天权限代替。

官方依据：[MiMo Code](https://github.com/XiaomiMiMo/MiMo-Code)、[MiMo 在 Claude Code 中使用](https://mimo.mi.com/docs/zh-CN/tokenplan/integration/claudecode)、[Claude Code 网关配置](https://code.claude.com/docs/en/llm-gateway-connect)。
