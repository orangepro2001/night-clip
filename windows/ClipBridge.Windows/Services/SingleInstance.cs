namespace ClipBridge.Windows.Services;

/// <summary>
/// One ClipBridge per Windows session: a named mutex decides who is first; a later start signals a
/// named auto-reset event (the first instance brings its window forward) and exits.
/// </summary>
internal sealed class SingleInstance : IDisposable
{
    private const string MutexName = @"Local\ClipBridge.SingleInstance.6b1f6d1e";
    private const string EventName = @"Local\ClipBridge.Activate.6b1f6d1e";

    private Mutex? _mutex;
    private EventWaitHandle? _activate;
    private ManualResetEvent? _stop;
    private Thread? _waiter;

    /// <summary>Raised on a background thread when another start asked us to show the window.</summary>
    public event Action? ActivationRequested;

    /// <summary>True when this process is the first instance. Otherwise the first one has been told to show itself.</summary>
    public bool TryAcquire()
    {
        // Created (or opened) before the mutex decision so a racing second start can always signal it.
        _activate = new EventWaitHandle(false, EventResetMode.AutoReset, EventName);
        _mutex = new Mutex(true, MutexName, out var createdNew);
        if (!createdNew)
        {
            _mutex.Dispose();
            _mutex = null;
            // We have the foreground right (the user just started us): hand it to the running instance.
            NativeMethods.AllowSetForegroundWindow(-1);
            _activate.Set();
            return false;
        }

        _stop = new ManualResetEvent(false);
        _waiter = new Thread(WaitLoop) { IsBackground = true, Name = "clipbridge-single-instance" };
        _waiter.Start();
        return true;
    }

    private void WaitLoop()
    {
        var handles = new WaitHandle[] { _activate!, _stop! };
        while (WaitHandle.WaitAny(handles) == 0)
        {
            try { ActivationRequested?.Invoke(); }
            catch (Exception ex) { AppLog.Error("activation handler", ex); }
        }
    }

    public void Dispose()
    {
        _stop?.Set();
        _waiter?.Join(TimeSpan.FromSeconds(1));
        try { _mutex?.ReleaseMutex(); }
        catch (ApplicationException) { /* not owned by this thread: process exit releases it */ }
        _mutex?.Dispose();
        _activate?.Dispose();
        _stop?.Dispose();
    }
}
