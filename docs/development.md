# 开发与发布

## 项目结构

- 根目录 Go：HTTP、SQLite、任务队列、Codex / Claude CLI、飞书、终端、硬件和版本检查。
- `web/`：TypeScript 和 CSS；`scripts/build.mjs` 生成供 Go 嵌入的 `app.optimized.js`。
- `portable/Launcher.cs`：Windows 托盘、首次配置、启动与退出。
- `installer/windows/JianzuoSetup.cs`：Windows 当前用户安装器、快捷方式、数据目录和计划任务。
- `scripts/Build-Portable.ps1`：检查、测试、构建便携包和安装器，并生成 SHA-256 校验文件。

## 检查

```powershell
npm ci
npm run check
npm run build
go test ./... -count=1
node scripts/Test-Updates-Web.mjs
```

完整 Windows 便携包使用 `scripts/Build-Portable.ps1`。构建依赖 Go 1.27+、支持 TypeScript 类型去除的 Node.js、npm，以及 Windows .NET Framework C# 编译器。运行包不需要这些构建工具。

`scripts/Build-Installer.ps1` 单独构建安装器并把便携 ZIP 嵌入 EXE；`scripts/Test-Installer.ps1` 校验内嵌包、PE 头、安全契约和 SHA-256。需要覆盖安装和卸载流程时，可运行 `Test-Installer.ps1 -Full`；该模式使用隔离目录和 `--no-shortcuts --no-registry --no-startup`，不会改动当前用户注册表、开始菜单或计划任务。注册表、快捷方式和计划任务契约由源码静态检查覆盖，最终用户账户中的正常安装流程负责验证实际写入。

`data`、`dist`、`build`、`_backups`、`_testdata`、本机部署脚本与验收资料不进入 Git。测试用密码与回环地址只是合成测试数据。

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
`Test-Model-Probe-Web.mjs` 覆盖单模型测试确认、禁止空模型触发批量测试、
重复点击锁和过期测试结果隔离。以上只使用内存 fixture，不消耗模型额度。

需要在真实浏览器点击验收时，显式启动隔离 HTTP fixture：

```powershell
$env:JIANZUO_TEST_UI_FIXTURE='1'
go test -run '^TestUIFixture$' -count=1 -v -timeout=11m
Remove-Item Env:\JIANZUO_TEST_UI_FIXTURE
```

启动后终端输出临时回环地址和合成密码。任务、知识、配置和工作区均使用测试临时目录；
普通文字返回模拟回复，`[wait]` 开头的消息等待停止，`[fail]` 开头模拟执行失败。
预置失效 Harness 会话可验收“新建空白会话”后保留旧记录/知识/草稿。
fixture 不启动 CLI、不读取真实模型缓存、不调用模型，并禁止模型探测、设备和配置写入；
最多运行 10 分钟，也可用登录 cookie、Origin 和 CSRF 提交 `POST /__fixture/finish` 提前结束。
它只存在于 `_test.go`，不进入发行程序。不要把该开关带入普通全量测试环境。

浏览器至少检查 1280×800 和 390×844 下的创建页、会话页、浅/深色设置页，
确认自定义模型可选择、模型按钮没有错误截断、重置提示不会冒充原生恢复。

## Windows 与 WSL 进程约定

- 调用 `wsl.exe` 时只传递 `SystemRoot` 和 `WINDIR`。不要把 Windows 的完整 `PATH`、代理变量或受限沙箱变量传给 WSL；Linux 命令必须使用所选用户的登录环境。
- 从 `command` 或 `environmentProbeCommand` 创建带超时的子进程时，统一使用 `commandWithContext`，以保留最小宿主环境和 `Dir`。
- Windows 自启使用当前用户的 `Jianzuo User` 计划任务。配置一致时应复用现有任务，不从受限进程强制重注册。
- WSL、计划任务相关测试必须在普通 Windows 用户上下文运行；Codex 沙箱账户通常没有 `wsl.exe` 和任务计划服务所需权限。

## 发布新版本

1. 同步修改 `portable.go`、`portable/Launcher.cs`、`package.json`、`package-lock.json` 和 `scripts/Test-Portable.py` 中的版本号。
2. 添加 `docs/releases/vX.Y.Z.md`，描述变更与迁移注意事项。
3. 完成测试和源码提交；确认 GitHub CLI 已登录目标仓库账号。
4. 运行 `scripts/Publish-Release.ps1`。脚本构建，推送分支和精确标签，创建草稿并上传便携 ZIP、安装器及两个 SHA-256 文件，检查大小后发布正式 Release。

脚本不会覆盖已发布的版本；失败后保留草稿供检查。公开更新接口只识别 `vX.Y.Z` 正式版本，草稿和 prerelease 不作为升级推荐。

## 更新检查

`updates.go` 中的默认仓库指向正式上游。用户可在设置中覆盖为另一个公开 GitHub 仓库。

检查由服务端请求 GitHub Releases API，超时 12 秒，结果短期缓存 30 秒。仅在用户点击检查时访问 GitHub；仓库输入与返回下载链接经过限定，下载重定向也只允许 GitHub 发布资产域名。检查状态包括有新版、已最新、本机较新、没有正式发布及网络失败。

发布资产固定为 `Jianzuo-portable-windows-x64.zip`、其 `.sha256`、`Jianzuo-Setup-User-x64.exe` 和其 `.sha256`。版本号按数值比较，不使用字符串排序。

Windows 便携版通过受登录和 CSRF 保护的 `POST /api/updates/install` 自动安装。服务端重新检查 Release、校验 SHA-256、限制 ZIP 大小与固定文件白名单，再复制自身为更新助手。助手等待托盘启动器退出和数据锁释放，备份并替换四个发布文件，启动新版；新版启动失败时回滚。`data` 不参与替换，日志写入 `data/update.log`。其他平台只提供手动下载。
