using ClipBridge.Windows.Logic;
using Microsoft.Win32;

namespace ClipBridge.Windows.Services;

/// <summary>Start-with-Windows via HKCU\...\Run (per user, no elevation), launching with <c>--minimized</c>.</summary>
internal static class AutoStartRegistry
{
    private static string? ExePath => Environment.ProcessPath;

    public static bool IsEnabled()
    {
        try
        {
            if (ExePath is null) return false;
            using var key = Registry.CurrentUser.OpenSubKey(AutoStartCommand.RunKeyPath);
            return AutoStartCommand.Matches(key?.GetValue(AutoStartCommand.ValueName) as string, ExePath);
        }
        catch (Exception ex) when (ex is System.Security.SecurityException or UnauthorizedAccessException or IOException)
        {
            return false;
        }
    }

    /// <summary>Returns false when the registry could not be written.</summary>
    public static bool Set(bool enabled)
    {
        try
        {
            using var key = Registry.CurrentUser.CreateSubKey(AutoStartCommand.RunKeyPath, writable: true);
            if (enabled)
            {
                if (ExePath is null) return false;
                key.SetValue(AutoStartCommand.ValueName, AutoStartCommand.Build(ExePath), RegistryValueKind.String);
            }
            else key.DeleteValue(AutoStartCommand.ValueName, throwOnMissingValue: false);
            return true;
        }
        catch (Exception ex) when (ex is System.Security.SecurityException or UnauthorizedAccessException or IOException)
        {
            AppLog.Error("autostart", ex);
            return false;
        }
    }
}
