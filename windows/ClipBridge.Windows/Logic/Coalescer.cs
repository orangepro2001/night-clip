namespace ClipBridge.Windows.Logic;

/// <summary>
/// Runs an async reload so that requests arriving while it is running are merged into exactly one
/// more run afterwards (a burst of peer.state events causes one extra reload, not one per event).
/// Single-threaded: call from the UI thread and let the awaits resume there.
/// </summary>
public sealed class Coalescer
{
    private readonly Func<Task> _work;
    private bool _running;
    private bool _dirty;

    public Coalescer(Func<Task> work) => _work = work;

    public bool IsRunning => _running;

    public async Task RunAsync()
    {
        if (_running)
        {
            _dirty = true;
            return;
        }
        _running = true;
        try
        {
            do
            {
                _dirty = false;
                await _work();
            } while (_dirty);
        }
        finally { _running = false; }
    }
}
