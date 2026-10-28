using Avalonia.Controls;

namespace ClipBridge.Mac.Platform;

/// <summary>
/// Keeps a window out of screenshots and screen recordings (the counterpart of the Windows shell's
/// SetWindowDisplayAffinity): NSWindow.sharingType = NSWindowSharingNone (0), back to ReadOnly (1).
/// Best effort; it never throws.
/// </summary>
internal static class MacWindowPrivacy
{
    private const long SharingNone = 0;
    private const long SharingReadOnly = 1;

    public static void Apply(Window window, bool exclude)
    {
        if (!OperatingSystem.IsMacOS()) return;
        try
        {
            if (window.TryGetPlatformHandle() is not { } handle || handle.Handle == IntPtr.Zero) return;
            var ns = handle.Handle;
            switch (handle.HandleDescriptor)
            {
                case "NSWindow":
                    break;
                case "NSView":
                    ns = ObjC.Send(ns, ObjC.Sel("window"));
                    break;
                default:
                    MacLog.Write("window sharing: unexpected handle " + handle.HandleDescriptor);
                    return;
            }
            if (ns == IntPtr.Zero) return;
            ObjC.Pool(() => ObjC.SendLongArg(ns, ObjC.Sel("setSharingType:"), exclude ? SharingNone : SharingReadOnly));
        }
        catch (Exception ex)
        {
            MacLog.Error("window sharing type", ex);
        }
    }
}
