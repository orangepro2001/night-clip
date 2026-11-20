using System.Runtime.InteropServices;
using Avalonia.Threading;

namespace ClipBridge.Windows.Services;

/// <summary>
/// A hidden message-only window registered with AddClipboardFormatListener. WM_CLIPBOARDUPDATE is
/// debounced (about 50 ms: apps often update the clipboard several times in a row) and then reported
/// via <see cref="Changed"/> on the UI thread. The window handle doubles as the clipboard owner for writes.
/// The window is created on the UI thread, whose Win32 message loop Avalonia runs, so its window procedure
/// is called there too.
/// </summary>
internal sealed class ClipboardMonitor : IDisposable
{
    private const int DebounceMs = 50;
    private const string ClassName = "ClipBridgeClipboardListener";
    private const int ERROR_CLASS_ALREADY_EXISTS = 1410;

    private readonly DispatcherTimer _debounce;
    private readonly WndProcDelegate _wndProc; // kept alive for as long as the window exists
    private IntPtr _hwnd;
    private bool _classRegistered;
    private bool _listening;

    /// <summary>Raised on the UI thread once the clipboard has been quiet for the debounce interval.</summary>
    public event Action? Changed;

    public ClipboardMonitor()
    {
        _wndProc = WndProc;
        _debounce = new DispatcherTimer(DispatcherPriority.Normal) { Interval = TimeSpan.FromMilliseconds(DebounceMs) };
        _debounce.Tick += (_, _) =>
        {
            _debounce.Stop();
            try { Changed?.Invoke(); }
            catch (Exception ex) { AppLog.Error("clipboard changed handler", ex); }
        };
    }

    /// <summary>Handle of the hidden window, or zero before <see cref="Start"/>.</summary>
    public IntPtr Handle => _hwnd;

    public bool Start()
    {
        if (_hwnd != IntPtr.Zero) return _listening;
        var instance = GetModuleHandleW(null);
        var wc = new WNDCLASSEXW
        {
            cbSize = (uint)Marshal.SizeOf<WNDCLASSEXW>(),
            lpfnWndProc = Marshal.GetFunctionPointerForDelegate(_wndProc),
            hInstance = instance,
            lpszClassName = ClassName,
        };
        if (RegisterClassExW(ref wc) != 0) _classRegistered = true;
        else if (Marshal.GetLastWin32Error() != ERROR_CLASS_ALREADY_EXISTS)
        {
            AppLog.Write("RegisterClassEx failed, win32 error " + Marshal.GetLastWin32Error());
            return false;
        }

        // Message-only (HWND_MESSAGE parent): invisible, no taskbar entry, never enumerated as a top-level window.
        _hwnd = CreateWindowExW(0, ClassName, "", 0, 0, 0, 0, 0, NativeMethods.HWND_MESSAGE, IntPtr.Zero, instance, IntPtr.Zero);
        if (_hwnd == IntPtr.Zero)
        {
            AppLog.Write("CreateWindowEx failed, win32 error " + Marshal.GetLastWin32Error());
            return false;
        }
        _listening = NativeMethods.AddClipboardFormatListener(_hwnd);
        if (!_listening) AppLog.Write("AddClipboardFormatListener failed, win32 error " + Marshal.GetLastWin32Error());
        return _listening;
    }

    private IntPtr WndProc(IntPtr hwnd, uint msg, IntPtr wParam, IntPtr lParam)
    {
        if (msg == NativeMethods.WM_CLIPBOARDUPDATE)
        {
            _debounce.Stop();
            _debounce.Start();
            return IntPtr.Zero;
        }
        return DefWindowProcW(hwnd, msg, wParam, lParam);
    }

    public void Dispose()
    {
        _debounce.Stop();
        if (_hwnd == IntPtr.Zero) return;
        try
        {
            if (_listening) NativeMethods.RemoveClipboardFormatListener(_hwnd);
        }
        finally
        {
            _listening = false;
            DestroyWindow(_hwnd);
            _hwnd = IntPtr.Zero;
            if (_classRegistered) UnregisterClassW(ClassName, GetModuleHandleW(null));
            _classRegistered = false;
        }
    }

    // ---- Win32 --------------------------------------------------------------------------

    [UnmanagedFunctionPointer(CallingConvention.Winapi)]
    private delegate IntPtr WndProcDelegate(IntPtr hwnd, uint msg, IntPtr wParam, IntPtr lParam);

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct WNDCLASSEXW
    {
        public uint cbSize;
        public uint style;
        public IntPtr lpfnWndProc;
        public int cbClsExtra;
        public int cbWndExtra;
        public IntPtr hInstance;
        public IntPtr hIcon;
        public IntPtr hCursor;
        public IntPtr hbrBackground;
        public string? lpszMenuName;
        public string lpszClassName;
        public IntPtr hIconSm;
    }

    [DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    private static extern ushort RegisterClassExW(ref WNDCLASSEXW wc);

    [DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    private static extern bool UnregisterClassW(string className, IntPtr instance);

    [DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    private static extern IntPtr CreateWindowExW(uint exStyle, string className, string windowName, uint style,
        int x, int y, int width, int height, IntPtr parent, IntPtr menu, IntPtr instance, IntPtr param);

    [DllImport("user32.dll", SetLastError = true)]
    private static extern bool DestroyWindow(IntPtr hwnd);

    [DllImport("user32.dll")]
    private static extern IntPtr DefWindowProcW(IntPtr hwnd, uint msg, IntPtr wParam, IntPtr lParam);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode)]
    private static extern IntPtr GetModuleHandleW(string? moduleName);
}
