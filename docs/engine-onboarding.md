# 工具安装、账号与云端模型

在“设置 → AI 引擎”选择本机 Windows 环境，点击“一键安装工具”。支持 Codex、Claude Code、DeepSeek Harness。Duo 下载固定版本并校验 SHA-256 的 Node.js，再从官方 npm 仓库安装工具；文件放在 Duo 数据目录的独立工具目录中，不修改全局 PATH 或覆盖原有 CLI。安装成功并通过 `--version` 后才保存新路径；已有任务仍保留原执行配置，可通过“切换 AI”选用新配置。

首次实现支持 Windows x64 自动安装；WSL/SSH 继续使用已有的安装指南和环境配置。工具安装会访问网络，不下载本地模型权重。

Codex 账号配置旁提供“同步到环境”。它只读取用户选择的本地 `codex_home/auth.json`，并在用户选择的 Windows、WSL、SSH 目标中写入原生 `~/.codex/auth.json`；已有文件会先保存为 `auth.json.duo-backup`。WSL/SSH 通过标准输入传输内容，不把凭据放入命令行或日志。同步完成后，已运行的 Codex app-server 需要重启才能读取新账号；当前实现不会自动停止任务，也不会修改 `config.toml`。

Codex 可直接“添加账号 / API”：

- ChatGPT：打开设备登录链接，输入页面给出的设备码；原生 Codex 完成 OAuth 并保存令牌。
- API：填写 key，可选自定义 API 地址和默认模型。自定义服务需兼容 Responses API，具体模型能力以服务端为准。
- 每个账号使用独立 CODEX_HOME。数据库只保存引用；密钥由原生工具保存在该目录，不回传到网页、会话记录或安装状态。
- 添加后可设为新任务默认，也可在现有任务的“切换 AI”中选择。切换账号会创建新原生会话，以任务历史接续。
- Claude/Harness 的登录与 provider 配置继续使用工具自己的配置，再将配置目录作为账号/API 引用添加到 Duo。

安装、登录状态可在重新打开设置后继续查看；取消后已创建的独立目录保留，成功配置不会被失败的后续安装覆盖。服务重启后进行中的登录需重新开始。模型目录仍通过原生接口按需获取，添加账号和检查安装不会调用生成模型。

本实现直接调用官方协议，未复制 Cockpit Tools 的代码或凭据迁移逻辑。

协议依据：https://learn.chatgpt.com/docs/app-server （account/login/start、device-code flow）；安装依据：https://code.claude.com/docs/en/setup 、https://github.com/deepseek-ai/deepseek-harness 。
