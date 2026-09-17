[CmdletBinding()]
param([string]$Go = 'go')
$ErrorActionPreference='Stop'
$projectRoot = Split-Path $PSScriptRoot

function Resolve-Go([string]$Value, [string]$Root) {
    if (Test-Path -LiteralPath $Value -PathType Leaf) {
        return [IO.Path]::GetFullPath($Value)
    }
    $command = Get-Command $Value -ErrorAction SilentlyContinue
    if ($command) {
        return $command.Source
    }
    foreach ($candidate in @(
        (Join-Path (Split-Path $Root -Parent) '.tools\go\bin\go.exe'),
        (Join-Path $Root '.tools\go\bin\go.exe')
    )) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            return [IO.Path]::GetFullPath($candidate)
        }
    }
    throw 'Go toolchain was not found. Pass -Go with the go executable path.'
}

function Test-WritableDirectory([string]$Path) {
    if (!$Path) { return $false }
    try {
        New-Item -ItemType Directory -Force -Path $Path | Out-Null
        $probe = Join-Path $Path ('.write-test-' + [Guid]::NewGuid().ToString('N'))
        [IO.File]::WriteAllText($probe, 'test')
        Remove-Item -LiteralPath $probe -Force
        return $true
    } catch {
        return $false
    }
}

function Use-GoEnvironment([string]$GoPath, [string]$Root) {
    if (!$env:GOCACHE -or !(Test-WritableDirectory $env:GOCACHE)) {
        foreach ($candidate in @(
            (Join-Path (Split-Path $Root -Parent) '.gocache'),
            (Join-Path $Root '.gocache'),
            (Join-Path ([IO.Path]::GetTempPath()) 'jianzuo-gocache')
        )) {
            if (Test-WritableDirectory $candidate) {
                $env:GOCACHE = [IO.Path]::GetFullPath($candidate)
                break
            }
        }
    }
    if (!$env:GOPATH -or !(Test-WritableDirectory $env:GOPATH)) {
        foreach ($candidate in @(
            (Join-Path (Split-Path $Root -Parent) '.gopath'),
            (Join-Path $Root '.gopath'),
            (Join-Path ([IO.Path]::GetTempPath()) 'jianzuo-gopath')
        )) {
            if (Test-WritableDirectory $candidate) {
                $env:GOPATH = [IO.Path]::GetFullPath($candidate)
                break
            }
        }
    }
}

$Go = Resolve-Go $Go $projectRoot
Use-GoEnvironment $Go $projectRoot
$packageDir = Join-Path $projectRoot 'dist\Jianzuo-portable-windows-x64'
$compiler = Join-Path $env:WINDIR 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
if (!(Test-Path -LiteralPath $compiler)) { throw '.NET Framework 4.x C# compiler is required to build the launcher.' }
Push-Location $projectRoot
try {
    New-Item -ItemType Directory -Force -Path $packageDir | Out-Null
    & npm.cmd run check
    if ($LASTEXITCODE -ne 0) { throw 'TypeScript check failed' }
    & node scripts/Test-Hardware-Web.mjs
    if ($LASTEXITCODE -ne 0) { throw 'Hardware web tests failed' }
    & node scripts/Test-Conversation-Web.mjs
    if ($LASTEXITCODE -ne 0) { throw 'Conversation web tests failed' }
    & node scripts/Test-Workspace-Web.mjs
    if ($LASTEXITCODE -ne 0) { throw 'Workspace web tests failed' }
    & node scripts/Test-Workbench-Web.mjs
    if ($LASTEXITCODE -ne 0) { throw 'Workbench web tests failed' }
    & node scripts/Test-Updates-Web.mjs
    if ($LASTEXITCODE -ne 0) { throw 'Update web tests failed' }
    & node scripts/build.mjs
    if ($LASTEXITCODE -ne 0) { throw 'Web build failed' }
    & $Go test ./... -count=1
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
    & $Go build -buildvcs=false -trimpath -ldflags '-H windowsgui -s -w' -o (Join-Path $packageDir 'jianzuo-service.exe') .
    if ($LASTEXITCODE -ne 0) { throw 'Service build failed' }
    # csc.exe parses its command line through the system ANSI code page, so a non-ASCII /out: path
    # turns into a mangled name (CS2021). Compile to an ASCII temp name and rename afterwards.
    $launcher = Join-Path $packageDir 'jianzuo-launcher.exe'
    Remove-Item -LiteralPath $launcher -Force -ErrorAction SilentlyContinue
    & $compiler /nologo /target:winexe /platform:x64 /utf8output /codepage:65001 ("/out:"+$launcher) /reference:System.Windows.Forms.dll /reference:System.Drawing.dll /reference:System.Web.Extensions.dll portable\Launcher.cs
    if ($LASTEXITCODE -ne 0) { throw 'Launcher build failed' }
    Move-Item -LiteralPath $launcher -Destination (Join-Path $packageDir '简作.exe') -Force
    Copy-Item -LiteralPath 'portable\使用说明.md' -Destination $packageDir -Force
    $notices = [System.Text.StringBuilder]::new()
    [void]$notices.AppendLine('Jianzuo portable - Third-party notices')
    [void]$notices.AppendLine('Original license texts for the Go runtime and Go module dependencies follow.')
    [void]$notices.AppendLine("`r`n=== xterm.js 6.0.0 (MIT) ===")
    [void]$notices.AppendLine([IO.File]::ReadAllText((Join-Path $projectRoot 'web\vendor\xterm.LICENSE')))
    $goRoot = & $Go env GOROOT
    if ($LASTEXITCODE -ne 0) { throw 'Cannot locate Go runtime license' }
    [void]$notices.AppendLine("`r`n=== Go runtime ===")
    [void]$notices.AppendLine([IO.File]::ReadAllText((Join-Path $goRoot 'LICENSE')))
    $modules = & $Go list -buildvcs=false -deps -f '{{with .Module}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}' . | Where-Object { $_ } | Sort-Object -Unique
    if ($LASTEXITCODE -ne 0) { throw 'Cannot enumerate dependencies' }
    foreach ($line in $modules) {
        $parts = $line -split '\|',3
        if ($parts[0] -eq 'jianzuo') { continue }
        if (!$parts[2]) { throw "Missing dependency directory: $($parts[0])" }
        $licenses = @(Get-ChildItem -LiteralPath $parts[2] -File | Where-Object { $_.Name -match '^(LICENSE|COPYING|NOTICE)(\..*)?$' })
        if ($licenses.Count -eq 0) { throw "Missing license: $($parts[0])" }
        [void]$notices.AppendLine("`r`n=== $($parts[0]) $($parts[1]) ===")
        foreach ($license in $licenses) { [void]$notices.AppendLine([IO.File]::ReadAllText($license.FullName)) }
    }
    [IO.File]::WriteAllText((Join-Path $packageDir 'THIRD-PARTY-NOTICES.txt'),$notices.ToString(),[Text.UTF8Encoding]::new($false))
    # Explicit allowlist: no data, auth files, local paths, or logs are included.
    $files = @('简作.exe','jianzuo-service.exe','使用说明.md','THIRD-PARTY-NOTICES.txt') | ForEach-Object { Join-Path $packageDir $_ }
    $zip = Join-Path $projectRoot 'dist\Jianzuo-portable-windows-x64.zip'
    Compress-Archive -LiteralPath $files -DestinationPath $zip -Force
    Get-Item -LiteralPath $zip | Select-Object FullName,Length
    $hash = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText(($zip+'.sha256'),($hash+'  '+[IO.Path]::GetFileName($zip)+[Environment]::NewLine),[Text.UTF8Encoding]::new($false))
    Write-Output $hash
    & (Join-Path $PSScriptRoot 'Build-Installer.ps1') -ProjectRoot $projectRoot -Payload $zip -OutputDir (Split-Path -Parent $zip)
    if ($LASTEXITCODE -ne 0) { throw 'Installer build failed' }
} finally { Pop-Location }
