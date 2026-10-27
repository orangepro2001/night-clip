using System.Text.Json;
using ClipBridge.Windows.Interop;

namespace ClipBridge.Windows.Logic;

/// <summary>Where the editor's core calls run. Lets the tests run them inline or hold them back.</summary>
public interface INotesGateway
{
    /// <summary>Runs <paramref name="call"/> off the calling (UI) thread.</summary>
    Task<T> RunAsync<T>(Func<NotesApi, T> call);

    /// <summary>Runs <paramref name="call"/> right here (only for shutdown, when nothing can be awaited any more).</summary>
    T Run<T>(Func<NotesApi, T> call);
}

/// <summary>The production gateway: core calls go to the thread pool, so the UI thread never waits for the core.</summary>
public sealed class ThreadPoolNotesGateway : INotesGateway
{
    private readonly NotesApi _api;
    public ThreadPoolNotesGateway(NotesApi api) => _api = api;

    public Task<T> RunAsync<T>(Func<NotesApi, T> call) => Task.Run(() => call(_api));

    public T Run<T>(Func<NotesApi, T> call) => call(_api);
}

/// <summary>
/// Drives a <see cref="NoteEditorModel"/>: starts the saves it asks for, reads the note again after a merge or a change from
/// another device, and tells the view what to show. All members are meant for the UI thread; the core calls run through the
/// gateway and the results come back to the UI thread by the continuation of the await. Events are raised on that thread.
/// </summary>
public sealed class NoteEditorSession
{
    private readonly INotesGateway _gateway;
    private readonly Func<DateTimeOffset> _clock;
    private Task? _saving;

    public NoteEditorSession(INotesGateway gateway, Func<DateTimeOffset>? clock = null)
    {
        _gateway = gateway;
        _clock = clock ?? (() => DateTimeOffset.UtcNow);
    }

    public NoteEditorModel Model { get; } = new();

    /// <summary>The save state, the base revision or the trash state changed: refresh the status text and the buttons.</summary>
    public event Action? StateChanged;

    /// <summary>The boxes must show this text (title, body): the core's text replaced theirs.</summary>
    public event Action<string, string>? TextReplaced;

    /// <summary>A plain message for the user (already worded): the note was merged, a copy was set aside, ...</summary>
    public event Action<string>? Notice;

    /// <summary>A friendly error text for a failed save or reload.</summary>
    public event Action<string>? Failed;

    /// <summary>The note has a new head revision (note id, revision id): render the preview again.</summary>
    public event Action<string, string>? RevisionChanged;

    /// <summary>A save produced a conflict copy (its note id): the conflict banner has to be refreshed.</summary>
    public event Action<string>? ConflictCopyMade;

    // ---- opening and editing ---------------------------------------------------------------------------------------

    /// <summary>Shows a note. The previous one must have been flushed (see <see cref="FlushAsync"/>) or given up.</summary>
    public void Open(NoteDetail detail)
    {
        Model.Load(detail);
        _saving = null;
        StateChanged?.Invoke();
    }

    public void Close()
    {
        Model.Close();
        _saving = null;
        StateChanged?.Invoke();
    }

    /// <summary>The user changed the title or the text (whole values).</summary>
    public void OnEdited(string title, string body)
    {
        if (Model.Edit(title, body, _clock())) StateChanged?.Invoke();
    }

    /// <summary>Called by a timer a few times a second: starts the save when the debounce has run out.</summary>
    public async Task TickAsync()
    {
        if (_saving is { IsCompleted: false }) return;
        var request = Model.TakeDue(_clock());
        if (request is not null) await StartAsync(request);
    }

    /// <summary>
    /// Saves everything that is typed (focus lost, switching notes, closing). True when the core holds the text afterwards;
    /// false when it could not be saved (error, too large, in the recycle bin): the caller must not throw the text away
    /// without asking.
    /// </summary>
    public async Task<bool> FlushAsync()
    {
        for (var round = 0; round < 6; round++)
        {
            if (_saving is { IsCompleted: false } running) await running;
            var request = Model.TakeNow();
            if (request is null) return !Model.NeedsSave;
            await StartAsync(request);
            if (Model.State is EditorSaveState.Failed) return false;
        }
        return !Model.NeedsSave;
    }

    /// <summary>The synchronous flush for the moment the application exits. No events are raised.</summary>
    public bool FlushBlocking()
    {
        for (var round = 0; round < 3; round++)
        {
            var request = Model.TakeNow();
            if (request is null) break;
            try
            {
                var result = _gateway.Run(api => Save(api, request));
                Model.Complete(request, result);
            }
            catch (Exception ex) when (IsCoreFailure(ex))
            {
                Model.Fail(request, CodeOf(ex));
                return false;
            }
        }
        return !Model.NeedsSave;
    }

    // ---- saving ----------------------------------------------------------------------------------------------------

    private Task StartAsync(SaveRequest request)
    {
        var task = RunSaveAsync(request);
        if (!task.IsCompleted) _saving = task;
        return task;
    }

    private async Task RunSaveAsync(SaveRequest request)
    {
        StateChanged?.Invoke();
        SaveOutcome outcome;
        try
        {
            var result = await _gateway.RunAsync(api => Save(api, request));
            outcome = Model.Complete(request, result);
        }
        catch (Exception ex) when (IsCoreFailure(ex))
        {
            var code = CodeOf(ex);
            Model.Fail(request, code);
            StateChanged?.Invoke();
            if (Model.NoteId == request.NoteId) Failed?.Invoke(ErrorMessages.ForNotes(code)); // not for a note that was closed meanwhile
            return;
        }
        StateChanged?.Invoke();
        switch (outcome.Kind)
        {
            case SaveOutcomeKind.Saved:
                RevisionChanged?.Invoke(request.NoteId, outcome.RevId);
                if (outcome.PendingRemoteRev.Length > 0) await RemoteChangedAsync(request.NoteId, outcome.PendingRemoteRev);
                break;
            case SaveOutcomeKind.NeedsReload:
                if (outcome.ConflictNoteId.Length > 0) ConflictCopyMade?.Invoke(outcome.ConflictNoteId);
                await ReloadAsync(outcome);
                break;
        }
    }

    private static NoteSaveResult Save(NotesApi api, SaveRequest request) =>
        api.Update(request.NoteId, request.BaseRevId, request.TitleChange, request.BodyChange);

    // ---- reading the note again ---------------------------------------------------------------------------------------

    /// <summary>A <c>notes.changed</c> event arrived for a note (called for every event; other notes are ignored).</summary>
    public async Task RemoteChangedAsync(string noteId, string revId)
    {
        if (Model.OnRemoteChange(noteId, revId) == RemoteAction.Reload) await ReloadAsync(null);
    }

    private async Task ReloadAsync(SaveOutcome? cause)
    {
        var noteId = Model.NoteId;
        NoteDetail detail;
        try { detail = await _gateway.RunAsync(api => api.Get(noteId)); }
        catch (Exception ex) when (IsCoreFailure(ex))
        {
            Model.ReloadFailed();
            StateChanged?.Invoke();
            Failed?.Invoke(ErrorMessages.ForNotes(CodeOf(ex)));
            return;
        }
        var result = Model.ApplyReload(detail);
        if (result == ReloadResult.Replaced) TextReplaced?.Invoke(Model.Title, Model.Body);
        if (result != ReloadResult.Ignored) RevisionChanged?.Invoke(noteId, detail.RevId);
        StateChanged?.Invoke();
        if (cause is null || result == ReloadResult.Ignored) return;
        if (cause.ConflictNoteId.Length > 0) Notice?.Invoke(Strings.NoteConflictCopyMade);
        else if (cause.Merged) Notice?.Invoke(result == ReloadResult.Kept ? Strings.NoteMergedKept : Strings.NoteMerged);
    }

    // ---- recycle bin and history --------------------------------------------------------------------------------------

    /// <summary>Takes the open note out of the recycle bin.</summary>
    public async Task<bool> RestoreNoteAsync()
    {
        if (!Model.IsOpen) return false;
        var noteId = Model.NoteId;
        try { await _gateway.RunAsync(api => api.Restore(noteId)); }
        catch (Exception ex) when (IsCoreFailure(ex))
        {
            Failed?.Invoke(ErrorMessages.ForNotes(CodeOf(ex)));
            return false;
        }
        if (Model.NoteId != noteId) return true;
        Model.MarkRestored(_clock());
        StateChanged?.Invoke();
        if (!Model.Dirty) await ReloadAsync(null); // the restored revision becomes the base
        return true;
    }

    /// <summary>
    /// "Restore this version": the text of an older revision becomes the text of the note through a normal save. What was typed
    /// before is saved first, so it stays in the history.
    /// </summary>
    public async Task<bool> UseRevisionAsync(string title, string body)
    {
        if (!Model.IsOpen || Model.ReadOnly) return false;
        if (!await FlushAsync()) return false;
        if (!Model.Edit(title, body, _clock()))
        {
            Notice?.Invoke(Strings.NoteRevisionSame);
            return true;
        }
        TextReplaced?.Invoke(Model.Title, Model.Body);
        StateChanged?.Invoke();
        return await FlushAsync();
    }

    // ---- errors ----------------------------------------------------------------------------------------------------

    /// <summary>A failed core call, or a reply the shell could not read (CoreHost.Call parses the envelope itself).</summary>
    public static bool IsCoreFailure(Exception ex) => ex is CoreException or JsonException or KeyNotFoundException or InvalidOperationException;

    public static string CodeOf(Exception ex) => ex is CoreException c ? c.Code : "INTERNAL";
}
