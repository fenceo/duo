#ifndef AppVersion
  #error AppVersion is required
#endif
#ifndef PayloadDir
  #error PayloadDir is required
#endif
#ifndef HelperPath
  #error HelperPath is required
#endif
#ifndef OutputPath
  #error OutputPath is required
#endif
#ifdef TestNamespace
  #define ProductKey "Jianzuo-Test-" + TestNamespace
  #define ProductName "Jianzuo Test " + TestNamespace
#else
  #define ProductKey "Jianzuo"
  #define ProductName "Duo"
#endif

[Setup]
AppId={#ProductKey}
AppName={#ProductName}
AppVersion={#AppVersion}
AppPublisher=fenceo
AppPublisherURL=https://github.com/fenceo/duo
AppSupportURL=https://github.com/fenceo/duo/issues
AppUpdatesURL=https://github.com/fenceo/duo/releases/latest
DefaultDirName={localappdata}\Programs\Duo
DefaultGroupName={#ProductName}
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0
UsePreviousAppDir=yes
UsePreviousTasks=no
DisableDirPage=no
DisableWelcomePage=no
DisableStartupPrompt=yes
UninstallDisplayIcon={app}\Duo.exe
UninstallDisplayName={#ProductName}
OutputDir={#OutputPath}
OutputBaseFilename=Duo-Setup-User-x64
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
SetupLogging=yes
CloseApplications=no
RestartApplications=no
AlwaysRestart=no
AllowNoIcons=no
Uninstallable=yes
UninstallFilesDir={app}
VersionInfoVersion={#AppVersion}

[Languages]
Name: "chinesesimp"; MessagesFile: "compiler:Languages\ChineseSimplified.isl"

[Files]
; Explicit payload allowlist. Data, local configuration and model credentials are never packaged.
Source: "{#HelperPath}"; Flags: dontcopy
Source: "{#PayloadDir}\Duo.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#PayloadDir}\duo-service.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#PayloadDir}\使用说明.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#PayloadDir}\THIRD-PARTY-NOTICES.txt"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#HelperPath}"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{userprograms}\{#ProductName}\Duo"; Filename: "{app}\Duo.exe"; Parameters: "--data ""{code:GetDataDir}"""; WorkingDir: "{app}"
Name: "{userprograms}\{#ProductName}\卸载 Duo"; Filename: "{uninstallexe}"
Name: "{userdesktop}\{#ProductName}"; Filename: "{app}\Duo.exe"; Parameters: "--data ""{code:GetDataDir}"""; WorkingDir: "{app}"; Check: WantDesktop

[Registry]
Root: HKCU; Subkey: "Software\{#ProductKey}"; Flags: uninsdeletekeyifempty
Root: HKCU; Subkey: "Software\{#ProductKey}"; ValueType: string; ValueName: "InstallDir"; ValueData: "{app}"; Flags: uninsdeletevalue
Root: HKCU; Subkey: "Software\{#ProductKey}"; ValueType: string; ValueName: "DataDir"; ValueData: "{code:GetDataDir}"; Flags: uninsdeletevalue
Root: HKCU; Subkey: "Software\{#ProductKey}"; ValueType: string; ValueName: "Version"; ValueData: "{#AppVersion}"; Flags: uninsdeletevalue
Root: HKCU; Subkey: "Software\{#ProductKey}"; ValueType: string; ValueName: "Installer"; ValueData: "inno"; Flags: uninsdeletevalue
Root: HKCU; Subkey: "Software\{#ProductKey}"; ValueType: string; ValueName: "Startup"; ValueData: "{code:GetStartup}"; Flags: uninsdeletevalue
Root: HKCU; Subkey: "Software\{#ProductKey}"; ValueType: string; ValueName: "Desktop"; ValueData: "{code:GetDesktop}"; Flags: uninsdeletevalue

[UninstallDelete]
; Data and installer-backups are outside {app}; never use filesandordirs or a wildcard.
Type: files; Name: "{app}\.jianzuo-install"

[Run]
Filename: "{app}\Duo.exe"; Parameters: "--data ""{code:GetDataDir}"""; Description: "启动Duo"; Flags: postinstall nowait skipifsilent; Check: MayLaunch

[Code]
var
  DataPage: TInputDirWizardPage;
  DataModePage, DataConfirmPage: TInputOptionWizardPage;
  ChoicePage: TInputOptionWizardPage;
  MigrationPage: TInputOptionWizardPage;
  ProbeFile, ResultFile, TransactionFile, HelperFile, ValidatorFile: String;
  OldInstallDir, OldDataDir, OldTaskDir, OldTaskData: String;
  OldStartup, OldDesktop, HasOldInstall, TaskRecognized, TaskPresent: Boolean;
  UpdateMode, VerifiedOnly, Prepared, Applied, Finished, DataGuardReleased: Boolean;
  HasPreviousData: Boolean;
  LastDataMode: Integer;

function GetCurrentProcessId: LongWord;
  external 'GetCurrentProcessId@kernel32.dll stdcall';

function Q(const Value: String): String;
var I: Integer;
begin
  if Pos('"', Value) > 0 then RaiseException('目录不能包含双引号。');
  Result := '"' + Value;
  I := Length(Value);
  while (I > 0) and (Copy(Value, I, 1) = '\') do begin Result := Result + '\'; I := I - 1; end;
  Result := Result + '"';
end;

function ParamValue(const Name: String): String;
begin
  Result := ExpandConstant('{param:' + Name + '|}');
end;

function HasParameter(const Name: String): Boolean;
var I: Integer;
begin
  Result := False;
  for I := 1 to ParamCount do
    if CompareText(ParamStr(I), '/' + Name) = 0 then Result := True;
end;

function GetDataDir(Param: String): String;
begin
  Result := RemoveBackslashUnlessRoot(DataPage.Values[0]);
end;

function WantDesktop: Boolean;
begin
  { A registered installation is a normal in-place upgrade.  Keep the
    existing entry without making the user walk through a second preference
    page; the tray/settings UI remains the place to change it. }
  if UpdateMode or HasOldInstall then Result := OldDesktop else Result := ChoicePage.Values[0];
end;

function WantStartup: Boolean;
begin
  if UpdateMode or HasOldInstall then Result := OldStartup else Result := ChoicePage.Values[1];
end;

function GetDesktop(Param: String): String;
begin
  if WantDesktop then Result := '1' else Result := '0';
end;

function GetStartup(Param: String): String;
begin
  if WantStartup then Result := '1' else Result := '0';
end;

function MayLaunch: Boolean;
begin
#ifdef TestNamespace
  Result := False;
#else
  Result := not UpdateMode and not HasParameter('NOLAUNCH') and DataGuardReleased;
#endif
end;

function NeedsMigration: Boolean;
begin
  Result := (OldTaskDir <> '') and
    (CompareText(RemoveBackslashUnlessRoot(OldTaskDir), RemoveBackslashUnlessRoot(WizardDirValue)) <> 0);
end;

function MigrationConfirmed: Boolean;
begin
  Result := not WizardSilent and MigrationPage.Values[0];
#ifdef TestNamespace
  { Test-only compile-time namespace; this switch is absent in release builds. }
  if ParamValue('TESTCONFIRMMIGRATION') = '1' then Result := True;
#endif
end;

function OptionArguments: String;
var DataMode: String; InteractiveData: Boolean;
begin
  Result := ' --install-dir ' + Q(WizardDirValue) + ' --data ' + Q(GetDataDir(''));
  DataMode := 'keep';
  if DataModePage.SelectedValueIndex = 1 then DataMode := 'fresh';
  if DataModePage.SelectedValueIndex = 2 then DataMode := 'existing';
  InteractiveData := not WizardSilent;
#ifdef TestNamespace
  if ParamValue('TESTCONFIRMDATA') = '1' then InteractiveData := True;
#endif
  Result := Result + ' --data-mode ' + DataMode + ' --data-validator ' + Q(ValidatorFile) + ' --owner-pid ' + IntToStr(GetCurrentProcessId);
  if HasPreviousData then Result := Result + ' --previous-data ' + Q(OldDataDir);
  if InteractiveData then Result := Result + ' --interactive';
  if InteractiveData and DataConfirmPage.Values[0] then Result := Result + ' --confirm-data';
  if UpdateMode then Result := Result + ' --update';
  if WantStartup then Result := Result + ' --startup';
  if WantDesktop then Result := Result + ' --desktop';
  if NeedsMigration and MigrationConfirmed then
    Result := Result + ' --migrate-from ' + Q(OldTaskDir);
#ifdef TestNamespace
  if ParamValue('TESTFAILAFTERTASK') = '1' then Result := Result + ' --test-fail-after-task';
#endif
end;

function RunHelper(const Command, Arguments: String): String;
var Code: Integer;
begin
  Code := -1;
  if FileExists(ResultFile) and not DeleteFile(ResultFile) then begin
    Result := '无法清除旧安装探测结果，未继续：' + ResultFile;
    Log(Result); exit;
  end;
  if not Exec(HelperFile, Command + Arguments + ' --result ' + Q(ResultFile), '', SW_HIDE, ewWaitUntilTerminated, Code) then
    Result := '无法运行安装维护程序：' + SysErrorMessage(Code)
  else if not FileExists(ResultFile) then
    Result := '安装维护程序未返回结果（代码 ' + IntToStr(Code) + '），未继续。'
  else if Code <> 0 then
    Result := GetIniString('Result', 'Error', '安装维护失败（代码 ' + IntToStr(Code) + '），请查看安装日志。', ResultFile)
  else Result := '';
  Log('Maintenance command=' + Command + '; exit=' + IntToStr(Code) + '; result=' + ResultFile + '; present=' + IntToStr(Ord(FileExists(ResultFile))));
  if Result <> '' then Log(Result);
end;

function ProbeValue(const Name: String): String;
begin
  Result := GetIniString('Result', Name, '__duo_probe_missing__', ProbeFile);
end;

function ValidProbeFlag(const Name: String): Boolean;
var Value: String;
begin
  Value := ProbeValue(Name);
  Result := (Value = '0') or (Value = '1');
end;

function ValidateProbeResult: String;
begin
  Result := '';
  if ProbeValue('Protocol') <> 'duo-install-probe-v1' then
    Result := '安装探测协议缺失或无效，未更改程序或数据。'
  else if not (ValidProbeFlag('HasInstall') and ValidProbeFlag('Startup') and ValidProbeFlag('Desktop') and
    ValidProbeFlag('TaskPresent') and ValidProbeFlag('TaskRecognized') and ValidProbeFlag('LegacyInstall')) then
    Result := '安装探测状态字段缺失或无效，未更改程序或数据。'
  else if (ProbeValue('InstallDir') = '__duo_probe_missing__') or (ProbeValue('DataDir') = '__duo_probe_missing__') or
    (ProbeValue('TaskDirectory') = '__duo_probe_missing__') or (ProbeValue('TaskData') = '__duo_probe_missing__') or
    (ProbeValue('MigrationDirectory') = '__duo_probe_missing__') or (ProbeValue('MigrationData') = '__duo_probe_missing__') then
    Result := '安装探测缺少必要路径字段，未更改程序或数据。'
  else if (ProbeValue('HasInstall') = '1') and ((ProbeValue('InstallDir') = '') or (ProbeValue('DataDir') = '')) then
    Result := '已安装版本缺少程序或数据目录登记，请先核查，未更改程序或数据。'
  else if (ProbeValue('TaskRecognized') = '1') and (ProbeValue('TaskPresent') <> '1') then
    Result := '安装探测的启动任务状态不一致，未继续。';
end;

function InitializeSetup: Boolean;
var Error: String;
begin
  Result := True;
  VerifiedOnly := ParamValue('VERIFY') = '1';
  UpdateMode := ParamValue('UPDATE') = '1';
  ResultFile := ExpandConstant('{tmp}\jianzuo-result.ini');
  ProbeFile := ExpandConstant('{tmp}\jianzuo-probe.ini');
  TransactionFile := ExpandConstant('{tmp}\jianzuo-transaction.xml');
  ExtractTemporaryFile('DuoMaintenance.exe');
  HelperFile := ExpandConstant('{tmp}\DuoMaintenance.exe');
  if VerifiedOnly then begin
    Error := RunHelper('--verify', '');
    if (Error = '') and (ParamValue('VERIFYRESULT') <> '') then
      SaveStringToFile(ParamValue('VERIFYRESULT'), 'Jianzuo installer verification v1', False);
    Result := False;
    exit;
  end;
  Error := RunHelper('--probe', '');
  if Error <> '' then begin
    SuppressibleMsgBox(Error, mbError, MB_OK, IDOK); Result := False; exit;
  end;
  if not FileCopy(ResultFile, ProbeFile, False) then begin
    Error := '无法保留安装探测结果，未继续：' + ProbeFile;
    Log(Error); SuppressibleMsgBox(Error, mbError, MB_OK, IDOK); Result := False; exit;
  end;
  Log('Probe copy succeeded: ' + ProbeFile);
  Error := ValidateProbeResult;
  Log('Probe protocol=' + ProbeValue('Protocol') + '; HasInstall=' + ProbeValue('HasInstall') + '; InstallDir=' + ProbeValue('InstallDir') + '; DataDir=' + ProbeValue('DataDir'));
  if Error <> '' then begin
    Log(Error); SuppressibleMsgBox(Error, mbError, MB_OK, IDOK); Result := False; exit;
  end;
  OldInstallDir := ProbeValue('InstallDir');
  OldDataDir := ProbeValue('DataDir');
  OldTaskDir := ProbeValue('MigrationDirectory');
  OldTaskData := ProbeValue('MigrationData');
  OldStartup := ProbeValue('Startup') = '1';
  OldDesktop := ProbeValue('Desktop') = '1';
  HasOldInstall := ProbeValue('HasInstall') = '1';
  HasPreviousData := OldDataDir <> '';
  TaskRecognized := ProbeValue('TaskRecognized') = '1';
  TaskPresent := ProbeValue('TaskPresent') = '1';
  if UpdateMode and not HasOldInstall then begin
    SuppressibleMsgBox('自动更新只适用于已安装的Duo，请手动运行安装包。', mbError, MB_OK, IDOK); Result := False;
  end;
  if HasOldInstall and (ParamValue('DIR') <> '') and
    (CompareText(RemoveBackslashUnlessRoot(OldInstallDir), RemoveBackslashUnlessRoot(ParamValue('DIR'))) <> 0) then begin
    SuppressibleMsgBox('已安装 Duo，请在原程序目录升级/修复：' + OldInstallDir, mbError, MB_OK, IDOK); Result := False;
  end;
  if Result then begin
    ExtractTemporaryFile('duo-service.exe');
    ValidatorFile := ExpandConstant('{tmp}\duo-service.exe');
  end;
end;

procedure InitializeWizard;
begin
  if (OldInstallDir <> '') and (ParamValue('DIR') = '') then WizardForm.DirEdit.Text := OldInstallDir;
  if HasOldInstall then begin
    WizardForm.DirEdit.ReadOnly := True;
    WizardForm.DirBrowseButton.Enabled := False;
    WizardForm.SelectDirLabel.Caption := '已安装 Duo：程序目录保持原位置。下面可选择保留、新建或载入数据，程序目录不会随品牌名称改变。';
  end;
  DataModePage := CreateInputOptionPage(wpSelectDir, '选择数据', '程序升级与数据选择相互独立。', '默认保留当前数据。需要重新配置时选择全新目录；需要切回旧数据时重新运行同一安装器并选择载入。不会删除、复制或合并新旧数据。', True, False);
  if HasPreviousData then DataModePage.Add('保留当前数据（推荐）') else DataModePage.Add('在默认/指定空目录首次配置（推荐）');
  DataModePage.Add('使用全新空目录，启动后重新配置');
  DataModePage.Add('载入已有 Duo / 简作数据目录');
  DataModePage.SelectedValueIndex := 0; LastDataMode := 0;
  DataPage := CreateInputDirPage(DataModePage.ID, '数据目录', '任务、知识库和配置不会在升级或卸载时删除。', '全新目录必须为空；载入只检查数据格式，不重置密码、不合并内容。切换前请退出所有使用新旧数据的 Duo。', False, '');
  DataPage.Add('数据目录：');
  if OldDataDir = '' then OldDataDir := ExpandConstant('{localappdata}\Duo\data');
  DataPage.Values[0] := OldDataDir;
  if ParamValue('DATADIR') <> '' then DataPage.Values[0] := ParamValue('DATADIR');
  DataPage.Edits[0].ReadOnly := HasPreviousData;
  DataPage.Buttons[0].Enabled := not HasPreviousData;
  DataConfirmPage := CreateInputOptionPage(DataPage.ID, '确认数据选择', '旧数据完整保留；可以再次运行安装器切回。', '新建数据不会带入旧密码、任务或设置；载入已有数据会继续使用该数据原有的登录密码。安装后启动 Duo 可能按新版本进行数据库升级，请先自行备份重要数据。', False, False);
  DataConfirmPage.Add('我确认切换到上一步选择的数据目录；不删除或覆盖旧数据');
  DataConfirmPage.Values[0] := False;
  ChoicePage := CreateInputOptionPage(DataConfirmPage.ID, '启动入口', '安装、升级和修复使用同一套入口。', '已安装时默认保留现有选项。安装器不会强制关闭正在运行的任务。', False, False);
  ChoicePage.Add('创建桌面快捷方式'); ChoicePage.Add('登录 Windows 后自动启动');
  ChoicePage.Values[0] := OldDesktop; ChoicePage.Values[1] := OldStartup;
  MigrationPage := CreateInputOptionPage(ChoicePage.ID, '迁移旧Duo入口', '只迁移启动入口，不搬运数据。', '旧程序目录：' + OldTaskDir + #13#10 + '原数据目录：' + OldTaskData + #13#10#13#10 + '确认后只会备份并更新启动任务和已识别快捷方式，使其指向本次选择的程序与数据目录；不会复制、合并或删除旧数据，也不会删除旧程序。取消安装或失败时恢复原启动任务。', False, False);
  MigrationPage.Add('我确认只迁移启动任务和快捷方式，不复制或删除旧程序、旧数据');
  MigrationPage.Values[0] := False;
#ifdef TestNamespace
  if ParamValue('TESTDATAMODE') = 'fresh' then DataModePage.SelectedValueIndex := 1;
  if ParamValue('TESTDATAMODE') = 'existing' then DataModePage.SelectedValueIndex := 2;
  LastDataMode := DataModePage.SelectedValueIndex;
  if ParamValue('TESTCONFIRMDATA') = '1' then DataConfirmPage.Values[0] := True;
  if ParamValue('TESTDEFAULTSRESULT') <> '' then begin
    SaveStringToFile(ParamValue('TESTDEFAULTSRESULT'), WizardForm.DirEdit.Text + #13#10 + DataPage.Values[0], False);
    Abort;
  end;
#endif
end;

procedure CurPageChanged(CurPageID: Integer);
var Previous: String;
begin
  if CurPageID = DataPage.ID then begin
    if DataModePage.SelectedValueIndex <> LastDataMode then begin
      DataConfirmPage.Values[0] := False;
      if DataModePage.SelectedValueIndex = 0 then DataPage.Values[0] := OldDataDir else DataPage.Values[0] := '';
      LastDataMode := DataModePage.SelectedValueIndex;
    end;
    DataPage.Edits[0].ReadOnly := HasPreviousData and (DataModePage.SelectedValueIndex = 0);
    DataPage.Buttons[0].Enabled := not DataPage.Edits[0].ReadOnly;
  end;
  if CurPageID = DataConfirmPage.ID then begin
    if HasPreviousData then Previous := OldDataDir else Previous := '无（首次安装）';
    DataConfirmPage.SubCaptionLabel.Caption := '原数据：' + Previous + #13#10 + '本次数据：' + GetDataDir('') + #13#10 + '旧目录保留。载入继续使用原密码；全新目录启动后重新配置。';
  end;
end;

function ShouldSkipPage(PageID: Integer): Boolean;
begin
  { The normal path is deliberately short:
      * first install: show the data directory, but there is no meaningful
        "data mode" or confirmation choice yet;
      * an installed copy: keep its registered program/data/entry paths and
        perform an in-place upgrade without asking the same questions again;
      * a recognised portable/legacy entry: show the detected data path so it
        is visible, then require only the explicit entry-migration consent.
    TestNamespace keeps the old pages so the isolated lifecycle tests can
    exercise every guarded data-switch branch. }
#ifdef TestNamespace
  Result := False;
#else
  Result := (UpdateMode and ((PageID = wpSelectDir) or (PageID = DataModePage.ID) or (PageID = DataPage.ID) or (PageID = ChoicePage.ID))) or
    ((PageID = DataModePage.ID) and (HasOldInstall or ((not HasPreviousData) and (not NeedsMigration)))) or
    ((PageID = DataPage.ID) and HasOldInstall) or
    ((PageID = DataConfirmPage.ID) and (HasOldInstall or (DataModePage.SelectedValueIndex = 0))) or
    ((PageID = ChoicePage.ID) and HasOldInstall) or
    ((PageID = MigrationPage.ID) and not NeedsMigration);
#endif
end;

function NextButtonClick(CurPageID: Integer): Boolean;
var Error: String;
begin
  Result := True;
  if (CurPageID = DataConfirmPage.ID) and not DataConfirmPage.Values[0] then begin
    SuppressibleMsgBox('请明确确认数据选择，或返回选择保留当前数据。', mbError, MB_OK, IDOK); Result := False;
  end;
  if CurPageID = DataPage.ID then begin
    { Revisiting the directory page requires confirmation again. }
#ifndef TestNamespace
    DataConfirmPage.Values[0] := False;
#endif
  end;
  if (CurPageID = MigrationPage.ID) and NeedsMigration and not MigrationConfirmed then begin
    SuppressibleMsgBox('迁移旧入口需要在向导中明确确认；静默更新不能跨目录接管。', mbError, MB_OK, IDOK); Result := False;
  end;
  if CurPageID = wpReady then begin
    Log('Selected program directory: ' + WizardDirValue);
    Log('Selected data directory: ' + GetDataDir(''));
    Error := RunHelper('--validate', OptionArguments);
    if Error <> '' then begin SuppressibleMsgBox(Error, mbError, MB_OK, IDOK); Result := False; end;
  end;
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  Result := RunHelper('--prepare', OptionArguments + ' --transaction ' + Q(TransactionFile));
  Prepared := Result = '';
end;

procedure ApplyStartup;
var Error: String;
begin
  Error := RunHelper('--apply', OptionArguments + ' --transaction ' + Q(TransactionFile));
  if Error <> '' then RaiseException(Error);
  Applied := True;
end;

procedure CurStepChanged(CurStep: TSetupStep);
var Error: String;
begin
  if CurStep = ssInstall then ApplyStartup;
  if CurStep = ssPostInstall then begin
    { The native uninstaller/registry are finalized: do not roll back only the
      task if optional old-installer cleanup fails beyond this success boundary. }
    Finished := True;
    Error := RunHelper('--commit', OptionArguments + ' --transaction ' + Q(TransactionFile));
    if Error <> '' then SuppressibleMsgBox('Duo已安装，但旧入口清理未全部完成：' + Error, mbInformation, MB_OK, IDOK);
    Error := RunHelper('--release-guard', ' --transaction ' + Q(TransactionFile));
    DataGuardReleased := Error = '';
    if Error <> '' then SuppressibleMsgBox(Error, mbError, MB_OK, IDOK);
  end;
end;

procedure DeinitializeSetup;
var Error: String;
begin
  if Prepared and not Finished then begin
    Error := RunHelper('--rollback', ' --transaction ' + Q(TransactionFile));
    if Error <> '' then SuppressibleMsgBox('恢复启动任务失败：' + Error, mbError, MB_OK, IDOK);
  end;
  if Prepared then RunHelper('--release-guard', ' --transaction ' + Q(TransactionFile));
end;

function InitializeUninstall: Boolean;
var Code: Integer; DataDir: String;
begin
  Result := False;
  if not RegQueryStringValue(HKCU, 'Software\{#ProductKey}', 'DataDir', DataDir) then exit;
  HelperFile := ExpandConstant('{app}\DuoMaintenance.exe');
  ResultFile := ExpandConstant('{tmp}\jianzuo-uninstall-result.ini');
  Result := RunHelper('--uninstall-check', ' --install-dir ' + Q(ExpandConstant('{app}')) + ' --data ' + Q(DataDir)) = '';
  if not Result then SuppressibleMsgBox(GetIniString('Result', 'Error', '无法确认安装所有权，未卸载。', ResultFile), mbError, MB_OK, IDOK);
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var DataDir, Error: String;
begin
  if CurUninstallStep = usUninstall then begin
    RegQueryStringValue(HKCU, 'Software\{#ProductKey}', 'DataDir', DataDir);
    Error := RunHelper('--uninstall-startup', ' --install-dir ' + Q(ExpandConstant('{app}')) + ' --data ' + Q(DataDir));
    if Error <> '' then RaiseException(Error);
  end;
end;
