using System.Runtime.InteropServices;

namespace ClipBridge.Windows.Services;

/// <summary>
/// A hidden message-only window that owns the clipboard while the app writes a copied secret (SetClipboardData
/// needs an owner window, and the main window may not exist while the app sits in the tray). It is created on the
/// UI thread, whose Win32 message loop Avalonia runs, so its window procedure runs there too. It does not listen
/// to clipboard changes: the app never reads the clipboard.
/// </summary>
internal sealed class ClipboardOwnerWindow : IDisposable
{
    private const string ClassName = "MoonPasswordClipboardOwner";
    private const int ERROR_CLASS_ALREADY_EXISTS = 1410;

    private readonly WndProcDelegate _wndProc; // kept alive for as long as the window exists
    private IntPtr _hwnd;
    private bool _classRegistered;

    public ClipboardOwnerWindow() => _wndProc = WndProc;

    /// <summary>Handle of the hidden window, created on first use; zero when it could not be created.</summary>
    public IntPtr Handle
    {
        get
        {
            if (_hwnd == IntPtr.Zero) Create();
            return _hwnd;
        }
    }

    private void Create()
    {
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
            return;
        }

        // Message-only (HWND_MESSAGE parent): invisible, no taskbar entry, never enumerated as a top-level window.
        _hwnd = CreateWindowExW(0, ClassName, "", 0, 0, 0, 0, 0, NativeMethods.HWND_MESSAGE, IntPtr.Zero, instance, IntPtr.Zero);
        if (_hwnd == IntPtr.Zero) AppLog.Write("CreateWindowEx failed, win32 error " + Marshal.GetLastWin32Error());
    }

    private IntPtr WndProc(IntPtr hwnd, uint msg, IntPtr wParam, IntPtr lParam) => DefWindowProcW(hwnd, msg, wParam, lParam);

    public void Dispose()
    {
        if (_hwnd == IntPtr.Zero) return;
        DestroyWindow(_hwnd);
        _hwnd = IntPtr.Zero;
        if (_classRegistered) UnregisterClassW(ClassName, GetModuleHandleW(null));
        _classRegistered = false;
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
