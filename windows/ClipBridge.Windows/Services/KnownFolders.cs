using System.Runtime.InteropServices;

namespace ClipBridge.Windows.Services;

internal static class KnownFolders
{
    private static readonly Guid Downloads = new("374DE290-123F-4565-9164-39C4925E467B");

    /// <summary>The user's real Downloads folder (it can be redirected), falling back to %USERPROFILE%\Downloads.</summary>
    public static string DownloadsFolder()
    {
        var ptr = IntPtr.Zero;
        try
        {
            if (NativeMethods.SHGetKnownFolderPath(Downloads, 0, IntPtr.Zero, out ptr) == 0 && ptr != IntPtr.Zero)
            {
                var path = Marshal.PtrToStringUni(ptr);
                if (!string.IsNullOrEmpty(path)) return path;
            }
        }
        catch (Exception ex) when (ex is DllNotFoundException or EntryPointNotFoundException) { }
        finally
        {
            if (ptr != IntPtr.Zero) Marshal.FreeCoTaskMem(ptr);
        }
        return Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), "Downloads");
    }

    /// <summary>Default place for received files: Downloads\ClipBridge.</summary>
    public static string DefaultReceiveDir() => Path.Combine(DownloadsFolder(), "ClipBridge");
}
