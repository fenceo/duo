# 开发与发布

## 项目结构

- 根目录 Go：HTTP、SQLite、任务队列、Codex / Claude CLI、飞书、终端、硬件和版本检查。
- `web/`：TypeScript 和 CSS；`scripts/build.mjs` 生成供 Go 嵌入的 `app.optimized.js`。
- `portable/Launcher.cs`：Windows 托盘、首次配置、启动与退出。
- `installer/windows/Jianzuo.iss`：Inno Setup 当前用户安装、同目录升级/修复和原生卸载。
- `installer/windows/JianzuoMaintenance.cs`：路径/归属校验、旧入口迁移和启动任务事务，不负责程序文件安装。
- `installer/windows/JianzuoSetup.cs`：旧自制安装器源码，保留作兼容参考，不再作为发布安装器构建。
- `scripts/Build-Portable.ps1`：检查、测试、构建便携包和安装器，并生成 SHA-256 校验文件。
- `scripts/Install-Startup.ps1`：旧兼容入口已停用，仅提示使用安装器或托盘；不再直接覆盖计划任务。

## 检查

```powershell
npm ci
npm run check
npm run build
go test ./... -count=1
node scripts/Test-Updates-Web.mjs
```

完整 Windows 发布包使用 `scripts/Build-Portable.ps1`。构建依赖 Go 1.27+、支持 TypeScript 类型去除的 Node.js、npm、Windows .NET Framework C# 编译器及 Inno Setup 6.7.3。先运行 `scripts/Setup-InnoToolchain.ps1` 可将固定版本编译器以便携模式放到项目上级 `.tools`，不创建系统入口；脚本验证下载 SHA-256 和 Pyrsys B.V. 发布者签名，并固定官方源码版本中的中文语言资源校验值。运行包不需要这些构建工具。

`scripts/Build-Installer.ps1` 单独构建 Inno 安装器：白名单解开便携 ZIP，编译维护 helper，再由 Inno 原生管理 Files / Icons / Registry / uninstaller。新安装器无副作用校验参数为 `/VERIFY=1`；不要把旧自制安装器的 `--verify` 或 `--quiet` 参数用于新包。

`scripts/Test-Launcher.ps1` 和 `scripts/Test-Installer.ps1` 覆盖参数兼容、真实内存 COM 索引读取、路径/归属、迁移确认及数据保留边界；应使用正常用户上下文的 Windows PowerShell / .NET Framework，受限沙箱可能无法访问任务计划服务。生产安装器的 Full 安装卸载测试仍仅允许一次性 Windows VM/测试用户，不在日常账号运行。命名空间隔离测试构建必须为 AppId、任务、注册表、入口使用独立 GUID，安装及数据目录在专用临时目录内，禁止启动真实服务；不得伪称它覆盖了另一台真实电脑的所有环境差异。

`data`、`dist`、`build`、`_backups`、`_testdata`、本机部署脚本与验收资料不进入 Git。测试用密码与回环地址只是合成测试数据。

数据加载验证器 `duo-service.exe --validate-data --data <directory>` 在任何目录创建、配置加载或数据库迁移之前运行。它仅输出固定成功协议或不含凭据的错误，不初始化、不修复数据。调用方必须保护源/目标数据在验证至安装期间不被运行实例打开。SQLite 以只读 immutable 模式检查，非空 WAL/recovery journal 会拒绝并要求原程序正常退出，避免忽略未落盘内容；不能删除 WAL 来绕过检查。安装器只调用发布包内的验证器，不执行用户数据目录中的程序。

交互安装可显式选择保留、全新或载入数据。静默更新不得改变 data；原程序安装目录不能在维护向导中变更。对新建/载入模式应使用独立 GUID 安装身份验证旧数据保留、已有数据哈希不变、活动锁拒绝、失败回滚与无参数默认路径。不得把用户现有安装作为自动化卸载测试对象。

## 工作流与浏览器验收

提交前运行所有网页回归，而不只是布局检查：

```powershell
Get-ChildItem scripts/Test-*-Web.mjs | ForEach-Object {
  node $_.FullName
  if ($LASTEXITCODE -ne 0) { throw "Web regression failed" }
}
```

`Test-Task-Workflow-Web.mjs` 覆盖重复提交锁、创建部分失败的草稿与附件保留、
仅重试未完成的上传、切换任务后的异步结果隔离，以及空白会话重置。
`Test-Model-Probe-Web.mjs` 覆盖列表模型的明确范围确认、空列表拒绝、逐项流式结果、
停止与中断、重复点击锁和过期测试结果隔离。以上只使用内存 fixture，不消耗模型额度。

需要在真实浏览器点击验收时，显式启动隔离 HTTP fixture：

```powershell
$env:JIANZUO_TEST_UI_FIXTURE='1'
go test -run '^TestUIFixture$' -count=1 -v -timeout=11m
Remove-Item Env:\JIANZUO_TEST_UI_FIXTURE
```

启动后终端输出临时回环地址和合成密码。任务、知识、配置和工作区均使用测试临时目录；
普通文字返回模拟回复，`[wait]` 开头的消息等待停止，`[fail]` 开头模拟执行失败。
预置失效 Harness 会话可验收“新建空白会话”后保留旧记录/知识/草稿。
fixture 不启动 CLI、不读取真实模型缓存、不调用模型；列表测试仅返回延迟的合成成功/失败/超时，设备和配置写入被禁止；
最多运行 10 分钟，也可用登录 cookie、Origin 和 CSRF 提交 `POST /__fixture/finish` 提前结束。
它只存在于 `_test.go`，不进入发行程序。不要把该开关带入普通全量测试环境。

浏览器至少检查 1280×800 和 390×844 下的创建页、会话页、浅/深色设置页，
确认自定义模型可选择、模型按钮没有错误截断、重置提示不会冒充原生恢复。

## Windows 与 WSL 进程约定

Windows Harness 的无提示词原生握手检查可在正常用户上下文运行。该测试只发送 `initialize` / `shutdown`，不创建会话或消耗模型额度；默认跳过。模型必须是本机 DSH 配置中的 ID，provider 留空用于验证 Duo 的自动路由：

```powershell
$env:DUO_HARNESS_WINDOWS_HANDSHAKE='1'
$env:DUO_HARNESS_MODEL='<本机已配置模型 ID>'
$env:DUO_HARNESS_BINARY='<本机 dsh.cmd 的绝对路径>'
go test -run '^TestHarnessSDKNativeWindowsHandshake$' -count=1 -v
Remove-Item Env:DUO_HARNESS_WINDOWS_HANDSHAKE,Env:DUO_HARNESS_MODEL,Env:DUO_HARNESS_BINARY
```

可用 `DUO_HARNESS_PROVIDER` 验证显式路由，`DUO_HARNESS_EFFORT` 验证特定推理强度；握手成功不代表密钥、余额或实际回复已验证。

- 调用 `wsl.exe` 时只传递 `SystemRoot` 和 `WINDIR`。不要把 Windows 的完整 `PATH`、代理变量或受限沙箱变量传给 WSL；Linux 命令必须使用所选用户的登录环境。
- 从 `command` 或 `environmentProbeCommand` 创建带超时的子进程时，统一使用 `commandWithContext`，以保留最小宿主环境和 `Dir`。
- Windows 自启使用当前用户的 `Jianzuo User` 计划任务。配置一致时应复用现有任务，不从受限进程强制重注册。
- WSL、计划任务相关测试必须在普通 Windows 用户上下文运行；Codex 沙箱账户通常没有 `wsl.exe` 和任务计划服务所需权限。

## 发布新版本

维护者约定：每轮完成的功能优化都发布正式 Release，不以仅推送提交或本地体验构建代替。当前任务明确要求暂不发布时例外；测试失败或发布受阻时说明原因，不跳过校验。

1. 同步修改 `portable.go`、`package.json` 和 `package-lock.json` 中的版本号。成品验收脚本、安装器从 package.json 读取版本，启动器不维护独立版本常量。
2. 添加 `docs/releases/vX.Y.Z.md`，描述变更与迁移注意事项。
3. 完成测试和源码提交；确认 GitHub CLI 已登录目标仓库账号。
4. 运行 `scripts/Publish-Release.ps1`。脚本构建，推送分支和精确标签，创建草稿并上传便携 ZIP、安装器及两个 SHA-256 文件，检查大小后发布正式 Release。

脚本不会覆盖已发布的版本；失败后保留草稿供检查。公开更新接口只识别 `vX.Y.Z` 正式版本，草稿和 prerelease 不作为升级推荐。

发布会运行全部 `Test-*-Web.mjs`、TypeScript 检查和完整 Go 测试；构建后再次检查 Git 工作区，禁止发布与标签源码不一致的生成资源。随后核对服务 `--version` 并运行不调用模型的成品 HTTP 验收。最终应确认正式 Release 为 latest、四个资产及大小正确，并将 Release 链接反馈给维护者。

## 更新检查

`updates.go` 中的默认仓库指向正式上游。用户可在设置中覆盖为另一个公开 GitHub 仓库。

检查由服务端请求 GitHub Releases API，超时 12 秒，结果短期缓存 30 秒。仅在用户点击检查时访问 GitHub；仓库输入与返回下载链接经过限定，下载重定向也只允许 GitHub 发布资产域名。检查状态包括有新版、已最新、本机较新、没有正式发布及网络失败。

发布资产固定为 `Duo-portable-windows-x64.zip`、其 `.sha256`、`Duo-Setup-User-x64.exe` 和其 `.sha256`。安装 EXE 是主要下载入口。0.19 品牌迁移更换了资产和程序文件名，旧版简作必须手动运行 EXE 首次迁移，不宣称旧更新器可以自动升级。已有数据库、安装 AppId/注册表/任务名/所有权标记保持兼容，不能机械替换这些持久化身份。版本号按数值比较；核心版本不再含安装类型后缀，API 单独返回 `installation_mode`。健康接口保留既有协议标识，新增 `release_version` 表示实际版本。

`POST /api/updates/install` 保留登录、CSRF 和忙状态检查。模式识别核验程序绝对路径、普通文件和绑定本路径的安装标记；坏标记不降级为便携版。安装版下载 EXE，校验 SHA-256、PE/Inno 格式及最低协议版本，由外部 helper 等待原进程退出并持有数据锁，以 `/VERYSILENT /SUPPRESSMSGBOXES /NORESTART /UPDATE=1 /NOLAUNCH /DIR=... /DATADIR=... /LOG=...` 运行同一安装器。安装器仅允许原目录、原数据的升级，不允许静默接管旧便携入口。便携版继续校验 ZIP 白名单并更新四个运行文件。

维护前原子检查 AI 队列、终端及硬件连接，期间拦截网页、飞书和 WebSocket 新工作；失败释放维护状态，不强杀进程。网页断线后只有健康接口的目标版本匹配才刷新，超时显示未确认。更新日志在 `data/update.log`，安装版另有 `data/update-installer.log`。安装元数据或新版数据库可能已改变时，不宣称仅恢复旧 EXE 就完成安全回退；失败时保留日志和数据，提供手动修复路径。新装、手动升级、修复共用 EXE，旧版用户首次迁移原生安装体系也应使用 EXE，而不是用 ZIP 覆盖安装目录。
