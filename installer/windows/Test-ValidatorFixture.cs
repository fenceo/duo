// Test-only read-only-validator protocol fixture. Never packaged or executed by
// the installer. Compiled by Test-Installer.ps1 only inside its GUID directory.
using System;
using System.Diagnostics;
using System.IO;
using System.Threading;
static class ValidatorFixture
{
    static int Main(string[] args)
    {
        int index = Array.IndexOf(args, "--data");
        if (index < 0 || index + 1 >= args.Length) return 1;
        string data = args[index + 1];
        if (Path.GetFileName(data) == "validator-timeout")
        {
            File.WriteAllText(Path.Combine(data, "validator.pid"), Process.GetCurrentProcess().Id.ToString());
            Thread.Sleep(60000);
            return 1;
        }
        Console.WriteLine("{\"app\":\"jianzuo\",\"valid\":true,\"protocol\":2}");
        return 0;
    }
}
