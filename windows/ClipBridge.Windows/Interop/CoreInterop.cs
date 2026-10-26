using System.Runtime.InteropServices;
using System.Text.Json;

namespace ClipBridge.Windows.Interop;

/// <summary>
/// Raw C ABI exported by clipbridge_core.dll (docs/architecture.md §2.2).
/// All strings are UTF-8. Every char* returned by the DLL must be released
/// with cb_free.
/// </summary>
internal static class Native
{
    private const string Dll = "clipbridge_core";

    [DllImport(Dll, CallingConvention = CallingConvention.Cdecl)]
    internal static extern int cb_init(
        [MarshalAs(UnmanagedType.LPUTF8Str)] string configJson, out IntPtr errOut);

    [DllImport(Dll, CallingConvention = CallingConvention.Cdecl)]
    internal static extern IntPtr cb_call(
        [MarshalAs(UnmanagedType.LPUTF8Str)] string method,
        [MarshalAs(UnmanagedType.LPUTF8Str)] string paramsJson);

    [DllImport(Dll, CallingConvention = CallingConvention.Cdecl)]
    internal static extern IntPtr cb_next_event(int timeoutMs);

    [DllImport(Dll, CallingConvention = CallingConvention.Cdecl)]
    internal static extern void cb_shutdown();

    [DllImport(Dll, CallingConvention = CallingConvention.Cdecl)]
    internal static extern void cb_free(IntPtr p);
}

public sealed class CoreException : Exception
{
    public string Code { get; }
    public CoreException(string code, string message) : base($"{code}: {message}") => Code = code;
}

/// <summary>
/// Managed facade over the Go core. Owns a dedicated event-pump thread that
/// blocks in cb_next_event; there are no Go→C# callbacks.
/// </summary>
public sealed class CoreHost : IDisposable
{
    private Thread? _pump;
    private volatile bool _stop;
    private bool _started;
    private bool _disposed;

    /// <summary>Raised on the pump thread. Marshal to the UI thread before touching UI.</summary>
    public event Action<JsonElement>? EventReceived;

    public void Start(object config)
    {
        if (_started) throw new InvalidOperationException("core already started");
        var json = JsonSerializer.Serialize(config);
        var rc = Native.cb_init(json, out var err);
        if (rc != 0)
        {
            var msg = TakeString(err) ?? "cb_init failed";
            throw new CoreException("INIT_FAILED", msg);
        }
        _started = true;
        _stop = false;
        _pump = new Thread(PumpLoop) { IsBackground = true, Name = "clipbridge-core-events" };
        _pump.Start();
    }

    /// <summary>Calls a facade method and returns the "result" element, or throws CoreException.</summary>
    public JsonElement Call(string method, object? parameters = null)
    {
        var p = parameters is null ? "{}" : JsonSerializer.Serialize(parameters);
        var ptr = Native.cb_call(method, p);
        var text = TakeString(ptr) ?? throw new CoreException("INTERNAL", "null reply from core");
        using var doc = JsonDocument.Parse(text);
        var root = doc.RootElement;
        if (root.GetProperty("ok").GetBoolean())
            return root.GetProperty("result").Clone();
        var e = root.GetProperty("error");
        throw new CoreException(e.GetProperty("code").GetString() ?? "INTERNAL",
                                e.GetProperty("message").GetString() ?? "");
    }

    private void PumpLoop()
    {
        while (!_stop)
        {
            var ptr = Native.cb_next_event(1000);
            if (ptr == IntPtr.Zero) continue;
            var text = TakeString(ptr);
            if (text is null) continue;
            try
            {
                using var doc = JsonDocument.Parse(text);
                EventReceived?.Invoke(doc.RootElement.Clone());
            }
            catch (JsonException) { /* malformed event: drop, core owns the contract */ }
        }
    }

    private static string? TakeString(IntPtr p)
    {
        if (p == IntPtr.Zero) return null;
        try { return Marshal.PtrToStringUTF8(p); }
        finally { Native.cb_free(p); }
    }

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;
        if (!_started) return;
        _stop = true;
        // cb_shutdown wakes a blocked cb_next_event, so the join is prompt.
        Native.cb_shutdown();
        _pump?.Join(TimeSpan.FromSeconds(3));
    }
}
