using System.Runtime.InteropServices;

namespace ClipBridge.Windows.Services;

internal enum ClipReadStatus
{
    Ok,
    /// <summary>The clipboard holds no Unicode text (an image, files, ...).</summary>
    NoText,
    /// <summary>The source asked not to be monitored (password managers).</summary>
    Excluded,
    /// <summary>Another program kept the clipboard open for all retries.</summary>
    Busy,
    /// <summary>More text than we are willing to even look at.</summary>
    TooBig,
    Empty,
}

internal readonly record struct ClipReadResult(ClipReadStatus Status, string Text);

/// <summary>
/// Raw clipboard text access (docs/shell-guide.md §3 items 4 and 5). Every OpenClipboard is paired
/// with CloseClipboard in a finally block, every GlobalLock with GlobalUnlock, and memory that the
/// clipboard did not take over is freed.
/// </summary>
internal sealed class ClipboardAccess
{
    private const int OpenAttempts = 3;
    private const int OpenRetryDelayMs = 30;
    /// <summary>Clipboard text beyond this many bytes is not even decoded.</summary>
    private const long MaxReadBytes = 16L * 1024 * 1024;

    private readonly uint _excludeFormat = NativeMethods.RegisterClipboardFormat("ExcludeClipboardContentFromMonitorProcessing");
    private readonly uint _historyFormat = NativeMethods.RegisterClipboardFormat("CanIncludeInClipboardHistory");
    private readonly uint _cloudFormat = NativeMethods.RegisterClipboardFormat("CanUploadToCloudClipboard");

    /// <summary>Safe from any thread. 0 means the system could not tell (no window station).</summary>
    public static uint SequenceNumber() => NativeMethods.GetClipboardSequenceNumber();

    private static bool TryOpen(IntPtr owner)
    {
        for (var i = 0; i < OpenAttempts; i++)
        {
            if (NativeMethods.OpenClipboard(owner)) return true;
            if (i < OpenAttempts - 1) Thread.Sleep(OpenRetryDelayMs);
        }
        return false;
    }

    /// <summary>Reads the Unicode text. Safe from any thread (opens the clipboard without an owner).</summary>
    public ClipReadResult ReadText()
    {
        if (_excludeFormat != 0 && NativeMethods.IsClipboardFormatAvailable(_excludeFormat))
            return new ClipReadResult(ClipReadStatus.Excluded, "");
        if (!NativeMethods.IsClipboardFormatAvailable(NativeMethods.CF_UNICODETEXT))
            return new ClipReadResult(ClipReadStatus.NoText, "");
        if (!TryOpen(IntPtr.Zero)) return new ClipReadResult(ClipReadStatus.Busy, "");
        try
        {
            var handle = NativeMethods.GetClipboardData(NativeMethods.CF_UNICODETEXT);
            if (handle == IntPtr.Zero) return new ClipReadResult(ClipReadStatus.NoText, "");
            var size = (long)NativeMethods.GlobalSize(handle);
            if (size < 2) return new ClipReadResult(ClipReadStatus.Empty, "");
            if (size > MaxReadBytes) return new ClipReadResult(ClipReadStatus.TooBig, "");
            var ptr = NativeMethods.GlobalLock(handle);
            if (ptr == IntPtr.Zero) return new ClipReadResult(ClipReadStatus.Busy, "");
            try
            {
                // Bound the read by the allocation size (the data should be NUL terminated, but do not rely on it).
                var text = Marshal.PtrToStringUni(ptr, (int)(size / 2)) ?? "";
                var nul = text.IndexOf('\0');
                if (nul >= 0) text = text[..nul];
                return text.Length == 0
                    ? new ClipReadResult(ClipReadStatus.Empty, "")
                    : new ClipReadResult(ClipReadStatus.Ok, text);
            }
            finally { NativeMethods.GlobalUnlock(handle); }
        }
        finally { NativeMethods.CloseClipboard(); }
    }

    /// <summary>
    /// Replaces the clipboard with <paramref name="clipboardText"/> (already "\r\n"-converted when needed).
    /// <paramref name="owner"/> must be a real window handle: with a NULL owner SetClipboardData is
    /// documented to fail after EmptyClipboard. Call on the thread that owns that window.
    /// <paramref name="sensitive"/>: a secret from the vault. The three formats that keep it out of clipboard
    /// monitors, Windows clipboard history and the cloud clipboard are set in the same clipboard session; if any of
    /// them cannot be set the clipboard is emptied again and the write fails (a secret never stays unprotected).
    /// </summary>
    public bool WriteText(IntPtr owner, string clipboardText, bool sensitive = false)
    {
        if (sensitive && (_excludeFormat == 0 || _historyFormat == 0 || _cloudFormat == 0)) return false;

        var data = new char[clipboardText.Length + 1];
        clipboardText.CopyTo(0, data, 0, clipboardText.Length); // data[^1] stays '\0'
        var bytes = (UIntPtr)(uint)(data.Length * sizeof(char));

        var hMem = NativeMethods.GlobalAlloc(NativeMethods.GMEM_MOVEABLE, bytes);
        if (hMem == IntPtr.Zero) return false;
        var handedOver = false;
        try
        {
            var ptr = NativeMethods.GlobalLock(hMem);
            if (ptr == IntPtr.Zero) return false;
            try { Marshal.Copy(data, 0, ptr, data.Length); }
            finally
            {
                NativeMethods.GlobalUnlock(hMem);
                Array.Clear(data); // the managed copy of the secret does not linger
            }

            if (!TryOpen(owner)) return false;
            try
            {
                if (!NativeMethods.EmptyClipboard()) return false;
                if (NativeMethods.SetClipboardData(NativeMethods.CF_UNICODETEXT, hMem) == IntPtr.Zero) return false;
                handedOver = true; // the system owns hMem now; it must not be freed by us
                if (sensitive)
                {
                    var flagsSet = SetDword(_excludeFormat, 1)      // ExcludeClipboardContentFromMonitorProcessing: any data, presence is what counts
                        && SetDword(_historyFormat, 0)              // CanIncludeInClipboardHistory = 0
                        && SetDword(_cloudFormat, 0);               // CanUploadToCloudClipboard = 0
                    if (!flagsSet)
                    {
                        NativeMethods.EmptyClipboard();
                        return false;
                    }
                }
                return true;
            }
            finally { NativeMethods.CloseClipboard(); }
        }
        finally
        {
            if (!handedOver) NativeMethods.GlobalFree(hMem);
        }
    }

    /// <summary>Puts a DWORD on the open clipboard under a registered format. Frees the memory when the system does not take it.</summary>
    private static bool SetDword(uint format, uint value)
    {
        var h = NativeMethods.GlobalAlloc(NativeMethods.GMEM_MOVEABLE, (UIntPtr)4u);
        if (h == IntPtr.Zero) return false;
        var ptr = NativeMethods.GlobalLock(h);
        if (ptr == IntPtr.Zero)
        {
            NativeMethods.GlobalFree(h);
            return false;
        }
        Marshal.WriteInt32(ptr, unchecked((int)value));
        NativeMethods.GlobalUnlock(h);
        if (NativeMethods.SetClipboardData(format, h) != IntPtr.Zero) return true;
        NativeMethods.GlobalFree(h);
        return false;
    }

    /// <summary>Empties the clipboard. <paramref name="owner"/> is the clipboard window; call on its thread.</summary>
    public bool Clear(IntPtr owner)
    {
        if (!TryOpen(owner)) return false;
        try { return NativeMethods.EmptyClipboard(); }
        finally { NativeMethods.CloseClipboard(); }
    }
}
