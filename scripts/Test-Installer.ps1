[CmdletBinding()]
param(
    [string]$ProjectRoot = (Split-Path $PSScriptRoot),
    [string]$Setup = '',
    [switch]$Full
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$root = [IO.Path]::GetFullPath($ProjectRoot)
if (!$Setup) {
    $Setup = Join-Path $root 'dist\Jianzuo-Setup-User-x64.exe'
}
$setup = [IO.Path]::GetFullPath($Setup)
$hashFile = $setup + '.sha256'
if (!(Test-Path -LiteralPath $setup -PathType Leaf)) {
    throw "Installer is missing: $setup"
}
if (!(Test-Path -LiteralPath $hashFile -PathType Leaf)) {
    throw "Installer checksum is missing: $hashFile"
}

$source = Join-Path $root 'installer\windows\JianzuoSetup.cs'
if (!(Test-Path -LiteralPath $source -PathType Leaf)) {
    throw "Installer source is missing: $source"
}
$sourceText = Get-Content -Raw -Encoding UTF8 -LiteralPath $source
foreach ($contract in @(
    'Environment.SpecialFolder.LocalApplicationData',
    'Registry.CurrentUser.CreateSubKey(UninstallKey)',
    'Invoke(root, "RegisterTaskDefinition"',
    'StopOwnedTask(options.InstallDir, true)',
    'static bool IsFilesystemRoot',
    'IsFilesystemRoot(options.InstallDir)',
    'IsFilesystemRoot(installDir)',
    'static bool IsJianzuoTask',
    'new string(''\\'', trailingBackslashes * 2)'
)) {
    if (!$sourceText.Contains($contract)) {
        throw "Installer safety contract is missing: $contract"
    }
}
if ($sourceText.Contains('(value ?? "").TrimEnd(''\\'') +')) {
    throw 'Installer still contains the unsafe path-quoting implementation.'
}

$verify = Start-Process -FilePath $setup -ArgumentList '--verify' -Wait -PassThru
if ($verify.ExitCode -ne 0) {
    throw "Installer payload verification failed with exit code $($verify.ExitCode)."
}

$expectedHash = ((Get-Content -Raw -Encoding ASCII -LiteralPath $hashFile).Trim() -split '\s+')[0].ToLowerInvariant()
$actualHash = (Get-FileHash -LiteralPath $setup -Algorithm SHA256).Hash.ToLowerInvariant()
if ($expectedHash -ne $actualHash) {
    throw "Installer checksum mismatch: expected $expectedHash, got $actualHash"
}

if ($Full) {
    $testRoot = Join-Path ([IO.Path]::GetTempPath()) ('JianzuoInstallerTest-' + [Guid]::NewGuid().ToString('N'))
    $installDir = Join-Path $testRoot 'program'
    $dataDir = Join-Path $testRoot 'data'
    New-Item -ItemType Directory -Force -Path $testRoot | Out-Null
    try {
        $install = Start-Process -FilePath $setup -Wait -PassThru -ArgumentList @(
            '--quiet',
            '--install-dir', $installDir,
            '--data', $dataDir,
            '--no-shortcuts',
            '--no-desktop',
            '--no-registry',
            '--no-startup',
            '--no-launch'
        )
        if ($install.ExitCode -ne 0) {
            throw "Full installer test failed with exit code $($install.ExitCode)."
        }
        $uninstaller = Join-Path $installDir '卸载简作.exe'
        foreach ($path in @(
            (Join-Path $installDir '简作.exe'),
            (Join-Path $installDir 'jianzuo-service.exe'),
            $uninstaller
        )) {
            if (!(Test-Path -LiteralPath $path -PathType Leaf)) {
                throw "Full installer test did not create: $path"
            }
        }
        $uninstall = Start-Process -FilePath $uninstaller -Wait -PassThru -ArgumentList @(
            '--quiet',
            '--uninstall',
            '--install-dir', $installDir,
            '--data', $dataDir
        )
        if ($uninstall.ExitCode -ne 0) {
            throw "Full uninstall test failed with exit code $($uninstall.ExitCode)."
        }
        for ($i = 0; $i -lt 40 -and (Test-Path -LiteralPath $installDir); $i++) {
            Start-Sleep -Milliseconds 250
        }
    } finally {
        if (Test-Path -LiteralPath $testRoot) {
            Remove-Item -LiteralPath $testRoot -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}

Write-Output ('Installer verification passed: ' + $setup)
Write-Output ('SHA256: ' + $actualHash)
