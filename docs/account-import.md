# 账号导入

从左侧「账号管理 → 导入账号」操作。每次导入保存为 Duo 数据目录中的独立账号副本，账号列表只保存引用；不会改写来源环境的登录，不会自动设为默认或同步到其他环境。

## 从已有环境读取

选择 Codex 或 Claude Code、保存账号的本机 Windows 环境，再选择来源 Windows、WSL 或 SSH 环境。来源使用所选用户的 `CODEX_HOME` / `CLAUDE_CONFIG_DIR`，未设置时读取用户默认配置目录。

本机指运行 Duo 服务的电脑，不是打开网页的设备。WSL/SSH 必须可连接且已配置 Python 3。仅保存在系统钥匙串、外部密钥助手或进程环境变量中的凭据无法作为原生文件导入；请使用账号管理中的登录/API 配置。

## 从 JSON 文件读取

- Codex：选择 `auth.json`；使用中转站时，同时选择对应 `config.toml`，否则账号文件本身不包含 API 地址。支持 `OPENAI_API_KEY` 或包含 `access_token` 的 `tokens` 登录信息。
- Claude Code 订阅：选择含 `claudeAiOauth.accessToken` 的 `.credentials.json`。
- Claude Code API / 中转站：选择 `settings.json`，其中 `env` 包含 `ANTHROPIC_API_KEY`、`ANTHROPIC_AUTH_TOKEN` 或 `CLAUDE_CODE_OAUTH_TOKEN`。

原生文件每个不超过 256 KB。管理器批量导出使用下面的独立导入方式。

只保存登录、服务路由和默认模型字段，不导入 MCP、hooks、工作区权限或可执行密钥助手。Codex 命名 profile 和环境变量密钥需要先转为独立账号配置；Codex 中转站必须支持 Responses 协议。Claude 云厂商身份需在目标环境单独配置。

导入不调用模型，也不验证令牌有效期。完成后可以「设为新任务默认」，现有任务通过「切换 AI」选择；需要更改 Windows/WSL/SSH 的原生登录时，另行使用「同步到环境」。源账号文件与副本之后的刷新和退出相互独立。

## Cockpit Tools 导出 JSON

在「导入方式」选择「Cockpit Tools 导出 JSON（可批量）」，选择导出文件所属引擎和 JSON，点击「解析账号」。预览显示名称、账号类型和不支持的原因；勾选后点击「导入勾选账号」。名称优先来自导出的 `account_name` / `name` / `email`。

- 支持单个对象、账号数组和 `{ "accounts": [...] }` 包装。文件最多 1 MB、每次最多 50 个条目；保存后的账号总数最多 50 个。不同引擎的导出文件分别导入。
- Codex：支持 Cockpit Tools 格式的平铺订阅令牌，以及旧版带 `tokens` 的账号记录；API 支持 `OPENAI_API_KEY` / `openai_api_key` 与 `api_base_url`。保留文件提供的服务地址和默认模型；Codex 中转站需支持 Responses。
- Claude Code：支持 `claude_credentials_raw.claudeAiOauth` 的 OAuth / Setup Token，及 `api_key`、`api_base_url`、`api_key_field` 的 API 账号。只保留允许的模型与请求头环境变量，不复制任意执行环境配置。
- 不支持的条目禁止勾选，可单独导入其余有效条目。Agent Identity、仅含 access token 的 Codex Personal Access Token、Claude 桌面专用身份、管理器专用网关/CDP/模型映射和云厂商身份需另行配置。不要选择 Sub2API 等其他导出协议；其他管理器只有字段符合这些已知格式时才可导入。

解析只处理文件，不调用模型或验证账号；预览响应不包含令牌。原始 JSON 仅在页面操作期间保留内存，不进入任务对话或浏览器存储。批量保存只创建独立账号副本，失败回滚本次新建目录，不改写已有账号。相同提交的网络重试复用请求标识，不重复创建或覆盖已刷新的凭据；重新解析则视作新的导入操作。分组、标签、密码备注及管理器设置不迁移。

字段适配依据 Cockpit Tools 提交 `4ea6a34df6aa3b2d494c3cca5983bd09a84a9e3a` 的 [Codex 导出结构](https://github.com/jlcodes99/cockpit-tools/blob/4ea6a34df6aa3b2d494c3cca5983bd09a84a9e3a/src/utils/codexExportFormats.ts) 与 [Claude 账号结构](https://github.com/jlcodes99/cockpit-tools/blob/4ea6a34df6aa3b2d494c3cca5983bd09a84a9e3a/src-tauri/src/models/claude.rs)。独立实现字段转换，使用合成凭据回归；未用真实账号验证登录。

## 环境与引擎

原「执行环境」「AI 引擎」「引擎安装」合并为「环境与引擎」。打开页面会检测已保存的环境，按环境列出已安装、未安装或未完成检测的 CLI。连接失败不当作未安装。

Codex、Claude Code 和 DeepSeek Harness 的未安装项提供「一键安装」。Kimi、MiMo 仍显示检测状态及文档入口，当前没有可执行适配器和一键安装。环境连接、工作目录和自定义模型保留在「管理环境、连接与工作目录」中；修改保存后重新检测。
