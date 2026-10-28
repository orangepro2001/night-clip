using System;
using System.IO;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Text.Json;
using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Threading;

namespace ClipBridge.MacSpike;

public partial class MainWindow : Window
{
    private readonly DispatcherTimer _clipTimer = new() { Interval = TimeSpan.FromMilliseconds(500) };
    private long _lastChange = -1;
    private int _changes;

    public MainWindow()
    {
        InitializeComponent();
        Closing += (_, e) => { e.Cancel = true; Hide(); }; // stay in the menu bar
        Opened += (_, _) => { ProbeCore(); StartClipboardWatch(); };
    }

    public void SimulateSend()
    {
        var text = Pasteboard.ReadText();
        Set("ClipStatus", $"剪贴板：已读取 {text?.Length ?? 0} 个字符（模拟发送，未连接设备）");
    }

    private void OnSend(object? sender, RoutedEventArgs e) => SimulateSend();
    private void OnSendMenu(object? sender, EventArgs e) => SimulateSend();

    private void ProbeCore()
    {
        try
        {
            var version = Core.Call("version", "{}");
            // A throw-away data dir per launch: proves SQLite, key handling and identity on macOS.
            var dir = Path.Combine(Path.GetTempPath(), "clipbridge-spike-" + Environment.ProcessId);
            Directory.CreateDirectory(dir);
            var config = JsonSerializer.Serialize(new
            {
                data_dir = dir,
                db_key_b64 = Convert.ToBase64String(RandomNumberGenerator.GetBytes(32)),
                device_name = Environment.MachineName,
                platform = "windows", // the core does not know "macos" yet (plan §4)
                listen = false,
                port = 0,
            });
            var rc = Core.Init(config, out var err);
            var identity = rc == 0 ? Core.Call("identity.get", "{}") : "init failed: " + err;
            Report("CoreStatus", $"核心：{version}\n{Shorten(identity)}");
        }
        catch (Exception ex)
        {
            Report("CoreStatus", "核心加载失败：" + ex.GetType().Name + ": " + ex.Message);
        }
    }

    private void StartClipboardWatch()
    {
        _clipTimer.Tick += (_, _) =>
        {
            try
            {
                var count = Pasteboard.ChangeCount();
                if (count == _lastChange) return;
                var first = _lastChange < 0;
                _lastChange = count;
                if (first) { Report("ClipStatus", $"剪贴板：监听中（changeCount={count}）"); return; }
                _changes++;
                var text = Pasteboard.ReadText();
                Report("ClipStatus", $"剪贴板：第 {_changes} 次变化，文本 {text?.Length.ToString() ?? "无"} 字符：{Shorten(text ?? "")}");
            }
            catch (Exception ex)
            {
                _clipTimer.Stop();
                Report("ClipStatus", "剪贴板读取失败：" + ex.Message);
            }
        };
        _clipTimer.Start();
    }

    private void Report(string name, string text)
    {
        Set(name, text);
        try
        {
            var log = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.Personal), "Library/Logs/ClipBridgeSpike.log");
            File.AppendAllText(log, $"{DateTime.Now:HH:mm:ss} {name}: {text}\n");
        }
        catch { /* the log is a convenience only */ }
    }

    private void Set(string name, string text)
    {
        if (this.FindControl<TextBlock>(name) is { } t) t.Text = text;
    }

    private static string Shorten(string s) => s.Length > 160 ? s[..160] + "…" : s;
}

/// <summary>The five-function C ABI of the Go core (same as the Windows DLL).</summary>
internal static class Core
{
    private const string Lib = "libclipbridge_core.dylib";

    [DllImport(Lib)] private static extern int cb_init(byte[] config, out IntPtr err);
    [DllImport(Lib)] private static extern IntPtr cb_call(byte[] method, byte[] paramsJson);
    [DllImport(Lib)] private static extern void cb_free(IntPtr p);

    public static int Init(string config, out string err)
    {
        var rc = cb_init(Utf8(config), out var e);
        err = Take(e) ?? "";
        return rc;
    }

    public static string Call(string method, string json) => Take(cb_call(Utf8(method), Utf8(json))) ?? "";

    private static byte[] Utf8(string s) => System.Text.Encoding.UTF8.GetBytes(s + "\0");

    private static string? Take(IntPtr p)
    {
        if (p == IntPtr.Zero) return null;
        try { return Marshal.PtrToStringUTF8(p); } finally { cb_free(p); }
    }
}

/// <summary>NSPasteboard through the Objective-C runtime: changeCount polling and plain-text read.</summary>
internal static class Pasteboard
{
    private const string ObjC = "/usr/lib/libobjc.A.dylib";

    [DllImport(ObjC)] private static extern IntPtr objc_getClass(string name);
    [DllImport(ObjC)] private static extern IntPtr sel_registerName(string name);
    [DllImport(ObjC, EntryPoint = "objc_msgSend")] private static extern IntPtr Send(IntPtr receiver, IntPtr selector);
    [DllImport(ObjC, EntryPoint = "objc_msgSend")] private static extern IntPtr Send(IntPtr receiver, IntPtr selector, IntPtr arg);
    [DllImport(ObjC, EntryPoint = "objc_msgSend")] private static extern long SendLong(IntPtr receiver, IntPtr selector);
    [DllImport(ObjC, EntryPoint = "objc_msgSend")] private static extern IntPtr SendStr(IntPtr receiver, IntPtr selector, byte[] utf8);

    private static IntPtr General() => Send(objc_getClass("NSPasteboard"), sel_registerName("generalPasteboard"));

    public static long ChangeCount() => SendLong(General(), sel_registerName("changeCount"));

    public static string? ReadText()
    {
        var type = SendStr(objc_getClass("NSString"), sel_registerName("stringWithUTF8String:"),
            System.Text.Encoding.UTF8.GetBytes("public.utf8-plain-text\0"));
        var str = Send(General(), sel_registerName("stringForType:"), type);
        if (str == IntPtr.Zero) return null;
        return Marshal.PtrToStringUTF8(Send(str, sel_registerName("UTF8String")));
    }
}
