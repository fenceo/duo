param([string]$DataDirectory = (Join-Path $PSScriptRoot '..\data'))
$ErrorActionPreference = 'Stop'
$program = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\build\jianzuo.exe'))
$secure = Read-Host '输入新的简作访问密码（先停止简作服务）' -AsSecureString
$credential = New-Object System.Management.Automation.PSCredential('owner', $secure)
$start = New-Object System.Diagnostics.ProcessStartInfo
$start.FileName = $program
$start.Arguments = '--data "' + [System.IO.Path]::GetFullPath($DataDirectory) + '" --init-password-stdin'
$start.UseShellExecute = $false
$start.CreateNoWindow = $true
$start.RedirectStandardInput = $true
$start.RedirectStandardOutput = $true
$start.RedirectStandardError = $true
$process = [System.Diagnostics.Process]::Start($start)
$process.StandardInput.WriteLine($credential.GetNetworkCredential().Password)
$process.StandardInput.Close()
$result = $process.StandardOutput.ReadToEnd()
$failure = $process.StandardError.ReadToEnd()
$process.WaitForExit()
$credential = $null
$secure = $null
if ($process.ExitCode -ne 0) { throw $failure }
$result
