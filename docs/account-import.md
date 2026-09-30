# 账号导入

从左侧「账号管理 → 导入账号」操作。每次导入保存为 Duo 数据目录中的独立账号副本，账号列表只保存引用；不会改写来源环境的登录，不会自动设为默认或同步到其他环境。

## 从已有环境读取

选择 Codex 或 Claude Code、保存账号的本机 Windows 环境，再选择来源 Windows、WSL 或 SSH 环境。来源使用所选用户的 `CODEX_HOME` / `CLAUDE_CONFIG_DIR`，未设置时读取用户默认配置目录。

本机指运行 Duo 服务的电脑，不是打开网页的设备。WSL/SSH 必须可连接且已配置 Python 3。仅保存在系统钥匙串、外部密钥助手或进程环境变量中的凭据无法作为原生文件导入；请使用账号管理中的登录/API 配置。

## 从 JSON 文件读取

- Codex：选择 `auth.json`；使用中转站时，同时选择对应 `config.toml`，否则账号文件本身不包含 API 地址。支持 `OPENAI_API_KEY` 或包含 `access_token` 的 `tokens` 登录信息。
- Claude Code 订阅：选择含 `claudeAiOauth.accessToken` 的 `.credentials.json`。
- Claude Code API / 中转站：选择 `settings.json`，其中 `env` 包含 `ANTHROPIC_API_KEY`、`ANTHROPIC_AUTH_TOKEN` 或 `CLAUDE_CODE_OAUTH_TOKEN`。

每个上传文件不超过 256 KB。当前不支持 Cockpit Tools 等第三方管理器的批量导出格式。无法识别时会报错，不会猜测账号字段。

只保存登录、服务路由和默认模型字段，不导入 MCP、hooks、工作区权限或可执行密钥助手。Codex 命名 profile 和环境变量密钥需要先转为独立账号配置；Codex 中转站必须支持 Responses 协议。Claude 云厂商身份需在目标环境单独配置。

导入不调用模型，也不验证令牌有效期。完成后可以「设为新任务默认」，现有任务通过「切换 AI」选择；需要更改 Windows/WSL/SSH 的原生登录时，另行使用「同步到环境」。源账号文件与副本之后的刷新和退出相互独立。

## 环境与引擎

原「执行环境」「AI 引擎」「引擎安装」合并为「环境与引擎」。打开页面会检测已保存的环境，按环境列出已安装、未安装或未完成检测的 CLI。连接失败不当作未安装。

Codex、Claude Code 和 DeepSeek Harness 的未安装项提供「一键安装」。Kimi、MiMo 仍显示检测状态及文档入口，当前没有可执行适配器和一键安装。环境连接、工作目录和自定义模型保留在「管理环境、连接与工作目录」中；修改保存后重新检测。
