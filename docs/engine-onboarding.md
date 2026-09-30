# 工具安装、账号与云端模型

工作台提供独立的“账号管理”“引擎安装”“桌面共享”入口；账号管理中的登录、API 添加和目录引用表单不再混在安装区域。

“引擎安装”中可检测全部已保存的 Windows、WSL、SSH 环境。检测区分未安装、已有登录配置、需要登录及未完成检测，不调用模型。连接失败不会报告成“未安装”；Harness/Kimi/MiMo 只检测 CLI 文件存在，不宣称账号可用。

自动安装支持 Codex、Claude Code、DeepSeek Harness。Windows 下载固定版本并校验 SHA-256 的 Node.js，再从官方 npm 仓库安装工具，放在 Duo 数据目录的独立目录中。WSL/SSH 在指定用户下使用已有 Node.js/npm，执行允许列表中的 `npm install --global --prefix "$HOME/.local/share/duo/engine-tools/<操作标识>" --registry=https://registry.npmjs.org <官方包>@latest`；不使用 sudo、不覆盖全局 CLI、不修改 PATH。目标需能联网；Node.js/npm 缺失时明确失败。成功运行 `--version` 后才保存路径；已有任务通过“切换 AI”选用新路径。当前不自动安装 Kimi/MiMo，也不安装桌面版或本地模型权重。

账号管理中的“切换到环境”将所选账号的登录和 API 服务配置写入勾选环境的原生目录，不改任务或会话。源目录和目标均可以位于 Windows、WSL 或 SSH；WSL/SSH 需有 Python 3 和已配置的连接。不在网页、命令行或日志回传密钥。

- Codex：复制 `auth.json`，合并 `config.toml` 的当前服务、API 地址、默认模型和文件凭据存储设置；保留目标 MCP、权限等设置以及未选中的服务定义。源使用命名 profile 或外部 `env_key` 时拒绝，需先转换成独立账号目录。
- Claude Code：支持文件形式的 claude.ai 订阅登录，以及 `settings.json` 中的 API/中转站环境变量；切换回订阅时清空旧 API 路由，切换到 API 时备份并移除旧订阅凭据。保留目标 hooks、权限等无关设置。Console 无 key 登录、WIF/命名身份和云厂商身份不在此同步范围。
- 每个目标创建独立 `.duo-account-backup-*` 备份目录；写入前检查文件是否被其他进程修改，失败时尝试恢复，保留备份供检查。不同目标分别报告结果，不承诺跨多台机器的原子事务。
- 目标遵循其 `CODEX_HOME` / `CLAUDE_CONFIG_DIR`（若设定），否则使用用户的 `.codex` / `.claude`。外部 shell 或项目设置可能覆盖原生账号设置，需在目标 CLI 中核对实际生效配置。
- 切换不强停或重启任务，不检查任务是否运行/排队，不修改任务账号绑定或会话。原生 CLI 是否即时读取新登录由该工具决定，必要时重新打开。

旧版 `POST /api/codex-sync` 保留仅同步 auth.json 的兼容行为；新版界面使用 `POST /api/account-sync` 同步账号和服务配置。

Codex 可直接“添加账号 / API”：

- ChatGPT：打开设备登录链接，输入页面给出的设备码；原生 Codex 完成 OAuth 并保存令牌。
- API：填写 key，可选自定义 API 地址和默认模型。自定义服务需兼容 Responses API，具体模型能力以服务端为准。
- 每个账号使用独立 CODEX_HOME。数据库只保存引用；密钥由原生工具保存在该目录，不回传到网页、会话记录或安装状态。
- 添加后可选择“切换到环境”。账号独立于任务，不触发新会话或历史接续。
- Claude 可直接添加 API / 中转站配置，使用 Anthropic Messages 协议，写入独立 `CLAUDE_CONFIG_DIR/settings.json`。添加向导目前在 Windows 保存账号，再同步到其他环境。订阅登录先使用官方工具登录到独立目录，再添加目录引用。
- Harness 的登录与 provider 配置继续使用工具自己的配置，再添加目录引用。

安装、登录状态可在重新打开设置后继续查看；取消后已创建的独立目录保留，成功配置不会被失败的后续安装覆盖。服务重启后进行中的登录需重新开始。模型目录仍通过原生接口按需获取，添加账号和检查安装不会调用生成模型。

本实现直接调用官方协议，未复制 Cockpit Tools 的代码或凭据迁移逻辑。

协议依据：https://learn.chatgpt.com/docs/app-server （account/login/start、device-code flow）；安装依据：https://code.claude.com/docs/en/setup 、https://github.com/deepseek-ai/deepseek-harness 。
