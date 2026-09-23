[CmdletBinding()]
param()

# Compatibility stub. Startup ownership/migration is managed by the native
# installer and tray UI. Forced registration could overwrite another install.
$ErrorActionPreference = 'Stop'
$taskInstaller = Join-Path (Split-Path $PSScriptRoot) 'dist\Duo-Setup-User-x64.exe'
throw ('此旧脚本已停用，不会创建或覆盖 Windows 启动任务。请运行 Duo 安装包，在向导中设置登录自启；已运行的便携版可从托盘菜单设置。安装包：' + $taskInstaller)
