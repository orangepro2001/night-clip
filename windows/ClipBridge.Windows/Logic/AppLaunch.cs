namespace ClipBridge.Windows.Logic;

/// <summary>Command-line options of a normal start (the update applier's flags are parsed by UpdateApplier).</summary>
public sealed record StartupOptions(bool Minimized)
{
    public const string MinimizedFlag = "--minimized";

    public static StartupOptions Parse(string[] args) =>
        new(args.Any(a => string.Equals(a, MinimizedFlag, StringComparison.OrdinalIgnoreCase)));
}

/// <summary>The value written to <c>HKCU\Software\Microsoft\Windows\CurrentVersion\Run</c>.</summary>
public static class AutoStartCommand
{
    public const string RunKeyPath = @"Software\Microsoft\Windows\CurrentVersion\Run";
    public const string ValueName = "ClipBridge";

    public static string Build(string exePath) => $"\"{exePath}\" {StartupOptions.MinimizedFlag}";

    /// <summary>True when the stored Run value starts this very executable (quotes and case ignored).</summary>
    public static bool Matches(string? stored, string exePath)
    {
        if (string.IsNullOrWhiteSpace(stored)) return false;
        var s = stored.Trim();
        string exe;
        if (s.StartsWith('"'))
        {
            var end = s.IndexOf('"', 1);
            exe = end < 0 ? s[1..] : s[1..end];
        }
        else exe = s.Split(' ')[0];
        return string.Equals(exe, exePath, StringComparison.OrdinalIgnoreCase);
    }
}
