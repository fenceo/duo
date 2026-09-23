# 引擎平台与升级设计

简作的长期结构分成四层：

1. **工作台控制面**：任务、权限、审批、知识、飞书、硬件授权、远程访问和升级。
2. **执行目标**：Windows、WSL 发行版/用户、SSH 主机。目标保存自己的路径、登录状态和工具安装状态。
3. **引擎适配器**：Codex app-server、Claude Code CLI、DeepSeek Harness headless/SDK、Kimi ACP、MiMo ACP。引擎负责会话、流式事件、审批和取消，不负责知识库或硬件业务。
4. **模型/账号绑定**：模型是引擎下的选择；账号/API 配置只保存目标环境中的 profile 引用，不在 Jianzuo 数据库或 Git 中保存密钥。

## 当前落地

`engine_registry.go` 提供不依赖具体 CLI 的目录和配置 API：

- `GET /api/engines`：查看引擎、传输协议、能力和目标环境支持情况。
- `GET /api/environments/{id}/engines/{engine}/install-plan`：生成目标环境的安装/检查计划。现在只返回安全的人工确认步骤；后续适配器稳定后再增加受限的一键执行。
- `PUT /api/engine-profiles`、`POST /api/engine-profiles/{id}/activate`：保存和切换外部账号/profile 引用。
- Codex profile 使用 `CODEX_HOME`，Claude profile 使用 `CLAUDE_CONFIG_DIR`；WSL/SSH 通过 `env` 前缀传入，Windows 通过子进程环境传入。
- `env_file` 暂时只记录引用，不由 HTTP 服务读取任意密钥文件。后续由目标适配器按最小权限读取，并在进程环境中使用，禁止出现在命令行、任务文本和事件日志。

切换账号只影响下一次运行；运行中的任务使用启动时捕获的目标、引擎、模型、权限和 profile，不会被设置页的修改中途改写。切换引擎也应创建新线程或显式 fork，不能把一个引擎的 session ID 传给另一个引擎。

## 接入顺序

1. Codex：继续使用原生 app-server，完整保留 thread/resume、审批、interrupt 和流式事件。
2. Claude Code：保留 CLI `stream-json`，补充稳定的取消/审批映射和 profile 检查。
3. Harness：优先 headless JSON 事件流或 SDK，不抓桌面 UI；固定一个版本并记录协议版本。
4. Kimi / MiMo：优先 ACP/CLI，桌面端仅作为人工兜底。适配器具备可验证的启动、发送和停止链路后才开放运行；恢复与审批按真实能力分别声明，不使用统一接口掩盖缺失能力。

## 升级流程

现有 Windows 便携版升级器已经具备下载来源约束、SHA-256 校验、暂存目录、替换前备份、启动健康检查和失败回滚。升级时只替换程序与说明文件，`data`、任务、账号引用、环境配置和工作区不动。

发布新引擎适配器时，使用同一个 Release：

- Release 上传便携包及 `.sha256` 文件；
- 版本号使用 `v主版本.次版本.修订版本`；
- 适配器协议或配置迁移在启动时做幂等迁移，并在失败时保留旧版本可回滚；
- 不把 `.demo/`、`.demo-data/`、本机配置、日志、密钥或测试账号放入 Release；
- 更新完成后用 `/healthz` 校验服务版本，再清理备份。

后续可在此基础上增加“后台下载 → 用户确认重启 → 一键回滚上一版”，但不能绕过校验或静默执行目标环境安装。

## Harness SDK 接入（2026-09-23）

当前适配本机 `@deepseek-ai/dsh 0.1.5-rc.2` 的 SDK stdio JSON-RPC，
不是此版本并不支持的 `headless --json`。模型通过 `initialize` 的
`provider/model/reasoningEffort` 传入。默认路由为 `deepseek-official/deepseek-flash`，
支持自定义模型 ID；推理为工具默认或 `off/low/high/max`。

任务详情仅对 Harness 返回顶层 `runtime`（`new/live/busy/closed`、`can_continue`、`reason`），
它是运行进程快照，不是由“已有聊天记录”推断可恢复。失效会话提交返回 409，
不创建失败消息或运行；`busy` 仍可排队。显式新建空白会话与提交/停止串行，
执行中、排队中、归档或已删除的任务不能重置。重置只清空原生会话标识，先关闭闲置进程，
不会清理历史、知识或在后台重放历史。

- Windows npm 入口使用 `dsh.cmd`，内部解析为 Node + npm 入口，不通过 cmd 拼接任务。
- WSL/SSH 使用目标环境的 `dsh`，默认路径缺失时查找该用户的 `~/.local/bin/dsh`。
- 账号由执行环境管理：`native` 继承本地默认配置，`dsh_home` 设置 `DSH_HOME`。
  简作不读取、复制或返回凭据文件。切换账号后请新建 Harness 任务。
- 同进程连续对话、已提交的正文/工具事件、token 统计及终止进程树已接入。
  这不是逐 token 输出，也不具备 Codex 的交互审批或自动风险评审。
- 一个会话保持一个进程，最多 16 个；闲置 30 分钟释放。停止、服务重启、
  闲置回收后不能原生恢复，可在原任务中显式新建空白会话，网页旧记录与知识保留，
  但不会自动带入 AI 上下文。禁止静默重放历史冒充恢复。
- 每次启动追加临时策略 patch：固定只读/工作区/完全访问边界，越界审批拒绝，
  不允许保存的原生 permission preset 覆盖。完全访问只能由用户显式选择。
  原生 Windows ACL 沙箱是有限边界，不等于强隔离虚拟机。
- `harness:read` 是明确可联网的只读模式，不改变原 `plan` 的离线语义。
  不支持离线保证、附件、自动知识总结或简作硬件授权时明确拒绝，普通笔记仍可使用。
- 启动时关闭可选 telemetry 和 session-log-deepseek 上传，不修改用户原生配置。

验证：Go 子进程 fixture 覆盖双轮、取消、错误、子会话隔离、回执乱序及策略；
HTTP 成品验收覆盖登录、CSRF、创建和模型配置约束；前端测试覆盖模型和模式选择。
本机 WSL 已通过真实 `initialize + shutdown`（隔离配置，无模型请求）。
本机 Windows 官方 SDK 在有/无策略 patch 时均未完成握手，仍需排查，不能视为可用。
经用户授权，已使用 WSL 原生配置完成两轮真实最小文字验收：第一轮返回指定标记，
第二轮未再次提供标记仍准确复述；同一 native session、同一 SDK worker，零工具事件。
测试工作目录独立于用户项目。仅验证所选路线，不代表其它模型/账号都可用。
验收同时发现并修复两类配置误判：应使用原生实际 provider/model，不能把网关路线
当作官方直连；没有声明推理档位的路线必须省略 reasoningEffort，而不是传入 `off`。
基础检查仍然只做 SDK 握手，不代表密钥、余额和模型调用已验证。
SSH 真机和硬件操作尚未验证。

可选原生检查：设置 `JIANZUO_TEST_HARNESS_NATIVE=1`，WSL 再设置
`JIANZUO_TEST_HARNESS_WSL_DISTRO=Ubuntu-22.04`，运行
`go test -run TestHarnessSDKNativeHandshake -v`。它使用临时配置和虚拟凭据，
不会发送 `session/prompt`。仅验证 WSL 时使用子测试选择器
`-run TestHarnessSDKNativeHandshake/wsl`。

真实双轮验收另有 `TestHarnessSDKPaidWSLRoundTrip`，默认跳过。只有获得用户
明确授权后，设置 `JIANZUO_TEST_HARNESS_PAID_WSL=1`、
`JIANZUO_TEST_HARNESS_WSL_DISTRO`、`JIANZUO_TEST_HARNESS_WSL_USER`、
`JIANZUO_TEST_HARNESS_PROVIDER` 和 `JIANZUO_TEST_HARNESS_MODEL` 才运行。
它使用隔离的临时工作目录和原生凭据；第一轮失败立即终止，不切换模型重试。
凭据应在对应执行环境的 Harness 原生配置中设置，不要填写到任务或上传到 Git。
通用 provider 未声明推理档位时必须选择“工具默认”（不发送 reasoningEffort），
不能把 `off` 当作对所有路线都通用的默认值。
