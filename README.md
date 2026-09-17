# 简作 Jianzuo

本地运行的 AI 任务工作台。通过浏览器或飞书，把任务交给 Windows、WSL 或 SSH 环境中的 Codex CLI / Claude Code，按任务保存对话、执行记录和知识。

## 下载与开始

推荐在 [Releases](https://github.com/fenceo/jianzuo/releases) 下载 `Jianzuo-Setup-User-x64.exe`，双击按向导安装。安装只作用于当前 Windows 用户，不需要管理员权限；默认程序目录为 `%LOCALAPPDATA%\Programs\Jianzuo`，数据目录为 `%LOCALAPPDATA%\Jianzuo\data`。安装后可创建开始菜单快捷方式和可选的桌面快捷方式，并注册普通权限的当前用户登录自启任务。

也可以下载 `Jianzuo-portable-windows-x64.zip`，完整解压后双击 **简作.exe**。首次选择执行环境、工作目录并设置面板密码。程序驻留 Windows 系统托盘，可打开面板、查看日志、重启或退出。

运行环境：Windows 10/11 x64、系统 .NET Framework 4.8。使用便携包无需安装 Go、Node.js；AI 执行环境仍需自行安装并登录 Codex CLI 或 Claude Code。WSL / SSH 的文件与运行辅助功能需要对应环境中的 Python 3。

## 主要功能

- Windows / WSL / SSH 工作区弹窗选择，任务固定环境、目录、AI 工具、模型及推理强度。
- 原生会话续接，直接执行或分析规划，自定义工作模式、快捷指令、附件、开始和停止。
- 对话与执行轨迹切换、日志筛选、每轮用量和耗时；不额外强制使用子代理。
- 网页与飞书共用任务，支持扫码创建和绑定机器人、任务清单、结果卡片与面板链接。
- 任务知识、五色便签、统一搜索、重命名、归档和可恢复回收站。
- 文件浏览、Git 差异、Windows / WSL / SSH 多终端。
- 共享串口、TCP、Telnet、SSH 硬件资源，串口继电器控制，按任务授权 AI 使用硬件。
- 局域网和 Tailscale 访问，密码登录，可调整主题和布局。
- 设置中的“版本更新”检查最新正式 Release；Windows 便携版可校验、替换并自动重启，手动下载仍可使用。

## 更新

在“设置 → 版本更新”点击“检查更新”。检测到新版后，Windows 安装版和便携版都可点击“下载并自动安装”，程序会先校验 Release 提供的 SHA-256，再停止任务、替换程序并自动重启；数据目录不会被替换。其他运行方式仍可手动下载新版并保留数据。

安装器和 ZIP 都附带 SHA-256 校验文件。手动下载后可用 `Get-FileHash .\Jianzuo-Setup-User-x64.exe -Algorithm SHA256` 或 `Get-FileHash .\Jianzuo-portable-windows-x64.zip -Algorithm SHA256` 比较校验值。自动更新日志保存在数据目录的 `update.log`。

## 数据与使用范围

安装版默认把任务、配置和数据库保存在 `%LOCALAPPDATA%\Jianzuo\data`；便携版保存在 `简作.exe` 旁的 `data`。发布包不带任何账号、任务、硬件配置或凭据。复制 data 不会迁移工作目录、WSL、SSH 密钥或 AI 工具的登录会话。

服务电脑需要保持运行。其他设备通过局域网 IP 或 Tailscale 地址访问，并输入面板密码。简作没有独立公网中转服务器；请按实际需要设置网络访问范围。

飞书消息和提交给 AI 的内容会进入对应服务。简作使用各执行环境已有的 AI 登录配置。规划模式沿用原生 CLI 权限机制，不是另一套系统隔离边界。AI 硬件权限按任务单独授权。

## Windows 与 WSL

安装器会自动配置当前用户的“Jianzuo User”计划任务，在用户登录后以普通权限启动，不要求管理员服务。便携版也可从托盘启用同一任务；程序会替换旧的简作任务，避免同时启动多个目录。WSL 任务使用所选发行版和 Linux 用户自己的 `HOME`、`PATH`、Git、SSH agent、代理和登录配置，不沿用 Windows 进程的 PATH 或受限环境影响。

请从资源管理器、开始菜单或普通快捷方式启动简作。若从 Codex 或其他受限沙箱进程启动，Windows 可能拒绝访问 WSL 服务和计划任务，此时环境检测会提示权限不足；换到普通用户上下文启动即可。

完整操作说明见 [便携版使用说明](portable/使用说明.md)。开发、构建与发布见 [开发说明](docs/development.md)。

## 本地构建

需要 Windows、Go 1.27+、支持 `node:module.stripTypeScriptTypes` 的 Node.js（建议 24.10+）、npm、系统 .NET Framework C# 编译器。

```powershell
npm ci
.\scripts\Build-Portable.ps1
```

生成 `dist/Jianzuo-portable-windows-x64.zip`、`dist/Jianzuo-Setup-User-x64.exe` 及各自的 `.sha256` 校验文件。脚本会检查 TypeScript、运行前后端测试，再构建服务、托盘启动器和当前用户安装器。仓库不跟踪生成的 `web/app.js`，直接运行 Go 前先执行 `npm run build`。

第三方组件及原始许可文本包含在发布包的 `THIRD-PARTY-NOTICES.txt` 中。
