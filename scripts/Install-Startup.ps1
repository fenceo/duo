[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$root = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$portableExe = Join-Path $root 'dist\Jianzuo-portable-windows-x64\简作.exe'
$developmentExe = Join-Path $root 'build\jianzuo.exe'
if (Test-Path -LiteralPath $portableExe -PathType Leaf) {
    $exe = $portableExe
} else {
    $exe = $developmentExe
}
if (!(Test-Path -LiteralPath $exe -PathType Leaf)) {
    throw '未找到便携版简作.exe，也未找到 build\jianzuo.exe。请先构建程序。'
}

$data = [System.IO.Path]::GetFullPath((Join-Path $root 'data'))
if ($data.EndsWith('\')) {
    $data += '.'
}
$identity = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
$taskName = 'Jianzuo User'
$taskArguments = '--data "{0}" --background' -f $data
$workingDirectory = Split-Path -Parent $exe

$action = New-ScheduledTaskAction -Execute $exe -Argument $taskArguments -WorkingDirectory $workingDirectory
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $identity
$principal = New-ScheduledTaskPrincipal -UserId $identity -LogonType Interactive -RunLevel Limited
$settings = New-ScheduledTaskSettingsSet -StartWhenAvailable -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew -Hidden

function Resolve-IdentitySid([string]$Value) {
    try {
        if ($Value -match '^S-') {
            return (New-Object Security.Principal.SecurityIdentifier($Value)).Value
        }
        return (New-Object Security.Principal.NTAccount($Value)).Translate([Security.Principal.SecurityIdentifier]).Value
    } catch {
        return ''
    }
}

function Test-ExistingJianzuoUserTask {
    try {
        $existing = Get-ScheduledTask -TaskName $taskName -ErrorAction Stop
        $actions = @($existing.Actions)
        if ($actions.Count -ne 1 -or $existing.State -eq 'Disabled') {
            return $false
        }
        $existingAction = $actions[0]
        if ([string]::IsNullOrWhiteSpace([string]$existingAction.Execute) -or
            [string]::IsNullOrWhiteSpace([string]$existingAction.WorkingDirectory)) {
            return $false
        }
        $sameExecutable = [string]::Equals([IO.Path]::GetFullPath([string]$existingAction.Execute), $exe, [StringComparison]::OrdinalIgnoreCase)
        $sameArguments = [string]::Equals([string]$existingAction.Arguments, $taskArguments, [StringComparison]::Ordinal)
        $sameWorkingDirectory = [string]::Equals([IO.Path]::GetFullPath([string]$existingAction.WorkingDirectory), $workingDirectory, [StringComparison]::OrdinalIgnoreCase)
        $sameIdentity = (Resolve-IdentitySid ([string]$existing.Principal.UserId)) -eq (Resolve-IdentitySid $identity)
        $limited = [string]::Equals([string]$existing.Principal.RunLevel, 'Limited', [StringComparison]::OrdinalIgnoreCase)
        return $sameExecutable -and $sameArguments -and $sameWorkingDirectory -and $sameIdentity -and $limited
    } catch {
        return $false
    }
}

# Reuse an exact task before writing. This keeps the command idempotent and
# avoids replacing a valid per-user task whose ACL rejects a forced update.
if (Test-ExistingJianzuoUserTask) {
    Write-Output '现有简作用户任务配置一致，继续使用。'
} else {
    Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Description '简作独立任务工作台（当前用户登录后启动）' -Force | Out-Null
}

Start-ScheduledTask -TaskName $taskName
Write-Output ('简作已设置为当前用户登录时启动：' + $exe)
