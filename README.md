# Duo

本地运行的 AI 任务工作台。通过浏览器或飞书，把任务交给 Windows、WSL 或 SSH 环境中的 Codex、Claude Code 或 DeepSeek Harness，按任务保存对话、执行记录和知识。

Duo 原名“简作 / Jianzuo”。对外名称和发布包统一改为 Duo，已有数据与账号配置继续使用。

## 下载与开始

**[下载 Windows 安装版（新装／升级通用）](https://github.com/fenceo/duo/releases/latest/download/Duo-Setup-User-x64.exe)** · [版本说明与其他下载](https://github.com/fenceo/duo/releases)

日常使用推荐安装版。从 0.19 起，Inno Setup 安装向导统一管理安装、同目录升级/修复和卸载，只作用于当前 Windows 用户，不需要管理员权限。新装默认程序目录为 `%LOCALAPPDATA%\Programs\Duo`，数据目录为 `%LOCALAPPDATA%\Duo\data`；已有安装沿用原程序和数据目录，不因改名移动数据。桌面入口和登录自启可选择，未知旧入口不会被直接覆盖。

也可以下载 `Duo-portable-windows-x64.zip`，完整解压后双击 **Duo.exe**。首次选择执行环境、工作目录并设置面板密码。程序驻留 Windows 系统托盘，可打开面板、查看日志、重启或退出。

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
- 设置中的“版本更新”显示安装方式并检查最新正式 Release；安装版使用安装包更新，便携版使用 ZIP 更新，完成后核验版本并重连。

## 更新

新装、手动升级和修复使用同一个 EXE，不需要寻找单独的“升级包”。**从旧版简作首次转入 Duo，请下载并运行上方安装包，不要用 ZIP 直接覆盖旧安装。** 新发布包使用 Duo 文件名，旧更新器不识别这些文件，不能自动完成这次品牌迁移。

此后在“设置 → 版本更新”点击“检查更新”。安装版校验并运行完整安装器，维护安装信息；便携版使用 ZIP 更新运行文件。其他运行方式提供手动下载。

执行任务、终端或硬件连接未结束时拒绝更新，不强制中断工作。准备更新期间阻止新工作进入；成功重启后，网页核对目标版本再刷新。下载失败可重试，重连超时不代表更新成功，请查看日志。程序更新不迁移、上传或重置工作台数据，也不更新 WSL / AI CLI / SSH 凭据。重要数据仍建议退出服务后完整备份；程序文件回退不等于数据库降级。

安装器和 ZIP 都附带 SHA-256 校验文件。手动下载后可用 `Get-FileHash .\Duo-Setup-User-x64.exe -Algorithm SHA256` 或 `Get-FileHash .\Duo-portable-windows-x64.zip -Algorithm SHA256` 比较校验值。自动更新日志保存在数据目录的 `update.log`。

## 数据与使用范围

新安装版默认把任务、配置和数据库保存在 `%LOCALAPPDATA%\Duo\data`；便携版保存在 `Duo.exe` 旁的 `data`。已有安装仍使用原数据目录。数据库文件名 `jianzuo.db`、安装所有权标记及 `Jianzuo User` 启动任务属于兼容标识，不因改名重建。发布包不带任何账号、任务、硬件配置或凭据。复制 data 不会迁移工作目录、WSL、SSH 密钥或 AI 工具的登录会话。

服务电脑需要保持运行。其他设备通过局域网 IP 或 Tailscale 地址访问，并输入面板密码。Duo没有独立公网中转服务器；请按实际需要设置网络访问范围。

飞书消息和提交给 AI 的内容会进入对应服务。Duo使用各执行环境已有的 AI 登录配置。规划模式沿用原生 CLI 权限机制，不是另一套系统隔离边界。AI 硬件权限按任务单独授权。

## Windows 与 WSL

安装器可配置当前用户的“Jianzuo User”计划任务，在用户登录后以普通权限启动，不要求管理员服务。便携版也可从托盘启用自启；发现其他目录的入口时不直接覆盖。迁移旧便携版请先退出旧程序，再在安装向导中核对来源、原数据目录并明确确认；保留原数据和旧文件，未知归属任务仍拒绝接管。WSL 任务使用所选发行版和 Linux 用户自己的 `HOME`、`PATH`、Git、SSH agent、代理和登录配置，不沿用 Windows 进程的 PATH 或受限环境影响。

请从资源管理器、开始菜单或普通快捷方式启动Duo。若从 Codex 或其他受限沙箱进程启动，Windows 可能拒绝访问 WSL 服务和计划任务，此时环境检测会提示权限不足；换到普通用户上下文启动即可。

完整操作说明见 [便携版使用说明](portable/使用说明.md)。开发、构建与发布见 [开发说明](docs/development.md)。

## 本地构建

需要 Windows、Go 1.27+、支持 `node:module.stripTypeScriptTypes` 的 Node.js（建议 24.10+）、npm、系统 .NET Framework C# 编译器及 Inno Setup 6.7.3。只构建者需要这些工具，最终用户不需要。

```powershell
npm ci
.\scripts\Setup-InnoToolchain.ps1
.\scripts\Build-Portable.ps1
```

生成 `dist/Duo-portable-windows-x64.zip`、`dist/Duo-Setup-User-x64.exe` 及各自的 `.sha256` 校验文件。脚本会检查 TypeScript、运行前后端测试，再构建服务、托盘启动器和当前用户安装器。前端构建会生成供 Go 嵌入的 `web/app.optimized.js`，直接运行 Go 前先执行 `npm run build`。

第三方组件及原始许可文本包含在发布包的 `THIRD-PARTY-NOTICES.txt` 中。
