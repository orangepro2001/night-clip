using System.Runtime.InteropServices;

namespace MoonPassword.Windows.Services;

/// <summary>
/// System message boxes for the moments when no Avalonia window can host a dialog: a fatal start-up error
/// (the core could not start) and the hidden update-applier process.
/// </summary>
internal static class NativeDialogs
{
    private const uint MB_OK = 0x0;
    private const uint MB_ICONERROR = 0x10;
    private const uint MB_ICONWARNING = 0x30;
    private const uint MB_SETFOREGROUND = 0x10000;
    private const uint MB_TOPMOST = 0x40000;

    [DllImport("user32.dll", CharSet = CharSet.Unicode)]
    private static extern int MessageBoxW(IntPtr hWnd, string text, string caption, uint type);

    public static void Error(string title, string text) =>
        MessageBoxW(IntPtr.Zero, text, title, MB_OK | MB_ICONERROR | MB_SETFOREGROUND | MB_TOPMOST);

    public static void Warning(string title, string text) =>
        MessageBoxW(IntPtr.Zero, text, title, MB_OK | MB_ICONWARNING | MB_SETFOREGROUND | MB_TOPMOST);
}
