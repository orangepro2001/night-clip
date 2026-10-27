using System.Diagnostics;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;

namespace ClipBridge.Windows.Services;

/// <summary>
/// The shell's side of the notes: calling the core off the UI thread, routing the notes.* events to the notes page, opening links
/// safely and flushing typed text when the application ends. All rules (merging, rendering, conflicts) live in the core and in
/// Logic/ (NoteEditorModel, NotesList, MarkdownEdit); this file is the glue.
/// </summary>
internal sealed partial class ShellController
{
    private NotesApi? _notes;
    private INotesGateway? _notesGateway;
    private AttachmentResolver? _attachments;

    public NotesApi Notes => _notes ??= new NotesApi(_core);

    /// <summary>Where the editor's core calls run: the thread pool, never the UI thread.</summary>
    public INotesGateway NotesGateway => _notesGateway ??= new ThreadPoolNotesGateway(Notes);

    /// <summary>A note changed here or on another device (UI thread). Carries no text.</summary>
    public event Action<NotesChangedEvent>? NotesChanged;

    /// <summary>A peer's note sync changed (UI thread).</summary>
    public event Action<NotesSyncUpdatedEvent>? NotesSyncUpdated;

    /// <summary>A conflict copy was set aside, or a note deleted elsewhere was kept (UI thread).</summary>
    public event Action<NotesSyncConflictEvent>? NotesConflict;

    /// <summary>An attachment reached this device (UI thread): a preview that shows a placeholder for it can show the file now.</summary>
    public event Action<NotesAttachmentEvent>? NotesAttachmentArrived;

    /// <summary>The application is ending: typed text must be saved now, synchronously (UI thread).</summary>
    public event Action? NotesShutdown;

    // ---- calling the core -----------------------------------------------------------------

    /// <summary>
    /// Runs a notes call off the UI thread. Errors come back as a friendly text and the core's code; <paramref name="message"/>
    /// words them when the call has its own wording (attachments, export, import). A file the call could not read or write
    /// itself comes back as the code IO.
    /// </summary>
    public async Task<NotesResult<T>> NotesCallAsync<T>(Func<NotesApi, T> call, Func<string?, string>? message = null)
    {
        try
        {
            return NotesResult<T>.Success(await Task.Run(() => call(Notes)));
        }
        catch (CoreException ex)
        {
            AppLog.Write("notes call failed: " + ex.Code); // the code only: never text, titles or paths
            return NotesResult<T>.Fail(ex.Code, message);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            AppLog.Error("notes file", ex);
            return NotesResult<T>.Fail("IO", message);
        }
        catch (Exception ex) when (IsMalformedReply(ex))
        {
            AppLog.Error("notes reply", ex);
            return NotesResult<T>.Fail("INTERNAL", message);
        }
    }

    /// <summary>Where the preview gets attachments from: pictures are cached under %LOCALAPPDATA%\ClipBridge\notes-cache, other files are exported to a temporary folder to be opened.</summary>
    public AttachmentResolver Attachments =>
        _attachments ??= new AttachmentResolver(Notes, Path.Combine(App.DataDir, "notes-cache"), Path.Combine(Path.GetTempPath(), "ClipBridge-notes"));

    /// <summary>Runs an attachment lookup or export off the UI thread; errors are worded for attachments.</summary>
    public async Task<NotesResult<T>> AttachmentCallAsync<T>(Func<AttachmentResolver, T> call)
    {
        try
        {
            return NotesResult<T>.Success(await Task.Run(() => call(Attachments)));
        }
        catch (CoreException ex)
        {
            AppLog.Write("attachment call failed: " + ex.Code);
            return NotesResult<T>.Fail(ex.Code, ErrorMessages.ForNotesAttachment);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            AppLog.Error("attachment file", ex);
            return NotesResult<T>.Fail("IO", ErrorMessages.ForNotesAttachment);
        }
        catch (Exception ex) when (IsMalformedReply(ex))
        {
            AppLog.Error("attachment reply", ex);
            return NotesResult<T>.Fail("INTERNAL", ErrorMessages.ForNotesAttachment);
        }
    }

    // ---- events ---------------------------------------------------------------------------

    /// <summary>Called by <c>Dispatch</c> on the UI thread for every notes.* event.</summary>
    private void HandleNotesEvent(CoreEvent ev)
    {
        switch (ev)
        {
            case NotesChangedEvent changed:
                NotesChanged?.Invoke(changed);
                break;
            case NotesSyncUpdatedEvent updated:
                NotesSyncUpdated?.Invoke(updated);
                break;
            case NotesAttachmentEvent attachment:
                NotesAttachmentArrived?.Invoke(attachment);
                break;
            case NotesSyncConflictEvent conflict:
                // No titles in a notification: they would show on the desktop and in the notification centre.
                if (Main is not { IsVisible: true })
                {
                    var deleted = conflict.Reason == NotesSyncConflictEvent.DeletedElsewhere;
                    Balloon(Strings.NoteConflictBalloonTitle, deleted ? Strings.NoteDeletedElsewhereBalloon : Strings.NoteConflictBalloon,
                        () => ShowMainWindow(MainPage.Notes));
                }
                NotesConflict?.Invoke(conflict);
                break;
        }
    }

    /// <summary>The application is closing: lets the notes page save what is typed, then the core can go.</summary>
    private void FlushNotesOnExit()
    {
        try { NotesShutdown?.Invoke(); }
        catch (Exception ex) { AppLog.Error("notes flush on exit", ex); }
    }

    // ---- preferences and links --------------------------------------------------------------

    public NotesViewMode NotesView => Prefs.NotesView;

    public void SetNotesView(NotesViewMode mode)
    {
        if (Prefs.NotesView == mode) return;
        Prefs.NotesView = mode;
        Prefs.Save(App.DataDir);
    }

    /// <summary>Opens an exported attachment with the default program. The caller has already asked about programs and scripts. Returns an error text or null.</summary>
    public string? OpenAttachmentFile(string path) => Launch(new ProcessStartInfo(path) { UseShellExecute = true });

    /// <summary>Opens a link of a note: only http, https and mailto (see <see cref="NoteLinks"/>). Returns an error text or null.</summary>
    public string? OpenNoteLink(string? href)
    {
        var uri = NoteLinks.TryGetOpenableUri(href);
        if (uri is null) return Strings.NoteLinkNotOpened;
        return Launch(new ProcessStartInfo(uri.AbsoluteUri) { UseShellExecute = true });
    }
}
