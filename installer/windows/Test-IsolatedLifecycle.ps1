[CmdletBinding()]
param([Parameter(Mandatory=$true)][string]$ProjectRoot,[string]$Payload='',[string]$DataValidator='')
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
$root=[IO.Path]::GetFullPath($ProjectRoot)
$identity=[Guid]::NewGuid().ToString('N')
$fixture=Join-Path ([IO.Path]::GetTempPath()) ('JianzuoInnoLifecycle-' + $identity)
$output=Join-Path $fixture 'build'
$program=Join-Path $fixture 'program'
$data=Join-Path $fixture 'data'
$oldProgram=Join-Path $fixture 'old-portable'
$key='Jianzuo-Test-' + $identity
$name='Jianzuo Test ' + $identity
$settings='HKCU:\Software\' + $key
$uninstall='HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\' + $key + '_is1'
$legacyUninstall='HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\' + $key
$runKey='HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
$runName=$key + 'Portable'
$logDirectory=Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) $key
$desktop=Join-Path ([Environment]::GetFolderPath('DesktopDirectory')) ($name + '.lnk')
$menu=Join-Path ([Environment]::GetFolderPath('Programs')) $name
$scheduler=$null; $folder=$null
$lastSetupLog=''
function Q([string]$Value) { if ($Value.Contains('"')) { throw 'Invalid test path.' }; $trailing=$Value.Length - $Value.TrimEnd('\').Length; return '"' + $Value + ('\' * $trailing) + '"' }
function Exec-Helper([string[]]$Arguments) {
    # Start-Process -Wait waits the entire descendant tree, including the guard
    # that deliberately outlives prepare. Wait only for this direct helper.
    $info=[Diagnostics.ProcessStartInfo]::new($helperPath,($Arguments -join ' '))
    $info.UseShellExecute=$false; $info.CreateNoWindow=$true
    $process=[Diagnostics.Process]::Start($info)
    try { $process.WaitForExit(); return $process.ExitCode } finally { $process.Dispose() }
}
function Invoke-Private([string]$Method,[object[]]$Arguments) {
    $member=$type.GetMethod($Method,$flags); $parameters=$member.GetParameters(); $converted=[object[]]::new($Arguments.Count)
    for ($i=0;$i -lt $Arguments.Count;$i++) { $converted[$i]=[System.Management.Automation.LanguagePrimitives]::ConvertTo($Arguments[$i].PSObject.BaseObject,$parameters[$i].ParameterType) }
    return ,($member.Invoke($null,$converted))
}
function Exec-Setup([string[]]$Extra) {
    $script:lastSetupLog=Join-Path $fixture ('setup-' + [Guid]::NewGuid().ToString('N') + '.log')
    $run=Start-Process -FilePath (Join-Path $output 'Duo-Setup-User-x64.exe') -WindowStyle Hidden -PassThru -Wait -ArgumentList (@('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART','/NOLAUNCH',('/DIR=' + (Q $program)),('/DATADIR=' + (Q $data)),('/LOG=' + (Q $script:lastSetupLog))) + $Extra)
    return $run.ExitCode
}
function Data-Snapshot([string]$Path) {
    return @(Get-ChildItem -LiteralPath $Path -Recurse -File | Sort-Object FullName | ForEach-Object { $_.FullName.Substring($Path.Length) + ':' + (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash }) -join "`n"
}
function Assert-NoDataChange([string]$Path,[string]$Before) { if ((Data-Snapshot $Path) -cne $Before) { throw 'Installer changed user data contents or file inventory.' } }
function Initialize-SyntheticData([string]$Path) {
    $info=[Diagnostics.ProcessStartInfo]::new((Join-Path $output '.installer-build\payload\duo-service.exe'),('--portable-init --data ' + (Q $Path)))
    $info.UseShellExecute=$false; $info.CreateNoWindow=$true; $info.RedirectStandardInput=$true; $info.RedirectStandardError=$true; $info.RedirectStandardOutput=$true
    $process=[Diagnostics.Process]::Start($info)
    try {
        $process.StandardInput.WriteLine('{"password":"fixture-password","config":{"listen":"127.0.0.1:18789","distro":"Ubuntu-22.04","user":"fixture","codex":"codex","workspaces":["/tmp"],"model":"fixture"}}')
        $process.StandardInput.Close()
        $errorText=$process.StandardError.ReadToEnd(); [void]$process.StandardOutput.ReadToEnd()
        if (!$process.WaitForExit(20000) -or $process.ExitCode -ne 0) { throw ('Synthetic data initialization failed: ' + $errorText) }
    } finally { $process.Dispose() }
}
function Read-TestTask {
    try { return $folder.GetTask($key) } catch { if ($_.Exception.GetBaseException().HResult -eq -2147024894) { return $null }; throw }
}
function Uninstall-Test {
    $exe=Join-Path $program 'unins000.exe'
    if (!(Test-Path -LiteralPath $exe)) { throw 'Missing native uninstaller.' }
    $run=Start-Process -FilePath $exe -ArgumentList @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART') -WindowStyle Hidden -PassThru -Wait
    if ($run.ExitCode -ne 0) { throw ('Native uninstaller failed: ' + $run.ExitCode) }
    if ((Read-TestTask) -or (Test-Path -LiteralPath $settings) -or (Test-Path -LiteralPath $uninstall) -or (Test-Path -LiteralPath $desktop)) { throw 'Native uninstall left owned fixture entries.' }
    if ([IO.File]::ReadAllText((Join-Path $data 'preserve-data.txt')) -cne 'synthetic data must survive') { throw 'Uninstall modified data.' }
}
New-Item -ItemType Directory -Path $fixture,$data,$oldProgram | Out-Null
try {
    if ($DataValidator) {
        if (!$Payload) { $Payload=Join-Path $root 'dist\Duo-portable-windows-x64.zip' }
        $fixturePayload=Join-Path $fixture 'fixture-payload'
        Expand-Archive -LiteralPath $Payload -DestinationPath $fixturePayload
        Copy-Item -LiteralPath $DataValidator -Destination (Join-Path $fixturePayload 'duo-service.exe') -Force
        $Payload=Join-Path $fixture 'fixture-payload.zip'
        Compress-Archive -LiteralPath @((Join-Path $fixturePayload 'Duo.exe'),(Join-Path $fixturePayload 'duo-service.exe'),(Join-Path $fixturePayload '使用说明.md'),(Join-Path $fixturePayload 'THIRD-PARTY-NOTICES.txt')) -DestinationPath $Payload
    }
    & (Join-Path $root 'scripts\Build-Installer.ps1') -ProjectRoot $root -Payload $Payload -OutputDir $output -TestNamespace $identity -SkipTests
    if ($LASTEXITCODE -ne 0) { throw 'Isolated installer compilation failed.' }
    # Confirm actual compiled helper identities before any external-state operation.
    $assembly=[Reflection.Assembly]::Load([IO.File]::ReadAllBytes((Join-Path $output '.installer-build\DuoMaintenance.exe')))
    $type=$assembly.GetType('Program',$true); $flags=[Reflection.BindingFlags]'NonPublic,Static'
    if ($type.GetField('TaskName',$flags).GetValue($null) -cne $key -or
        $type.GetField('SettingsKey',$flags).GetValue($null) -cne ('Software\' + $key)) { throw 'Compiled isolated identities differ; refusing lifecycle.' }
    $scheduler=[Activator]::CreateInstance([type]::GetTypeFromProgID('Schedule.Service')); $scheduler.Connect(); $folder=$scheduler.GetFolder('\')
    if ((Read-TestTask) -or (Test-Path -LiteralPath $settings) -or (Test-Path -LiteralPath $uninstall) -or (Test-Path -LiteralPath $desktop) -or (Test-Path -LiteralPath $menu)) { throw 'Generated fixture identities already exist.' }
    if ((Exec-Setup @('/UPDATE=1')) -eq 0) { throw 'UPDATE accepted a new installation.' }
    if (Test-Path -LiteralPath $settings) { throw 'Rejected UPDATE wrote registration.' }
    if ((Exec-Setup @()) -ne 0) { throw 'Isolated native first install failed.' }
    [IO.File]::WriteAllText((Join-Path $data 'preserve-data.txt'),'synthetic data must survive')
    # No /DIR or /DATADIR: exercise the actual wizard defaults independently of
    # installation. This early-abort hook only exists in GUID fixture builds.
    $defaults=Join-Path $fixture 'defaults.txt'
    $probe=Start-Process -FilePath (Join-Path $output 'Duo-Setup-User-x64.exe') -WindowStyle Hidden -PassThru -Wait -ArgumentList @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART',('/TESTDEFAULTSRESULT=' + (Q $defaults)))
    if ($probe.ExitCode -eq 0 -or !(Test-Path -LiteralPath $defaults) -or [IO.File]::ReadAllText($defaults) -cne ($program + "`r`n" + $data)) { throw 'Wizard failed to inherit existing program/data without command-line defaults.' }
    $dataWithoutSlash=$data; $data += '\'
    if ((Exec-Setup @('/UPDATE=1')) -ne 0) { throw 'Trailing-backslash data path broke helper argument quoting.' }
    $data=$dataWithoutSlash
    foreach($file in @('Duo.exe','duo-service.exe','DuoMaintenance.exe','unins000.exe','.jianzuo-install')) {
        if (!(Test-Path -LiteralPath (Join-Path $program $file))) { throw ('Installed file missing: ' + $file) }
    }
    if (!(Read-TestTask) -or !(Test-Path -LiteralPath $desktop) -or (Get-ItemPropertyValue -LiteralPath $settings -Name Installer) -cne 'inno') { throw 'Native registration/shortcut/startup missing.' }
    Initialize-SyntheticData $data
    $originalData=$data
    $originalSnapshot=Data-Snapshot $originalData
    # Inspect the cross-phase guard itself without modifying any task/registry.
    $guardTarget=Join-Path $fixture 'guard-only-data'
    $guardTransaction=Join-Path $fixture 'guard-only.xml'
    $helperPath=Join-Path $output '.installer-build\DuoMaintenance.exe'
    # Reusing the transaction path models retrying within one open installer;
    # the previous stopped sentinel must not poison the next guard.
    foreach ($guardAttempt in 1..2) {
      $guardPrepare=Exec-Helper @('--prepare','--install-dir',(Q $program),'--data',(Q $guardTarget),'--previous-data',(Q $originalData),'--data-mode','fresh','--interactive','--confirm-data','--owner-pid',$PID.ToString(),'--transaction',(Q $guardTransaction))
      if ($guardPrepare -ne 0) { throw 'Isolated guard prepare failed.' }
      try {
        $guardDocument=[Xml.XmlDocument]::new(); $guardDocument.Load($guardTransaction)
        Invoke-Private 'CheckDataGuard' @($guardDocument,$guardTransaction)
        foreach ($guardPath in @($originalData,$guardTarget)) {
            $mutexName=[string](Invoke-Private 'DataMutexName' @($guardPath))
            $opened=[Threading.Mutex]::OpenExisting($mutexName); $opened.Dispose()
            $busy=$false
            try { $probeLock=[IO.File]::Open((Join-Path $guardPath 'service.lock'),[IO.FileMode]::Open,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None); $probeLock.Dispose() } catch [IO.IOException] { $busy=$true }
            if (!$busy) { throw 'Guard did not exclusively protect both selected data directories.' }
        }
      } finally {
        $guardRelease=Exec-Helper @('--release-guard','--transaction',(Q $guardTransaction))
        if ($guardRelease -ne 0) { throw 'Isolated data guard failed to release.' }
      }
    }
    Assert-NoDataChange $originalData $originalSnapshot
    if (@(Get-ChildItem -LiteralPath $guardTarget -Force).Count -ne 0) { throw 'Guard left new lock file after release.' }
    $freshData=Join-Path $fixture 'fresh-data'
    $data=$freshData
    if ((Exec-Setup @()) -eq 0) { throw 'Silent keep mode switched data.' }
    if ((Exec-Setup @('/TESTDATAMODE=fresh')) -eq 0) { throw 'Data switch proceeded without interactive confirmation.' }
    if ((Exec-Setup @('/UPDATE=1','/TESTDATAMODE=fresh','/TESTCONFIRMDATA=1')) -eq 0) { throw 'UPDATE accepted explicit data switching.' }
    $taskBeforeSwitch=(Read-TestTask).Xml
    if ((Exec-Setup @('/TESTDATAMODE=fresh','/TESTCONFIRMDATA=1','/TESTFAILAFTERTASK=1')) -eq 0) { throw 'Injected data-switch failure was hidden.' }
    if ((Read-TestTask).Xml -cne $taskBeforeSwitch -or (Get-ItemPropertyValue -LiteralPath $settings -Name DataDir) -ine $originalData) { throw 'Failed data switch did not restore original task/registration.' }
    Assert-NoDataChange $originalData $originalSnapshot
    if ((Exec-Setup @('/TESTDATAMODE=fresh','/TESTCONFIRMDATA=1')) -ne 0) { throw 'Confirmed fresh-data switch failed.' }
    if ((Get-ItemPropertyValue -LiteralPath $settings -Name DataDir) -ine $freshData -or !(Read-TestTask).Definition.Actions.Item(1).Arguments.Contains($freshData)) { throw 'Fresh switch failed to retarget registered data and startup.' }
    Assert-NoDataChange $originalData $originalSnapshot
    if (@(Get-ChildItem -LiteralPath $freshData -Force).Count -ne 0) { throw 'Fresh data was initialized or left with guard/backup contents.' }
    $unknownData=Join-Path $fixture 'unknown-data'
    New-Item -ItemType Directory -Path $unknownData | Out-Null
    [IO.File]::WriteAllText((Join-Path $unknownData 'config.json'),'{}')
    [IO.File]::WriteAllText((Join-Path $unknownData 'jianzuo.db'),'not a Duo database')
    $unknownSnapshot=Data-Snapshot $unknownData; $data=$unknownData
    if ((Exec-Setup @('/TESTDATAMODE=existing','/TESTCONFIRMDATA=1')) -eq 0) { throw 'Arbitrary directory accepted as existing Duo data.' }
    Assert-NoDataChange $unknownData $unknownSnapshot
    $data=$originalData
    $lock=[IO.File]::Open((Join-Path $originalData 'service.lock'),[IO.FileMode]::Open,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
    try { if ((Exec-Setup @('/TESTDATAMODE=existing','/TESTCONFIRMDATA=1')) -eq 0) { throw 'Active existing-data lock was ignored.' } } finally { $lock.Dispose() }
    if ((Exec-Setup @('/TESTDATAMODE=existing','/TESTCONFIRMDATA=1')) -ne 0) { throw 'Loading existing Duo data failed.' }
    Assert-NoDataChange $originalData $originalSnapshot
    if ((Get-ItemPropertyValue -LiteralPath $settings -Name DataDir) -ine $originalData -or !(Read-TestTask).Definition.Actions.Item(1).Arguments.Contains($originalData)) { throw 'Load-existing failed to restore selected data references.' }
    # Neither selected database/config nor abandoned fresh directory is deleted.
    if (!(Test-Path -LiteralPath $freshData -PathType Container) -or @(Get-ChildItem -LiteralPath $freshData -Force).Count -ne 0) { throw 'Previous fresh data directory was altered during loading.' }
    # Simulate the pre-Inno same-directory installer/payload identities; these
    # files are synthetic, recognized by the old bound ownership marker.
    foreach ($oldName in @('简作.exe','jianzuo-service.exe','JianzuoMaintenance.exe','卸载简作.exe')) {
        Copy-Item -LiteralPath (Join-Path $output '.installer-build\payload\Duo.exe') -Destination (Join-Path $program $oldName)
    }
    New-Item -Path $legacyUninstall -Force | Out-Null
    New-ItemProperty -LiteralPath $legacyUninstall -Name InstallLocation -Value $program -PropertyType String | Out-Null
    # A cleanup failure after ssPostInstall must not roll back just the task or
    # ownership marker while leaving finalized native files/registration behind.
    # Only this synthetic old payload is made read-only; it remains readable for
    # the production backup/hash checks and fails specifically at File.Delete.
    $readOnlyLegacy=Join-Path $program '简作.exe'
    $originalAttributes=[IO.File]::GetAttributes($readOnlyLegacy)
    $originalLegacyHash=(Get-FileHash -LiteralPath $readOnlyLegacy -Algorithm SHA256).Hash
    try {
        [IO.File]::SetAttributes($readOnlyLegacy,($originalAttributes -bor [IO.FileAttributes]::ReadOnly))
        if ((Exec-Setup @('/UPDATE=1')) -ne 0) { throw 'Post-install optional cleanup warning was reported as install failure.' }
        if (!(Test-Path -LiteralPath $readOnlyLegacy) -or (Get-FileHash -LiteralPath $readOnlyLegacy -Algorithm SHA256).Hash -cne $originalLegacyHash) {
            throw 'Read-only legacy cleanup warning deleted or changed the old file.'
        }
        $currentTask=Read-TestTask
        if (!$currentTask -or $currentTask.Definition.Actions.Item(1).Path -ine (Join-Path $program 'Duo.exe') -or
            !(Test-Path -LiteralPath $settings) -or !(Test-Path -LiteralPath $uninstall) -or
            (Get-ItemPropertyValue -LiteralPath $settings -Name Installer) -cne 'inno' -or
            [IO.File]::ReadAllText((Join-Path $program '.jianzuo-install')) -cne ("Jianzuo installer ownership v1" + [char]10 + $program)) {
            throw 'Post-install cleanup warning undid finalized task/registry/marker state.'
        }
        $warningLog=[IO.File]::ReadAllText($lastSetupLog)
        if (!$warningLog.Contains('旧入口清理未全部完成') -or !$warningLog.Contains('--commit')) {
            throw 'Post-install cleanup warning was not recorded clearly in the setup log.'
        }
    } finally {
        if (Test-Path -LiteralPath $readOnlyLegacy -PathType Leaf) { [IO.File]::SetAttributes($readOnlyLegacy,$originalAttributes) }
    }
    # A normal repair after clearing only the fixture attribute finishes cleanup.
    if ((Exec-Setup @('/UPDATE=1')) -ne 0) { throw 'Native same-directory update failed.' }
    foreach ($oldName in @('简作.exe','jianzuo-service.exe','JianzuoMaintenance.exe','卸载简作.exe')) {
        if (Test-Path -LiteralPath (Join-Path $program $oldName)) { throw ('Old owned file not migrated: ' + $oldName) }
    }
    if (Test-Path -LiteralPath $legacyUninstall) { throw 'Duplicate legacy uninstall registration remains.' }
    [IO.File]::WriteAllText((Join-Path $program 'unknown.txt'),'preserve unrelated file')
    $task=Read-TestTask; $task.Enabled=$false
    Remove-Item -LiteralPath $desktop -Force
    if ((Exec-Setup @('/UPDATE=1')) -ne 0) { throw 'Preference-preserving update failed.' }
    if ((Read-TestTask) -or (Test-Path -LiteralPath $desktop) -or (Get-ItemPropertyValue -LiteralPath $settings -Name Startup) -ne '0') { throw 'Update enabled disabled startup/desktop preference.' }
    Uninstall-Test
    if ([IO.File]::ReadAllText((Join-Path $program 'unknown.txt')) -cne 'preserve unrelated file') { throw 'Native uninstall deleted unknown file.' }
    # Create only the GUID fixture task; no launcher is run by this test.
    $definition=$scheduler.NewTask(0)
    $definition.Principal.UserId=[Security.Principal.WindowsIdentity]::GetCurrent().Name
    $definition.Principal.LogonType=3; $definition.Principal.RunLevel=0
    $action=$definition.Actions.Create(0); $oldExe=Join-Path $oldProgram '简作.exe'
    Copy-Item -LiteralPath (Join-Path $output '.installer-build\payload\Duo.exe') -Destination $oldExe
    $action.Path=$oldExe; $action.Arguments=(Q $oldExe) + ' --data ' + (Q $data) + ' --background'; $action.WorkingDirectory=$oldProgram
    [void]$folder.RegisterTaskDefinition($key,$definition,6,$null,$null,3,$null)
    $before=(Read-TestTask).Xml
    if ((Exec-Setup @()) -eq 0) { throw 'Silent migration proceeded without consent.' }
    if ((Read-TestTask).Xml -cne $before) { throw 'Rejected migration changed old task.' }
    if ((Exec-Setup @('/TESTCONFIRMMIGRATION=1','/TESTFAILAFTERTASK=1')) -eq 0) { throw 'Injected post-mutation failure was hidden.' }
    if ((Read-TestTask).Xml -cne $before) { throw 'Partial failure did not restore original task XML.' }
    if ((Test-Path -LiteralPath $settings) -or (Test-Path -LiteralPath $uninstall) -or (Test-Path -LiteralPath (Join-Path $program 'Duo.exe'))) {
        throw 'Pre-install failure left native registration or payload behind.'
    }
    # A failed native install may leave its owned directory but no user data is removed.
    if ((Exec-Setup @('/TESTCONFIRMMIGRATION=1')) -ne 0) { throw 'Explicit isolated migration failed.' }
    if ((Read-TestTask).Definition.Actions.Item(1).Path -ine (Join-Path $program 'Duo.exe')) { throw 'Migration did not update target.' }
    if (!(Get-ChildItem -LiteralPath (Join-Path $data 'installer-backups') -Filter startup-task.xml -Recurse -File)) { throw 'Missing durable original task XML backup.' }
    if (!(Test-Path -LiteralPath $oldExe)) { throw 'Migration removed old portable program.' }
    Uninstall-Test
    # A historical HKCU Run entry can be the only migration source.
    $legacyCommand=(Q $oldExe) + ' --data ' + (Q $data) + ' --background'
    New-ItemProperty -LiteralPath $runKey -Name $runName -Value $legacyCommand -PropertyType String | Out-Null
    if ((Exec-Setup @()) -eq 0) { throw 'Run-only migration proceeded without consent.' }
    if ((Exec-Setup @('/TESTCONFIRMMIGRATION=1')) -ne 0) { throw 'Confirmed Run-only migration failed.' }
    if ((Get-ItemProperty -LiteralPath $runKey).PSObject.Properties.Name -contains $runName) { throw 'Owned legacy Run entry duplicated new startup task.' }
    if (!(Read-TestTask)) { throw 'Run-only migration did not create the new startup task.' }
    Uninstall-Test
    Write-Output 'PASS: GUID-isolated native install/update, no-argument wizard defaults, explicit fresh/load-existing data and guarded rollback with hashes unchanged, busy/unknown-data rejection, post-install cleanup warning preserves finalized state and repairs cleanly, old payload/ARP cleanup, disabled preferences, native uninstall/data survival, consent, pre-install injected rollback without residue, Task and Run-only migration.'
} catch {
    if ($folder) {
        $diagnostic=Read-TestTask
        if ($diagnostic) { Write-Output ('Synthetic task diagnostic: ' + $diagnostic.Xml) }
    }
    Get-ChildItem -LiteralPath $fixture -Filter 'setup-*.log' -File | Sort-Object LastWriteTime | Select-Object -Last 1 | ForEach-Object { Get-Content -LiteralPath $_.FullName -Tail 35 }
    throw
} finally {
    # Only generated identities may be touched. Never clean production task or registry.
    if ($folder -and $key -ceq ('Jianzuo-Test-' + $identity) -and (Read-TestTask)) { $folder.DeleteTask($key,0) }
    foreach($path in @($settings,$uninstall,$legacyUninstall)) { if ($path.Contains($identity) -and (Test-Path -LiteralPath $path)) { Remove-Item -LiteralPath $path -Recurse -Force } }
    if ($runName -ceq ($key + 'Portable')) { Remove-ItemProperty -LiteralPath $runKey -Name $runName -ErrorAction SilentlyContinue }
    if ($desktop.EndsWith($identity + '.lnk') -and (Test-Path -LiteralPath $desktop)) { Remove-Item -LiteralPath $desktop -Force }
    if ((Split-Path $menu -Leaf) -ceq $name -and (Test-Path -LiteralPath $menu)) { Remove-Item -LiteralPath $menu -Recurse -Force }
    $resolved=[IO.Path]::GetFullPath($fixture).TrimEnd('\')
    if ([IO.Path]::GetDirectoryName($resolved) -ine [IO.Path]::GetTempPath().TrimEnd('\') -or [IO.Path]::GetFileName($resolved) -cne ('JianzuoInnoLifecycle-' + $identity)) { throw 'Unsafe fixture cleanup target.' }
    if (Test-Path -LiteralPath $resolved) { Remove-Item -LiteralPath $resolved -Recurse -Force }
    if ([IO.Path]::GetDirectoryName($logDirectory) -ieq [Environment]::GetFolderPath('LocalApplicationData') -and [IO.Path]::GetFileName($logDirectory) -ceq $key -and (Test-Path -LiteralPath $logDirectory)) {
        Remove-Item -LiteralPath $logDirectory -Recurse -Force
    }
}
