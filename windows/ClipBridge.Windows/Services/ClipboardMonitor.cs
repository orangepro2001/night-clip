using System.Windows.Interop;
using System.Windows.Threading;

namespace ClipBridge.Windows.Services;

/// <summary>
/// A hidden message-only window registered with AddClipboardFormatListener. WM_CLIPBOARDUPDATE is
/// debounced (about 50 ms: apps often update the clipboard several times in a row) and then reported
/// via <see cref="Changed"/> on the UI thread. The window handle doubles as the clipboard owner for writes.
/// </summary>
internal sealed class ClipboardMonitor : IDisposable
{
    private const int DebounceMs = 50;

    private HwndSource? _source;
    private bool _listening;
    private readonly DispatcherTimer _debounce;

    /// <summary>Raised on the UI thread once the clipboard has been quiet for the debounce interval.</summary>
    public event Action? Changed;

    public ClipboardMonitor(Dispatcher dispatcher)
    {
        _debounce = new DispatcherTimer(DispatcherPriority.Normal, dispatcher)
        {
            Interval = TimeSpan.FromMilliseconds(DebounceMs),
        };
        _debounce.Tick += (_, _) =>
        {
            _debounce.Stop();
            try { Changed?.Invoke(); }
            catch (Exception ex) { AppLog.Error("clipboard changed handler", ex); }
        };
    }

    /// <summary>Handle of the hidden window, or zero before <see cref="Start"/>.</summary>
    public IntPtr Handle => _source?.Handle ?? IntPtr.Zero;

    public bool Start()
    {
        if (_source is not null) return _listening;
        var p = new HwndSourceParameters("ClipBridgeClipboardListener")
        {
            ParentWindow = NativeMethods.HWND_MESSAGE, // message-only: invisible, no taskbar entry
            WindowStyle = 0,
        };
        _source = new HwndSource(p);
        _source.AddHook(WndProc);
        _listening = NativeMethods.AddClipboardFormatListener(_source.Handle);
        if (!_listening) AppLog.Write("AddClipboardFormatListener failed, win32 error " + System.Runtime.InteropServices.Marshal.GetLastWin32Error());
        return _listening;
    }

    private IntPtr WndProc(IntPtr hwnd, int msg, IntPtr wParam, IntPtr lParam, ref bool handled)
    {
        if (msg == NativeMethods.WM_CLIPBOARDUPDATE)
        {
            _debounce.Stop();
            _debounce.Start();
            handled = true;
        }
        return IntPtr.Zero;
    }

    public void Dispose()
    {
        _debounce.Stop();
        if (_source is null) return;
        try
        {
            if (_listening) NativeMethods.RemoveClipboardFormatListener(_source.Handle);
        }
        finally
        {
            _listening = false;
            _source.RemoveHook(WndProc);
            _source.Dispose();
            _source = null;
        }
    }
}
