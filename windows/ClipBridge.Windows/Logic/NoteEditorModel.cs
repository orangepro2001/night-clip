namespace ClipBridge.Windows.Logic;

/// <summary>What the status text next to the editor says.</summary>
public enum EditorSaveState
{
    /// <summary>The text is what the core holds (or nothing is open).</summary>
    Saved,

    /// <summary>Typed, not saved yet: the 500 ms debounce is running or a save is due.</summary>
    Dirty,

    Saving,

    /// <summary>The last save failed; the text stays in the box and is saved again on the next edit or flush.</summary>
    Failed,

    /// <summary>The text is over the limits of the core and is not sent.</summary>
    TooLarge,

    /// <summary>The note is in the recycle bin (probably deleted on another device) but there is typed text that cannot be saved there.</summary>
    Trashed,
}

/// <summary>
/// One notes.update call, frozen when the save starts. <see cref="TitleChange"/> and <see cref="BodyChange"/> are the
/// parameters (null = unchanged since the base revision); <see cref="Title"/> and <see cref="Body"/> are the whole text at
/// that moment, which is what the base revision holds once the save went through.
/// </summary>
public sealed record SaveRequest(string NoteId, string BaseRevId, string? TitleChange, string? BodyChange, string Title, string Body);

public enum SaveOutcomeKind
{
    /// <summary>The result belongs to a note or save that is gone; ignore it.</summary>
    Stale,

    /// <summary>A new revision was stored on top of the base; it is the new base.</summary>
    Saved,

    /// <summary>The core merged, set a copy aside or did not store what was sent: the note has to be read again.</summary>
    NeedsReload,
}

/// <param name="RevId">The revision the note has now.</param>
/// <param name="Merged">The edit was combined with changes from elsewhere.</param>
/// <param name="ConflictNoteId">The copy set aside when both sides changed the same block.</param>
/// <param name="PendingRemoteRev">A <c>notes.changed</c> for an unknown revision arrived while the save ran: check it again.</param>
public sealed record SaveOutcome(SaveOutcomeKind Kind, string RevId = "", bool Merged = false, string ConflictNoteId = "", string PendingRemoteRev = "");

public enum RemoteAction { None, Reload }

public enum ReloadResult
{
    /// <summary>The answer is for another note or came while a save was running.</summary>
    Ignored,

    /// <summary>The core's text is the text in the box; only the base revision moved.</summary>
    Unchanged,

    /// <summary>The box has to show the core's text.</summary>
    Replaced,

    /// <summary>The user typed meanwhile: the box keeps their text and the next save merges it via the base revision.</summary>
    Kept,
}

/// <summary>
/// The editing state machine of one open note, UI-free and without any clock or thread of its own (the caller passes the
/// time and runs the calls; see <see cref="NoteEditorSession"/>). The rules:
/// <list type="bullet">
/// <item>The revision that was loaded is the <c>base_rev_id</c> of every save; after a save the returned revision is the new base.</item>
/// <item>A save is due 500 ms after the LAST edit, so typing never produces a revision storm; only one save runs at a time.</item>
/// <item>A merge or a conflict copy in the answer means the box must show what the core now holds, unless the user typed
/// meanwhile: typed text is never replaced.</item>
/// <item>A <c>notes.changed</c> for a different revision of the open note reloads it silently while nothing is unsaved;
/// with unsaved text the box is left alone and the next save merges via the base revision.</item>
/// </list>
/// Not thread-safe: use it from the UI thread.
/// </summary>
public sealed class NoteEditorModel
{
    public const int DebounceMs = 500;

    private string _savedTitle = "";
    private string _savedBody = "";
    private SaveRequest? _inflight;
    private SaveRequest? _reloadFor;
    private DateTimeOffset? _dueAt;
    private string _pendingRemoteRev = "";
    private readonly HashSet<string> _known = new();
    private bool _failed;
    private bool _tooLarge;

    public string NoteId { get; private set; } = "";
    public string BaseRevId { get; private set; } = "";
    public string Title { get; private set; } = "";
    public string Body { get; private set; } = "";
    public bool IsOpen => NoteId.Length > 0;

    /// <summary>The note is in the recycle bin: saving is suspended until it is restored.</summary>
    public bool InTrash { get; private set; }

    /// <summary>Opened from the recycle bin without typed text: the boxes are read-only.</summary>
    public bool ReadOnly { get; private set; }

    public bool IsSaving => _inflight is not null;

    /// <summary>The text differs from the base revision (a save in flight does not make it clean until it succeeded).</summary>
    public bool Dirty => IsOpen && (Title != _savedTitle || Body != _savedBody);

    /// <summary>Typed text that the core does not hold yet and that may be lost if the note is closed.</summary>
    public bool NeedsSave => Dirty && !ReadOnly;

    /// <summary>When the debounce fires; null when nothing is waiting.</summary>
    public DateTimeOffset? DueAt => _dueAt;

    public EditorSaveState State
    {
        get
        {
            if (!IsOpen) return EditorSaveState.Saved;
            if (InTrash && Dirty) return EditorSaveState.Trashed;
            if (_tooLarge && Dirty) return EditorSaveState.TooLarge;
            if (_failed && Dirty) return EditorSaveState.Failed;
            if (_inflight is not null) return EditorSaveState.Saving;
            return Dirty ? EditorSaveState.Dirty : EditorSaveState.Saved;
        }
    }

    // ---- opening and closing ---------------------------------------------------------------------------------------

    /// <summary>Opens a revision of a note as the editor's text and base.</summary>
    public void Load(NoteDetail d)
    {
        NoteId = d.NoteId;
        BaseRevId = d.RevId;
        Title = _savedTitle = d.Title;
        Body = _savedBody = d.Body;
        InTrash = ReadOnly = d.Deleted;
        _inflight = null;
        _reloadFor = null;
        _dueAt = null;
        _pendingRemoteRev = "";
        _known.Clear();
        _known.Add(d.RevId);
        _failed = _tooLarge = false;
    }

    public void Close()
    {
        NoteId = BaseRevId = Title = Body = _savedTitle = _savedBody = _pendingRemoteRev = "";
        InTrash = ReadOnly = _failed = _tooLarge = false;
        _inflight = null;
        _reloadFor = null;
        _dueAt = null;
        _known.Clear();
    }

    // ---- editing ---------------------------------------------------------------------------------------------------

    /// <summary>
    /// The user changed the text (called after every keystroke with the whole text; line breaks may be "\r\n"). Restarts the
    /// debounce. False when nothing changed.
    /// </summary>
    public bool Edit(string title, string body, DateTimeOffset now)
    {
        if (!IsOpen || ReadOnly) return false;
        title = ClipText.ToCore(title);
        body = ClipText.ToCore(body);
        if (title == Title && body == Body) return false;
        Title = title;
        Body = body;
        _tooLarge = NoteLimits.IsTooLarge(title, body);
        _failed = false;
        _dueAt = Dirty && !_tooLarge ? now.AddMilliseconds(DebounceMs) : null;
        return true;
    }

    // ---- saving ----------------------------------------------------------------------------------------------------

    /// <summary>The save to start now if the debounce has run out; null otherwise.</summary>
    public SaveRequest? TakeDue(DateTimeOffset now) => _dueAt is { } due && due <= now ? TakeNow() : null;

    /// <summary>
    /// The save to start now whatever the debounce says (focus lost, switching notes, closing); null when there is nothing to
    /// save, a save is already running (wait for it and ask again), or saving is impossible (recycle bin, too large).
    /// </summary>
    public SaveRequest? TakeNow()
    {
        if (!IsOpen || ReadOnly || InTrash || _inflight is not null || !Dirty || _tooLarge) return null;
        var request = new SaveRequest(NoteId, BaseRevId,
            Title != _savedTitle ? Title : null, Body != _savedBody ? Body : null, Title, Body);
        _inflight = request;
        _dueAt = null;
        _failed = false;
        return request;
    }

    /// <summary>
    /// The core accepted <paramref name="request"/>. A result that stored a revision (<c>changed</c>) on top of the base becomes
    /// the new base. One that stored nothing is only taken as the base when it is the base itself (the text equals what the core
    /// has); otherwise the answer names a newer revision made elsewhere, which does not hold our text: the note is read again.
    /// </summary>
    public SaveOutcome Complete(SaveRequest request, NoteSaveResult result)
    {
        if (!ReferenceEquals(request, _inflight)) return new SaveOutcome(SaveOutcomeKind.Stale);
        _inflight = null;
        var notOurs = !result.Changed && result.RevId != request.BaseRevId;
        if (result.Merged || result.ConflictNoteId.Length > 0 || notOurs || result.RevId.Length == 0)
        {
            _reloadFor = request;
            _pendingRemoteRev = "";
            return new SaveOutcome(SaveOutcomeKind.NeedsReload, result.RevId, result.Merged, result.ConflictNoteId);
        }
        _known.Add(result.RevId);
        BaseRevId = result.RevId;
        _savedTitle = request.Title;
        _savedBody = request.Body;
        var pending = _pendingRemoteRev;
        _pendingRemoteRev = "";
        if (pending.Length > 0 && _known.Contains(pending)) pending = "";
        return new SaveOutcome(SaveOutcomeKind.Saved, result.RevId, false, "", pending);
    }

    /// <summary>The save failed. The text stays; nothing retries by itself until the user edits again or a flush asks.</summary>
    public void Fail(SaveRequest request, string code)
    {
        if (!ReferenceEquals(request, _inflight)) return;
        _inflight = null;
        _failed = true;
        _dueAt = null;
        if (code == "CONFLICT") InTrash = true; // the note went to the recycle bin (here or on another device) while it was open
    }

    // ---- changes from elsewhere -------------------------------------------------------------------------------------

    /// <summary>A <c>notes.changed</c> event. Reload means: read the note and call <see cref="ApplyReload"/>.</summary>
    public RemoteAction OnRemoteChange(string noteId, string revId)
    {
        if (!IsOpen || noteId != NoteId || revId.Length == 0) return RemoteAction.None;
        if (revId == BaseRevId || _known.Contains(revId)) return RemoteAction.None; // our own save, or what we show already
        if (_inflight is not null)
        {
            _pendingRemoteRev = revId; // may be the event of the save that is running: decided when it completes
            return RemoteAction.None;
        }
        return Dirty ? RemoteAction.None : RemoteAction.Reload;
    }

    /// <summary>The note was read again (after a merge or a remote change). Decides whether the box must change.</summary>
    public ReloadResult ApplyReload(NoteDetail d)
    {
        if (!IsOpen || d.NoteId != NoteId) return ReloadResult.Ignored;
        if (_inflight is not null)
        {
            _reloadFor = null;
            return ReloadResult.Ignored;
        }
        // After a merged save the text of that save is what the answer already contains; anything typed since is new.
        var untouched = _reloadFor is { } r ? Title == r.Title && Body == r.Body : !Dirty;
        _reloadFor = null;
        _known.Add(d.RevId);
        if (!untouched)
        {
            InTrash = d.Deleted;
            return ReloadResult.Kept;
        }
        var same = Title == d.Title && Body == d.Body;
        BaseRevId = d.RevId;
        Title = _savedTitle = d.Title;
        Body = _savedBody = d.Body;
        InTrash = ReadOnly = d.Deleted;
        _failed = _tooLarge = false;
        _dueAt = null;
        return same ? ReloadResult.Unchanged : ReloadResult.Replaced;
    }

    /// <summary>Reading the note again failed: keep the text and do not save it again blindly.</summary>
    public void ReloadFailed()
    {
        _reloadFor = null;
        if (Dirty) _failed = true;
    }

    /// <summary>The note was taken out of the recycle bin. Typed text is saved at once (it merges with the restored revision).</summary>
    public void MarkRestored(DateTimeOffset now)
    {
        InTrash = ReadOnly = _failed = false;
        if (Dirty && !_tooLarge) _dueAt = now;
    }
}
