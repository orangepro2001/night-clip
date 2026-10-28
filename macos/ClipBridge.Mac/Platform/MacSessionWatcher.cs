using System.Diagnostics;
using System.Runtime.InteropServices;
using Avalonia.Threading;
using ClipBridge.Windows.Logic;

namespace ClipBridge.Mac.Platform;

/// <summary>
/// The macOS stand-in for the Windows SystemEvents.SessionSwitch / PowerModeChanged hooks the vault locks on.
/// macOS offers these only as NSWorkspace / distributed notifications (which need an Objective-C observer), so
/// this polls every 2 s on the UI thread instead:
/// <list type="bullet">
/// <item>screen lock: CGSessionCopyCurrentDictionary's "CGSSessionScreenIsLocked" turning on = <see cref="LockTrigger.SessionLock"/>;</item>
/// <item>fast user switching: "kCGSessionOnConsoleKey" turning off = <see cref="LockTrigger.UserSwitch"/>;</item>
/// <item>sleep: the wall clock moved more than 30 s further than the monotonic clock (which stops while the Mac
/// sleeps) = <see cref="LockTrigger.Suspend"/>. This is noticed after the wake, not before the sleep.</item>
/// </list>
/// Every probe is best effort: a missing framework only disables that probe.
/// </summary>
internal sealed class MacSessionWatcher : IDisposable
{
    private static readonly TimeSpan Interval = TimeSpan.FromSeconds(2);
    private static readonly TimeSpan SleepGap = TimeSpan.FromSeconds(30);

    private readonly DispatcherTimer _timer = new() { Interval = Interval };
    private readonly Stopwatch _mono = Stopwatch.StartNew();
    private TimeSpan _lastMono;
    private DateTime _lastWall;
    private bool _wasLocked;
    private bool _wasOnConsole = true;
    private bool _probeBroken;

    /// <summary>A session event, on the UI thread.</summary>
    public event Action<LockTrigger>? Triggered;

    public MacSessionWatcher() => _timer.Tick += (_, _) => Tick();

    public void Start()
    {
        _lastMono = _mono.Elapsed;
        _lastWall = DateTime.UtcNow;
        if (Probe() is { } s)
        {
            _wasLocked = s.Locked;
            _wasOnConsole = s.OnConsole;
        }
        _timer.Start();
    }

    public void Dispose() => _timer.Stop();

    private void Tick()
    {
        var mono = _mono.Elapsed;
        var wall = DateTime.UtcNow;
        var gap = (wall - _lastWall) - (mono - _lastMono);
        _lastMono = mono;
        _lastWall = wall;
        if (gap > SleepGap) Raise(LockTrigger.Suspend);

        if (Probe() is not { } s) return;
        if (s.Locked && !_wasLocked) Raise(LockTrigger.SessionLock);
        else if (!s.Locked && _wasLocked) Raise(LockTrigger.SessionUnlock);
        if (!s.OnConsole && _wasOnConsole) Raise(LockTrigger.UserSwitch);
        _wasLocked = s.Locked;
        _wasOnConsole = s.OnConsole;
    }

    private void Raise(LockTrigger trigger)
    {
        MacLog.Write("session event " + trigger);
        try { Triggered?.Invoke(trigger); }
        catch (Exception ex) { MacLog.Error("session event subscriber", ex); }
    }

    // ---- CGSession -------------------------------------------------------------------------

    private const string CoreGraphics = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics";
    private const string CoreFoundation = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation";
    private const uint Utf8Encoding = 0x08000100; // kCFStringEncodingUTF8
    private const long NumberIntType = 9;         // kCFNumberIntType

    [DllImport(CoreGraphics)] private static extern IntPtr CGSessionCopyCurrentDictionary();
    [DllImport(CoreFoundation)] private static extern IntPtr CFStringCreateWithCString(IntPtr alloc, string text, uint encoding);
    [DllImport(CoreFoundation)] private static extern IntPtr CFDictionaryGetValue(IntPtr dict, IntPtr key);
    [DllImport(CoreFoundation)] private static extern nuint CFGetTypeID(IntPtr cf);
    [DllImport(CoreFoundation)] private static extern nuint CFBooleanGetTypeID();
    [DllImport(CoreFoundation)] private static extern nuint CFNumberGetTypeID();
    [DllImport(CoreFoundation)] [return: MarshalAs(UnmanagedType.U1)] private static extern bool CFBooleanGetValue(IntPtr boolean);
    [DllImport(CoreFoundation)] [return: MarshalAs(UnmanagedType.U1)] private static extern bool CFNumberGetValue(IntPtr number, long type, out int value);
    [DllImport(CoreFoundation)] private static extern void CFRelease(IntPtr cf);

    private readonly record struct SessionState(bool Locked, bool OnConsole);

    /// <summary>Null when the session dictionary cannot be read (no window server session, or not macOS).</summary>
    private SessionState? Probe()
    {
        if (_probeBroken || !OperatingSystem.IsMacOS()) return null;
        try
        {
            var dict = CGSessionCopyCurrentDictionary();
            if (dict == IntPtr.Zero) return null;
            try
            {
                return new SessionState(ReadFlag(dict, "CGSSessionScreenIsLocked") ?? false, ReadFlag(dict, "kCGSessionOnConsoleKey") ?? true);
            }
            finally { CFRelease(dict); }
        }
        catch (Exception ex) when (ex is DllNotFoundException or EntryPointNotFoundException or MarshalDirectiveException)
        {
            _probeBroken = true;
            MacLog.Error("session probe", ex);
            return null;
        }
    }

    /// <summary>A CFBoolean (or CFNumber) value of the dictionary; null when the key is absent.</summary>
    private static bool? ReadFlag(IntPtr dict, string name)
    {
        var key = CFStringCreateWithCString(IntPtr.Zero, name, Utf8Encoding);
        if (key == IntPtr.Zero) return null;
        try
        {
            var value = CFDictionaryGetValue(dict, key); // "Get" rule: not ours to release
            if (value == IntPtr.Zero) return null;
            var type = CFGetTypeID(value);
            if (type == CFBooleanGetTypeID()) return CFBooleanGetValue(value);
            if (type == CFNumberGetTypeID()) return CFNumberGetValue(value, NumberIntType, out var n) && n != 0;
            return null;
        }
        finally { CFRelease(key); }
    }
}
