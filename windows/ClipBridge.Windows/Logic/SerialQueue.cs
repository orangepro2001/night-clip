namespace ClipBridge.Windows.Logic;

/// <summary>
/// Runs posted jobs one at a time, in order, on one background thread. Used for everything that
/// reads or writes the clipboard or talks to the core on its behalf, so that "write the clipboard +
/// clip.mark_applied" always completes before the next clipboard read can start (the echo guard
/// of docs/shell-guide.md §3 item 5). A throwing job is reported and never stops the worker.
/// </summary>
public sealed class SerialQueue : IDisposable
{
    private readonly Queue<Action> _jobs = new();
    private readonly object _gate = new();
    private readonly Thread _thread;
    private readonly Action<Exception>? _onError;
    private bool _stop;

    public SerialQueue(string name, Action<Exception>? onError = null)
    {
        _onError = onError;
        _thread = new Thread(Run) { IsBackground = true, Name = name };
        _thread.Start();
    }

    public void Post(Action job)
    {
        lock (_gate)
        {
            if (_stop) return;
            _jobs.Enqueue(job);
            Monitor.Pulse(_gate);
        }
    }

    /// <summary>Completes when every job posted before this call has run (used by tests and shutdown).</summary>
    public Task Drain()
    {
        var tcs = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        Post(() => tcs.TrySetResult());
        return tcs.Task;
    }

    private void Run()
    {
        while (true)
        {
            Action job;
            lock (_gate)
            {
                while (_jobs.Count == 0 && !_stop) Monitor.Wait(_gate);
                if (_jobs.Count == 0) return; // stopped and drained
                job = _jobs.Dequeue();
            }
            try { job(); }
            catch (Exception ex)
            {
                try { _onError?.Invoke(ex); } catch (Exception) { /* the error reporter must not kill the worker either */ }
            }
        }
    }

    public void Dispose()
    {
        lock (_gate)
        {
            _stop = true;
            Monitor.PulseAll(_gate);
        }
        if (Thread.CurrentThread != _thread) _thread.Join(TimeSpan.FromSeconds(2));
    }
}
