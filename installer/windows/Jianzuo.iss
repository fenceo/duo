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
  ChoicePage: TInputOptionWizardPage;
  MigrationPage: TInputOptionWizardPage;
  ProbeFile, ResultFile, TransactionFile, HelperFile: String;
  OldInstallDir, OldDataDir, OldTaskDir, OldTaskData: String;
  OldStartup, OldDesktop, HasOldInstall, TaskRecognized, TaskPresent: Boolean;
  UpdateMode, VerifiedOnly, Prepared, Applied, Finished: Boolean;

function Q(const Value: String): String;
begin
  if Pos('"', Value) > 0 then RaiseException('目录不能包含双引号。');
  Result := '"' + Value + '"';
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
  Result := DataPage.Values[0];
end;

function WantDesktop: Boolean;
begin
  if UpdateMode then Result := OldDesktop else Result := ChoicePage.Values[0];
end;

function WantStartup: Boolean;
begin
  if UpdateMode then Result := OldStartup else Result := ChoicePage.Values[1];
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
  Result := not UpdateMode and not HasParameter('NOLAUNCH');
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
begin
  Result := ' --install-dir ' + Q(WizardDirValue) + ' --data ' + Q(GetDataDir(''));
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
  DeleteFile(ResultFile);
  if not Exec(HelperFile, Command + Arguments + ' --result ' + Q(ResultFile), '', SW_HIDE, ewWaitUntilTerminated, Code) then
    Result := '无法运行安装维护程序：' + SysErrorMessage(Code)
  else if Code <> 0 then
    Result := GetIniString('Result', 'Error', '安装维护失败（代码 ' + IntToStr(Code) + '），请查看安装日志。', ResultFile)
  else Result := '';
  if Result <> '' then Log(Result);
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
  FileCopy(ResultFile, ProbeFile, False);
  OldInstallDir := GetIniString('Result', 'InstallDir', '', ProbeFile);
  OldDataDir := GetIniString('Result', 'DataDir', '', ProbeFile);
  OldTaskDir := GetIniString('Result', 'MigrationDirectory', '', ProbeFile);
  OldTaskData := GetIniString('Result', 'MigrationData', '', ProbeFile);
  OldStartup := GetIniString('Result', 'Startup', '1', ProbeFile) = '1';
  OldDesktop := GetIniString('Result', 'Desktop', '1', ProbeFile) = '1';
  HasOldInstall := GetIniString('Result', 'HasInstall', '0', ProbeFile) = '1';
  TaskRecognized := GetIniString('Result', 'TaskRecognized', '0', ProbeFile) = '1';
  TaskPresent := GetIniString('Result', 'TaskPresent', '0', ProbeFile) = '1';
  if UpdateMode and not HasOldInstall then begin
    SuppressibleMsgBox('自动更新只适用于已安装的Duo，请手动运行安装包。', mbError, MB_OK, IDOK); Result := False;
  end;
end;

procedure InitializeWizard;
begin
  if (OldInstallDir <> '') and (ParamValue('DIR') = '') then WizardForm.DirEdit.Text := OldInstallDir;
  DataPage := CreateInputDirPage(wpSelectDir, '数据目录', '任务、知识库和配置不会在升级或卸载时删除。', '请选择数据目录。升级/修复会沿用已有数据，不迁移或复制数据库。', False, '');
  DataPage.Add('数据目录：');
  if OldDataDir = '' then OldDataDir := ExpandConstant('{localappdata}\Duo\data');
  DataPage.Values[0] := OldDataDir;
  if ParamValue('DATADIR') <> '' then DataPage.Values[0] := ParamValue('DATADIR');
  if HasOldInstall or (OldTaskData <> '') then DataPage.Edits[0].ReadOnly := True;
  ChoicePage := CreateInputOptionPage(DataPage.ID, '启动入口', '安装、升级和修复使用同一套入口。', '已安装时默认保留现有选项。安装器不会强制关闭正在运行的任务。', False, False);
  ChoicePage.Add('创建桌面快捷方式'); ChoicePage.Add('登录 Windows 后自动启动');
  ChoicePage.Values[0] := OldDesktop; ChoicePage.Values[1] := OldStartup;
  MigrationPage := CreateInputOptionPage(ChoicePage.ID, '迁移旧Duo入口', '只迁移入口，不删除旧程序或数据。', '旧程序目录：' + OldTaskDir + #13#10 + '原数据目录：' + OldTaskData + #13#10#13#10 + '确认后会先备份旧任务 XML，再将启动任务和已识别快捷方式指向新目录。取消安装或失败时恢复原启动任务。', False, False);
  MigrationPage.Add('我确认将上述旧入口迁移到本次安装目录，并继续使用原数据');
  MigrationPage.Values[0] := False;
end;

function ShouldSkipPage(PageID: Integer): Boolean;
begin
  Result := (UpdateMode and ((PageID = wpSelectDir) or (PageID = DataPage.ID) or (PageID = ChoicePage.ID))) or
    ((PageID = MigrationPage.ID) and not NeedsMigration);
end;

function NextButtonClick(CurPageID: Integer): Boolean;
var Error: String;
begin
  Result := True;
  if (CurPageID = MigrationPage.ID) and NeedsMigration and not MigrationConfirmed then begin
    SuppressibleMsgBox('迁移旧入口需要在向导中明确确认；静默更新不能跨目录接管。', mbError, MB_OK, IDOK); Result := False;
  end;
  if CurPageID = wpReady then begin
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
  end;
end;

procedure DeinitializeSetup;
var Error: String;
begin
  if Prepared and not Finished then begin
    Error := RunHelper('--rollback', ' --transaction ' + Q(TransactionFile));
    if Error <> '' then SuppressibleMsgBox('恢复启动任务失败：' + Error, mbError, MB_OK, IDOK);
  end;
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
