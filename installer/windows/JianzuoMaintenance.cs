using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Reflection;
using System.Runtime.InteropServices;
using System.Security.Principal;
using System.Security.Cryptography;
using System.Text;
using System.Threading;
using System.Xml;
using Microsoft.Win32;

// Inno Setup owns files, shortcuts, HKCU settings and native uninstallation.
// This helper only validates ownership and transacts the Windows login task.
// It never copies payloads, recursively deletes directories or kills applications.
static class Program
{
    // The test namespace is a compile-time embedded resource, never an end-user
    // runtime switch. Isolated fixture builds cannot touch production identities.
    static readonly string TestNamespace = ReadTestNamespace();
    static readonly string ProductKey = TestNamespace.Length == 0 ? "Jianzuo" : "Jianzuo-Test-" + TestNamespace;
    static readonly string ProductName = TestNamespace.Length == 0 ? "Duo" : "Jianzuo Test " + TestNamespace;
    static readonly string TaskName = TestNamespace.Length == 0 ? "Jianzuo User" : ProductKey;
    static readonly string SettingsKey = @"Software\" + ProductKey;
    static readonly string LegacyKey = @"Software\Microsoft\Windows\CurrentVersion\Uninstall\" + ProductKey;
    static readonly string NativeKey = LegacyKey + "_is1";
    const string RunKey = @"Software\Microsoft\Windows\CurrentVersion\Run";
    static readonly string RunName = TestNamespace.Length == 0 ? "JianzuoPortable" : ProductKey + "Portable";
    const string LauncherName = "Duo.exe";
    const string MarkerName = ".jianzuo-install";
    static readonly string[] ManagedNames = { LauncherName, "duo-service.exe", "简作.exe", "jianzuo-service.exe", "使用说明.md", "THIRD-PARTY-NOTICES.txt", "DuoMaintenance.exe", "JianzuoMaintenance.exe", "卸载简作.exe", MarkerName, "unins000.exe", "unins000.dat" };

    [STAThread]
    static int Main(string[] args)
    {
        string result = Value(args, "--result"), command = args.FirstOrDefault() ?? "";
        try
        {
            if (command == "--verify") { WriteResult(result, null); return 0; }
            if (command == "--probe") { WriteResult(result, Probe()); return 0; }
            if (command == "--rollback") { Rollback(Value(args, "--transaction")); WriteResult(result, null); return 0; }
            if (command == "--guard") { GuardData(Value(args, "--transaction")); return 0; }
            if (command == "--release-guard") { ReleaseDataGuard(Value(args, "--transaction")); WriteResult(result, null); return 0; }
            Options options = ReadOptions(args);
            if (command == "--validate") Validate(options);
            else if (command == "--prepare") Prepare(options, Value(args, "--transaction"));
            else if (command == "--apply") Apply(options, Value(args, "--transaction"));
            else if (command == "--commit") Commit(options, Value(args, "--transaction"));
            else if (command == "--uninstall-check") ValidateUninstall(options);
            else if (command == "--uninstall-startup") RemoveStartup(options);
            else throw new Exception("安装维护操作无效。");
            WriteResult(result, null); return 0;
        }
        catch (Exception error)
        {
            WriteResult(result, new Dictionary<string, string> { { "Error", FormatInstallError(error, command, WriteError(error, command)) } });
            return 1;
        }
    }
    static string Value(string[] args, string name)
    {
        for (int i = 0; i < args.Length; i++) if (args[i].Equals(name, StringComparison.OrdinalIgnoreCase))
        { if (i + 1 == args.Length) throw new Exception(name + " 缺少参数。"); return args[i + 1]; }
        return "";
    }
    static string ReadTestNamespace()
    {
        using (Stream stream = Assembly.GetExecutingAssembly().GetManifestResourceStream("JianzuoTestNamespace.txt"))
        {
            if (stream == null) return "";
            using (StreamReader reader = new StreamReader(stream))
            {
                string value = reader.ReadToEnd().Trim(); Guid guid;
                if (!Guid.TryParseExact(value, "N", out guid)) throw new Exception("Invalid isolated installer identity.");
                return value;
            }
        }
    }
    static bool Flag(string[] args, string name) { return args.Any(a => a.Equals(name, StringComparison.OrdinalIgnoreCase)); }
    static Options ReadOptions(string[] args)
    {
        return new Options { InstallDir = NormalizeDirectory(Value(args, "--install-dir")), DataDir = NormalizeDirectory(Value(args, "--data")),
            DataMode = Value(args, "--data-mode").Length == 0 ? "keep" : Value(args, "--data-mode"), PreviousData = Value(args, "--previous-data"),
            Interactive = Flag(args, "--interactive"), ConfirmData = Flag(args, "--confirm-data"), Validator = Value(args, "--data-validator"), OwnerPid = Value(args, "--owner-pid"),
            Update = Flag(args, "--update"), MigrateFrom = Value(args, "--migrate-from"), EnableStartup = Flag(args, "--startup"), Desktop = Flag(args, "--desktop"), TestFailAfterTask = TestNamespace.Length > 0 && Flag(args, "--test-fail-after-task") };
    }
    static string NormalizeDirectory(string value)
    {
        value = value ?? "";
        // Inno writes the displayed path into registry/shortcut arguments. Do
        // not accept a different expanded/trimmed path only inside this helper.
        if (value != value.Trim() || value.Contains("%"))
            throw new Exception("目录不能含首尾空白或未展开的 % 环境变量，请选择实际的绝对目录；目录内部的空格可以保留。");
        if (value.Length < 3 || value.IndexOfAny(new[] { '"', '\r', '\n' }) >= 0 || value.StartsWith(@"\\") ||
            !Path.IsPathRooted(value) || value[1] != ':' || (value[2] != '\\' && value[2] != '/'))
            throw new Exception("请选择本机绝对目录路径（不能是网络共享）。");
        string full = Path.GetFullPath(value).TrimEnd('\\', '/');
        if (full.Length <= 2) throw new Exception("目录不能是磁盘根目录。");
        return full;
    }
    static bool SamePath(string a, string b)
    {
        try { return string.Equals(NormalizeDirectory(a), NormalizeDirectory(b), StringComparison.OrdinalIgnoreCase); } catch { return false; }
    }
    static bool PathWithin(string parent, string child)
    { return SamePath(parent, child) || NormalizeDirectory(child).StartsWith(NormalizeDirectory(parent) + "\\", StringComparison.OrdinalIgnoreCase); }
    static void RejectReparsePath(string path)
    {
        for (DirectoryInfo directory = new DirectoryInfo(path); directory != null; directory = directory.Parent)
            if (directory.Exists && (directory.Attributes & FileAttributes.ReparsePoint) != 0)
                throw new Exception("目录不能包含符号链接或目录联接：" + path);
    }
    static string InstallMarker(string installDir) { return "Jianzuo installer ownership v1\n" + NormalizeDirectory(installDir); }
    static bool RegistryOwned(string key, string name, string path)
    { using (RegistryKey registry = Registry.CurrentUser.OpenSubKey(key)) return registry != null && SamePath(Convert.ToString(registry.GetValue(name)), path); }
    static string Saved(string name)
    { using (RegistryKey key = Registry.CurrentUser.OpenSubKey(SettingsKey)) return key == null ? "" : Convert.ToString(key.GetValue(name)); }
    static bool OwnedInstallation(string path)
    {
        string marker = Path.Combine(path, MarkerName);
        if (File.Exists(marker)) return (File.GetAttributes(marker) & FileAttributes.ReparsePoint) == 0 &&
            string.Equals(File.ReadAllText(marker, Encoding.UTF8), InstallMarker(path), StringComparison.OrdinalIgnoreCase);
        return RegistryOwned(SettingsKey, "InstallDir", path) && RegistryOwned(LegacyKey, "InstallLocation", path) && File.Exists(Path.Combine(path, "卸载简作.exe"));
    }
    static bool OwnedExecutable(string root, string executable)
    {
        return new[] { LauncherName, "duo-service.exe", "简作.exe", "jianzuo-launcher.exe", "jianzuo.exe", "jianzuo-service.exe", "卸载简作.exe", "unins000.exe" }
            .Any(name => SamePath(Path.Combine(root, name), executable));
    }
    static void ValidateInstallDestination(string path)
    {
        RejectReparsePath(path); bool owned = OwnedInstallation(path);
        foreach (string name in ManagedNames)
        {
            string file = Path.Combine(path, name);
            if (Directory.Exists(file) || (File.Exists(file) && (!owned || (File.GetAttributes(file) & FileAttributes.ReparsePoint) != 0)))
                throw new Exception("程序目录中存在不能覆盖的同名文件，请选择专用目录：" + file);
        }
        if (Directory.Exists(path) && Directory.EnumerateFiles(path, "unins*.exe").Any(p => !SamePath(p, Path.Combine(path, "unins000.exe"))))
            throw new Exception("目录包含其他卸载程序，请使用 Duo 原目录或空白专用目录。");
    }
    static void ValidateOptions(Options options)
    {
        options.InstallDir = NormalizeDirectory(options.InstallDir); options.DataDir = NormalizeDirectory(options.DataDir);
        if (PathWithin(options.InstallDir, options.DataDir) || PathWithin(options.DataDir, options.InstallDir))
            throw new Exception("程序目录和数据目录不能相同或互相包含。");
        RejectReparsePath(options.InstallDir); RejectReparsePath(options.DataDir);
    }
    static void ValidateUpdate(Options options, bool owned, string savedInstall, string savedData)
    {
        if (options.Update && (!owned || !SamePath(savedInstall, options.InstallDir) || !SamePath(savedData, options.DataDir) || options.MigrateFrom.Length != 0))
            throw new Exception("自动更新仅允许原目录、原数据目录中的已安装版本；不能新装或迁移。");
    }
    static void ValidateTaskChoice(Options options, ExistingTask task)
    {
        if (task == null) return; // A recognized HKCU Run entry can be the migration source.
        if (!task.Recognized) throw new Exception("已有同名启动任务无法确认属于 Duo（原简作），未作修改。请人工核查任务：" + TaskName);
        string source = Path.GetDirectoryName(task.Executable);
        if (!SamePath(source, options.InstallDir))
        {
            if (options.Update || !SamePath(options.MigrateFrom, source))
                throw new Exception("发现旧简作入口：" + source + "；请在向导明确勾选迁移，原程序和数据不会删除。");
            if (!SamePath(task.DataDir, SourceData(options))) throw new Exception("旧启动任务的数据目录与已确认的来源不一致：" + task.DataDir);
        }
        else if (options.MigrateFrom.Length > 0) throw new Exception("迁移来源与当前启动任务不一致，请重新确认。");
    }
    static string SourceData(Options options) { return options.DataMode == "keep" || options.PreviousData.Length == 0 ? options.DataDir : options.PreviousData; }
    static void ValidateDataChoice(Options options, string previous)
    {
        if (!new[] { "keep", "fresh", "existing" }.Contains(options.DataMode)) throw new Exception("数据模式无效。");
        if (options.DataMode == "keep")
        {
            if (previous.Length > 0 && !SamePath(previous, options.DataDir)) throw new Exception("保留模式必须沿用原数据目录：" + previous);
            if (previous.Length == 0) ValidateFreshData(options.DataDir);
            return;
        }
        if (options.Update || !options.Interactive || !options.ConfirmData) throw new Exception("新建或载入数据必须在交互向导中明确确认；静默安装和自动更新不能切换数据。");
        if (previous.Length > 0)
        {
            if (!SamePath(previous, options.PreviousData)) throw new Exception("原数据目录已改变，请重新打开安装器确认。");
            if (PathWithin(previous, options.DataDir) || PathWithin(options.DataDir, previous)) throw new Exception("新旧数据目录不能相同或互相包含。");
            RejectReparsePath(previous);
        }
        else if (options.PreviousData.Length > 0) throw new Exception("数据切换来源无法确认。");
        if (options.DataMode == "fresh") ValidateFreshData(options.DataDir);
        else ValidateExistingData(options);
    }
    static void ValidateFreshData(string path)
    {
        RejectReparsePath(path);
        if (File.Exists(path) || (Directory.Exists(path) && Directory.EnumerateFileSystemEntries(path).Any()))
            throw new Exception("全新数据目录必须为空或尚不存在；不会覆盖、清空或合并任何已有内容。");
    }
    static void ValidateDataIdle(string path)
    {
        if (path.Length == 0) return;
        RejectReparsePath(path);
        bool created;
        using (Mutex mutex = new Mutex(false, DataMutexName(path), out created))
        {
            if (!created) throw new Exception("数据目录的 Duo 托盘/首次配置窗口仍在运行，请先正常退出：" + path);
            string lockPath = Path.Combine(path, "service.lock");
            if (Directory.Exists(lockPath) || (File.Exists(lockPath) && (File.GetAttributes(lockPath) & FileAttributes.ReparsePoint) != 0)) throw new Exception("数据锁不是安全的普通文件。");
            if (File.Exists(lockPath))
                try { using (FileStream stream = new FileStream(lockPath, FileMode.Open, FileAccess.Read, FileShare.None)) { } }
                catch (IOException) { throw new Exception("数据目录正在使用，请先退出使用它的 Duo 服务：" + path); }
        }
    }
    static string DataMutexName(string path)
    {
        using (SHA256 hash = SHA256.Create()) return "Local\\JianzuoPortable_" + BitConverter.ToString(hash.ComputeHash(Encoding.UTF8.GetBytes(Path.GetFullPath(path).ToLowerInvariant()))).Replace("-", "");
    }
    static void ValidateExistingData(Options options)
    {
        if (!Directory.Exists(options.DataDir)) throw new Exception("请选择已存在的 Duo 数据目录。");
        foreach (string name in new[] { "config.json", "jianzuo.db", "jianzuo.db-wal", "jianzuo.db-shm", "jianzuo.db-journal", "service.lock" })
        {
            string path = Path.Combine(options.DataDir, name);
            if (Directory.Exists(path) || (File.Exists(path) && (File.GetAttributes(path) & FileAttributes.ReparsePoint) != 0)) throw new Exception("数据文件不能是目录或符号链接：" + name);
        }
        if (!File.Exists(options.Validator) || !Path.GetFileName(options.Validator).Equals("duo-service.exe", StringComparison.OrdinalIgnoreCase)) throw new Exception("缺少安装包内的只读数据验证器。");
        ProcessStartInfo info = new ProcessStartInfo(options.Validator, "--validate-data --data " + Quote(options.DataDir)) { UseShellExecute = false, CreateNoWindow = true, RedirectStandardOutput = true, RedirectStandardError = true, StandardOutputEncoding = Encoding.UTF8, StandardErrorEncoding = Encoding.UTF8 };
        using (Process process = new Process())
        {
            process.StartInfo = info; StringBuilder output = new StringBuilder(), errors = new StringBuilder();
            process.OutputDataReceived += delegate(object sender, DataReceivedEventArgs e) { if (e.Data != null && output.Length < 4096) output.AppendLine(e.Data); };
            process.ErrorDataReceived += delegate(object sender, DataReceivedEventArgs e) { if (e.Data != null && errors.Length < 4096) errors.AppendLine(e.Data); };
            process.Start(); process.BeginOutputReadLine(); process.BeginErrorReadLine();
            if (!process.WaitForExit(15000))
            {
                // This is exclusively the read-only validator we just created,
                // never a user's running Duo/CLI/task process.
                try { if (!process.HasExited) process.Kill(); } catch (InvalidOperationException) { }
                process.WaitForExit();
                throw new Exception("只读数据验证超时，验证子进程已退出，未切换；请稍后重试。");
            }
            process.WaitForExit();
            if (process.ExitCode != 0) throw new Exception("无法载入该数据目录：" + errors.ToString().Trim());
            if (output.ToString().Trim() != "{\"app\":\"jianzuo\",\"valid\":true,\"protocol\":1}") throw new Exception("数据验证器未返回有效确认，未切换。");
        }
    }
    static void Validate(Options options)
    {
        ValidateOptions(options); ValidateInstallDestination(options.InstallDir);
        string installed = Saved("InstallDir"), data = Saved("DataDir");
        ValidateUpdate(options, OwnedInstallation(options.InstallDir), installed, data);
        if (installed.Length > 0 && !SamePath(installed, options.InstallDir)) throw new Exception("已安装 Duo 位于 " + installed + "，请在原目录升级/修复。");
        foreach (string key in new[] { LegacyKey, NativeKey })
            using (RegistryKey registry = Registry.CurrentUser.OpenSubKey(key))
                if (registry != null && !SamePath(Convert.ToString(registry.GetValue("InstallLocation")), options.InstallDir))
                    throw new Exception("另一个目录已注册 Duo，请沿用原安装目录。");
        ExistingTask task = ReadExistingTask(); ValidateTaskChoice(options, task);
        ExistingTask run = InspectLegacyRun(ReadLegacyRun());
        ExistingTask source = task != null && task.Recognized ? task : (run != null && run.Recognized ? run : null);
        string previous = data.Length > 0 ? data : (source == null ? "" : source.DataDir);
        if (source != null && previous.Length > 0 && !SamePath(source.DataDir, previous)) throw new Exception("启动入口与已登记的数据目录不一致，请先核查，未切换数据。");
        if (options.DataMode != "keep") { ValidateDataIdle(previous); ValidateDataIdle(options.DataDir); }
        ValidateDataChoice(options, previous);
        if (task == null && options.MigrateFrom.Length > 0 && (run == null || !run.Recognized || !SamePath(Path.GetDirectoryName(run.Executable), options.MigrateFrom)))
            throw new Exception("旧入口已改变，请重新打开安装器确认来源。");
        ValidateShortcuts(options, task ?? run); ValidateLegacyRun(options);
        EnsureNotRunning(options.InstallDir); if (options.MigrateFrom.Length > 0) EnsureNotRunning(options.MigrateFrom);
    }
    static Dictionary<string, string> Probe()
    {
        ExistingTask task = ReadExistingTask(), run = InspectLegacyRun(ReadLegacyRun());
        ExistingTask source = task != null && task.Recognized ? task : (run != null && run.Recognized ? run : null);
        string install = Saved("InstallDir"), data = Saved("DataDir");
        if (data.Length == 0 && source != null) data = source.DataDir;
        string desktopPath = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.DesktopDirectory), ProductName + ".lnk");
        string desktopOwner = install.Length > 0 ? install : (source == null ? "" : Path.GetDirectoryName(source.Executable));
        return new Dictionary<string, string> {
            { "InstallDir", install }, { "DataDir", data }, { "HasInstall", install.Length > 0 && OwnedInstallation(install) ? "1" : "0" },
            { "Startup", task != null ? (task.Recognized && task.Enabled ? "1" : "0") : (run != null && run.Recognized ? "1" : (install.Length > 0 ? "0" : "1")) },
            { "Desktop", desktopOwner.Length == 0 || ShortcutOwned(desktopPath, desktopOwner) || LegacyShortcutPaths().Any(p => p.EndsWith("简作.lnk", StringComparison.OrdinalIgnoreCase) && Path.GetDirectoryName(p).Equals(Environment.GetFolderPath(Environment.SpecialFolder.DesktopDirectory), StringComparison.OrdinalIgnoreCase) && ShortcutOwned(p, desktopOwner)) ? "1" : "0" },
            { "TaskPresent", task == null ? "0" : "1" }, { "TaskRecognized", task != null && task.Recognized ? "1" : "0" },
            { "TaskDirectory", task == null || !task.Recognized ? "" : Path.GetDirectoryName(task.Executable) }, { "TaskData", task == null ? "" : task.DataDir },
            { "MigrationDirectory", source == null ? "" : Path.GetDirectoryName(source.Executable) }, { "MigrationData", source == null ? "" : source.DataDir },
            { "LegacyInstall", install.Length > 0 && RegistryOwned(LegacyKey, "InstallLocation", install) ? "1" : "0" } };
    }
    static IEnumerable<string> ShortcutPaths()
    {
        string group = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.Programs), ProductName);
        return new[] { Path.Combine(group, "Duo.lnk"), Path.Combine(group, "卸载 Duo.lnk"), Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.DesktopDirectory), ProductName + ".lnk") }.Concat(LegacyShortcutPaths());
    }
    static IEnumerable<string> LegacyShortcutPaths()
    {
        if (TestNamespace.Length > 0) return new string[0];
        string group = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.Programs), "简作");
        return new[] { Path.Combine(group, "简作.lnk"), Path.Combine(group, "卸载 简作.lnk"), Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.DesktopDirectory), "简作.lnk") };
    }
    static bool ShortcutOwned(string path, string installDir)
    {
        if (!File.Exists(path) || (File.GetAttributes(path) & FileAttributes.ReparsePoint) != 0) return false;
        object shell = null, shortcut = null;
        try { shell = Activator.CreateInstance(Type.GetTypeFromProgID("WScript.Shell")); shortcut = Invoke(shell, "CreateShortcut", path);
            return OwnedExecutable(installDir, Convert.ToString(Get(shortcut, "TargetPath"))); }
        finally { Release(shortcut); Release(shell); }
    }
    static void ValidateShortcuts(Options options, ExistingTask task)
    {
        foreach (string path in ShortcutPaths())
        {
            RejectReparsePath(Path.GetDirectoryName(path));
            ValidateShortcutVariants(path);
            if (Directory.Exists(path) || (File.Exists(path) && !ShortcutOwned(path, options.InstallDir) &&
                !(options.MigrateFrom.Length > 0 && task != null && task.Recognized && ShortcutOwned(path, options.MigrateFrom))))
                throw new Exception("快捷方式指向未知程序，不会覆盖：" + path);
        }
    }
    static void ValidateShortcutVariants(string link)
    {
        string stem = Path.Combine(Path.GetDirectoryName(link), Path.GetFileNameWithoutExtension(link));
        // Inno removes these alternate forms when creating icons; never let it
        // overwrite an unrelated same-basename URL, PIF or folder shortcut.
        foreach (string candidate in new[] { stem, stem + ".pif", stem + ".url" })
            if (File.Exists(candidate) || Directory.Exists(candidate)) throw new Exception("存在未知的同名快捷方式变体，不会覆盖：" + candidate);
    }
    static void EnsureNotRunning(string root)
    {
        foreach (string name in new[] { "Duo", "duo-service", "简作", "jianzuo-service", "jianzuo-launcher", "jianzuo" })
            foreach (Process process in Process.GetProcessesByName(name)) using (process)
            {
                string path;
                try { path = process.MainModule.FileName; } catch { throw new Exception("无法确认运行中 Duo 的目录，请退出 Duo 后重试。"); }
                if (OwnedExecutable(root, path)) throw new Exception("Duo 仍在运行。请完成任务并从托盘退出后重试；不会强制结束任务。");
            }
    }
    static string ReadLegacyRun()
    { using (RegistryKey key = Registry.CurrentUser.OpenSubKey(RunKey)) return key == null ? "" : Convert.ToString(key.GetValue(RunName)); }
    static ExistingTask InspectLegacyRun(string command)
    {
        if (command.Length == 0) return null;
        string[] args = SplitArguments(command); string data = "";
        bool recognized = args.Length > 0 && RecognizeArguments(args[0], string.Join(" ", args.Skip(1).Select(Quote)), out data);
        return new ExistingTask { Executable = args.Length == 0 ? "" : args[0], DataDir = data, Recognized = recognized, Enabled = true, Xml = "" };
    }
    static bool LegacyRunOwned(string command, Options options)
    {
        string[] args = SplitArguments(command); string data;
        return args.Length > 0 && (OwnedExecutable(options.InstallDir, args[0]) ||
            (options.MigrateFrom.Length > 0 && OwnedExecutable(options.MigrateFrom, args[0]))) &&
            RecognizeArguments(args[0], string.Join(" ", args.Skip(1).Select(Quote)), out data) && SamePath(data, SourceData(options));
    }
    static void ValidateLegacyRun(Options options)
    {
        string command = ReadLegacyRun();
        if (command.Length > 0 && !LegacyRunOwned(command, options))
            throw new Exception("旧登录启动项 " + RunName + " 指向未确认的程序或数据目录，未修改。请先核查此启动项，避免重复启动。");
    }
    static string FileHash(string path)
    { using (SHA256 hash = SHA256.Create()) using (Stream stream = File.OpenRead(path)) return Convert.ToBase64String(hash.ComputeHash(stream)); }
    static void WriteResult(string path, Dictionary<string, string> values)
    {
        if (string.IsNullOrEmpty(path)) return;
        StringBuilder ini = new StringBuilder("[Result]\r\n");
        if (values != null) foreach (KeyValuePair<string, string> pair in values)
            ini.Append(pair.Key).Append('=').Append((pair.Value ?? "").Replace("\r", " ").Replace("\n", " ")).Append("\r\n");
        File.WriteAllText(path, ini.ToString(), Encoding.Unicode);
    }
    static void Prepare(Options options, string path)
    {
        Validate(options); ExistingTask task = ReadExistingTask(); ValidateTaskChoice(options, task);
        // Selecting another data set never writes backups into that data set.
        string backupRoot = options.DataMode == "keep" && options.PreviousData.Length > 0 ? options.DataDir : Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), TestNamespace.Length == 0 ? "Duo" : ProductKey);
        string backup = Path.Combine(backupRoot, "installer-backups", DateTime.UtcNow.ToString("yyyyMMdd-HHmmss") + "-" + Guid.NewGuid().ToString("N"));
        RejectReparsePath(backup); Directory.CreateDirectory(backup); string xml = task == null ? "" : task.Xml;
        if (xml.Length > 0) File.WriteAllText(Path.Combine(backup, "startup-task.xml"), xml, Encoding.Unicode);
        XmlDocument doc = new XmlDocument(); doc.AppendChild(doc.CreateElement("JianzuoInstallerTransaction"));
        foreach (KeyValuePair<string, string> item in new Dictionary<string, string> {
            { "InstallDir", options.InstallDir }, { "DataDir", options.DataDir }, { "Before", xml }, { "After", "" }, { "Intent", "0" }, { "Startup", options.EnableStartup ? "1" : "0" }, { "Applied", "0" }, { "Committed", "0" }, { "Backup", backup },
            { "PreviousData", options.PreviousData }, { "DataMode", options.DataMode }, { "Validator", options.Validator }, { "OwnerPid", options.OwnerPid },
            { "GuardEvent", "Local\\DuoInstallData_" + Guid.NewGuid().ToString("N") },
            { "MarkerBefore", File.Exists(Path.Combine(options.InstallDir, MarkerName)) ? File.ReadAllText(Path.Combine(options.InstallDir, MarkerName), Encoding.UTF8) : "" } })
        { XmlElement element = doc.CreateElement(item.Key); element.InnerText = item.Value; doc.DocumentElement.AppendChild(element); }
        int linkIndex = 0;
        foreach (string link in ShortcutPaths())
        {
            AddTransactionValue(doc, "ShortcutPath" + linkIndex, link);
            AddTransactionValue(doc, "ShortcutBefore" + linkIndex, File.Exists(link) ? Convert.ToBase64String(File.ReadAllBytes(link)) : "");
            linkIndex++;
        }
        AddTransactionValue(doc, "ShortcutCount", linkIndex.ToString());
        foreach (string oldName in new[] { "简作.exe", "jianzuo-service.exe", "JianzuoMaintenance.exe" })
        {
            string oldFile = Path.Combine(options.InstallDir, oldName);
            string value = OwnedInstallation(options.InstallDir) && File.Exists(oldFile) ? FileHash(oldFile) : "";
            AddTransactionValue(doc, "OldFile" + Array.IndexOf(new[] { "简作.exe", "jianzuo-service.exe", "JianzuoMaintenance.exe" }, oldName), value);
            if (value.Length > 0) File.Copy(oldFile, Path.Combine(backup, oldName), false);
        }
        string legacy = Path.Combine(options.InstallDir, "卸载简作.exe");
        bool ownedLegacy = RegistryOwned(LegacyKey, "InstallLocation", options.InstallDir) && File.Exists(legacy);
        AddTransactionValue(doc, "LegacyFile", ownedLegacy ? FileHash(legacy) : "");
        AddTransactionValue(doc, "LegacyRun", ReadLegacyRun());
        if (ownedLegacy) File.Copy(legacy, Path.Combine(backup, "legacy-uninstaller.exe"), false);
        doc.Save(path);
        File.Copy(path, Path.Combine(backup, "maintenance-transaction.xml"), false);
        if (options.DataMode != "keep")
        {
            try { StartDataGuard(path); }
            catch { ReleaseDataGuard(path); throw; }
        }
    }
    static void AddTransactionValue(XmlDocument doc, string key, string value)
    { XmlElement element = doc.CreateElement(key); element.InnerText = value; doc.DocumentElement.AppendChild(element); }
    static XmlDocument ReadTransaction(string path)
    { XmlDocument doc = new XmlDocument(); doc.XmlResolver = null; doc.Load(path); if (doc.DocumentElement.Name != "JianzuoInstallerTransaction") throw new Exception("安装事务记录无效。"); return doc; }
    static string T(XmlDocument doc, string name) { return doc.DocumentElement[name].InnerText; }
    static void StartDataGuard(string path)
    {
        XmlDocument doc = ReadTransaction(path); int owner;
        if (!int.TryParse(T(doc, "OwnerPid"), out owner) || owner <= 0) throw new Exception("数据切换缺少安装进程标识。");
        using (Process parent = Process.GetProcessById(owner)) if (parent.HasExited) throw new Exception("安装进程已退出。");
        string ready = path + ".guard-ready";
        if (File.Exists(ready)) File.Delete(ready);
        if (File.Exists(path + ".guard-stopped")) File.Delete(path + ".guard-stopped");
        ProcessStartInfo info = new ProcessStartInfo(Assembly.GetExecutingAssembly().Location, "--guard --transaction " + Quote(path)) { UseShellExecute = false, CreateNoWindow = true };
        using (Process guard = Process.Start(info))
        {
            for (int i = 0; i < 100; i++)
            {
                if (File.Exists(ready))
                {
                    string status = File.ReadAllText(ready, Encoding.UTF8);
                    if (status == "ready" && !guard.HasExited) return;
                    throw new Exception(status.Length > 0 ? status : "无法锁定数据目录。");
                }
                if (guard.WaitForExit(200)) break;
            }
            throw new Exception("无法锁定新旧数据目录；未改变入口，请退出 Duo 后重试。");
        }
    }
    static void GuardData(string path)
    {
        XmlDocument doc = ReadTransaction(path); string ready = path + ".guard-ready";
        List<Mutex> mutexes = new List<Mutex>(); List<FileStream> streams = new List<FileStream>(); List<string> createdLocks = new List<string>();
        using (EventWaitHandle release = new EventWaitHandle(false, EventResetMode.ManualReset, T(doc, "GuardEvent")))
        using (Process parent = Process.GetProcessById(int.Parse(T(doc, "OwnerPid"))))
        {
            try
            {
                string[] paths = new[] { T(doc, "PreviousData"), T(doc, "DataDir") }.Where(p => p.Length > 0).Distinct(StringComparer.OrdinalIgnoreCase).OrderBy(p => p, StringComparer.OrdinalIgnoreCase).ToArray();
                foreach (string data in paths)
                {
                    RejectReparsePath(data); bool created;
                    Mutex mutex = new Mutex(true, DataMutexName(data), out created);
                    if (!created) { mutex.Dispose(); throw new Exception("数据目录的 Duo 窗口仍在运行，请退出后重试。"); }
                    mutexes.Add(mutex);
                }
                Options options = new Options { DataDir = T(doc, "DataDir"), DataMode = T(doc, "DataMode"), Validator = T(doc, "Validator") };
                if (options.DataMode == "fresh") ValidateFreshData(options.DataDir);
                foreach (string data in paths)
                {
                    RejectReparsePath(data);
                    if (!Directory.Exists(data) && !SamePath(data, T(doc, "DataDir"))) continue;
                    Directory.CreateDirectory(data); string lockPath = Path.Combine(data, "service.lock");
                    if (Directory.Exists(lockPath) || (File.Exists(lockPath) && (File.GetAttributes(lockPath) & FileAttributes.ReparsePoint) != 0)) throw new Exception("数据锁不是普通文件。");
                    bool existed = File.Exists(lockPath);
                    streams.Add(new FileStream(lockPath, FileMode.OpenOrCreate, FileAccess.ReadWrite, FileShare.None));
                    if (!existed) createdLocks.Add(lockPath);
                }
                if (options.DataMode == "existing") ValidateExistingData(options);
                WriteGuardStatus(ready, "ready");
                while (!parent.HasExited && !release.WaitOne(200)) { }
            }
            catch (Exception error) { WriteGuardStatus(ready, error.GetBaseException().Message); }
            finally
            {
                foreach (FileStream stream in streams) stream.Dispose();
                // Only empty lock files created by this guard are disposable.
                // Configurations, databases, logs and old directories are never removed.
                foreach (string lockPath in createdLocks) try { if (File.Exists(lockPath) && new FileInfo(lockPath).Length == 0) File.Delete(lockPath); } catch { }
                foreach (Mutex mutex in mutexes) { mutex.ReleaseMutex(); mutex.Dispose(); }
                File.WriteAllText(path + ".guard-stopped", "stopped", new UTF8Encoding(false));
            }
        }
    }
    static void WriteGuardStatus(string path, string status)
    {
        string temporary = path + ".tmp";
        File.WriteAllText(temporary, status, new UTF8Encoding(false));
        if (File.Exists(path)) File.Delete(path);
        File.Move(temporary, path);
    }
    static void CheckDataGuard(XmlDocument doc, string path)
    {
        if (T(doc, "DataMode") == "keep") return;
        EventWaitHandle handle;
        if (File.Exists(path + ".guard-stopped") || !File.Exists(path + ".guard-ready") || File.ReadAllText(path + ".guard-ready") != "ready" || !EventWaitHandle.TryOpenExisting(T(doc, "GuardEvent"), out handle))
            throw new Exception("数据保护已中断，未切换入口。请重新运行安装器。");
        handle.Dispose();
    }
    static void ReleaseDataGuard(string path)
    {
        if (!File.Exists(path)) return; XmlDocument doc = ReadTransaction(path);
        if (T(doc, "DataMode") == "keep") return;
        EventWaitHandle handle;
        if (EventWaitHandle.TryOpenExisting(T(doc, "GuardEvent"), out handle)) using (handle) handle.Set();
        else return;
        for (int i = 0; i < 100; i++) { if (File.Exists(path + ".guard-stopped")) return; Thread.Sleep(100); }
        throw new Exception("数据保护进程尚未退出，请关闭安装器后再启动 Duo。");
    }
    static void Apply(Options options, string path)
    {
        XmlDocument doc = ReadTransaction(path);
        if (!SamePath(T(doc, "InstallDir"), options.InstallDir) || !SamePath(T(doc, "DataDir"), options.DataDir)) throw new Exception("安装事务路径不一致。");
        CheckDataGuard(doc, path);
        ExistingTask current = ReadExistingTask(); ValidateTaskChoice(options, current);
        if ((current == null ? "" : current.Xml) != T(doc, "Before")) throw new Exception("启动任务在安装期间发生变化，未覆盖，请重试。");
        // Persist intent before COM mutation. Recovery must also cover a successful
        // Register/Delete followed by failure reading or persisting its result.
        doc.DocumentElement["Intent"].InnerText = "1"; doc.Save(path);
        object service = null, root = null, definition = null;
        try
        {
            string legacyRun = ReadLegacyRun();
            if (legacyRun != T(doc, "LegacyRun")) throw new Exception("安装过程中旧登录启动项被修改，未覆盖。");
            if (legacyRun.Length > 0)
            {
                if (!LegacyRunOwned(legacyRun, options)) throw new Exception("旧登录启动项不属于已确认安装，未删除。");
                using (RegistryKey key = Registry.CurrentUser.OpenSubKey(RunKey, true)) key.DeleteValue(RunName, false);
            }
            service = ConnectScheduler(); root = Invoke(service, "GetFolder", "\\");
            if (options.EnableStartup) { definition = BuildStartupTaskDefinition(service, options, Path.Combine(options.InstallDir, LauncherName)); Invoke(root, "RegisterTaskDefinition", TaskName, definition, 6, null, null, 3, null); }
            else if (current != null) Invoke(root, "DeleteTask", TaskName, 0);
            if (options.TestFailAfterTask) throw new Exception("Isolated fixture: failure after task mutation, before result persistence.");
            ExistingTask after = ReadExistingTask(); doc.DocumentElement["After"].InnerText = after == null ? "" : after.Xml;
            doc.DocumentElement["Applied"].InnerText = "1"; doc.Save(path);
            Directory.CreateDirectory(options.InstallDir);
            File.WriteAllText(Path.Combine(options.InstallDir, MarkerName), InstallMarker(options.InstallDir), new UTF8Encoding(false));
        }
        catch { Rollback(path); throw; }
        finally { Release(definition); Release(root); Release(service); }
    }
    static void Rollback(string path)
    {
        if (!File.Exists(path)) return; XmlDocument doc = ReadTransaction(path);
        if (T(doc, "Intent") != "1" || T(doc, "Committed") == "1") return;
        ExistingTask current = ReadExistingTask();
        string currentXml = current == null ? "" : current.Xml;
        bool unchanged = currentXml == T(doc, "Before");
        bool ours = T(doc, "Applied") == "1" ? currentXml == T(doc, "After") :
            (T(doc, "Startup") == "0" ? current == null : current != null && current.Recognized &&
                SamePath(current.Executable, Path.Combine(T(doc, "InstallDir"), LauncherName)) && SamePath(current.DataDir, T(doc, "DataDir")));
        if (!unchanged && !ours) throw new Exception("启动任务已被外部修改，未覆盖。原任务备份：" + T(doc, "Backup"));
        object service = null, root = null;
        try
        {
            service = ConnectScheduler(); root = Invoke(service, "GetFolder", "\\");
            if (!unchanged)
            {
                if (T(doc, "Before").Length > 0) Invoke(root, "RegisterTask", TaskName, T(doc, "Before"), 6, null, null, 3, null);
                else if (current != null) Invoke(root, "DeleteTask", TaskName, 0);
            }
            string marker = Path.Combine(NormalizeDirectory(T(doc, "InstallDir")), MarkerName);
            if (File.Exists(marker) && File.ReadAllText(marker, Encoding.UTF8) == InstallMarker(T(doc, "InstallDir")))
            { if (T(doc, "MarkerBefore").Length > 0) File.WriteAllText(marker, T(doc, "MarkerBefore"), new UTF8Encoding(false)); else File.Delete(marker); }
            for (int i = 0; i < int.Parse(T(doc, "ShortcutCount")); i++)
            {
                string shortcut = T(doc, "ShortcutPath" + i), before = T(doc, "ShortcutBefore" + i);
                if (File.Exists(shortcut) && !ShortcutOwned(shortcut, T(doc, "InstallDir"))) continue;
                if (before.Length > 0) { Directory.CreateDirectory(Path.GetDirectoryName(shortcut)); File.WriteAllBytes(shortcut, Convert.FromBase64String(before)); }
                else if (File.Exists(shortcut)) File.Delete(shortcut);
            }
            string legacy = Path.Combine(T(doc, "InstallDir"), "卸载简作.exe");
            string backupLegacy = Path.Combine(T(doc, "Backup"), "legacy-uninstaller.exe");
            if (T(doc, "LegacyFile").Length > 0 && !File.Exists(legacy) && File.Exists(backupLegacy) && FileHash(backupLegacy) == T(doc, "LegacyFile"))
                File.Copy(backupLegacy, legacy, false);
            if (T(doc, "LegacyRun").Length > 0 && ReadLegacyRun().Length == 0)
                using (RegistryKey key = Registry.CurrentUser.CreateSubKey(RunKey)) key.SetValue(RunName, T(doc, "LegacyRun"), RegistryValueKind.String);
            doc.DocumentElement["Applied"].InnerText = "0"; doc.DocumentElement["Intent"].InnerText = "0"; doc.Save(path);
        }
        finally { Release(root); Release(service); }
    }
    static void Commit(Options options, string path)
    {
        XmlDocument doc = ReadTransaction(path);
        if (!SamePath(T(doc, "InstallDir"), options.InstallDir) || T(doc, "Applied") != "1") throw new Exception("安装事务未完成。");
        // Inno has finalized native files and registration before this callback.
        // From here failures are maintenance warnings, never rollback the task
        // to an old directory while leaving the new native install in place.
        doc.DocumentElement["Committed"].InnerText = "1"; doc.Save(path);
        string legacy = Path.Combine(options.InstallDir, "卸载简作.exe");
        if (T(doc, "LegacyFile").Length > 0 && File.Exists(legacy) &&
            FileHash(legacy) == T(doc, "LegacyFile")) File.Delete(legacy);
        string desktop = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.DesktopDirectory), ProductName + ".lnk");
        if (!options.Desktop && (ShortcutOwned(desktop, options.InstallDir) ||
            (options.MigrateFrom.Length > 0 && ShortcutOwned(desktop, options.MigrateFrom)))) File.Delete(desktop);
        foreach (string oldLink in LegacyShortcutPaths())
            if (ShortcutOwned(oldLink, options.InstallDir) || (options.MigrateFrom.Length > 0 && ShortcutOwned(oldLink, options.MigrateFrom))) File.Delete(oldLink);
        string[] oldNames = { "简作.exe", "jianzuo-service.exe", "JianzuoMaintenance.exe" };
        for (int i = 0; i < oldNames.Length; i++)
        {
            string oldFile = Path.Combine(options.InstallDir, oldNames[i]);
            if (T(doc, "OldFile" + i).Length > 0 && File.Exists(oldFile) && FileHash(oldFile) == T(doc, "OldFile" + i)) File.Delete(oldFile);
        }
        if (RegistryOwned(LegacyKey, "InstallLocation", options.InstallDir)) Registry.CurrentUser.DeleteSubKeyTree(LegacyKey, false);
    }
    static void ValidateUninstall(Options options)
    {
        ValidateOptions(options);
        if (!OwnedInstallation(options.InstallDir) || !RegistryOwned(SettingsKey, "InstallDir", options.InstallDir)) throw new Exception("无法确认本安装所有权，未卸载。数据目录保持不变。");
        ValidateShortcuts(options, null); ValidateLegacyRun(options); EnsureNotRunning(options.InstallDir); ExistingTask task = ReadExistingTask();
        if (task != null && (!task.Recognized || !SamePath(Path.GetDirectoryName(task.Executable), options.InstallDir))) throw new Exception("启动入口属于其他程序/目录，请先核查入口归属。");
    }
    static void RemoveStartup(Options options)
    {
        ValidateUninstall(options); object service = null, root = null;
        try
        {
            if (ReadExistingTask() != null) { service = ConnectScheduler(); root = Invoke(service, "GetFolder", "\\"); Invoke(root, "DeleteTask", TaskName, 0); }
            string command = ReadLegacyRun();
            if (command.Length > 0 && LegacyRunOwned(command, options)) using (RegistryKey key = Registry.CurrentUser.OpenSubKey(RunKey, true)) key.DeleteValue(RunName, false);
        }
        finally { Release(root); Release(service); }
    }
    static object ConnectScheduler()
    {
        Type type = Type.GetTypeFromProgID("Schedule.Service"); if (type == null) throw new Exception("Windows 任务计划服务不可用。");
        object service = Activator.CreateInstance(type); try { Invoke(service, "Connect"); return service; } catch { Release(service); throw; }
    }
    static ExistingTask ReadExistingTask()
    {
        object service = null, root = null, task = null;
        try
        {
            service = ConnectScheduler(); root = Invoke(service, "GetFolder", "\\");
            try { task = Invoke(root, "GetTask", TaskName); } catch (Exception error) { if (IsMissingTaskError(error)) return null; throw; }
            return InspectTask(task);
        }
        finally { Release(task); Release(root); Release(service); }
    }
    static ExistingTask InspectTask(object task)
    {
        string path = TaskExecutable(task), args = TaskArguments(task), data; bool valid = RecognizeArguments(path, args, out data);
        object definition = null, principal = null;
        try
        {
            definition = Get(task, "Definition"); principal = Get(definition, "Principal"); string user = Convert.ToString(Get(principal, "UserId"));
            valid = valid && IsCurrentUser(user) &&
                Convert.ToInt32(Get(principal, "LogonType")) == 3 && Convert.ToInt32(Get(principal, "RunLevel")) == 0;
            return new ExistingTask { Executable = path, Arguments = args, DataDir = data, Recognized = valid, Enabled = Convert.ToBoolean(Get(task, "Enabled")), Xml = Convert.ToString(Get(task, "Xml")) };
        }
        finally { Release(principal); Release(definition); }
    }
    static bool IsCurrentUser(string user)
    {
        try
        {
            SecurityIdentifier sid = user.StartsWith("S-1-", StringComparison.OrdinalIgnoreCase) ? new SecurityIdentifier(user) :
                (SecurityIdentifier)new NTAccount(user).Translate(typeof(SecurityIdentifier));
            return sid.Equals(WindowsIdentity.GetCurrent().User);
        }
        catch { return false; }
    }
    static bool RecognizeArguments(string executable, string arguments, out string data)
    {
        data = ""; string fileName = Path.GetFileName(executable ?? "");
        if (!new[] { LauncherName, "简作.exe", "jianzuo-launcher.exe", "jianzuo.exe" }.Contains(fileName, StringComparer.OrdinalIgnoreCase)) return false;
        string[] args = SplitArguments(arguments); bool background = false;
        // Older launchers duplicated their exact executable as the first argument.
        // Accept only that exact path, not an arbitrary command prefix.
        if (args.Length > 0 && SamePath(args[0], executable)) args = args.Skip(1).ToArray();
        for (int i = 0; i < args.Length; i++)
        { if (args[i] == "--background" && !background) background = true; else if (args[i] == "--data" && data.Length == 0 && i + 1 < args.Length) data = args[++i]; else return false; }
        try { NormalizeDirectory(executable); data = NormalizeDirectory(data); return background; } catch { data = ""; return false; }
    }
    [DllImport("shell32.dll", SetLastError = true, CharSet = CharSet.Unicode)] static extern IntPtr CommandLineToArgvW(string command, out int count);
    [DllImport("kernel32.dll")] static extern IntPtr LocalFree(IntPtr memory);
    static string[] SplitArguments(string arguments)
    {
        int count; IntPtr block = CommandLineToArgvW("helper.exe " + (arguments ?? ""), out count); if (block == IntPtr.Zero) throw new Exception("无法解析原启动参数。");
        try { string[] values = new string[Math.Max(0, count - 1)]; for (int i = 1; i < count; i++) values[i - 1] = Marshal.PtrToStringUni(Marshal.ReadIntPtr(block, i * IntPtr.Size)); return values; } finally { LocalFree(block); }
    }
    static string TaskExecutable(object task) { return TaskActionValue(task, "Path"); }
    static string TaskArguments(object task) { return TaskActionValue(task, "Arguments"); }
    static string TaskActionValue(object task, string name)
    {
        object definition = null, actions = null, action = null;
        try { definition = Get(task, "Definition"); actions = Get(definition, "Actions"); if (Convert.ToInt32(Get(actions, "Count")) != 1) return "";
            action = GetIndexed(actions, "Item", 1); if (Convert.ToInt32(Get(action, "Type")) != 0) return ""; return Convert.ToString(Get(action, name)); }
        finally { Release(action); Release(actions); Release(definition); }
    }
    static bool IsMissingTaskError(Exception error) { return error.GetBaseException().HResult == unchecked((int)0x80070002); }
    static object BuildStartupTaskDefinition(object service, Options options, string launcher)
    {
        object definition = Invoke(service, "NewTask", 0), settings = null, principal = null, registration = null, triggers = null, trigger = null, actions = null, action = null;
        try
        {
            string identity = WindowsIdentity.GetCurrent().Name; settings = Get(definition, "Settings"); principal = Get(definition, "Principal"); registration = Get(definition, "RegistrationInfo");
            Set(registration, "Description", "Duo 独立任务工作台（当前用户登录后启动）");
            Set(settings, "StartWhenAvailable", true); Set(settings, "AllowDemandStart", true); Set(settings, "Hidden", true); Set(settings, "DisallowStartIfOnBatteries", false);
            Set(settings, "StopIfGoingOnBatteries", false); Set(settings, "ExecutionTimeLimit", "PT0S"); Set(settings, "RestartCount", 3); Set(settings, "RestartInterval", "PT1M"); Set(settings, "MultipleInstances", 2);
            Set(principal, "UserId", identity); Set(principal, "LogonType", 3); Set(principal, "RunLevel", 0);
            triggers = Get(definition, "Triggers"); trigger = Invoke(triggers, "Create", 9); Set(trigger, "UserId", identity); Set(trigger, "Enabled", true);
            actions = Get(definition, "Actions"); action = Invoke(actions, "Create", 0); Set(action, "Path", launcher); Set(action, "Arguments", "--data " + Quote(options.DataDir) + " --background"); Set(action, "WorkingDirectory", options.InstallDir);
            return definition;
        }
        catch { Release(definition); throw; }
        finally { Release(action); Release(actions); Release(trigger); Release(triggers); Release(registration); Release(principal); Release(settings); }
    }
    static string Quote(string value)
    { if (value.Contains("\"")) throw new Exception("参数不能包含引号。"); int trailing = value.Length - value.TrimEnd('\\').Length; return "\"" + value.Substring(0, value.Length - trailing) + new string('\\', trailing * 2) + "\""; }
    static string FormatInstallError(Exception error, string stage, string log)
    { Exception cause = error.GetBaseException(); return "失败步骤：" + stage + "；具体原因：" + cause.Message + "；错误代码：0x" + cause.HResult.ToString("X8") + "；诊断日志：" + log + "。请保留数据目录，不要删除后重装。"; }
    static string WriteError(Exception error, string stage)
    {
        foreach (string root in new[] { Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), TestNamespace.Length == 0 ? "Duo" : ProductKey), Path.GetTempPath() })
            try { Directory.CreateDirectory(root); string path = Path.Combine(root, "installer-error.log"); File.AppendAllText(path, DateTime.Now.ToString("O") + " stage=" + stage + Environment.NewLine + error + Environment.NewLine, new UTF8Encoding(false)); return path; } catch { }
        return "日志不可写，请保留截图";
    }
    static object Get(object target, string name) { return target.GetType().InvokeMember(name, BindingFlags.GetProperty, null, target, null); }
    // IActionCollection.Item is an indexed COM property, never InvokeMethod.
    static object GetIndexed(object target, string name, object index) { return target.GetType().InvokeMember(name, BindingFlags.GetProperty, null, target, new[] { index }); }
    static void Set(object target, string name, object value) { target.GetType().InvokeMember(name, BindingFlags.SetProperty, null, target, new[] { value }); }
    static object Invoke(object target, string name, params object[] args) { return target.GetType().InvokeMember(name, BindingFlags.InvokeMethod, null, target, args); }
    static void Release(object value) { if (value != null && Marshal.IsComObject(value)) try { Marshal.ReleaseComObject(value); } catch { } }
}
sealed class Options { public string InstallDir; public string DataDir; public string MigrateFrom = ""; public string DataMode = "keep"; public string PreviousData = ""; public string Validator = ""; public string OwnerPid = ""; public bool Interactive; public bool ConfirmData; public bool Update; public bool EnableStartup; public bool Desktop; public bool TestFailAfterTask; }
sealed class ExistingTask { public string Executable; public string Arguments; public string DataDir; public string Xml; public bool Recognized; public bool Enabled; }
