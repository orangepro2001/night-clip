using System.Windows;
using System.Windows.Interop;

namespace ClipBridge.Windows.Services;

/// <summary>
/// Keeps a window out of screenshots and screen recordings (docs/vault-api.md §4 rule 2) with
/// SetWindowDisplayAffinity. WDA_EXCLUDEFROMCAPTURE needs Windows 10 2004; on older systems the
/// call fails and WDA_MONITOR (the window shows black in captures) is tried instead.
/// </summary>
internal static class DisplayAffinity
{
    private static bool _loggedFailure;

    /// <summary>Sets or clears the exclusion. Returns false when the system refused both modes (only logged once).</summary>
    public static bool Set(Window window, bool exclude)
    {
        var handle = new WindowInteropHelper(window).EnsureHandle();
        if (handle == IntPtr.Zero) return false;
        if (!exclude) return NativeMethods.SetWindowDisplayAffinity(handle, NativeMethods.WDA_NONE);
        if (NativeMethods.SetWindowDisplayAffinity(handle, NativeMethods.WDA_EXCLUDEFROMCAPTURE)) return true;
        if (NativeMethods.SetWindowDisplayAffinity(handle, NativeMethods.WDA_MONITOR)) return true;
        if (!_loggedFailure)
        {
            _loggedFailure = true;
            AppLog.Write("SetWindowDisplayAffinity failed, win32 error " + System.Runtime.InteropServices.Marshal.GetLastWin32Error());
        }
        return false;
    }
}
