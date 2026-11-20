using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Text;
using ClipBridge.Windows.Logic;
using Microsoft.Win32;

namespace ClipBridge.Windows.Services;

/// <summary>
/// Reads the machine's SSH setup for the environment checks (SshEnv decides what it means): Git's
/// core.sshCommand, whether Windows' OpenSSH client is installed, the state of the "OpenSSH Authentication
/// Agent" service, and SSH_AUTH_SOCK. Read-only, except for the explicit, user-confirmed actions below.
/// </summary>
internal static class SshEnvProbe
{
    private static string WindowsSsh => Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.Windows), "System32", "OpenSSH", "ssh.exe");

    public static async Task<SshEnvFacts> ProbeAsync()
    {
        var gitTask = RunAsync("git", "config", "--global", "--get", "core.sshCommand");
        var sshTask = File.Exists(WindowsSsh) ? RunAsync(WindowsSsh, "-V") : Task.FromResult((-1, "", ""));
        var service = await Task.Run(ServiceState);
        var (gitCode, gitOut, _) = await gitTask;
        var (_, sshOut, sshErr) = await sshTask;
        var gitFound = gitCode is 0 or 1; // 1 = the key is not set; -1 = git could not be started
        string? userSock = null;
        try { userSock = Registry.CurrentUser.OpenSubKey("Environment")?.GetValue("SSH_AUTH_SOCK") as string; }
        catch (Exception ex) when (ex is System.Security.SecurityException or UnauthorizedAccessException or IOException) { }
        return new SshEnvFacts(
            gitFound, gitCode == 0 && gitOut.Trim().Length > 0 ? gitOut.Trim() : null,
            File.Exists(WindowsSsh), (sshErr.Length > 0 ? sshErr : sshOut).Trim(),
            service, NullIfEmpty(Environment.GetEnvironmentVariable("SSH_AUTH_SOCK")), NullIfEmpty(userSock));
    }

    private static string? NullIfEmpty(string? s) => string.IsNullOrWhiteSpace(s) ? null : s;

    /// <summary>Runs a program without a window; returns (exit code, stdout, stderr), exit code -1 when it could not run.</summary>
    public static async Task<(int Code, string Out, string Err)> RunAsync(string file, params string[] args)
    {
        var psi = new ProcessStartInfo(file)
        {
            UseShellExecute = false, CreateNoWindow = true, RedirectStandardOutput = true, RedirectStandardError = true,
            StandardOutputEncoding = Encoding.UTF8, StandardErrorEncoding = Encoding.UTF8,
        };
        foreach (var a in args) psi.ArgumentList.Add(a);
        try
        {
            using var p = Process.Start(psi);
            if (p is null) return (-1, "", "");
            var outTask = p.StandardOutput.ReadToEndAsync();
            var errTask = p.StandardError.ReadToEndAsync();
            using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(10));
            try { await p.WaitForExitAsync(cts.Token); }
            catch (OperationCanceledException)
            {
                try { p.Kill(true); } catch (InvalidOperationException) { }
                return (-1, "", "timeout");
            }
            return (p.ExitCode, await outTask, await errTask);
        }
        catch (Win32Exception) { return (-1, "", ""); } // not installed / not on PATH
    }

    /// <summary>"running", "stopped", "disabled", "missing" or "unknown" for the ssh-agent service.</summary>
    private static string ServiceState()
    {
        IntPtr scm = OpenSCManagerW(null, null, ScManagerConnect);
        if (scm == IntPtr.Zero) return "unknown";
        try
        {
            IntPtr svc = OpenServiceW(scm, "ssh-agent", ServiceQueryStatus | ServiceQueryConfig);
            if (svc == IntPtr.Zero) return Marshal.GetLastWin32Error() == ErrorServiceDoesNotExist ? "missing" : "unknown";
            try
            {
                var buf = new byte[36];
                if (QueryServiceStatusEx(svc, 0, buf, buf.Length, out _) && BitConverter.ToInt32(buf, 4) == ServiceRunning) return "running";
                QueryServiceConfigW(svc, IntPtr.Zero, 0, out var needed);
                if (needed > 0)
                {
                    var mem = Marshal.AllocHGlobal(needed);
                    try
                    {
                        if (QueryServiceConfigW(svc, mem, needed, out _) && Marshal.ReadInt32(mem, 4) == ServiceDisabled) return "disabled";
                    }
                    finally { Marshal.FreeHGlobal(mem); }
                }
                return "stopped";
            }
            finally { CloseServiceHandle(svc); }
        }
        finally { CloseServiceHandle(scm); }
    }

    /// <summary>sc stop + sc config start= disabled, elevated through UAC. False when the user declined or it failed.</summary>
    public static bool DisableServiceElevated()
    {
        var psi = new ProcessStartInfo("cmd.exe", "/c sc.exe stop ssh-agent & sc.exe config ssh-agent start= disabled")
        {
            UseShellExecute = true, Verb = "runas", WindowStyle = ProcessWindowStyle.Hidden,
        };
        try
        {
            using var p = Process.Start(psi);
            if (p is null) return false;
            p.WaitForExit(30_000);
            return ServiceState() is "disabled" or "missing";
        }
        catch (Win32Exception) { return false; } // the UAC prompt was declined
    }

    private const uint ScManagerConnect = 0x0001;
    private const uint ServiceQueryStatus = 0x0004;
    private const uint ServiceQueryConfig = 0x0001;
    private const int ServiceRunning = 4;
    private const int ServiceDisabled = 4;
    private const int ErrorServiceDoesNotExist = 1060;

    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr OpenSCManagerW(string? machine, string? database, uint access);

    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr OpenServiceW(IntPtr scm, string name, uint access);

    [DllImport("advapi32.dll", SetLastError = true)]
    private static extern bool QueryServiceStatusEx(IntPtr svc, int infoLevel, byte[] buffer, int size, out int needed);

    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern bool QueryServiceConfigW(IntPtr svc, IntPtr config, int size, out int needed);

    [DllImport("advapi32.dll", SetLastError = true)]
    private static extern bool CloseServiceHandle(IntPtr handle);
}

/// <summary>Moves files to the Recycle Bin (they can be restored from there).</summary>
internal static class RecycleBin
{
    public static bool Move(string path)
    {
        var op = new ShFileOpStruct
        {
            wFunc = FoDelete,
            pFrom = path + "\0\0",
            fFlags = FofAllowUndo | FofNoConfirmation | FofSilent | FofNoErrorUi,
        };
        return SHFileOperationW(ref op) == 0 && !op.fAnyOperationsAborted && !File.Exists(path);
    }

    private const uint FoDelete = 0x0003;
    private const ushort FofSilent = 0x0004;
    private const ushort FofNoConfirmation = 0x0010;
    private const ushort FofAllowUndo = 0x0040;
    private const ushort FofNoErrorUi = 0x0400;

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct ShFileOpStruct
    {
        public IntPtr hwnd;
        public uint wFunc;
        public string pFrom;
        public string? pTo;
        public ushort fFlags;
        [MarshalAs(UnmanagedType.Bool)] public bool fAnyOperationsAborted;
        public IntPtr hNameMappings;
        public string? lpszProgressTitle;
    }

    [DllImport("shell32.dll", CharSet = CharSet.Unicode)]
    private static extern int SHFileOperationW(ref ShFileOpStruct op);
}
