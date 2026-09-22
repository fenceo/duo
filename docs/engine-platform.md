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
4. Kimi / MiMo：优先 ACP/CLI，桌面端仅作为人工兜底。适配器必须实现统一的 `start/resume/send/interrupt/approve` 生命周期后，才把 `Runnable` 打开。

## 升级流程

现有 Windows 便携版升级器已经具备下载来源约束、SHA-256 校验、暂存目录、替换前备份、启动健康检查和失败回滚。升级时只替换程序与说明文件，`data`、任务、账号引用、环境配置和工作区不动。

发布新引擎适配器时，使用同一个 Release：

- Release 上传便携包及 `.sha256` 文件；
- 版本号使用 `v主版本.次版本.修订版本`；
- 适配器协议或配置迁移在启动时做幂等迁移，并在失败时保留旧版本可回滚；
- 不把 `.demo/`、`.demo-data/`、本机配置、日志、密钥或测试账号放入 Release；
- 更新完成后用 `/healthz` 校验服务版本，再清理备份。

后续可在此基础上增加“后台下载 → 用户确认重启 → 一键回滚上一版”，但不能绕过校验或静默执行目标环境安装。
