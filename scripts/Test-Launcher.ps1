[CmdletBinding()]
param([string]$Launcher = (Join-Path (Split-Path $PSScriptRoot -Parent) 'dist\Duo-portable-windows-x64\duo-launcher.exe'))
$ErrorActionPreference = 'Stop'
if (!(Test-Path -LiteralPath $Launcher -PathType Leaf)) { throw 'Compiled launcher fixture is missing.' }
$assembly = [Reflection.Assembly]::Load([IO.File]::ReadAllBytes([IO.Path]::GetFullPath($Launcher)))
$startupType = $assembly.GetType('UserStartup', $true)
function Invoke-LauncherHelper([string]$Name, [object[]]$Arguments) {
    $method = $startupType.GetMethod($Name, [Reflection.BindingFlags]'NonPublic,Static')
    if (!$method) { throw ('Missing launcher helper: ' + $Name) }
    $values = [object[]]::new($Arguments.Count)
    for ($i = 0; $i -lt $Arguments.Count; $i++) {
        $values[$i] = [System.Management.Automation.LanguagePrimitives]::ConvertTo($Arguments[$i].PSObject.BaseObject, $method.GetParameters()[$i].ParameterType)
    }
    return ,($method.Invoke($null, $values))
}
$taskRoot = 'C:\Jianzuo launcher fixture'
$taskExecutable = Join-Path $taskRoot 'launcher.exe'
$taskArguments = '--data "C:\Jianzuo data fixture" --background'
$legacyArguments = '"' + $taskExecutable + '" ' + $taskArguments
if ((Invoke-LauncherHelper 'NormalizeTaskArguments' @($legacyArguments, $taskExecutable)) -ne $taskArguments) { throw 'Legacy executable prefix normalization failed.' }
if ((Invoke-LauncherHelper 'NormalizeTaskArguments' @($taskArguments, $taskExecutable)) -ne $taskArguments) { throw 'Current arguments must not be changed.' }
foreach ($candidate in @($taskArguments, $legacyArguments)) {
    if (!(Invoke-LauncherHelper 'TaskActionMatches' @($taskExecutable, $candidate, $taskRoot, $taskExecutable, $taskArguments, $taskRoot))) { throw 'Current/legacy task action was not recognized.' }
}
foreach ($candidate in @(
    @('C:\Other\launcher.exe', $taskArguments, $taskRoot),
    @($taskExecutable, '--data "C:\Other data" --background', $taskRoot),
    @($taskExecutable, $taskArguments, 'C:\Other'),
    @($taskExecutable, ('"C:\Other\launcher.exe" ' + $taskArguments), $taskRoot)
)) {
    if (Invoke-LauncherHelper 'TaskActionMatches' @($candidate[0], $candidate[1], $candidate[2], $taskExecutable, $taskArguments, $taskRoot)) { throw 'Another installation must not match the current startup action.' }
}
$scheduler = $null; $definition = $null; $actions = $null; $action = $null; $readAction = $null
try {
    $scheduler = New-Object -ComObject Schedule.Service
    $scheduler.Connect()
    $definition = $scheduler.NewTask(0)
    $actions = $definition.Actions
    $action = $actions.Create(0)
    $action.Path = $taskExecutable
    $action.Arguments = $taskArguments
    $readAction = Invoke-LauncherHelper 'GetIndexed' @($actions, 'Item', 1)
    if ($readAction.Path -ne $taskExecutable -or $readAction.Arguments -ne $taskArguments) { throw 'Real indexed COM action property regression.' }
} finally {
    foreach ($com in @($action, $actions, $definition, $scheduler)) {
        if ($null -ne $com -and [Runtime.InteropServices.Marshal]::IsComObject($com)) { [void][Runtime.InteropServices.Marshal]::ReleaseComObject($com) }
    }
}
Write-Output 'PASS: launcher current/legacy argument matching, foreign-owner rejection, and real in-memory indexed COM action. No task registered.'
