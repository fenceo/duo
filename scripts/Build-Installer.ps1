[CmdletBinding()]
param(
    [string]$ProjectRoot = (Split-Path $PSScriptRoot),
    [string]$Payload = '',
    [string]$OutputDir = '',
    [string]$Version = '',
    [switch]$ValidateOnly
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$root = [IO.Path]::GetFullPath($ProjectRoot)
$source = Join-Path $root 'installer\windows\JianzuoSetup.cs'
if (!(Test-Path -LiteralPath $source -PathType Leaf)) {
    throw "Installer source is missing: $source"
}

$sourceText = Get-Content -Raw -Encoding UTF8 -LiteralPath $source
foreach ($contract in @(
    'const string ProductName = "',
    'TaskName = "Jianzuo User"',
    'PayloadResource = "JianzuoPayload.zip"',
    'Registry.CurrentUser.CreateSubKey(UninstallKey)',
    'Invoke(root, "RegisterTaskDefinition"',
    'NewTask',
    'Environment.SpecialFolder.LocalApplicationData',
    'Environment.SpecialFolder.Programs',
    'Environment.SpecialFolder.DesktopDirectory',
    '--uninstall',
    '--verify',
    'PathWithin',
    'IsFilesystemRoot',
    'IsJianzuoTask',
    'trailingBackslashes'
)) {
    if (!$sourceText.Contains($contract)) {
        throw "Installer contract is missing: $contract"
    }
}

$compiler = Join-Path $env:WINDIR 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
$compression = Join-Path $env:WINDIR 'Microsoft.NET\Framework64\v4.0.30319\System.IO.Compression.dll'
foreach ($required in @($compiler, $compression)) {
    if (!(Test-Path -LiteralPath $required -PathType Leaf)) {
        throw "Windows .NET Framework build component is missing: $required"
    }
}

if (!$OutputDir) {
    $OutputDir = Join-Path $root 'dist'
}
$output = [IO.Path]::GetFullPath($OutputDir)
$setup = Join-Path $output 'Jianzuo-Setup-User-x64.exe'
$hashFile = $setup + '.sha256'

if (!$Payload) {
    $Payload = Join-Path $output 'Jianzuo-portable-windows-x64.zip'
}
$payloadPath = [IO.Path]::GetFullPath($Payload)

if (!$Version) {
    $package = Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $root 'package.json') | ConvertFrom-Json
    $Version = [string]$package.version
}
$normalizedVersion = $Version.Trim().TrimStart('v')
if ($normalizedVersion -notmatch '^[0-9]+\.[0-9]+\.[0-9]+$') {
    throw "Installer version is invalid: $Version"
}

if ($ValidateOnly) {
    $payloadState = if (Test-Path -LiteralPath $payloadPath -PathType Leaf) { 'found' } else { 'not present' }
    Write-Output "Windows per-user installer definition valid; payload $payloadState."
    exit 0
}

if (!(Test-Path -LiteralPath $payloadPath -PathType Leaf)) {
    throw "Portable payload is missing: $payloadPath"
}

New-Item -ItemType Directory -Force -Path $output | Out-Null
$work = Join-Path $output '.installer-build'
New-Item -ItemType Directory -Force -Path $work | Out-Null
$versionFile = Join-Path $work 'version.txt'
[IO.File]::WriteAllText($versionFile, $normalizedVersion + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))

Remove-Item -LiteralPath $setup,$hashFile -Force -ErrorAction SilentlyContinue
& $compiler /nologo /target:winexe /platform:x64 /optimize+ /utf8output /codepage:65001 `
    ("/out:" + $setup) `
    ("/resource:" + $payloadPath + ",JianzuoPayload.zip") `
    ("/resource:" + $versionFile + ",JianzuoVersion.txt") `
    ("/reference:" + $compression) `
    /reference:System.Windows.Forms.dll `
    /reference:System.Drawing.dll `
    $source
if ($LASTEXITCODE -ne 0 -or !(Test-Path -LiteralPath $setup -PathType Leaf)) {
    throw 'Installer build failed.'
}

$verify = Start-Process -FilePath $setup -ArgumentList '--verify' -WindowStyle Hidden -Wait -PassThru
if ($verify.ExitCode -ne 0) {
    throw "Installer payload verification failed with exit code $($verify.ExitCode)."
}

$hash = (Get-FileHash -LiteralPath $setup -Algorithm SHA256).Hash.ToLowerInvariant()
[IO.File]::WriteAllText($hashFile, $hash + '  ' + [IO.Path]::GetFileName($setup) + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
Write-Output ('Installer: ' + $setup)
Write-Output ('SHA256: ' + $hash)

& (Join-Path $PSScriptRoot 'Test-Installer.ps1') -ProjectRoot $root -Setup $setup
if ($LASTEXITCODE -ne 0) {
    throw 'Installer contract test failed.'
}
