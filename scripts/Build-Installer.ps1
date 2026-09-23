[CmdletBinding()]
param(
    [string]$ProjectRoot = (Split-Path $PSScriptRoot),
    [string]$Payload = '',
    [string]$OutputDir = '',
    [string]$Version = '',
    [string]$InnoCompiler = '',
    [string]$TestNamespace = '',
    [switch]$ValidateOnly,
    [switch]$SkipTests
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$root = [IO.Path]::GetFullPath($ProjectRoot)
$source = Join-Path $root 'installer\windows\JianzuoMaintenance.cs'
$definition = Join-Path $root 'installer\windows\Jianzuo.iss'
if (!$OutputDir) { $OutputDir = Join-Path $root 'dist' }
$output = [IO.Path]::GetFullPath($OutputDir)
if (!$Payload) { $Payload = Join-Path $root 'dist\Duo-portable-windows-x64.zip' }
$payloadPath = [IO.Path]::GetFullPath($Payload)
if (!$Version) { $Version = (Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $root 'package.json') | ConvertFrom-Json).version }
$versionValue = $Version.Trim().TrimStart('v')
if ($versionValue -notmatch '^[0-9]+\.[0-9]+\.[0-9]+$') { throw 'Invalid installer version.' }
if ($TestNamespace -and $TestNamespace -notmatch '^[a-f0-9]{32}$') { throw 'Test namespace must be a GUID in N format.' }
if (!$InnoCompiler) { $InnoCompiler = $env:JIANZUO_INNO_COMPILER }
if (!$InnoCompiler) { $InnoCompiler = Join-Path (Split-Path $root) '.tools\inno-6.7.3\ISCC.exe' }
$csc = Join-Path $env:WINDIR 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
foreach ($required in @($source,$definition,$csc)) {
    if (!(Test-Path -LiteralPath $required -PathType Leaf)) { throw "Missing installer input: $required" }
}
$iss = Get-Content -Raw -Encoding UTF8 -LiteralPath $definition
foreach ($contract in @('PrivilegesRequired=lowest','AppId={#ProductKey}','[Files]','[Icons]','[Registry]','[UninstallDelete]','--rollback','--update')) {
    if (!$iss.Contains($contract)) { throw "Inno installer contract missing: $contract" }
}
if ($iss -match '(?im)^\s*Type:\s*filesandordirs' -or $iss.Contains('JianzuoSetup.cs')) {
    throw 'The native installer must not recursively remove data or wrap the legacy installer.'
}
if ($ValidateOnly) { Write-Output 'Native Inno per-user installer definition valid.'; return }
if (!(Test-Path -LiteralPath $InnoCompiler -PathType Leaf)) {
    throw 'Inno Setup 6.7.3 is required. Run scripts/Setup-InnoToolchain.ps1 or set JIANZUO_INNO_COMPILER.'
}
if (!(Test-Path -LiteralPath $payloadPath -PathType Leaf)) { throw "Missing payload: $payloadPath" }
New-Item -ItemType Directory -Force -Path $output | Out-Null
$work = Join-Path $output '.installer-build'
$payloadDir = Join-Path $work 'payload'
New-Item -ItemType Directory -Force -Path $payloadDir | Out-Null
Add-Type -AssemblyName System.IO.Compression
$allowed = @('Duo.exe','duo-service.exe','使用说明.md','THIRD-PARTY-NOTICES.txt')
$zipStream = [IO.File]::OpenRead($payloadPath)
$zip = [IO.Compression.ZipArchive]::new($zipStream, [IO.Compression.ZipArchiveMode]::Read)
try {
    if ($zip.Entries.Count -ne $allowed.Count) { throw 'Payload must contain exactly the four published files.' }
    $seen = @{}
    foreach ($entry in $zip.Entries) {
        if ($entry.FullName -cne $entry.Name -or $allowed -cnotcontains $entry.Name -or $seen.ContainsKey($entry.Name) -or $entry.Length -le 0) {
            throw 'Unsafe or duplicate file in portable payload.'
        }
        $seen[$entry.Name] = $true
        $inputStream = $entry.Open()
        $outputStream = [IO.File]::Open((Join-Path $payloadDir $entry.Name), [IO.FileMode]::Create, [IO.FileAccess]::Write)
        try { $inputStream.CopyTo($outputStream) } finally { $outputStream.Dispose(); $inputStream.Dispose() }
    }
} finally { $zip.Dispose(); $zipStream.Dispose() }
$helper = Join-Path $work 'DuoMaintenance.exe'
$compilerArgs = @('/nologo','/target:winexe','/platform:x64','/optimize+','/utf8output','/codepage:65001',('/out:' + $helper),'/reference:System.Xml.dll')
if ($TestNamespace) {
    $identityFile = Join-Path $work 'test-namespace.txt'
    [IO.File]::WriteAllText($identityFile, $TestNamespace, [Text.UTF8Encoding]::new($false))
    $compilerArgs += '/resource:' + $identityFile + ',JianzuoTestNamespace.txt'
}
& $csc @compilerArgs $source
if ($LASTEXITCODE -ne 0) { throw 'Installer maintenance helper compilation failed.' }
$setup = Join-Path $output 'Duo-Setup-User-x64.exe'
$hashFile = $setup + '.sha256'
$innoArgs = @('/Qp',('/DAppVersion=' + $versionValue),('/DPayloadDir=' + $payloadDir),('/DHelperPath=' + $helper),('/DOutputPath=' + $output))
if ($TestNamespace) { $innoArgs += '/DTestNamespace=' + $TestNamespace }
& $InnoCompiler @innoArgs $definition
if ($LASTEXITCODE -ne 0 -or !(Test-Path -LiteralPath $setup -PathType Leaf)) { throw 'Native Inno Setup compilation failed.' }
$hash = (Get-FileHash -LiteralPath $setup -Algorithm SHA256).Hash.ToLowerInvariant()
[IO.File]::WriteAllText($hashFile, $hash + '  ' + [IO.Path]::GetFileName($setup) + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
Write-Output ('Installer: ' + $setup)
Write-Output ('SHA256: ' + $hash)
if (!$SkipTests) {
    $windowsPowerShell = Join-Path $env:WINDIR 'System32\WindowsPowerShell\v1.0\powershell.exe'
    & $windowsPowerShell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'Test-Installer.ps1') -ProjectRoot $root -Setup $setup -Helper $helper
    if ($LASTEXITCODE -ne 0) { throw 'Native installer safety regression failed.' }
}
