using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.IO;
using System.IO.Compression;
using System.Linq;
using System.Reflection;
using System.Runtime.InteropServices;
using System.Security.Principal;
using System.Text;
using System.Windows.Forms;
using Microsoft.Win32;

static class Program
{
    const string ProductName = "简作";
    const string TaskName = "Jianzuo User";
    const string LegacyRunKey = @"Software\Microsoft\Windows\CurrentVersion\Run";
    const string LegacyRunName = "JianzuoPortable";
    const string SettingsKey = @"Software\Jianzuo";
    const string UninstallKey = @"Software\Microsoft\Windows\CurrentVersion\Uninstall\Jianzuo";
    const string PayloadResource = "JianzuoPayload.zip";
    const string VersionResource = "JianzuoVersion.txt";
    const string LauncherName = "简作.exe";
    const string ServiceName = "jianzuo-service.exe";
    const string UninstallerName = "卸载简作.exe";
    const string NoticesName = "THIRD-PARTY-NOTICES.txt";
    const string InstructionsName = "使用说明.md";

    static readonly string[] PayloadFiles =
    {
        LauncherName,
        ServiceName,
        InstructionsName,
        NoticesName,
    };

    static readonly HashSet<string> PayloadFileSet =
        new HashSet<string>(PayloadFiles, StringComparer.OrdinalIgnoreCase);

    [STAThread]
    static int Main(string[] args)
    {
        Application.EnableVisualStyles();
        Application.SetCompatibleTextRenderingDefault(false);

        bool quiet = HasFlag(args, "--quiet") || HasFlag(args, "-q");
        try
        {
            if (HasFlag(args, "--help") || HasFlag(args, "-h") || HasFlag(args, "/?"))
            {
                ShowHelp();
                return 0;
            }
            if (HasFlag(args, "--verify"))
            {
                VerifyPayload();
                return 0;
            }
            if (HasFlag(args, "--uninstall"))
            {
                Uninstall(ParseOptions(args), quiet);
                return 0;
            }

            Options options = ParseOptions(args);
            if (!quiet)
            {
                using (SetupForm form = new SetupForm(options))
                {
                    if (form.ShowDialog() != DialogResult.OK)
                    {
                        return 2;
                    }
                    options = form.Options;
                }
            }
            ValidateOptions(options);
            Install(options, quiet);
            return 0;
        }
        catch (Exception error)
        {
            if (quiet)
            {
                TryWriteInstallError(error);
            }
            else
            {
                MessageBox.Show(error.Message, ProductName + " 安装失败",
                    MessageBoxButtons.OK, MessageBoxIcon.Error);
            }
            return 1;
        }
    }

    static bool HasFlag(string[] args, string name)
    {
        return args.Any(value => string.Equals(value, name, StringComparison.OrdinalIgnoreCase));
    }

    static string ArgumentValue(string[] args, string name)
    {
        for (int index = 0; index < args.Length; index++)
        {
            string value = args[index];
            if (string.Equals(value, name, StringComparison.OrdinalIgnoreCase))
            {
                if (index + 1 >= args.Length)
                {
                    throw new Exception(name + " 缺少参数值。");
                }
                return args[index + 1];
            }
            string prefix = name + "=";
            if (value.StartsWith(prefix, StringComparison.OrdinalIgnoreCase))
            {
                return value.Substring(prefix.Length);
            }
        }
        return "";
    }

    static Options ParseOptions(string[] args)
    {
        string installDir = ArgumentValue(args, "--install-dir");
        string dataDir = ArgumentValue(args, "--data");
        string executable = Application.ExecutablePath;
        bool uninstallMode = HasFlag(args, "--uninstall");

        if (uninstallMode && string.IsNullOrWhiteSpace(installDir))
        {
            installDir = Path.GetDirectoryName(executable);
        }

        if (string.IsNullOrWhiteSpace(installDir))
        {
            installDir = ReadSavedPath("InstallDir");
        }
        if (string.IsNullOrWhiteSpace(installDir))
        {
            installDir = Path.Combine(Environment.GetFolderPath(
                Environment.SpecialFolder.LocalApplicationData), "Programs", "Jianzuo");
        }

        ExistingTask existing = uninstallMode ? null : ReadExistingTask();
        if (string.IsNullOrWhiteSpace(dataDir))
        {
            dataDir = ReadSavedPath("DataDir");
        }
        if (string.IsNullOrWhiteSpace(dataDir) && existing != null)
        {
            dataDir = existing.DataDir;
        }
        if (string.IsNullOrWhiteSpace(dataDir))
        {
            dataDir = Path.Combine(Environment.GetFolderPath(
                Environment.SpecialFolder.LocalApplicationData), "Jianzuo", "data");
        }

        return new Options
        {
            InstallDir = NormalizeDirectory(installDir),
            DataDir = NormalizeDirectory(dataDir),
            CreateShortcuts = !HasFlag(args, "--no-shortcuts"),
            CreateDesktopShortcut = !HasFlag(args, "--no-desktop"),
            WriteRegistry = !HasFlag(args, "--no-registry"),
            EnableStartup = !HasFlag(args, "--no-startup"),
            LaunchAfterInstall = !HasFlag(args, "--no-launch"),
            ExistingTask = existing,
        };
    }

    static string NormalizeDirectory(string value)
    {
        value = Environment.ExpandEnvironmentVariables((value ?? "").Trim());
        if (value.Length == 0)
        {
            throw new Exception("安装目录和数据目录不能为空。");
        }
        if (value.Contains("\""))
        {
            throw new Exception("目录路径不能包含双引号。");
        }
        string full = Path.GetFullPath(value);
        if (full.StartsWith(@"\\", StringComparison.Ordinal))
        {
            throw new Exception("目录不能位于网络共享路径。");
        }
        return TrimDirectory(full);
    }

    static string TrimDirectory(string value)
    {
        string trimmed = value.TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);
        string root = Path.GetPathRoot(trimmed);
        return root != null && string.Equals(root, trimmed, StringComparison.OrdinalIgnoreCase)
            ? root
            : trimmed;
    }

    static void ValidateOptions(Options options)
    {
        if (options == null)
        {
            throw new Exception("安装配置无效。");
        }
        options.InstallDir = NormalizeDirectory(options.InstallDir);
        options.DataDir = NormalizeDirectory(options.DataDir);
        if (Path.GetPathRoot(options.InstallDir) == null || Path.GetPathRoot(options.DataDir) == null)
        {
            throw new Exception("安装目录和数据目录必须是本机绝对路径。");
        }
        if (IsFilesystemRoot(options.InstallDir) || IsFilesystemRoot(options.DataDir))
        {
            throw new Exception("安装目录和数据目录不能是磁盘或文件系统根目录。");
        }
        if (string.Equals(options.InstallDir, options.DataDir, StringComparison.OrdinalIgnoreCase) ||
            PathWithin(options.InstallDir, options.DataDir) ||
            PathWithin(options.DataDir, options.InstallDir))
        {
            throw new Exception("安装目录和数据目录不能互相包含，避免升级或卸载时影响数据。");
        }
    }

    static bool IsFilesystemRoot(string value)
    {
        string root = Path.GetPathRoot(value);
        return !string.IsNullOrWhiteSpace(root) &&
            string.Equals(TrimDirectory(value), TrimDirectory(root), StringComparison.OrdinalIgnoreCase);
    }

    static bool PathWithin(string root, string candidate)
    {
        string relative = RelativePath(root, candidate);
        return !relative.Equals("..", StringComparison.Ordinal) &&
            !relative.StartsWith(".." + Path.DirectorySeparatorChar, StringComparison.Ordinal);
    }

    static string RelativePath(string root, string candidate)
    {
        string fullRoot = Path.GetFullPath(root);
        string fullCandidate = Path.GetFullPath(candidate);
        if (!string.Equals(Path.GetPathRoot(fullRoot), Path.GetPathRoot(fullCandidate), StringComparison.OrdinalIgnoreCase))
        {
            return "..";
        }
        string basePath = fullRoot
            .TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar)
            + Path.DirectorySeparatorChar;
        Uri baseUri = new Uri(basePath, UriKind.Absolute);
        Uri targetUri = new Uri(fullCandidate, UriKind.Absolute);
        string relative = Uri.UnescapeDataString(baseUri.MakeRelativeUri(targetUri).ToString())
            .Replace('/', Path.DirectorySeparatorChar);
        return relative.Length == 0 ? "." : relative;
    }

    static void VerifyPayload()
    {
        string temp = NewTempDirectory();
        try
        {
            ExtractPayload(temp);
        }
        finally
        {
            TryDeleteDirectory(temp);
        }
    }

    static void ExtractPayload(string destination)
    {
        Directory.CreateDirectory(destination);
        using (Stream payload = Assembly.GetExecutingAssembly().GetManifestResourceStream(PayloadResource))
        {
            if (payload == null)
            {
                throw new Exception("安装包缺少便携程序资源。");
            }
            using (ZipArchive archive = new ZipArchive(payload, ZipArchiveMode.Read, false, Encoding.UTF8))
            {
                Dictionary<string, ZipArchiveEntry> entries =
                    new Dictionary<string, ZipArchiveEntry>(StringComparer.OrdinalIgnoreCase);
                foreach (ZipArchiveEntry entry in archive.Entries)
                {
                    string name = entry.FullName;
                    if (entry.Length <= 0 || name != entry.Name || name.Contains("/") ||
                        name.Contains("\\") || !PayloadFileSet.Contains(name) || entries.ContainsKey(name))
                    {
                        throw new Exception("安装包包含不允许的文件。");
                    }
                    entries.Add(name, entry);
                }
                if (entries.Count != PayloadFiles.Length)
                {
                    throw new Exception("安装包内容不完整。");
                }
                foreach (string name in PayloadFiles)
                {
                    ZipArchiveEntry entry = entries[name];
                    string path = Path.Combine(destination, name);
                    using (Stream input = entry.Open())
                    using (FileStream output = new FileStream(path, FileMode.CreateNew, FileAccess.Write, FileShare.None))
                    {
                        input.CopyTo(output);
                        output.Flush(true);
                    }
                }
            }
        }

        ValidatePe(Path.Combine(destination, LauncherName));
        ValidatePe(Path.Combine(destination, ServiceName));
    }

    static void ValidatePe(string path)
    {
        using (FileStream file = File.OpenRead(path))
        {
            if (file.Length < 128)
            {
                throw new Exception(Path.GetFileName(path) + " 不是有效的 Windows 程序。");
            }
            byte[] header = new byte[64];
            if (file.Read(header, 0, header.Length) != header.Length ||
                header[0] != (byte)'M' || header[1] != (byte)'Z')
            {
                throw new Exception(Path.GetFileName(path) + " 不是有效的 Windows 程序。");
            }
            int offset = BitConverter.ToInt32(header, 60);
            if (offset < 64 || offset + 4 > file.Length)
            {
                throw new Exception(Path.GetFileName(path) + " 的 PE 头无效。");
            }
            byte[] signature = new byte[4];
            file.Position = offset;
            if (file.Read(signature, 0, signature.Length) != signature.Length ||
                signature[0] != (byte)'P' || signature[1] != (byte)'E' ||
                signature[2] != 0 || signature[3] != 0)
            {
                throw new Exception(Path.GetFileName(path) + " 的 PE 标记无效。");
            }
        }
    }

    static void Install(Options options, bool quiet)
    {
        ValidateOptions(options);
        string version = ReadVersion();
        string temp = NewTempDirectory();
        string backup = Path.Combine(options.InstallDir, ".setup-backup-" + Guid.NewGuid().ToString("N"));
        bool committed = false;
        try
        {
            ExtractPayload(temp);
            Directory.CreateDirectory(options.InstallDir);
            Directory.CreateDirectory(options.DataDir);

            StopInstalledProcesses(options.InstallDir);
            StopOwnedTask(options.InstallDir, true);
            PrepareBackup(options.InstallDir, backup);
            try
            {
                foreach (string name in PayloadFiles)
                {
                    File.Copy(Path.Combine(temp, name), Path.Combine(options.InstallDir, name), true);
                }
                File.Copy(Application.ExecutablePath, Path.Combine(options.InstallDir, UninstallerName), true);
            }
            catch
            {
                RestoreBackup(options.InstallDir, backup);
                throw;
            }

            string launcher = Path.Combine(options.InstallDir, LauncherName);
            string uninstaller = Path.Combine(options.InstallDir, UninstallerName);
            if (options.CreateShortcuts)
            {
                CreateShortcuts(options, launcher, uninstaller);
            }
            if (options.EnableStartup)
            {
                RegisterStartupTask(options, launcher);
            }
            else
            {
                StopOwnedTask(options.InstallDir, true);
            }
            DeleteLegacyRunEntry();
            if (options.WriteRegistry)
            {
                SaveInstallation(options, version, launcher, uninstaller);
            }
            committed = true;
            TryDeleteDirectory(backup);

            if (options.LaunchAfterInstall)
            {
                StartLauncher(launcher, options.DataDir);
            }
            if (!quiet)
            {
                MessageBox.Show(
                    "简作已安装。" + Environment.NewLine + Environment.NewLine +
                    "程序：" + options.InstallDir + Environment.NewLine +
                    "数据：" + options.DataDir + Environment.NewLine + Environment.NewLine +
                    "以后可从开始菜单或桌面快捷方式启动。",
                    ProductName + " 安装完成", MessageBoxButtons.OK, MessageBoxIcon.Information);
            }
        }
        finally
        {
            if (!committed)
            {
                RestoreBackup(options.InstallDir, backup);
            }
            TryDeleteDirectory(temp);
            TryDeleteDirectory(backup);
        }
    }

    static void PrepareBackup(string installDir, string backup)
    {
        TryDeleteDirectory(backup);
        Directory.CreateDirectory(backup);
        foreach (string name in PayloadFiles.Concat(new[] { UninstallerName }))
        {
            string source = Path.Combine(installDir, name);
            if (File.Exists(source))
            {
                File.Move(source, Path.Combine(backup, name));
            }
        }
    }

    static void RestoreBackup(string installDir, string backup)
    {
        if (!Directory.Exists(backup))
        {
            return;
        }
        foreach (string name in PayloadFiles.Concat(new[] { UninstallerName }))
        {
            string saved = Path.Combine(backup, name);
            string destination = Path.Combine(installDir, name);
            if (!File.Exists(saved))
            {
                continue;
            }
            try
            {
                if (File.Exists(destination))
                {
                    File.Delete(destination);
                }
                File.Move(saved, destination);
            }
            catch
            {
            }
        }
    }

    static void SaveInstallation(Options options, string version, string launcher, string uninstaller)
    {
        using (RegistryKey key = Registry.CurrentUser.CreateSubKey(SettingsKey))
        {
            key.SetValue("InstallDir", options.InstallDir, RegistryValueKind.String);
            key.SetValue("DataDir", options.DataDir, RegistryValueKind.String);
            key.SetValue("Version", version, RegistryValueKind.String);
        }
        using (RegistryKey key = Registry.CurrentUser.CreateSubKey(UninstallKey))
        {
            string arguments = " --uninstall --install-dir " + Quote(options.InstallDir) +
                " --data " + Quote(options.DataDir);
            key.SetValue("DisplayName", ProductName, RegistryValueKind.String);
            key.SetValue("DisplayVersion", version, RegistryValueKind.String);
            key.SetValue("Publisher", "fenceo", RegistryValueKind.String);
            key.SetValue("InstallLocation", options.InstallDir, RegistryValueKind.String);
            key.SetValue("DisplayIcon", launcher, RegistryValueKind.String);
            key.SetValue("UninstallString", Quote(uninstaller) + arguments, RegistryValueKind.String);
            key.SetValue("QuietUninstallString", Quote(uninstaller) + arguments + " --quiet", RegistryValueKind.String);
            key.SetValue("NoModify", 1, RegistryValueKind.DWord);
            key.SetValue("NoRepair", 1, RegistryValueKind.DWord);
            key.SetValue("EstimatedSize", EstimateInstallSize(options.InstallDir), RegistryValueKind.DWord);
        }
    }

    static int EstimateInstallSize(string installDir)
    {
        try
        {
            long bytes = PayloadFiles.Concat(new[] { UninstallerName })
                .Select(name => new FileInfo(Path.Combine(installDir, name)))
                .Where(file => file.Exists)
                .Sum(file => file.Length);
            return (int)Math.Min(int.MaxValue, Math.Max(1, bytes / 1024));
        }
        catch
        {
            return 1;
        }
    }

    static void CreateShortcuts(Options options, string launcher, string uninstaller)
    {
        string programs = Environment.GetFolderPath(Environment.SpecialFolder.Programs);
        string group = Path.Combine(programs, ProductName);
        Directory.CreateDirectory(group);
        string applicationArguments = "--data " + Quote(options.DataDir);
        CreateShortcut(
            Path.Combine(group, ProductName + ".lnk"),
            launcher,
            applicationArguments,
            options.InstallDir,
            launcher);
        CreateShortcut(
            Path.Combine(group, "卸载 " + ProductName + ".lnk"),
            uninstaller,
            "--uninstall --install-dir " + Quote(options.InstallDir) + " --data " + Quote(options.DataDir),
            options.InstallDir,
            uninstaller);

        string desktop = Environment.GetFolderPath(Environment.SpecialFolder.DesktopDirectory);
        if (options.CreateDesktopShortcut && !string.IsNullOrWhiteSpace(desktop))
        {
            CreateShortcut(
                Path.Combine(desktop, ProductName + ".lnk"),
                launcher,
                applicationArguments,
                options.InstallDir,
                launcher);
        }
        else
        {
            TryDeleteFile(Path.Combine(desktop, ProductName + ".lnk"));
        }
    }

    static void CreateShortcut(string path, string target, string arguments, string workingDirectory, string icon)
    {
        Directory.CreateDirectory(Path.GetDirectoryName(path));
        Type shellType = Type.GetTypeFromProgID("WScript.Shell");
        if (shellType == null)
        {
            throw new Exception("当前系统无法创建快捷方式。");
        }
        object shell = Activator.CreateInstance(shellType);
        try
        {
            object shortcut = Invoke(shell, "CreateShortcut", path);
            Set(shortcut, "TargetPath", target);
            Set(shortcut, "Arguments", arguments);
            Set(shortcut, "WorkingDirectory", workingDirectory);
            Set(shortcut, "IconLocation", icon + ",0");
            Set(shortcut, "Description", ProductName + "独立任务工作台");
            Invoke(shortcut, "Save");
            Release(shortcut);
        }
        finally
        {
            Release(shell);
        }
    }

    static void Uninstall(Options options, bool quiet)
    {
        string installDir;
        if (!string.IsNullOrWhiteSpace(options.InstallDir))
        {
            installDir = NormalizeDirectory(options.InstallDir);
        }
        else
        {
            installDir = Path.GetDirectoryName(Application.ExecutablePath);
        }
        if (string.IsNullOrWhiteSpace(installDir))
        {
            throw new Exception("无法确定简作安装目录。");
        }
        if (IsFilesystemRoot(installDir))
        {
            throw new Exception("拒绝卸载文件系统根目录。");
        }

        StopInstalledProcesses(installDir);
        StopOwnedTask(installDir, true);
        RemoveShortcuts(installDir);
        try
        {
            Registry.CurrentUser.DeleteSubKeyTree(SettingsKey, false);
        }
        catch
        {
        }
        try
        {
            Registry.CurrentUser.DeleteSubKeyTree(UninstallKey, false);
        }
        catch
        {
        }

        string executable = Application.ExecutablePath;
        if (PathWithin(installDir, executable))
        {
            ScheduleDirectoryRemoval(installDir);
        }
        if (!quiet)
        {
            MessageBox.Show(
                "简作已卸载，数据目录未删除：" + Environment.NewLine +
                (string.IsNullOrWhiteSpace(options.DataDir) ? "(原安装配置)" : options.DataDir),
                ProductName + " 卸载完成", MessageBoxButtons.OK, MessageBoxIcon.Information);
        }
    }

    static void RemoveShortcuts(string installDir)
    {
        string programs = Environment.GetFolderPath(Environment.SpecialFolder.Programs);
        TryDeleteDirectory(Path.Combine(programs, ProductName));
        string desktop = Environment.GetFolderPath(Environment.SpecialFolder.DesktopDirectory);
        TryDeleteFile(Path.Combine(desktop, ProductName + ".lnk"));
    }

    static void ScheduleDirectoryRemoval(string directory)
    {
        string command = "/d /c ping 127.0.0.1 -n 3 >nul & rmdir /s /q " + Quote(directory);
        ProcessStartInfo info = new ProcessStartInfo("cmd.exe", command)
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            WindowStyle = ProcessWindowStyle.Hidden,
        };
        Process.Start(info);
    }

    static void StartLauncher(string launcher, string dataDir)
    {
        Process.Start(new ProcessStartInfo(launcher)
        {
            Arguments = "--data " + Quote(dataDir),
            WorkingDirectory = Path.GetDirectoryName(launcher),
            UseShellExecute = true,
        });
    }

    static void StopInstalledProcesses(string installDir)
    {
        foreach (string name in new[] { "简作", "jianzuo-service" })
        {
            foreach (Process process in Process.GetProcessesByName(name))
            {
                try
                {
                    string path = process.MainModule.FileName;
                    if (!PathWithin(installDir, path))
                    {
                        continue;
                    }
                    ProcessStartInfo info = new ProcessStartInfo(
                        Path.Combine(Environment.SystemDirectory, "taskkill.exe"),
                        "/PID " + process.Id + " /T /F")
                    {
                        UseShellExecute = false,
                        CreateNoWindow = true,
                        WindowStyle = ProcessWindowStyle.Hidden,
                    };
                    Process.Start(info).WaitForExit(5000);
                }
                catch
                {
                }
                finally
                {
                    process.Dispose();
                }
            }
        }
    }

    static void RegisterStartupTask(Options options, string launcher)
    {
        string identity = WindowsIdentity.GetCurrent().Name;
        string arguments = "--data " + Quote(options.DataDir) + " --background";
        object service = null;
        object root = null;
        try
        {
            Type type = Type.GetTypeFromProgID("Schedule.Service");
            if (type == null)
            {
                throw new Exception("当前系统没有 Windows 任务计划服务。");
            }
            service = Activator.CreateInstance(type);
            Invoke(service, "Connect");
            root = Invoke(service, "GetFolder", "\\");

            object definition = Invoke(service, "NewTask", 0);
            object settings = Get(definition, "Settings");
            object principal = Get(definition, "Principal");
            Set(Get(definition, "RegistrationInfo"), "Description", ProductName + "独立任务工作台（当前用户登录后启动）");
            Set(settings, "StartWhenAvailable", true);
            Set(settings, "AllowDemandStart", true);
            Set(settings, "Hidden", true);
            Set(settings, "DisallowStartIfOnBatteries", false);
            Set(settings, "StopIfGoingOnBatteries", false);
            Set(settings, "ExecutionTimeLimit", "PT0S");
            Set(settings, "RestartCount", 3);
            Set(settings, "RestartInterval", "PT1M");
            Set(settings, "MultipleInstances", 2);
            Set(principal, "UserId", identity);
            Set(principal, "LogonType", 3);
            Set(principal, "RunLevel", 0);

            object trigger = Invoke(Get(definition, "Triggers"), "Create", 9);
            Set(trigger, "UserId", identity);
            Set(trigger, "Enabled", true);
            object action = Invoke(Get(definition, "Actions"), "Create", 0);
            Set(action, "Path", launcher);
            Set(action, "Arguments", arguments);
            Set(action, "WorkingDirectory", options.InstallDir);
            Invoke(root, "RegisterTaskDefinition", TaskName, definition, 6, null, null, 3, null);
        }
        finally
        {
            Release(root);
            Release(service);
        }
    }

    static void StopOwnedTask(string installDir, bool removeAnyJianzuo)
    {
        object service = null;
        object root = null;
        try
        {
            Type type = Type.GetTypeFromProgID("Schedule.Service");
            if (type == null)
            {
                return;
            }
            service = Activator.CreateInstance(type);
            Invoke(service, "Connect");
            root = Invoke(service, "GetFolder", "\\");
            object task;
            try
            {
                task = Invoke(root, "GetTask", TaskName);
            }
            catch
            {
                return;
            }
            string executable = TaskExecutable(task);
            bool ownedPath = !string.IsNullOrWhiteSpace(executable) && PathWithin(installDir, executable);
            bool jianzuoTask = IsJianzuoTask(task, executable);
            if (ownedPath || (removeAnyJianzuo && jianzuoTask))
            {
                try
                {
                    Invoke(task, "Stop", 0);
                }
                catch
                {
                }
                Invoke(root, "DeleteTask", TaskName, 0);
            }
        }
        catch
        {
        }
        finally
        {
            Release(root);
            Release(service);
        }
    }

    static bool IsJianzuoTask(object task, string executable)
    {
        string fileName = Path.GetFileName(executable ?? "");
        bool launcher = fileName.Equals(LauncherName, StringComparison.OrdinalIgnoreCase) ||
            fileName.Equals("jianzuo-launcher.exe", StringComparison.OrdinalIgnoreCase) ||
            fileName.Equals("jianzuo.exe", StringComparison.OrdinalIgnoreCase);
        if (!launcher)
        {
            return false;
        }
        string arguments = TaskArguments(task);
        return arguments.IndexOf("--background", StringComparison.OrdinalIgnoreCase) >= 0 &&
            arguments.IndexOf("--data", StringComparison.OrdinalIgnoreCase) >= 0;
    }

    static ExistingTask ReadExistingTask()
    {
        object service = null;
        object root = null;
        try
        {
            Type type = Type.GetTypeFromProgID("Schedule.Service");
            if (type == null)
            {
                return null;
            }
            service = Activator.CreateInstance(type);
            Invoke(service, "Connect");
            root = Invoke(service, "GetFolder", "\\");
            object task = Invoke(root, "GetTask", TaskName);
            object actions = Get(Get(task, "Definition"), "Actions");
            if (Convert.ToInt32(Get(actions, "Count")) != 1)
            {
                return null;
            }
            object action = Invoke(actions, "Item", 1);
            string executable = Convert.ToString(Get(action, "Path"));
            string arguments = Convert.ToString(Get(action, "Arguments"));
            return new ExistingTask
            {
                Executable = executable,
                Arguments = arguments,
                DataDir = QuotedArgument(arguments, "--data"),
            };
        }
        catch
        {
            return null;
        }
        finally
        {
            Release(root);
            Release(service);
        }
    }

    static string TaskExecutable(object task)
    {
        object actions = Get(Get(task, "Definition"), "Actions");
        if (Convert.ToInt32(Get(actions, "Count")) != 1)
        {
            return "";
        }
        object action = Invoke(actions, "Item", 1);
        return Convert.ToString(Get(action, "Path"));
    }

    static string TaskArguments(object task)
    {
        object actions = Get(Get(task, "Definition"), "Actions");
        if (Convert.ToInt32(Get(actions, "Count")) != 1)
        {
            return "";
        }
        object action = Invoke(actions, "Item", 1);
        return Convert.ToString(Get(action, "Arguments"));
    }

    static string QuotedArgument(string arguments, string name)
    {
        if (string.IsNullOrWhiteSpace(arguments))
        {
            return "";
        }
        string marker = name + " \"";
        int start = arguments.IndexOf(marker, StringComparison.OrdinalIgnoreCase);
        if (start < 0)
        {
            return "";
        }
        start += marker.Length;
        int end = arguments.IndexOf('"', start);
        return end > start ? arguments.Substring(start, end - start) : "";
    }

    static void DeleteLegacyRunEntry()
    {
        try
        {
            using (RegistryKey key = Registry.CurrentUser.OpenSubKey(LegacyRunKey, true))
            {
                if (key != null)
                {
                    key.DeleteValue(LegacyRunName, false);
                }
            }
        }
        catch
        {
        }
    }

    static string ReadSavedPath(string name)
    {
        try
        {
            using (RegistryKey key = Registry.CurrentUser.OpenSubKey(SettingsKey))
            {
                return key == null ? "" : Convert.ToString(key.GetValue(name));
            }
        }
        catch
        {
            return "";
        }
    }

    static string ReadVersion()
    {
        using (Stream stream = Assembly.GetExecutingAssembly().GetManifestResourceStream(VersionResource))
        {
            if (stream == null)
            {
                return "0.0.0";
            }
            using (StreamReader reader = new StreamReader(stream, Encoding.UTF8, true))
            {
                string version = reader.ReadToEnd().Trim();
                return version.Length == 0 ? "0.0.0" : version;
            }
        }
    }

    static void TryWriteInstallError(Exception error)
    {
        foreach (string root in new[]
        {
            Path.Combine(Environment.GetFolderPath(
                Environment.SpecialFolder.LocalApplicationData), "Jianzuo"),
            Path.Combine(Path.GetTempPath(), "Jianzuo"),
        })
        {
            try
            {
                Directory.CreateDirectory(root);
                File.AppendAllText(
                    Path.Combine(root, "installer-error.log"),
                    DateTime.Now.ToString("O") + " " + error + Environment.NewLine,
                    new UTF8Encoding(false));
                return;
            }
            catch
            {
            }
        }
    }

    static string NewTempDirectory()
    {
        string path = Path.Combine(Path.GetTempPath(), "JianzuoSetup-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(path);
        return path;
    }

    static void TryDeleteFile(string path)
    {
        try
        {
            if (!string.IsNullOrWhiteSpace(path) && File.Exists(path))
            {
                File.Delete(path);
            }
        }
        catch
        {
        }
    }

    static void TryDeleteDirectory(string path)
    {
        try
        {
            if (!string.IsNullOrWhiteSpace(path) && Directory.Exists(path))
            {
                Directory.Delete(path, true);
            }
        }
        catch
        {
        }
    }

    static string Quote(string value)
    {
        value = value ?? "";
        if (value.Contains("\""))
        {
            throw new Exception("命令参数不能包含双引号。");
        }
        int trailingBackslashes = value.Length - value.TrimEnd('\\').Length;
        string body = value.Substring(0, value.Length - trailingBackslashes);
        return "\"" + body + new string('\\', trailingBackslashes * 2) + "\"";
    }

    static object Get(object target, string name)
    {
        return target.GetType().InvokeMember(name, BindingFlags.GetProperty, null, target, null);
    }

    static void Set(object target, string name, object value)
    {
        target.GetType().InvokeMember(name, BindingFlags.SetProperty, null, target, new[] { value });
    }

    static object Invoke(object target, string name, params object[] args)
    {
        return target.GetType().InvokeMember(name, BindingFlags.InvokeMethod, null, target, args);
    }

    static void Release(object value)
    {
        if (value != null && Marshal.IsComObject(value))
        {
            try
            {
                Marshal.FinalReleaseComObject(value);
            }
            catch
            {
            }
        }
    }

    static void ShowHelp()
    {
        MessageBox.Show(
            "简作安装程序" + Environment.NewLine + Environment.NewLine +
            "双击后按向导安装到当前用户，不请求管理员权限。" + Environment.NewLine + Environment.NewLine +
            "静默安装：" + Environment.NewLine +
            "Jianzuo-Setup-User-x64.exe --quiet --data \"D:\\Jianzuo\\data\"" + Environment.NewLine + Environment.NewLine +
            "卸载时保留数据目录。",
            ProductName + "安装", MessageBoxButtons.OK, MessageBoxIcon.Information);
    }
}

sealed class Options
{
    public string InstallDir;
    public string DataDir;
    public bool CreateShortcuts;
    public bool CreateDesktopShortcut;
    public bool WriteRegistry;
    public bool EnableStartup;
    public bool LaunchAfterInstall;
    public ExistingTask ExistingTask;
}

sealed class ExistingTask
{
    public string Executable;
    public string Arguments;
    public string DataDir;
}

sealed class SetupForm : Form
{
    readonly TextBox installDir = new TextBox();
    readonly TextBox dataDir = new TextBox();
    readonly CheckBox desktop = new CheckBox();
    readonly CheckBox startup = new CheckBox();
    readonly CheckBox launch = new CheckBox();
    readonly Button install = new Button();
    readonly Label status = new Label();

    public Options Options { get; private set; }

    public SetupForm(Options options)
    {
        Options = options;
        Text = "安装简作";
        StartPosition = FormStartPosition.CenterScreen;
        ClientSize = new Size(650, 430);
        MinimumSize = new Size(600, 460);
        Font = new Font("Microsoft YaHei UI", 9F);
        AutoScaleMode = AutoScaleMode.Dpi;
        BackColor = Color.White;

        TableLayoutPanel layout = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            Padding = new Padding(28),
            ColumnCount = 1,
            RowCount = 10,
            AutoScroll = true,
        };
        layout.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F));
        Controls.Add(layout);

        Label title = new Label
        {
            Text = "安装简作到当前用户",
            AutoSize = true,
            Font = new Font(Font.FontFamily, 17F, FontStyle.Bold),
            Margin = new Padding(0, 0, 0, 6),
        };
        layout.Controls.Add(title);

        layout.Controls.Add(LabelFor("程序目录"));
        installDir.Text = options.InstallDir;
        installDir.Anchor = AnchorStyles.Left | AnchorStyles.Right;
        layout.Controls.Add(PathRow(installDir, BrowseInstall));

        layout.Controls.Add(LabelFor("数据目录（升级和卸载不会删除）"));
        dataDir.Text = options.DataDir;
        dataDir.Anchor = AnchorStyles.Left | AnchorStyles.Right;
        layout.Controls.Add(PathRow(dataDir, BrowseData));

        desktop.Text = "创建桌面快捷方式";
        desktop.Checked = options.CreateDesktopShortcut;
        desktop.AutoSize = true;
        desktop.Margin = new Padding(0, 12, 0, 0);
        layout.Controls.Add(desktop);

        startup.Text = "登录 Windows 后自动启动";
        startup.Checked = options.EnableStartup;
        startup.AutoSize = true;
        startup.Margin = new Padding(0, 4, 0, 0);
        layout.Controls.Add(startup);

        launch.Text = "安装完成后启动简作";
        launch.Checked = options.LaunchAfterInstall;
        launch.AutoSize = true;
        launch.Margin = new Padding(0, 4, 0, 0);
        layout.Controls.Add(launch);

        status.Text = "安装不需要管理员权限；现有任务中的数据目录会自动沿用。";
        status.AutoSize = true;
        status.ForeColor = Color.DimGray;
        status.Margin = new Padding(0, 12, 0, 0);
        layout.Controls.Add(status);

        FlowLayoutPanel actions = new FlowLayoutPanel
        {
            FlowDirection = FlowDirection.RightToLeft,
            Dock = DockStyle.Fill,
            AutoSize = true,
            Margin = new Padding(0, 18, 0, 0),
        };
        install.Text = "开始安装";
        install.AutoSize = true;
        install.Padding = new Padding(18, 5, 18, 5);
        install.Click += (sender, args) => ConfirmInstall();
        Button cancel = new Button
        {
            Text = "取消",
            AutoSize = true,
            Padding = new Padding(12, 5, 12, 5),
        };
        cancel.Click += (sender, args) => DialogResult = DialogResult.Cancel;
        actions.Controls.Add(install);
        actions.Controls.Add(cancel);
        layout.Controls.Add(actions);
        AcceptButton = install;
    }

    static Label LabelFor(string text)
    {
        return new Label
        {
            Text = text,
            AutoSize = true,
            Margin = new Padding(0, 12, 0, 4),
        };
    }

    static Control PathRow(TextBox textBox, EventHandler browse)
    {
        TableLayoutPanel row = new TableLayoutPanel
        {
            ColumnCount = 2,
            Dock = DockStyle.Fill,
            AutoSize = true,
            Margin = new Padding(0),
        };
        row.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F));
        row.ColumnStyles.Add(new ColumnStyle(SizeType.AutoSize));
        textBox.Dock = DockStyle.Fill;
        row.Controls.Add(textBox, 0, 0);
        Button button = new Button
        {
            Text = "浏览...",
            AutoSize = true,
            Margin = new Padding(8, 0, 0, 0),
        };
        button.Click += browse;
        row.Controls.Add(button, 1, 0);
        return row;
    }

    void BrowseInstall(object sender, EventArgs args)
    {
        BrowseInto(installDir);
    }

    void BrowseData(object sender, EventArgs args)
    {
        BrowseInto(dataDir);
    }

    static void BrowseInto(TextBox target)
    {
        using (FolderBrowserDialog dialog = new FolderBrowserDialog
        {
            Description = "选择文件夹",
            SelectedPath = Directory.Exists(target.Text) ? target.Text : "",
            ShowNewFolderButton = true,
        })
        {
            if (dialog.ShowDialog() == DialogResult.OK)
            {
                target.Text = dialog.SelectedPath;
            }
        }
    }

    void ConfirmInstall()
    {
        try
        {
            Options.InstallDir = ProgramNormalize(installDir.Text);
            Options.DataDir = ProgramNormalize(dataDir.Text);
            ProgramValidate(Options);
            Options.CreateDesktopShortcut = desktop.Checked;
            Options.EnableStartup = startup.Checked;
            Options.LaunchAfterInstall = launch.Checked;
            DialogResult = DialogResult.OK;
        }
        catch (Exception error)
        {
            MessageBox.Show(error.Message, "请检查安装配置",
                MessageBoxButtons.OK, MessageBoxIcon.Warning);
        }
    }

    static string ProgramNormalize(string value)
    {
        value = Environment.ExpandEnvironmentVariables((value ?? "").Trim());
        if (value.Length == 0 || value.Contains("\""))
        {
            throw new Exception("目录路径不能为空且不能包含双引号。");
        }
        string full = Path.GetFullPath(value);
        if (full.StartsWith(@"\\", StringComparison.Ordinal))
        {
            throw new Exception("目录不能位于网络共享路径。");
        }
        string trimmed = full.TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);
        string root = Path.GetPathRoot(trimmed);
        return root != null && string.Equals(root, trimmed, StringComparison.OrdinalIgnoreCase)
            ? root
            : trimmed;
    }

    static void ProgramValidate(Options options)
    {
        if (Path.GetPathRoot(options.InstallDir) == null || Path.GetPathRoot(options.DataDir) == null)
        {
            throw new Exception("安装目录和数据目录必须是本机绝对路径。");
        }
        string relative = RelativePath(options.InstallDir, options.DataDir);
        string reverse = RelativePath(options.DataDir, options.InstallDir);
        if (string.Equals(options.InstallDir, options.DataDir, StringComparison.OrdinalIgnoreCase) ||
            (!relative.Equals("..", StringComparison.Ordinal) &&
             !relative.StartsWith(".." + Path.DirectorySeparatorChar, StringComparison.Ordinal)) ||
            (!reverse.Equals("..", StringComparison.Ordinal) &&
             !reverse.StartsWith(".." + Path.DirectorySeparatorChar, StringComparison.Ordinal)))
        {
            throw new Exception("安装目录和数据目录不能互相包含。");
        }
    }

    static string RelativePath(string root, string candidate)
    {
        string fullRoot = Path.GetFullPath(root);
        string fullCandidate = Path.GetFullPath(candidate);
        if (!string.Equals(Path.GetPathRoot(fullRoot), Path.GetPathRoot(fullCandidate), StringComparison.OrdinalIgnoreCase))
        {
            return "..";
        }
        string basePath = fullRoot
            .TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar)
            + Path.DirectorySeparatorChar;
        Uri baseUri = new Uri(basePath, UriKind.Absolute);
        Uri targetUri = new Uri(fullCandidate, UriKind.Absolute);
        string relative = Uri.UnescapeDataString(baseUri.MakeRelativeUri(targetUri).ToString())
            .Replace('/', Path.DirectorySeparatorChar);
        return relative.Length == 0 ? "." : relative;
    }
}
