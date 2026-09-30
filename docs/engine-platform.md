# 引擎平台与升级设计

Duo的长期结构分成四层：

1. **工作台控制面**：任务、权限、审批、知识、飞书、硬件授权、远程访问和升级。
2. **执行目标**：Windows、WSL 发行版/用户、SSH 主机。目标保存自己的路径、登录状态和工具安装状态。
3. **引擎适配器**：Codex app-server、Claude Code CLI、DeepSeek Harness headless/SDK、Kimi ACP、MiMo ACP。引擎负责会话、流式事件、审批和取消，不负责知识库或硬件业务。
4. **模型/账号绑定**：模型是引擎下的选择；账号/API 配置只保存目标环境中的 profile 引用，不在 Duo 数据库或 Git 中保存密钥。

## 代码边界与本轮收敛

目前是一个 Go 服务加嵌入式网页，不是多个独立服务。模块仍在同一 Go package；下表是实际职责划分，不把目录拆分当成已经完成的插件化。

| 层 | 主要代码 | 不应承担的职责 |
| --- | --- | --- |
| 工作台与入口 | `app.go`、`http.go`、`feishu*.go`、`knowledge.go`、`hardware*.go` | 不保存模型密钥，不把某家原生会话当通用会话 |
| 执行环境 | `environment.go`、`runner.go`、`process_*.go` | 不管理知识库或飞书业务，不把 Windows 环境变量直接灌入 WSL |
| 引擎适配 | `codex_appserver.go`、`claude.go`、`harness.go`、`engine_registry.go` | 不承诺未实现的恢复、审批或工具能力 |
| 安装与维护 | `update_*.go`、`updates.go`、`installer/windows/` | 不更新 AI CLI、不搬动工作区、不在忙时强停服务 |

本轮优先收敛状态边界，而非全项目改写：引擎账号选择与模型目录保持一致；删除非当前账号不得清空当前绑定；模型测试与更新互斥；网页重新登录时清理旧监听并隔离旧请求；原生安装成功与后续清理分开判定，不把完整安装回滚成半安装。

后续拆包应先稳定 `ExecutionContext`（环境、引擎、账号引用、模型、权限）和运行事件契约，再逐个迁出引擎适配器。不要为尚未实现的引擎声明可运行能力，也不要引入仅改名称的通用接口。

## 当前落地

`engine_registry.go` 提供不依赖具体 CLI 的目录和配置 API：

- `GET /api/engines`：查看引擎、传输协议、能力和目标环境支持情况。
- `GET /api/engine-status`：检测已保存的 Windows/WSL/SSH 环境中的 CLI 路径和可读取的登录状态；连接失败返回未知。
- `POST /api/engine-setup`：在选定目标安装允许列表中的 CLI，或在本机创建独立账号；通过同名 GET/DELETE 接口读取状态或取消。
- `POST /api/account-sync`：按源账号所在环境读取 Codex/Claude 原生账号文件，将登录与 API 路由合并到勾选目标，保留备份和其他工具设置。
- `GET /api/environments/{id}/engines/{engine}/install-plan`：提供所选目标的安装/检查指南；允许列表中的一键安装使用独立 setup API。
- `PUT /api/engine-profiles`、`POST /api/engine-profiles/{id}/activate`：保存和切换外部账号/profile 引用。
- `POST /api/codex-sync`：在用户明确选择后，把本地可读取的 Codex `auth.json` 安全投影到 Windows、WSL 或 SSH 目标的原生 `~/.codex/auth.json`；目标先保留 `.duo-backup`，远程凭据只经标准输入传输，已运行的 app-server 需要重启。
- Codex profile 使用 `CODEX_HOME`，Claude profile 使用 `CLAUDE_CONFIG_DIR`；WSL/SSH 通过 `env` 前缀传入，Windows 通过子进程环境传入。
- `env_file` 暂时只记录引用，不由 HTTP 服务读取任意密钥文件。后续由目标适配器按最小权限读取，并在进程环境中使用，禁止出现在命令行、任务文本和事件日志。

v0.24.1 起，设置页的激活配置只作为新任务默认值。任务创建时固定 profile 引用，排队提交时固定引擎、模型和 profile 快照；正在运行和已有任务不会因全局默认改变而切到另一个目录。配置被编辑或删除时，原任务仍使用其已保存的目录引用。任务模型列表也使用任务绑定的配置，不再误读全局默认账号。

“切换 AI”在同一个 Duo 任务内修改执行配置。相同引擎、环境、目录和账号引用仅换模型时复用原生会话；换引擎、账号、执行位置或 Harness 权限时清空目标原生 session，下一条消息携带接续摘要及完整所选历史文本。记录旧配置和原生会话供诊断；不把 session ID 跨引擎传递，不复制凭据。切换本身不调用模型，运行和排队期间拒绝切换。

旧版带原生 session 的任务没有可信账号绑定，升级后首次继续需通过“切换 AI”确认配置、创建新会话并接续历史。不能根据当前默认账号猜测旧 session 归属。外部登录、密钥、API 地址仍由原生配置管理；修改同一配置目录里的账号或 provider 不属于 Duo 可隔离的行为。不同账号/API 建议使用不同配置目录。详见 [任务接续](continuation.md)。

## 接入顺序

1. Codex：继续使用原生 app-server，完整保留 thread/resume、审批、interrupt 和流式事件。
2. Claude Code：保留 CLI `stream-json`，补充稳定的取消/审批映射和 profile 检查。
3. Harness：优先 headless JSON 事件流或 SDK，不抓桌面 UI；固定一个版本并记录协议版本。
4. Kimi / MiMo：优先 ACP/CLI，桌面端仅作为人工兜底。适配器具备可验证的启动、发送和停止链路后才开放运行；恢复与审批按真实能力分别声明，不使用统一接口掩盖缺失能力。

## 升级流程

Windows 安装版和便携版共用版本查询、来源约束、SHA-256 校验和维护门禁；安装版交给 Inno Setup 管理程序与安装信息，便携版仅更新白名单内四个文件。任务、模型测试、终端或硬件忙时不得更新，进入维护后不得再启动新工作。

发布新引擎适配器时，使用同一个 Release：

- Release 上传安装 EXE、便携 ZIP 及各自的 `.sha256` 文件；
- 版本号使用 `v主版本.次版本.修订版本`；
- 适配器协议或配置迁移应幂等且可诊断；新服务已启动后不能把恢复旧 EXE 冒充数据库回滚；
- 不把 `.demo/`、`.demo-data/`、本机配置、日志、密钥或测试账号放入 Release；
- 更新完成后用 `/healthz` 确认目标版本再报告成功；失败时保留日志与恢复文件，不强杀新进程。

0.19 更名为 Duo 后，旧版须手动运行新安装器首次过渡；不删除旧数据、不重建账号。后续的引擎安装功能仍须展示执行环境、安装命令和确认步骤，不能与工作台自身升级混在一起。

## Harness ACP 接入（2026-09-28）

使用 `dsh --profile acp` 的原生会话接口，已在本机 `@deepseek-ai/dsh 0.1.5-rc.2` 验证。
启动握手要求 ACP v1 及 `session/resume`、`session/close` 能力；旧版接口不满足时明确要求升级。
模型路由保留 Windows 原生配置解析，使用 ACP 的 `session/set_config_option` 设置 provider/model，
推理强度只接受当前模型声明的选项；更换模型会清除旧的推理覆盖。

- 任务空闲时可以直接切换模型，保留同一个原生 session ID。执行中或排队中需先等待或停止。
- 首次消息调用 `session/new`；进程消失后调用 `session/resume`，恢复 Harness 自己的持久化事件日志。
  旧 SDK 任务也使用原 session ID 尝试恢复，不重放 Duo 聊天记录，不自动创建替代空白会话。
- 最多保留 16 个运行进程，闲置 30 分钟回收。回收、停止或 Duo 重启不会清空任务会话标识。
  `session/cancel` 取消当前轮，`session/close` 保存并释放会话，故障时才强制清理进程树。
- 恢复依赖原执行环境、账号目录和原生会话文件。文件缺失、损坏或不可读取时保留任务和聊天记录，
  显示恢复错误。用户仍可主动“新建会话”，保留网页记录但清空 AI 会话标识；开启自动恢复时会附带当前任务的少量知识。
- 任务详情中的 `runtime` 使用 `new/live/busy/resumable`。`resumable` 表示下次发送时尝试恢复，
  不代表已经读取或验证磁盘记录。失去进程不会阻止消息提交。
- Windows npm 入口解析为 Node 和标准 `dsh` 入口，不通过 shell 拼接任务；WSL/SSH 在目标环境运行 ACP。
- `native` 继承原生配置；`dsh_home` 指定原生账号目录。Duo 不读取、复制或返回凭据文件。
  活跃进程保持原账号；恢复时需确保原账号目录可用。
- 每次启动使用临时策略 patch 固定只读/工作区/完全访问边界，越界审批拒绝。
  不提供 ACP 客户端文件、终端或自动批准能力。关闭可选 telemetry 和 session-log-deepseek 上传。
- 当前仍不支持 Duo 附件、硬件授权、交互审批、离线网络保证和独立知识总结。
  `harness:read` 是可联网只读模式，不改变 Codex 的离线 plan 模式。
- ACP 输出正文、思考和工具更新，最终状态以对应 prompt 响应为准；不把上下文占用量冒充计费 token 用量。

测试使用 Go 子进程 fixture 验证原生 ID、路由切换、历史恢复、取消、错误、外来会话过滤和客户端权限拒绝。
`TestHarnessACPNativeLocalProvider` 使用实际安装的 Harness、临时空白 DSH_HOME 和回环模拟 OpenAI 服务，
验证原生持久化与多轮记忆，不读取用户项目或凭据、不发送付费请求。见开发文档中的 opt-in 命令。
WSL/SSH 共用协议和策略边界，本轮原生验收范围为 Windows。

真实模型测试仍必须先取得明确授权；无提示词握手只验证进程及 ACP 能力，不证明账号、余额或回复可用。
