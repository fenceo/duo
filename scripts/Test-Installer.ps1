[CmdletBinding()]
param(
    [string]$ProjectRoot = (Split-Path $PSScriptRoot),
    [string]$Setup = '',
    [string]$Helper = '',
    [switch]$Full,
    [switch]$IsolatedUser,
    [switch]$IsolatedLifecycle
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$root = [IO.Path]::GetFullPath($ProjectRoot)
if ($Full) { throw 'Production Full install/uninstall tests are disabled. Use -IsolatedLifecycle (compile-time GUID namespace) or a disposable VM.' }
if (!$Setup) { $Setup = Join-Path $root 'dist\Duo-Setup-User-x64.exe' }
if (!$Helper) { $Helper = Join-Path (Split-Path $Setup) '.installer-build\DuoMaintenance.exe' }
$setup = [IO.Path]::GetFullPath($Setup)
$helper = [IO.Path]::GetFullPath($Helper)
foreach ($file in @($setup,($setup + '.sha256'),$helper)) {
    if (!(Test-Path -LiteralPath $file -PathType Leaf)) { throw "Missing fixture input: $file" }
}
$expected = ((Get-Content -Raw -Encoding ASCII -LiteralPath ($setup + '.sha256')).Trim() -split '\s+')[0]
if ((Get-FileHash -LiteralPath $setup -Algorithm SHA256).Hash -ine $expected) { throw 'Installer SHA256 mismatch.' }
$binary = [Text.Encoding]::ASCII.GetString([IO.File]::ReadAllBytes($setup))
if (!$binary.StartsWith('MZ') -or !$binary.Contains('Inno Setup Setup Data')) { throw 'Release artifact is not a native Inno Setup executable.' }
$definition = Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $root 'installer\windows\Jianzuo.iss')
$source = Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $root 'installer\windows\JianzuoMaintenance.cs')
foreach ($contract in @('PrivilegesRequired=lowest','[Files]','[Icons]','[Registry]','if CurStep = ssInstall then ApplyStartup','--rollback','--commit','--update','TaskRecognized','MigrationPage.Values[0] := False','CloseApplications=no','WizardForm.DirBrowseButton.Enabled := False','DataPage.Buttons[0].Enabled','--confirm-data','--release-guard')) {
    if (!$definition.Contains($contract)) { throw "Missing Inno safety contract: $contract" }
}
foreach ($unsafe in @('taskkill','Directory.Delete(','ExtractPayload','--uninstall --install-dir')) {
    if ($source.Contains($unsafe)) { throw "Maintenance helper is not narrowly scoped: $unsafe" }
}
$assembly = [Reflection.Assembly]::Load([IO.File]::ReadAllBytes($helper))
$program = $assembly.GetType('Program',$true)
$binding = [Reflection.BindingFlags]'NonPublic,Static'
function Invoke-Helper([string]$Name,[object[]]$Arguments) {
    $method = $program.GetMethod($Name,$binding)
    if (!$method) { throw "Missing helper: $Name" }
    $parameters = $method.GetParameters()
    $converted = [object[]]::new($Arguments.Count)
    for ($index = 0; $index -lt $Arguments.Count; $index++) {
        $value = $Arguments[$index]
        if ($null -ne $value) { $value = $value.PSObject.BaseObject }
        $converted[$index] = [System.Management.Automation.LanguagePrimitives]::ConvertTo($value, $parameters[$index].ParameterType)
    }
    return ,($method.Invoke($null,$converted))
}
function New-Options([string]$ProgramDir,[string]$DataDir) {
    $type = $assembly.GetType('Options',$true)
    $value = [Activator]::CreateInstance($type,$true)
    $type.GetField('InstallDir').SetValue($value,$ProgramDir)
    $type.GetField('DataDir').SetValue($value,$DataDir)
    return $value
}
function Set-Field($Object,[string]$Name,$Value) { $Object.GetType().GetField($Name).SetValue($Object,$Value) }
function Assert-Rejected([scriptblock]$Action,[string]$Message) {
    $rejected = $false
    try { & $Action | Out-Null } catch { $rejected = $true }
    if (!$rejected) { throw $Message }
}
function Quote-Arg([string]$Value) {
    if ($Value.Contains('"')) { throw 'Unexpected quote in fixture path.' }
    return '"' + $Value + '"'
}
function Run-Process([string]$Exe,[string[]]$Arguments) {
    $process = Start-Process -FilePath $Exe -ArgumentList $Arguments -WindowStyle Hidden -PassThru -Wait
    return $process.ExitCode
}
function Remove-Fixture([string]$Path,[string]$Parent,[string]$Prefix) {
    $resolved = [IO.Path]::GetFullPath($Path).TrimEnd('\')
    if ([IO.Path]::GetDirectoryName($resolved) -ine [IO.Path]::GetFullPath($Parent).TrimEnd('\') -or
        [IO.Path]::GetFileName($resolved) -notmatch ('^' + [regex]::Escape($Prefix) + '[a-f0-9]{32}$')) { throw 'Unsafe fixture cleanup target.' }
    if (Test-Path -LiteralPath $resolved) { Remove-Item -LiteralPath $resolved -Recurse -Force }
}
$parent = [IO.Path]::GetTempPath()
$fixture = Join-Path $parent ('JianzuoInstallerSafety-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $fixture | Out-Null
try {
    # /VERIFY explicitly exits before wizard/registry/task inspection. Inno returns
    # 1 when InitializeSetup returns false; a separate exact sentinel proves success.
    $sentinel = Join-Path $fixture 'verify.txt'
    $exitCode = Run-Process $setup @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART','/VERIFY=1',('/VERIFYRESULT=' + (Quote-Arg $sentinel)))
    if ($exitCode -ne 1 -or !(Test-Path -LiteralPath $sentinel) -or [IO.File]::ReadAllText($sentinel) -cne 'Jianzuo installer verification v1') {
        throw 'Native installer pure verification did not exit before installation.'
    }
    $programDir = Join-Path $fixture 'program'
    $dataDir = Join-Path $fixture 'data'
    New-Item -ItemType Directory -Path $programDir,$dataDir | Out-Null
    $options = New-Options $programDir $dataDir
    Invoke-Helper 'ValidateOptions' @($options)
    foreach ($uncanonical in @(' ' + $dataDir,$dataDir + ' ','%LOCALAPPDATA%\Duo\data','C:\%USERNAME%\data')) {
        Assert-Rejected { Invoke-Helper 'NormalizeDirectory' @($uncanonical) } 'Path normalization accepted a different path from native registration/launch.'
    }
    $spacedData=Join-Path $fixture 'legitimate internal spaces'
    if ([string](Invoke-Helper 'NormalizeDirectory' @($spacedData)) -cne $spacedData) { throw 'Internal path spaces were damaged.' }
    Invoke-Helper 'ValidateDataChoice' @($options,'')
    $otherData = Join-Path $fixture 'new-data'
    Set-Field $options 'DataDir' $otherData
    Assert-Rejected { Invoke-Helper 'ValidateDataChoice' @($options,$dataDir) } 'Keep mode silently switched data.'
    Set-Field $options 'DataMode' 'fresh'
    Set-Field $options 'PreviousData' $dataDir
    Assert-Rejected { Invoke-Helper 'ValidateDataChoice' @($options,$dataDir) } 'Fresh data accepted without interaction/confirmation.'
    Set-Field $options 'Interactive' $true
    Set-Field $options 'ConfirmData' $true
    Invoke-Helper 'ValidateDataChoice' @($options,$dataDir)
    Set-Field $options 'Update' $true
    Assert-Rejected { Invoke-Helper 'ValidateDataChoice' @($options,$dataDir) } 'UPDATE changed data with confirmation flags.'
    Set-Field $options 'Update' $false
    New-Item -ItemType Directory -Path $otherData | Out-Null
    [IO.File]::WriteAllText((Join-Path $otherData 'unknown.txt'),'never overwrite')
    Assert-Rejected { Invoke-Helper 'ValidateDataChoice' @($options,$dataDir) } 'Fresh mode accepted non-empty data.'
    Set-Field $options 'DataDir' (Join-Path $dataDir 'nested')
    Assert-Rejected { Invoke-Helper 'ValidateDataChoice' @($options,$dataDir) } 'Nested data directory was accepted.'
    Set-Field $options 'DataDir' $dataDir
    Set-Field $options 'DataMode' 'keep'
    Set-Field $options 'PreviousData' ''
    $mutexName=[string](Invoke-Helper 'DataMutexName' @($dataDir))
    $mutex=[Threading.Mutex]::new($true,$mutexName)
    try { Assert-Rejected { Invoke-Helper 'ValidateDataIdle' @($dataDir) } 'Running launcher mutex ignored.' }
    finally { $mutex.ReleaseMutex(); $mutex.Dispose() }
    $lockFile=Join-Path $dataDir 'service.lock'
    $lock=[IO.File]::Open($lockFile,[IO.FileMode]::CreateNew,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
    try { Assert-Rejected { Invoke-Helper 'ValidateDataIdle' @($dataDir) } 'Running service lock ignored.' }
    finally { $lock.Dispose() }
    Invoke-Helper 'ValidateDataIdle' @($dataDir)
    $validatorDir=Join-Path $fixture 'validator-bin'
    $validatorTimeout=Join-Path $fixture 'validator-timeout'
    New-Item -ItemType Directory -Path $validatorDir,$validatorTimeout | Out-Null
    $fakeValidator=Join-Path $validatorDir 'duo-service.exe'
    & (Join-Path $env:WINDIR 'Microsoft.NET\Framework64\v4.0.30319\csc.exe') /nologo /target:exe ('/out:' + $fakeValidator) (Join-Path $root 'installer\windows\Test-ValidatorFixture.cs')
    if ($LASTEXITCODE -ne 0) { throw 'Validator protocol fixture compilation failed.' }
    Set-Field $options 'Validator' $fakeValidator
    Assert-Rejected { Invoke-Helper 'ValidateExistingData' @($options) } 'Unknown validator protocol accepted.'
    Set-Field $options 'DataDir' $validatorTimeout
    Assert-Rejected { Invoke-Helper 'ValidateExistingData' @($options) } 'Hung validator accepted.'
    $validatorPid=[int][IO.File]::ReadAllText((Join-Path $validatorTimeout 'validator.pid'))
    if (Get-Process -Id $validatorPid -ErrorAction SilentlyContinue) { throw 'Timed-out installer-owned validator remains running.' }
    Set-Field $options 'DataDir' $dataDir
    Set-Field $options 'Validator' ''
    foreach ($invalid in @('C:\','relative\path','\\server\share','C:\bad"path')) {
        Assert-Rejected { Invoke-Helper 'NormalizeDirectory' @($invalid) } "Accepted unsafe path: $invalid"
    }
    Assert-Rejected { Invoke-Helper 'ValidateOptions' @((New-Options $programDir (Join-Path $programDir 'data'))) } 'Nested data accepted.'
    $collision = Join-Path $programDir 'duo-service.exe'
    [IO.File]::WriteAllText($collision,'unknown existing file')
    Assert-Rejected { Invoke-Helper 'ValidateInstallDestination' @($programDir) } 'Unowned collision accepted.'
    if ([IO.File]::ReadAllText($collision) -cne 'unknown existing file') { throw 'Validation altered user file.' }
    $marker = [string](Invoke-Helper 'InstallMarker' @($programDir))
    if ($marker -cne ("Jianzuo installer ownership v1" + [char]10 + $programDir)) { throw 'Old marker format changed.' }
    [IO.File]::WriteAllText((Join-Path $programDir '.jianzuo-install'),$marker,[Text.UTF8Encoding]::new($false))
    Invoke-Helper 'ValidateInstallDestination' @($programDir)
    foreach ($extension in @('.url','.pif','')) {
        $variant = Join-Path $fixture ('foreign-shortcut' + $extension)
        [IO.File]::WriteAllText($variant,'unknown shortcut must survive')
        Assert-Rejected { Invoke-Helper 'ValidateShortcutVariants' @((Join-Path $fixture 'foreign-shortcut.lnk')) } 'Native icon creation may delete an unknown shortcut variant.'
        if ([IO.File]::ReadAllText($variant) -cne 'unknown shortcut must survive') { throw 'Variant preflight changed unknown content.' }
        Remove-Item -LiteralPath $variant -Force
    }
    Set-Field $options 'Update' $true
    Invoke-Helper 'ValidateUpdate' @($options,$true,$programDir,$dataDir)
    Assert-Rejected { Invoke-Helper 'ValidateUpdate' @($options,$false,$programDir,$dataDir) } 'Update accepted a new installation.'
    Assert-Rejected { Invoke-Helper 'ValidateUpdate' @($options,$true,$programDir,($dataDir + '-other')) } 'Update moved data.'
    Set-Field $options 'Update' $false
    $taskType = $assembly.GetType('ExistingTask',$true)
    $task = [Activator]::CreateInstance($taskType,$true)
    $oldDir = Join-Path $fixture 'old-portable'
    $oldExe = Join-Path $oldDir '简作.exe'
    Set-Field $task 'Executable' $oldExe
    Set-Field $task 'DataDir' $dataDir
    Set-Field $task 'Recognized' $true
    Assert-Rejected { Invoke-Helper 'ValidateTaskChoice' @($options,$task) } 'Cross-directory task migration lacked consent.'
    Set-Field $options 'MigrateFrom' $oldDir
    Invoke-Helper 'ValidateTaskChoice' @($options,$task)
    Set-Field $options 'Update' $true
    Assert-Rejected { Invoke-Helper 'ValidateTaskChoice' @($options,$task) } 'Silent update took over old portable task.'
    Set-Field $options 'Update' $false
    Set-Field $task 'Recognized' $false
    Assert-Rejected { Invoke-Helper 'ValidateTaskChoice' @($options,$task) } 'Unknown same-name task accepted.'
    foreach ($arguments in @(('--data "' + $dataDir + '" --background'),('"' + $oldExe + '" --data "' + $dataDir + '" --background'))) {
        $parameters = [object[]]@([string]$oldExe,[string]$arguments,[string]'')
        $recognized = $program.GetMethod('RecognizeArguments',$binding).Invoke($null,$parameters)
        if (!$recognized -or $parameters[2] -ine $dataDir) { throw 'Valid legacy launcher arguments not recognized.' }
    }
    foreach ($arguments in @(('"C:\unknown\other.exe" --data "' + $dataDir + '" --background'),('--data "' + $dataDir + '" --background --extra'))) {
        $parameters = [object[]]@([string]$oldExe,[string]$arguments,[string]'')
        if ($program.GetMethod('RecognizeArguments',$binding).Invoke($null,$parameters)) { throw 'Unknown launcher argument accepted.' }
    }
    $cause = [Runtime.InteropServices.COMException]::new('fixture denied',[int]-2147024891)
    $formatted = [string](Invoke-Helper 'FormatInstallError' @([Reflection.TargetInvocationException]::new($cause),'fixture stage','fixture.log'))
    if (!$formatted.Contains('fixture denied') -or !$formatted.Contains('0x80070005')) { throw 'Inner diagnostics lost.' }
    # Genuine Task Scheduler definition, only TASK_VALIDATE_ONLY (1); no persisted task.
    $scheduler=$null; $folder=$null; $definitionObject=$null
    try {
        $scheduler=[Activator]::CreateInstance([type]::GetTypeFromProgID('Schedule.Service')); $scheduler.Connect(); $folder=$scheduler.GetFolder('\')
        $definitionObject = Invoke-Helper 'BuildStartupTaskDefinition' @($scheduler,$options,(Join-Path $programDir 'Duo.exe'))
        if (!('JianzuoRegisteredTaskFixtureV19' -as [type])) {
            Add-Type 'public sealed class JianzuoRegisteredTaskFixtureV19 { private object definition; public object Definition { get { System.IntPtr p=System.Runtime.InteropServices.Marshal.GetIUnknownForObject(definition); try { return System.Runtime.InteropServices.Marshal.GetObjectForIUnknown(p); } finally { System.Runtime.InteropServices.Marshal.Release(p); } } set { definition=value; } } public string Xml { get; set; } public bool Enabled { get; set; } }'
        }
        $wrapper=[JianzuoRegisteredTaskFixtureV19]::new(); $wrapper.Definition=$definitionObject.PSObject.BaseObject; $wrapper.Xml=$definitionObject.XmlText; $wrapper.Enabled=$true
        if ([string](Invoke-Helper 'TaskExecutable' @($wrapper)) -ine (Join-Path $programDir 'Duo.exe')) { throw 'Indexed COM action regression.' }
        $inspected = Invoke-Helper 'InspectTask' @($wrapper)
        if (!$inspected.GetType().GetField('Recognized').GetValue($inspected)) { throw 'Own current-user task rejected.' }
        $validationName='Jianzuo-ValidateOnly-' + [Guid]::NewGuid().ToString('N')
        [void]$folder.RegisterTaskDefinition($validationName,$definitionObject,1,$null,$null,3,$null)
        $absent=$false
        try { [void]$folder.GetTask($validationName) } catch { $absent=$_.Exception.GetBaseException().HResult -eq -2147024894 }
        if (!$absent) { throw 'Validate-only persisted a task.' }
    } finally {
        foreach($com in @($definitionObject,$folder,$scheduler)) { if ($null -ne $com) { Invoke-Helper 'Release' @($com) } }
    }
    Write-Output 'PASS: native Inno pure verification, SHA256, marker compatibility, path/update ownership, explicit migration, strict legacy arguments, real COM validate-only.'
} finally { Remove-Fixture $fixture $parent 'JianzuoInstallerSafety-' }

if ($IsolatedLifecycle) {
    & (Join-Path $root 'installer\windows\Test-IsolatedLifecycle.ps1') -ProjectRoot $root
    if ($LASTEXITCODE -ne 0) { throw 'Isolated Inno lifecycle failed.' }
}
Write-Output ('Installer verification passed: ' + $setup)
