[CmdletBinding()]
param(
    [string]$ProjectRoot = (Split-Path $PSScriptRoot),
    [string]$Setup = '',
    [switch]$Full,
    [switch]$IsolatedUser
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
    'if (options.EnableStartup) StopOwnedTask(options.InstallDir)',
    'if (options.CreateShortcuts) RemoveShortcuts(installDir, options.CreateDesktopShortcut)',
    'static bool OwnedInstallation',
    'static bool OwnedExecutable',
    'static bool RegistryOwned',
    'static bool ShortcutOwned',
    'static void ValidateInstallDestination',
    'static void ScheduleOwnedFileRemoval',
    'static string OwnedFileRemovalScript',
    'Remove-Item -LiteralPath $path -Force',
    '[IO.Directory]::Delete($target, $false)',
    'ownedPath && jianzuoTask',
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
foreach ($unsafe in @('Directory.Delete(path, true)', 'rmdir /s', 'removeAnyJianzuo', 'ScheduleDirectoryRemoval')) {
    if ($sourceText.Contains($unsafe)) { throw "Unsafe installer deletion contract: $unsafe" }
}
if ($Full -and !$IsolatedUser) {
    throw 'Full install/uninstall tests require a disposable Windows VM/user. Pass -IsolatedUser only there.'
}

$verify = Start-Process -FilePath $setup -ArgumentList '--verify' -WindowStyle Hidden -Wait -PassThru
if ($verify.ExitCode -ne 0) {
    throw "Installer payload verification failed with exit code $($verify.ExitCode)."
}

$expectedHash = ((Get-Content -Raw -Encoding ASCII -LiteralPath $hashFile).Trim() -split '\s+')[0].ToLowerInvariant()
$actualHash = (Get-FileHash -LiteralPath $setup -Algorithm SHA256).Hash.ToLowerInvariant()
if ($expectedHash -ne $actualHash) {
    throw "Installer checksum mismatch: expected $expectedHash, got $actualHash"
}

function Remove-FixtureTree([string]$Path, [string]$Parent, [string]$Prefix) {
    $target = [IO.Path]::GetFullPath($Path).TrimEnd('\')
    $allowedParent = [IO.Path]::GetFullPath($Parent).TrimEnd('\')
    if (![string]::Equals([IO.Path]::GetDirectoryName($target), $allowedParent, [StringComparison]::OrdinalIgnoreCase) -or
        [IO.Path]::GetFileName($target) -notmatch ('^' + [regex]::Escape($Prefix) + '[a-f0-9]{32}$')) {
        throw 'Refusing cleanup outside the exact generated fixture directory.'
    }
    if (Test-Path -LiteralPath $target) { Remove-Item -LiteralPath $target -Recurse -Force }
}

# Path/error helpers and temporary shortcut creation use an owned GUID directory.
# This is not an installation and never changes real user registry/shortcut/startup entries.
$assembly = [Reflection.Assembly]::Load([IO.File]::ReadAllBytes($setup))
$program = $assembly.GetType('Program', $true)
function Invoke-SafetyHelper([string]$Name, [object[]]$Arguments) {
    $method = $program.GetMethod($Name, [Reflection.BindingFlags]'NonPublic,Static')
    if (!$method) { throw "Built installer is missing safety helper: $Name" }
    $parameters = $method.GetParameters()
    $converted = [object[]]::new($Arguments.Count)
    for ($index = 0; $index -lt $Arguments.Count; $index++) {
        $value = $Arguments[$index].PSObject.BaseObject
        $converted[$index] = [System.Management.Automation.LanguagePrimitives]::ConvertTo($value, $parameters[$index].ParameterType)
    }
    # Preserve COM collections as one return object; do not pipeline-enumerate Actions.
    return ,($method.Invoke($null, $converted))
}
$safetyParent = [IO.Path]::GetTempPath()
$safetyRoot = Join-Path $safetyParent ('JianzuoInstallerSafety-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $safetyRoot | Out-Null
try {
    $stage = 'fixture task startup stage'
    $logPath = Join-Path $safetyRoot 'installer-error.log'
    $cause = [Runtime.InteropServices.COMException]::new('fixture task registration denied', [int]-2147024891)
    $wrappedCause = [Reflection.TargetInvocationException]::new([Reflection.TargetInvocationException]::new($cause))
    $formatted = [string](Invoke-SafetyHelper 'FormatInstallError' @($wrappedCause, $stage, $logPath))
    if (!$formatted.Contains($cause.Message) -or !$formatted.Contains($stage) -or !$formatted.Contains($logPath) -or
        $formatted -notmatch '(?i)0x80070005') {
        throw 'Installer error diagnostics must show the root COM cause, HRESULT, stage and log path through nested reflection wrappers.'
    }
    $missingTask = [IO.FileNotFoundException]::new('fixture missing task')
    $wrappedMissingTask = [Reflection.TargetInvocationException]::new($missingTask)
    if (!(Invoke-SafetyHelper 'IsMissingTaskError' @($missingTask)) -or
        !(Invoke-SafetyHelper 'IsMissingTaskError' @($wrappedMissingTask)) -or
        (Invoke-SafetyHelper 'IsMissingTaskError' @([UnauthorizedAccessException]::new('fixture access denied')))) {
        throw 'Missing startup task handling must support direct/wrapped COM errors and fail closed for access errors.'
    }
    $ownedPath = Join-Path $safetyRoot 'jianzuo-service.exe'
    $unknownPath = Join-Path $safetyRoot 'unrelated-user-file.txt'
    [IO.File]::WriteAllText($ownedPath, 'existing unknown collision')
    [IO.File]::WriteAllText($unknownPath, 'must survive')
    $rejected = $false
    try { Invoke-SafetyHelper 'ValidateInstallDestination' @($safetyRoot) } catch { $rejected = $true }
    if (!$rejected -or [IO.File]::ReadAllText($ownedPath) -ne 'existing unknown collision') {
        throw 'Unowned same-name executable must be rejected without overwrite.'
    }
    if (!(Invoke-SafetyHelper 'OwnedExecutable' @($safetyRoot, $ownedPath)) -or
        (Invoke-SafetyHelper 'OwnedExecutable' @($safetyRoot, (Join-Path $safetyRoot 'nested\jianzuo-service.exe'))) -or
        (Invoke-SafetyHelper 'OwnedExecutable' @($safetyRoot, ($safetyRoot + '-other\jianzuo-service.exe')))) {
        throw 'Executable ownership must match a direct, exact installation filename.'
    }
    $marker = Invoke-SafetyHelper 'InstallMarker' @($safetyRoot)
    [IO.File]::WriteAllText((Join-Path $safetyRoot '.jianzuo-install'), $marker, [Text.UTF8Encoding]::new($false))
    Invoke-SafetyHelper 'ValidateInstallDestination' @($safetyRoot)
    if (!(Invoke-SafetyHelper 'OwnedInstallation' @($safetyRoot))) { throw 'Bound installation marker was not recognized.' }
    Invoke-SafetyHelper 'TryDeleteDirectory' @($safetyRoot)
    if ((Test-Path -LiteralPath $ownedPath) -or !(Test-Path -LiteralPath $unknownPath) -or
        [IO.File]::ReadAllText($unknownPath) -ne 'must survive') {
        throw 'Whitelist cleanup must preserve unknown files and their containing directory.'
    }
    # Exercise the actual WScript COM path locally, never the user's desktop/menu.
    $shortcutPath = Join-Path $safetyRoot 'fixture-shortcut.lnk'
    Invoke-SafetyHelper 'CreateShortcut' @($shortcutPath, $ownedPath, '--data "fixture data"', $safetyRoot, $ownedPath)
    if (!(Test-Path -LiteralPath $shortcutPath -PathType Leaf) -or
        !(Invoke-SafetyHelper 'ShortcutOwned' @($shortcutPath, $safetyRoot)) -or
        (Invoke-SafetyHelper 'ShortcutOwned' @($shortcutPath, ($safetyRoot + '-other')))) {
        throw 'Actual temporary shortcut creation and exact installation ownership checks failed.'
    }
    $originalShortcut = [Convert]::ToBase64String([IO.File]::ReadAllBytes($shortcutPath))
    $shortcutRejected = $false
    try { Invoke-SafetyHelper 'CreateShortcut' @($shortcutPath, $ownedPath, '', ($safetyRoot + '-other'), $ownedPath) }
    catch { $shortcutRejected = $true }
    if (!$shortcutRejected -or [Convert]::ToBase64String([IO.File]::ReadAllBytes($shortcutPath)) -ne $originalShortcut) {
        throw 'A shortcut belonging to another installation must not be overwritten.'
    }
    # Use the production task builder and genuine COM collections in memory.
    # TASK_VALIDATE_ONLY (1) never persists a scheduled task; flags 6 are not used here.
    $scheduler = $null; $taskFolder = $null; $definition = $null; $actions = $null; $action = $null
    try {
        $scheduler = [Activator]::CreateInstance([type]::GetTypeFromProgID('Schedule.Service'))
        $scheduler.Connect()
        $taskFolder = $scheduler.GetFolder('\')
        $optionsType = $assembly.GetType('Options', $true)
        $options = [Activator]::CreateInstance($optionsType, $true)
        $optionsType.GetField('InstallDir').SetValue($options, [string]$safetyRoot)
        $fixtureData = Join-Path $safetyRoot 'fixture-data'
        New-Item -ItemType Directory -Path $fixtureData | Out-Null
        $optionsType.GetField('DataDir').SetValue($options, [string]$fixtureData)
        $fixtureLauncher = Join-Path $safetyRoot '简作.exe'
        Copy-Item -LiteralPath $setup -Destination $fixtureLauncher
        $definition = Invoke-SafetyHelper 'BuildStartupTaskDefinition' @($scheduler, $options, $fixtureLauncher)
        $actions = Invoke-SafetyHelper 'Get' @($definition, 'Actions')
        $action = Invoke-SafetyHelper 'GetIndexed' @($actions, 'Item', 1)
        if ([string](Invoke-SafetyHelper 'Get' @($action, 'Path')) -ne $fixtureLauncher) {
            throw 'The real IActionCollection.Item indexed property must return the configured action.'
        }
        if (!('JianzuoInstallerRegisteredTaskFixture' -as [type])) {
            Add-Type -TypeDefinition 'public sealed class JianzuoInstallerRegisteredTaskFixture { public object Definition { get; set; } }'
        }
        $registeredTask = [JianzuoInstallerRegisteredTaskFixture]::new()
        $registeredTask.Definition = $definition.PSObject.BaseObject
        $taskExecutable = [string](Invoke-SafetyHelper 'TaskExecutable' @($registeredTask))
        $taskArguments = [string](Invoke-SafetyHelper 'TaskArguments' @($registeredTask))
        if ($taskExecutable -ne $fixtureLauncher -or $taskArguments -notmatch '--background' -or
            $taskArguments -notmatch '--data' -or !(Invoke-SafetyHelper 'IsJianzuoTask' @($registeredTask, $taskExecutable))) {
            throw 'Existing-task inspection must read the real COM indexed action through the production helpers.'
        }
        $validationName = 'Jianzuo-Installer-ValidateOnly-' + [Guid]::NewGuid().ToString('N')
        [void]$taskFolder.RegisterTaskDefinition($validationName, $definition, 1, $null, $null, 3, $null)
        $notRegistered = $false
        try {
            [void]$taskFolder.GetTask($validationName)
        } catch {
            $notRegistered = $_.Exception.GetBaseException().HResult -eq -2147024894
        }
        if (!$notRegistered) { throw 'Validate-only must not register or persist a task.' }
        Write-Output 'PASS: production startup definition, real COM indexed action inspection, validate-only, and no registered task.'
    } finally {
        foreach ($com in @($action,$actions,$definition,$taskFolder,$scheduler)) {
            if ($null -ne $com) { Invoke-SafetyHelper 'Release' @($com) }
        }
    }
    $quotedRoot = Join-Path $safetyRoot "quoted ' &% path"
    New-Item -ItemType Directory -Path $quotedRoot | Out-Null
    $quotedMarker = Join-Path $quotedRoot '.jianzuo-install'
    $quotedProgram = Join-Path $quotedRoot 'jianzuo-service.exe'
    $quotedUnknown = Join-Path $quotedRoot 'preserve.txt'
    [IO.File]::WriteAllText($quotedProgram, 'owned fixture')
    [IO.File]::WriteAllText($quotedUnknown, 'unknown fixture')
    [IO.File]::WriteAllText($quotedMarker, 'wrong owner')
    $removalScript = Invoke-SafetyHelper 'OwnedFileRemovalScript' @($quotedRoot, [int]::MaxValue)
    [void][scriptblock]::Create($removalScript)
    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($removalScript))
    $powershell = Join-Path $env:WINDIR 'System32\WindowsPowerShell\v1.0\powershell.exe'
    $rejectedCleanup = Start-Process -FilePath $powershell -WindowStyle Hidden -Wait -PassThru -ArgumentList @('-NoProfile','-NonInteractive','-EncodedCommand',$encoded)
    if ($rejectedCleanup.ExitCode -eq 0 -or !(Test-Path -LiteralPath $quotedProgram)) {
        throw 'Removal helper must reject a marker belonging to a different installation.'
    }
    [IO.File]::WriteAllText($quotedMarker, (Invoke-SafetyHelper 'InstallMarker' @($quotedRoot)), [Text.UTF8Encoding]::new($false))
    $cleanup = Start-Process -FilePath $powershell -WindowStyle Hidden -Wait -PassThru -ArgumentList @('-NoProfile','-NonInteractive','-EncodedCommand',$encoded)
    if ($cleanup.ExitCode -ne 0 -or (Test-Path -LiteralPath $quotedProgram) -or (Test-Path -LiteralPath $quotedMarker) -or
        [IO.File]::ReadAllText($quotedUnknown) -ne 'unknown fixture') {
        throw 'Encoded literal-path cleanup failed to preserve unrelated files or handle special characters.'
    }
    Write-Output 'PASS: unowned collisions, exact path ownership, bound marker, and non-recursive whitelist cleanup.'
    Write-Output 'PASS: encoded self-removal script rejects wrong owners, handles quoted paths, and preserves unrelated files.'
    Write-Output 'PASS: nested COM failure diagnostics and real temporary shortcut creation/ownership without changing user entries.'
} finally {
    Remove-FixtureTree $safetyRoot $safetyParent 'JianzuoInstallerSafety-'
}

function Quote-ProcessArgument([string]$Value) {
    if ($Value.Contains('"')) { throw 'Unexpected quote in fixture path.' }
    return '"' + $Value + '"'
}

function Invoke-InstallerRoundTrip {
    param(
        [string]$Label,
        [string]$InstallDir,
        [string]$DataDir,
        [switch]$SharedFiles
    )

    New-Item -ItemType Directory -Force -Path $InstallDir,$DataDir | Out-Null
    $sentinel = Join-Path $DataDir 'preserve-data.txt'
    [IO.File]::WriteAllText($sentinel, 'data must survive install, upgrade and uninstall')
    $shared = Join-Path $InstallDir 'unrelated-user-file.txt'
    $nested = Join-Path $InstallDir 'shared-files'
    if ($SharedFiles) {
        [IO.File]::WriteAllText($shared, 'unrelated file')
        New-Item -ItemType Directory -Path $nested | Out-Null
        [IO.File]::WriteAllText((Join-Path $nested 'jianzuo-service.exe'), 'unrelated nested file')
    }
        $install = Start-Process -FilePath $setup -WindowStyle Hidden -Wait -PassThru -ArgumentList @(
            '--quiet',
            '--install-dir', (Quote-ProcessArgument $InstallDir),
            '--data', (Quote-ProcessArgument $DataDir),
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
        $uninstall = Start-Process -FilePath $uninstaller -WindowStyle Hidden -Wait -PassThru -ArgumentList @(
            '--quiet',
            '--uninstall',
            '--install-dir', (Quote-ProcessArgument $InstallDir),
            '--data', (Quote-ProcessArgument $DataDir),
            '--no-shortcuts', '--no-desktop', '--no-registry', '--no-startup', '--no-launch'
        )
        if ($uninstall.ExitCode -ne 0) {
            throw "$Label uninstall test failed with exit code $($uninstall.ExitCode)."
        }
        $ownedNames = @('简作.exe','jianzuo-service.exe','卸载简作.exe','使用说明.md','THIRD-PARTY-NOTICES.txt','.jianzuo-install')
        for ($i = 0; $i -lt 80; $i++) {
            $remaining = @($ownedNames | Where-Object { Test-Path -LiteralPath (Join-Path $InstallDir $_) })
            if (!$remaining.Count -and ($SharedFiles -or !(Test-Path -LiteralPath $InstallDir))) { break }
            Start-Sleep -Milliseconds 250
        }
        if ($remaining.Count) { throw "$Label uninstall did not remove all owned files: $($remaining -join ', ')" }
        if (!(Test-Path -LiteralPath $sentinel) -or [IO.File]::ReadAllText($sentinel) -ne 'data must survive install, upgrade and uninstall') {
            throw "$Label uninstall changed or removed data."
        }
        if ($SharedFiles) {
            if (!(Test-Path -LiteralPath $shared) -or [IO.File]::ReadAllText($shared) -ne 'unrelated file' -or
                [IO.File]::ReadAllText((Join-Path $nested 'jianzuo-service.exe')) -ne 'unrelated nested file') {
                throw "$Label uninstall changed unrelated shared-directory files."
            }
        } elseif (Test-Path -LiteralPath $InstallDir) {
            throw "$Label uninstall did not remove the empty installation directory."
        }
        Write-Output ("PASS: $Label owned files removed, data and unrelated files preserved.")
}

if ($Full) {
    $testRoot = Join-Path ([IO.Path]::GetTempPath()) ('JianzuoInstallerTest-' + [Guid]::NewGuid().ToString('N'))
    try {
        Invoke-InstallerRoundTrip -Label 'same-volume' `
            -InstallDir (Join-Path $testRoot 'program') `
            -DataDir (Join-Path $testRoot 'data') -SharedFiles
        Invoke-InstallerRoundTrip -Label 'empty-program-directory' `
            -InstallDir (Join-Path $testRoot 'empty-program') `
            -DataDir (Join-Path $testRoot 'empty-data')
    } finally {
        Remove-FixtureTree $testRoot ([IO.Path]::GetTempPath()) 'JianzuoInstallerTest-'
    }

    $tempVolume = [IO.Path]::GetPathRoot([IO.Path]::GetFullPath($testRoot))
    $projectVolume = [IO.Path]::GetPathRoot($root)
    if (![string]::Equals($tempVolume, $projectVolume, [StringComparison]::OrdinalIgnoreCase)) {
        $crossRoot = Join-Path $root ('.installer-test-' + [Guid]::NewGuid().ToString('N'))
        try {
            Invoke-InstallerRoundTrip -Label 'cross-volume' `
                -InstallDir (Join-Path $testRoot 'cross-program') `
                -DataDir (Join-Path $crossRoot 'data') -SharedFiles
        } finally {
            Remove-FixtureTree $crossRoot $root '.installer-test-'
            Remove-FixtureTree $testRoot ([IO.Path]::GetTempPath()) 'JianzuoInstallerTest-'
        }
    } else {
        Write-Output 'SKIP: cross-volume test requires a second local temporary volume.'
    }
}

Write-Output ('Installer verification passed: ' + $setup)
Write-Output ('SHA256: ' + $actualHash)
