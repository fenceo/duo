# 个人工作台与 AI 引擎

Duo 是个人工作台，统一管理任务、对话、执行环境和工具入口。引擎适配器调用各家的官方 CLI 或原生协议，不把模型 API 包装成某家 CLI，也不抓取桌面界面作为执行协议。

GPT 是 OpenAI 的模型系列，Codex 是调用模型、管理原生会话并执行工具的引擎。用户在新建任务时选择引擎，再选择该引擎和环境可用的模型。

| 引擎 | 执行协议 | 安装与环境 | 认证与配置 | 权限 |
| --- | --- | --- | --- | --- |
| Codex | 官方 CLI 的 app-server | Windows / WSL / SSH 检测与一键安装 | ChatGPT 设备码登录、官方 API Key 或兼容 Responses 的服务；账号卡片切换到环境 | 使用 Codex 原生权限和自动风险评审 |
| DeepSeek Harness | 官方 `dsh --profile acp` | Windows / WSL / SSH 检测与一键安装 | 每环境配置 API 地址、Key，验证认证并读取模型列表；不提供订阅账号登录 | 官方工作区审批与完全访问两档；支持批准或拒绝本次 |
| Claude Code | 官方 CLI 的 stream-json | Windows / WSL / SSH 检测与一键安装 | API 配置（含 Z.ai / GLM 的 Anthropic 兼容预设）；已有订阅登录可引用和切换 | 按现有 CLI 适配器的能力展示 |
| MiMo Code | 官方 CLI 的 `mimo acp` | Windows / WSL / SSH 检测与一键安装 | 每环境配置 API 地址、协议、Key，验证后读取模型目录；不提供账号登录 | 原生 Build/Plan/allow 档位；支持批准或拒绝本次；授权设备后按轮挂载 Duo 硬件工具 |
| OpenCode | 官方 CLI 的 `opencode acp` | Windows / WSL / SSH 检测与一键安装 | 引用环境中原生 `opencode auth login` 的登录状态；Duo 不保存 Key | 无原生档位；只读模式自动拒绝工具授权请求，其他模式单次批准或拒绝 |
| Kimi Code | 预留 ACP 适配器 | 仅检测已配置 CLI，提供安装指引 | 引用已有原生配置 | 尚未开放任务运行 |
| ZCode（Z.ai / GLM） | 官方 `zcode` CLI 或 ZCode 桌面内置 CLI | 仅检测 CLI；未开放安装 | 引用原生 `zcode login` 的 Z.AI OAuth 登录状态；Duo 不保存 Key | 尚未开放任务运行；当前请用 Claude Code 的 Z.ai/GLM 预设或 OpenCode 的原生供应商调用 GLM |

## 环境与账号

账号属于执行环境，不属于任务。Windows、WSL 的不同发行版/用户、SSH 的不同主机/用户分别管理工具与原生配置。

- “引擎安装”检测环境并安装 CLI。安装完成不代表已登录或 API 服务可用。
- “账号管理”保存 Codex/Claude 的账号库存。v0.34.10 起新增账号可选择 Windows、WSL 或 SSH；Codex 在所选环境运行设备码登录或 API 登录。
- 新增账号保存在独立的原生目录，避免覆盖当前环境的登录。Windows 使用 Duo 数据目录，WSL/SSH 使用所选用户的 `~/.local/share/duo/engine-accounts/<请求标识>`，需要 Python 3。
- 在账号卡片点击“切换到环境”，将所选账号应用到该环境的原生目录。该环境后续启动的 CLI 读取它；原有进程何时刷新账号由原生工具决定。
- Harness 在对应环境点击“配置 API”，保存到该环境的原生 provider，验证地址与 Key 并读取模型列表。原生目录引用仅选择配置保存位置，不代表另有订阅账号登录方式。
- 新增或切换账号不修改任务、会话、排队消息，也不自动复制或回放对话历史。

## 对齐范围

对齐各家原生工具的会话、流式过程、模型、审批、停止与恢复语义，界面统一由 Duo 提供。只有已接入并验证的能力才显示可用；不承诺所有官方桌面功能已经齐全。

Codex 的 API 登录和 ChatGPT 订阅登录有不同的计费与功能范围。兼容 API 服务必须支持该 CLI 所需协议，仅保存 Key 不代表已经验证模型回复。真实模型调用需由用户明确发起。

Harness 已接入单次审批、附件和 Duo 桌面/硬件工具（HTTP MCP；任务未授权设备时本轮不带工具运行）。PNG/JPEG/WebP/GIF 使用 ACP 原生图片块，要求 CLI 握手与所选模型均声明图片输入能力；未声明时明确拒绝，不把图片路径冒充看图结果。小型 UTF-8 文本直接进入原生会话，其他文件传到目标环境，由 CLI 按原权限读取。仍不支持 Codex 自动风险评审。

任务已添加硬件设备但没有保存“AI 使用”授权时，对话里会先说明本轮不带硬件工具运行、授权从下一轮生效；只读模式同样说明不接入控制工具。这样“没有工具”不会看起来像设备掉线。授权、收回与并发控制的细节见 [桌面共享](desktop-sharing.md)。

MiMo Code 已接入原生 ACP 的创建/恢复、流式输出、模型切换、单次审批，以及 Duo 桌面/硬件工具（HTTP MCP）：任务授权后按轮声明工具地址与凭据。MiMo 的原生 ACP 是否声明 HTTP MCP 由 CLI 版本决定，未声明时明确拒绝本次执行而不是在缺少工具的情况下运行。独立知识整理和无工具推理测试仍未开放。Kimi Code 的可运行适配器仍待实现。

OpenCode 通过 `opencode acp` 接入：原生会话创建与恢复、模型切换（目录来自 `opencode models` 与本机显式配置）、附件与图片、单次审批、取消，以及 Duo 桌面/硬件工具。OpenCode 不提供 ACP 权限档位，因此只读任务以自动拒绝工具授权请求实现，其他任务在选择完全访问时也仍会为每次工具请求确认；这不等于操作系统沙箱或禁用联网保证。独立知识整理尚未接入。

ZCode 是智谱 Z.ai / GLM 的官方 CLI，随 ZCode 桌面安装（Windows 上是 `resources/glm/zcode.cjs`，需要 Node；也可自行放置 `zcode` 到 PATH）。Duo 目前只检测它是否可用，不安装、不登录、不运行任务：它的自有协议、会话存储与 `--prompt/--json` 输出结构尚未在真实模型调用下核实，只有核实后才能按“已接入并验证”的标准开放运行。要用 GLM 完成任务，请用 Claude Code 的 Z.ai/GLM（Anthropic 兼容）预设，或在目标环境用 `opencode auth login` 配置 Z.ai 后选择 OpenCode，模型目录由 `opencode models` 读取，Duo 不保存 Key。
