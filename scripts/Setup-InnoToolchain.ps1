[CmdletBinding()]
param(
    [string]$ToolsRoot = (Join-Path (Split-Path (Split-Path $PSScriptRoot -Parent) -Parent) '.tools')
)
$ErrorActionPreference = 'Stop'
$version = '6.7.3'
$expectedHash = '9c73c3bae7ed48d44112a0f48e66742c00090bdb5bef71d9d3c056c66e97b732'
$toolsDirectory = [IO.Path]::GetFullPath($ToolsRoot)
$compilerDirectory = Join-Path $toolsDirectory ('inno-' + $version)
$compiler = Join-Path $compilerDirectory 'ISCC.exe'
function Install-VerifiedLanguage([string]$CompilerRoot) {
    $language = Join-Path $CompilerRoot 'Languages\ChineseSimplified.isl'
    if (!(Test-Path -LiteralPath $language -PathType Leaf)) {
        Invoke-WebRequest -UseBasicParsing -Uri 'https://raw.githubusercontent.com/jrsoftware/issrc/is-6_7_3/Files/Languages/Unofficial/ChineseSimplified.isl' -OutFile $language
    }
    if ((Get-FileHash -LiteralPath $language -Algorithm SHA256).Hash.ToLowerInvariant() -ne '7d544b9bb1d142cfa11f2e5d3cc8abe2e55f8e066c5124e3772675aa236e1278') {
        throw 'Chinese language resource does not match the pinned upstream source.'
    }
}
if (Test-Path -LiteralPath $compiler -PathType Leaf) {
    Install-VerifiedLanguage $compilerDirectory
    Write-Output ('Inno Setup compiler: ' + $compiler)
    exit 0
}
if (Test-Path -LiteralPath $compilerDirectory) {
    throw 'Compiler destination already exists without ISCC.exe; inspect it before retrying.'
}
New-Item -ItemType Directory -Force -Path $toolsDirectory | Out-Null
$download = Join-Path $toolsDirectory ('innosetup-' + $version + '.exe')
if (!(Test-Path -LiteralPath $download -PathType Leaf)) {
    Invoke-WebRequest -UseBasicParsing -Uri 'https://github.com/jrsoftware/issrc/releases/download/is-6_7_3/innosetup-6.7.3.exe' -OutFile $download
}
$actualHash = (Get-FileHash -LiteralPath $download -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualHash -ne $expectedHash) { throw 'Official Inno Setup download checksum does not match the pinned release.' }
$signature = Get-AuthenticodeSignature -LiteralPath $download
if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -notmatch 'CN=Pyrsys B\.V\.') {
    throw 'Official Inno Setup publisher signature is not valid; the downloaded file was not executed.'
}
# Inno's documented portable mode does not install an uninstaller, register an
# installed application, or create system shortcuts/file associations.
$result = Start-Process -FilePath $download -WindowStyle Hidden -Wait -PassThru -ArgumentList @(
    '/PORTABLE=1', '/CURRENTUSER', '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART',
    '/NOICONS', '/TASKS=', ('/DIR="' + $compilerDirectory + '"')
)
if ($result.ExitCode -ne 0 -or !(Test-Path -LiteralPath $compiler -PathType Leaf)) {
    throw ('Portable Inno Setup compiler preparation failed with exit code ' + $result.ExitCode)
}
Install-VerifiedLanguage $compilerDirectory
Write-Output ('Verified Inno Setup ' + $version + ' compiler: ' + $compiler)
