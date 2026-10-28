using System.Diagnostics;
using System.Security.Cryptography;
using ClipBridge.Windows.Logic;

namespace ClipBridge.Mac.Platform;

/// <summary>Per-user folders (the macOS counterparts of %LOCALAPPDATA%\ClipBridge).</summary>
public static class MacPaths
{
    public static string Home => Environment.GetEnvironmentVariable("HOME") is { Length: > 0 } h ? h : "/tmp";
    public static string DataDir => Path.Combine(Home, "Library", "Application Support", "ClipBridge");
    public static string LogDir => Path.Combine(Home, "Library", "Logs", "ClipBridge");
    public static string StagingDir => Path.Combine(Home, "Library", "Caches", "ClipBridge", "staging");
    public static string DefaultReceiveDir => Path.Combine(Home, "Downloads", "ClipBridge");

    /// <summary>The running .app bundle, or null when started outside one (a dev build).</summary>
    public static string? BundlePath
    {
        get
        {
            var dir = new DirectoryInfo(AppContext.BaseDirectory.TrimEnd('/'));   // …/X.app/Contents/MacOS
            var bundle = dir.Parent?.Parent;
            return bundle is { Extension: ".app" } && dir.Name == "MacOS" ? bundle.FullName : null;
        }
    }
}

/// <summary>Log file: event types, ids and error codes only, never clipboard text (same policy as Windows).</summary>
public static class MacLog
{
    private static LogFile? _file;

    public static void Init() => _file ??= new LogFile(Path.Combine(MacPaths.LogDir, "clipbridge.log"));

    public static void Write(string line) => _file?.Write(line);

    public static void Error(string where, Exception ex) => Write($"error in {where}: {ex.GetType().Name}: {ex.Message}");

    public static void Event(CoreEvent ev)
    {
        if (ev is FileProgressEvent) return;
        Write("event " + ev.LogLine);
    }
}

public sealed class KeyStoreException(string message) : Exception(message);

/// <summary>
/// The 32-byte database key, kept in the login keychain (generic password "ClipBridge"/"db-key").
/// It goes through /usr/bin/security, so the item's access list names Apple's tool rather than this
/// ad-hoc signed app: an update (new signature) can still read it without a keychain prompt.
/// A missing key next to an existing database is an error, never a reason to make a new key.
/// </summary>
public static class MacKeyStore
{
    private const string Service = "ClipBridge";
    private const string Account = "db-key";

    public static string LoadOrCreate(string dataDir)
    {
        var (code, output) = Run("find-generic-password", "-s", Service, "-a", Account, "-w");
        if (code == 0)
        {
            var key = output.Trim();
            if (IsValid(key)) return key;
            throw new KeyStoreException("the keychain item ClipBridge/db-key is not a 32-byte base64 key");
        }
        if (File.Exists(Path.Combine(dataDir, "clipbridge.db")))
            throw new KeyStoreException("the database exists but its key is missing from the login keychain (item ClipBridge/db-key)");

        var created = Convert.ToBase64String(RandomNumberGenerator.GetBytes(32));
        var (addCode, addOut) = Run("add-generic-password", "-U", "-s", Service, "-a", Account, "-l", "ClipBridge database key", "-w", created);
        if (addCode != 0) throw new KeyStoreException("cannot store the key in the login keychain: " + addOut.Trim());
        return created;
    }

    private static bool IsValid(string b64)
    {
        try { return Convert.FromBase64String(b64).Length == 32; }
        catch (FormatException) { return false; }
    }

    private static (int Code, string Output) Run(params string[] args)
    {
        var psi = new ProcessStartInfo("/usr/bin/security") { RedirectStandardOutput = true, RedirectStandardError = true, UseShellExecute = false };
        foreach (var a in args) psi.ArgumentList.Add(a);
        using var p = Process.Start(psi) ?? throw new KeyStoreException("cannot run /usr/bin/security");
        var output = p.StandardOutput.ReadToEnd() + p.StandardError.ReadToEnd();
        p.WaitForExit();
        return (p.ExitCode, output);
    }
}

/// <summary>Start at login through a per-user LaunchAgent that opens the bundle minimized.</summary>
public static class MacAutoStart
{
    private const string Label = "com.nightclip.clipbridge";
    private static string PlistPath => Path.Combine(MacPaths.Home, "Library", "LaunchAgents", Label + ".plist");

    public static bool IsEnabled() => File.Exists(PlistPath);

    public static bool Set(bool on)
    {
        try
        {
            if (!on)
            {
                if (File.Exists(PlistPath)) File.Delete(PlistPath);
                return true;
            }
            if (MacPaths.BundlePath is not { } bundle) return false; // a dev build has nothing stable to start
            Directory.CreateDirectory(Path.GetDirectoryName(PlistPath)!);
            File.WriteAllText(PlistPath, $"""
                <?xml version="1.0" encoding="UTF-8"?>
                <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
                <plist version="1.0">
                <dict>
                  <key>Label</key><string>{Label}</string>
                  <key>ProgramArguments</key>
                  <array><string>/usr/bin/open</string><string>-g</string><string>-a</string><string>{System.Security.SecurityElement.Escape(bundle)}</string><string>--args</string><string>--minimized</string></array>
                  <key>RunAtLoad</key><true/>
                </dict>
                </plist>
                """);
            return true;
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            MacLog.Error("autostart", ex);
            return false;
        }
    }
}

/// <summary>Finder and default-application actions through /usr/bin/open.</summary>
public static class MacOpen
{
    public static string? File(string path) => Run(path);

    public static string? Reveal(string path) => Run("-R", path);

    public static string? Folder(string dir)
    {
        try { Directory.CreateDirectory(dir); }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { return ex.Message; }
        return Run(dir);
    }

    private static string? Run(params string[] args)
    {
        try
        {
            var psi = new ProcessStartInfo("/usr/bin/open") { UseShellExecute = false };
            foreach (var a in args) psi.ArgumentList.Add(a);
            using var p = Process.Start(psi);
            return null;
        }
        catch (Exception ex) when (ex is System.ComponentModel.Win32Exception or InvalidOperationException)
        {
            MacLog.Error("open", ex);
            return ex.Message;
        }
    }
}

/// <summary>
/// Notification Center banners through NSUserNotificationCenter: deprecated but still delivered for
/// bundled apps, and it needs neither blocks nor an entitlement. Clicking a banner activates the app
/// (handled as a reopen: the main window comes up). Best effort: failures only go to the log.
/// </summary>
public static class MacNotifier
{
    public static void Show(string title, string text)
    {
        if (MacPaths.BundlePath is null) { MacLog.Write("notify (no bundle): " + title); return; }
        try
        {
            ObjC.Pool(() =>
            {
                var center = ObjC.Send(ObjC.Class("NSUserNotificationCenter"), ObjC.Sel("defaultUserNotificationCenter"));
                if (center == IntPtr.Zero) return 0;
                var n = ObjC.Send(ObjC.Send(ObjC.Class("NSUserNotification"), ObjC.Sel("alloc")), ObjC.Sel("init"));
                ObjC.Send(n, ObjC.Sel("setTitle:"), ObjC.NSString(title));
                ObjC.Send(n, ObjC.Sel("setInformativeText:"), ObjC.NSString(text));
                ObjC.Send(center, ObjC.Sel("deliverNotification:"), n);
                ObjC.Send(n, ObjC.Sel("release"));
                return 0;
            });
        }
        catch (Exception ex) { MacLog.Error("notify", ex); }
    }
}
