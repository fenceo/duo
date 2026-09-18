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
    'Path.GetPathRoot(fullRoot)',
    'Path.GetPathRoot(fullCandidate)',
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

function Invoke-InstallerRoundTrip {
    param(
        [string]$Label,
        [string]$InstallDir,
        [string]$DataDir
    )

    New-Item -ItemType Directory -Force -Path $InstallDir,$DataDir | Out-Null
    try {
        $install = Start-Process -FilePath $setup -Wait -PassThru -ArgumentList @(
            '--quiet',
            '--install-dir', $InstallDir,
            '--data', $DataDir,
            '--no-shortcuts',
            '--no-desktop',
            '--no-registry',
            '--no-startup',
            '--no-launch'
        )
        if ($install.ExitCode -ne 0) {
            throw "$Label installer test failed with exit code $($install.ExitCode)."
        }
        $uninstaller = Join-Path $InstallDir '卸载简作.exe'
        foreach ($path in @(
            (Join-Path $InstallDir '简作.exe'),
            (Join-Path $InstallDir 'jianzuo-service.exe'),
            $uninstaller
        )) {
            if (!(Test-Path -LiteralPath $path -PathType Leaf)) {
                throw "$Label installer test did not create: $path"
            }
        }
        $uninstall = Start-Process -FilePath $uninstaller -Wait -PassThru -ArgumentList @(
            '--quiet',
            '--uninstall',
            '--install-dir', $InstallDir,
            '--data', $DataDir
        )
        if ($uninstall.ExitCode -ne 0) {
            throw "$Label uninstall test failed with exit code $($uninstall.ExitCode)."
        }
        for ($i = 0; $i -lt 40 -and (Test-Path -LiteralPath $InstallDir); $i++) {
            Start-Sleep -Milliseconds 250
        }
        Write-Output ("PASS: $Label install and uninstall round trip.")
    } finally {
        if (Test-Path -LiteralPath $InstallDir) {
            Remove-Item -LiteralPath $InstallDir -Recurse -Force -ErrorAction SilentlyContinue
        }
        if (Test-Path -LiteralPath $DataDir) {
            Remove-Item -LiteralPath $DataDir -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}

if ($Full) {
    $testRoot = Join-Path ([IO.Path]::GetTempPath()) ('JianzuoInstallerTest-' + [Guid]::NewGuid().ToString('N'))
    try {
        Invoke-InstallerRoundTrip -Label 'same-volume' `
            -InstallDir (Join-Path $testRoot 'program') `
            -DataDir (Join-Path $testRoot 'data')
    } finally {
        if (Test-Path -LiteralPath $testRoot) {
            Remove-Item -LiteralPath $testRoot -Recurse -Force -ErrorAction SilentlyContinue
        }
    }

    $tempVolume = [IO.Path]::GetPathRoot([IO.Path]::GetFullPath($testRoot))
    $projectVolume = [IO.Path]::GetPathRoot($root)
    if (![string]::Equals($tempVolume, $projectVolume, [StringComparison]::OrdinalIgnoreCase)) {
        $crossRoot = Join-Path $root ('.installer-test-' + [Guid]::NewGuid().ToString('N'))
        try {
            Invoke-InstallerRoundTrip -Label 'cross-volume' `
                -InstallDir (Join-Path $testRoot 'cross-program') `
                -DataDir (Join-Path $crossRoot 'data')
        } finally {
            if (Test-Path -LiteralPath $crossRoot) {
                Remove-Item -LiteralPath $crossRoot -Recurse -Force -ErrorAction SilentlyContinue
            }
        }
    } else {
        Write-Output 'SKIP: cross-volume test requires a second local temporary volume.'
    }
}

Write-Output ('Installer verification passed: ' + $setup)
Write-Output ('SHA256: ' + $actualHash)
