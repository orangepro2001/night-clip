namespace ClipBridge.Windows.Logic;

/// <summary>One piece of work taken from a <see cref="DebouncedWork{T}"/>: the value to work on and the number of the request it belongs to.</summary>
public sealed record WorkJob<T>(long Ticket, T Value);

/// <summary>
/// "Do this for the newest value, but only after things have been quiet for a moment, one at a time, and throw away answers that
/// the newest value has overtaken." It has no clock and no thread: the caller passes the time and runs the work. Used for the
/// live preview (the text) and for the search (the query).
/// </summary>
public sealed class DebouncedWork<T> where T : notnull
{
    private readonly int _delayMs;
    private T? _pending;
    private bool _hasPending;
    private DateTimeOffset _dueAt;
    private long _latest;
    private WorkJob<T>? _inflight;

    public DebouncedWork(int delayMs) => _delayMs = delayMs;

    public bool Busy => _inflight is not null;
    public bool HasPending => _hasPending;
    public DateTimeOffset? DueAt => _hasPending ? _dueAt : null;

    /// <summary>A new value: replaces a waiting one and restarts the wait.</summary>
    public void Post(T value, DateTimeOffset now) => Schedule(value, now.AddMilliseconds(_delayMs));

    /// <summary>A new value that needs no waiting (a note was opened, a mode switched).</summary>
    public void PostNow(T value, DateTimeOffset now) => Schedule(value, now);

    private void Schedule(T value, DateTimeOffset due)
    {
        _pending = value;
        _hasPending = true;
        _dueAt = due;
        _latest++;
    }

    /// <summary>The job to start now: the newest value once it has waited long enough and no job is running; null otherwise.</summary>
    public WorkJob<T>? TakeDue(DateTimeOffset now)
    {
        if (!_hasPending || _inflight is not null || _dueAt > now) return null;
        _hasPending = false;
        _inflight = new WorkJob<T>(_latest, _pending!);
        _pending = default;
        return _inflight;
    }

    /// <summary>
    /// The job is done (successfully or not). True when its value is still the newest, i.e. its answer may be shown; false when
    /// something newer was posted meanwhile (or the work was cancelled): the answer is stale and must be dropped.
    /// </summary>
    public bool Finish(WorkJob<T> job)
    {
        if (!ReferenceEquals(job, _inflight)) return false;
        _inflight = null;
        return job.Ticket == _latest;
    }

    /// <summary>Drops the waiting value and makes the answer of a running job stale (the note or the query it was for is gone).</summary>
    public void Cancel()
    {
        _hasPending = false;
        _pending = default;
        _latest++;
    }
}

/// <summary>
/// The live preview of the text being typed: <c>notes.render {body}</c> about 150 ms after the last keystroke, one render at a
/// time, stale answers dropped. The page keeps showing the last tree that rendered successfully when a render fails.
/// </summary>
public sealed class LivePreview
{
    public const int DebounceMs = 150;

    private readonly DebouncedWork<string> _work = new(DebounceMs);
    private string? _lastRequested;

    public bool Busy => _work.Busy;

    /// <summary>The text changed. The same text as the last request is not rendered again.</summary>
    public void Request(string body, DateTimeOffset now, bool immediately = false)
    {
        if (body == _lastRequested) return;
        _lastRequested = body;
        if (immediately) _work.PostNow(body, now);
        else _work.Post(body, now);
    }

    /// <summary>Another note (or none) is shown: nothing waiting is rendered and a running render is ignored when it ends.</summary>
    public void Reset()
    {
        _lastRequested = null;
        _work.Cancel();
    }

    public WorkJob<string>? TakeDue(DateTimeOffset now) => _work.TakeDue(now);

    /// <summary>The render ended with a tree: true when it may be shown.</summary>
    public bool Accept(WorkJob<string> job) => _work.Finish(job);

    /// <summary>The render failed: the last tree stays; the same text is tried again the next time it is requested.</summary>
    public void Fail(WorkJob<string> job)
    {
        if (_work.Finish(job) && job.Value == _lastRequested) _lastRequested = null;
    }
}
