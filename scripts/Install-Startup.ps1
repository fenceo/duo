$ErrorActionPreference = 'Stop'
$root = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$exe = Join-Path $root 'build\jianzuo.exe'
$data = Join-Path $root 'data'
if (!(Test-Path -LiteralPath $exe)) { throw '请先构建 build\jianzuo.exe' }
$identity = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
$action = New-ScheduledTaskAction -Execute $exe -Argument ('--data "' + $data + '"') -WorkingDirectory $root
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $identity
$principal = New-ScheduledTaskPrincipal -UserId $identity -LogonType Interactive -RunLevel Limited
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew
Register-ScheduledTask -TaskName 'Jianzuo User' -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Description '简作独立任务工作台' -Force | Out-Null
Start-ScheduledTask -TaskName 'Jianzuo User'
'简作已设置为当前用户登录时启动。'
