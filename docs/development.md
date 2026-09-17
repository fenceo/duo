# 开发与发布

## 项目结构

- 根目录 Go：HTTP、SQLite、任务队列、Codex / Claude CLI、飞书、终端、硬件和版本检查。
- `web/`：TypeScript 和 CSS；`scripts/build.mjs` 生成供 Go 嵌入的 app.js。
- `portable/Launcher.cs`：Windows 托盘、首次配置、启动与退出。
- `scripts/Build-Portable.ps1`：检查、测试、打包及 SHA-256 校验文件。

## 检查

```powershell
npm ci
npm run check
npm run build
go test ./... -count=1
node scripts/Test-Updates-Web.mjs
```

完整 Windows 便携包使用 `scripts/Build-Portable.ps1`。构建依赖 Go 1.27+、支持 TypeScript 类型去除的 Node.js、npm，以及 Windows .NET Framework C# 编译器。运行包不需要这些构建工具。

`data`、`dist`、`build`、`_backups`、`_testdata`、本机部署脚本与验收资料不进入 Git。测试用密码与回环地址只是合成测试数据。

## 发布新版本

1. 同步修改 `portable.go`、`portable/Launcher.cs`、`package.json`、`package-lock.json` 和 `scripts/Test-Portable.py` 中的版本号。
2. 添加 `docs/releases/vX.Y.Z.md`，描述变更与迁移注意事项。
3. 完成测试和源码提交；确认 GitHub CLI 已登录目标仓库账号。
4. 运行 `scripts/Publish-Release.ps1`。脚本构建，推送分支和精确标签，创建草稿并上传 ZIP 与 SHA-256 文件，检查大小后发布正式 Release。

脚本不会覆盖已发布的版本；失败后保留草稿供检查。公开更新接口只识别 `vX.Y.Z` 正式版本，草稿和 prerelease 不作为升级推荐。

## 更新检查

`updates.go` 中的默认仓库指向正式上游。用户可在设置中覆盖为另一个公开 GitHub 仓库。

检查由服务端请求 GitHub Releases API，超时 12 秒，结果短期缓存 30 秒。仅在用户点击检查时访问 GitHub；仓库输入与返回下载链接经过限定，不支持任意下载服务器或自动执行安装文件。检查状态包括有新版、已最新、本机较新、没有正式发布及网络失败。

发布资产固定名为 `Jianzuo-portable-windows-x64.zip` 及 `Jianzuo-portable-windows-x64.zip.sha256`。版本号按数值比较，不使用字符串排序。
