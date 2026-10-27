using ClipBridge.Windows.Interop;

namespace ClipBridge.Windows.Logic;

/// <summary>
/// Typed wrapper over the notes.* methods (notes/api-spec.md). Every method blocks: run it off the UI thread.
/// Failures throw <see cref="CoreException"/> with the core's code (NOT_FOUND, CONFLICT, TOO_LARGE, ...). All times in
/// the answers are Unix milliseconds. The core owns every rule (normalising, merging, rendering); this class only
/// carries parameters and reads answers.
/// </summary>
public sealed class NotesApi
{
    private readonly ICoreCaller _core;
    public NotesApi(ICoreCaller core) => _core = core;

    // ---- notes -------------------------------------------------------------------------------
    /// <summary>One page of notes, newest first. <paramref name="deleted"/> lists the recycle bin.</summary>
    public NotesPage List(bool deleted = false, int limit = NotesList.PageSize, string cursor = "")
    {
        var p = new Dictionary<string, object?> { ["limit"] = limit };
        if (deleted) p["deleted"] = true;
        if (cursor.Length > 0) p["cursor"] = cursor;
        return NotesPage.FromJson(_core.Call("notes.list", p));
    }

    /// <summary>The newest revision of a note, or the one named by <paramref name="revId"/>.</summary>
    public NoteDetail Get(string noteId, string revId = "")
    {
        var p = new Dictionary<string, object?> { ["note_id"] = noteId };
        if (revId.Length > 0) p["rev_id"] = revId;
        return NoteDetail.FromJson(_core.Call("notes.get", p));
    }

    public NoteCreated Create(string title = "", string body = "")
    {
        var p = new Dictionary<string, object?>();
        if (title.Length > 0) p["title"] = title;
        if (body.Length > 0) p["body"] = body;
        return NoteCreated.FromJson(_core.Call("notes.create", p));
    }

    /// <summary>
    /// Stores an edit made on revision <paramref name="baseRevId"/>. A null <paramref name="title"/> or
    /// <paramref name="body"/> leaves that part as it is in the base revision. When the base is no longer the newest
    /// revision the core merges instead of overwriting.
    /// </summary>
    public NoteSaveResult Update(string noteId, string baseRevId, string? title, string? body)
    {
        var p = new Dictionary<string, object?> { ["note_id"] = noteId };
        if (baseRevId.Length > 0) p["base_rev_id"] = baseRevId;
        if (title is not null) p["title"] = title;
        if (body is not null) p["body"] = body;
        return NoteSaveResult.FromJson(_core.Call("notes.update", p));
    }

    /// <summary>Moves a note to the recycle bin; returns the new revision id.</summary>
    public string Delete(string noteId) => JsonRead.Str(_core.Call("notes.delete", new { note_id = noteId }), "rev_id");

    public string Restore(string noteId) => JsonRead.Str(_core.Call("notes.restore", new { note_id = noteId }), "rev_id");

    public NoteRevisionPage History(string noteId, int limit = 50, string cursor = "")
    {
        var p = new Dictionary<string, object?> { ["note_id"] = noteId, ["limit"] = limit };
        if (cursor.Length > 0) p["cursor"] = cursor;
        return NoteRevisionPage.FromJson(_core.Call("notes.history", p));
    }

    // ---- rendering ---------------------------------------------------------------------------
    /// <summary>The render tree of a stored revision (the newest, or <paramref name="revId"/>): for history views.</summary>
    public RenderTree Render(string noteId, string revId = "")
    {
        var p = new Dictionary<string, object?> { ["note_id"] = noteId };
        if (revId.Length > 0) p["rev_id"] = revId;
        return RenderTree.FromJson(_core.Call("notes.render", p));
    }

    /// <summary>The render tree of a text that is not saved (the live preview). The answer has no note or revision id.</summary>
    public RenderTree RenderBody(string body) => RenderTree.FromJson(_core.Call("notes.render", new { body }));

    public NoteNormalized Normalize(string body) => NoteNormalized.FromJson(_core.Call("notes.normalize", new { body }));

    /// <summary>
    /// Full-text search over the notes that are not in the trash (every word must occur). An empty query is INVALID: the
    /// caller shows the normal list instead. Highlights are in code points (<see cref="CodePointText"/>).
    /// </summary>
    public NoteSearchPage Search(string query, int limit = NoteSearchResults.PageSize, string cursor = "")
    {
        var p = new Dictionary<string, object?> { ["query"] = query, ["limit"] = limit };
        if (cursor.Length > 0) p["cursor"] = cursor;
        return NoteSearchPage.FromJson(_core.Call("notes.search", p));
    }

    // ---- attachments -------------------------------------------------------------------------
    /// <summary>Stores the file at the absolute <paramref name="path"/> (at most 32 MiB); the answer's Markdown is what to insert into the note.</summary>
    public NoteAttachment AttachmentAdd(string path) => NoteAttachment.FromJson(_core.Call("notes.attachment_add", new { path }));

    public NoteAttachment AttachmentGet(string blobId) => NoteAttachment.FromJson(_core.Call("notes.attachment_get", new { blob_id = blobId }));

    /// <summary>Writes the attachment to the absolute <paramref name="path"/> (an existing plain file is replaced; the folder must exist). NOT_FOUND when it is not here.</summary>
    public NoteAttachment AttachmentExport(string blobId, string path) =>
        NoteAttachment.FromJson(_core.Call("notes.attachment_export", new { blob_id = blobId, path }));

    /// <summary>Deletes attachments that no revision of any note refers to any more (and are older than a week).</summary>
    public AttachmentCleanupResult AttachmentCleanup() => AttachmentCleanupResult.FromJson(_core.Call("notes.attachment_cleanup"));

    // ---- export and import ---------------------------------------------------------------------
    /// <summary>Writes a ZIP of all notes to the absolute <paramref name="path"/>. The core never overwrites: INVALID when the file exists.</summary>
    public NoteExportResult Export(string path, bool includeTrash)
    {
        var p = new Dictionary<string, object?> { ["path"] = path };
        if (includeTrash) p["include_trash"] = true;
        return NoteExportResult.FromJson(_core.Call("notes.export", p));
    }

    /// <summary>Imports a .zip, a .md/.markdown/.txt file or a folder (absolute path) as new notes.</summary>
    public NoteImportResult Import(string path) => NoteImportResult.FromJson(_core.Call("notes.import", new { path }));

    // ---- sync --------------------------------------------------------------------------------
    public NotesSyncStatus SyncStatus() => NotesSyncStatus.FromJson(_core.Call("notes.sync_status"));

    /// <summary>
    /// Starts a round with one peer (empty: all). Only the dialling side acts on it; Windows normally listens, so the
    /// UI offers no such button. Throws NOT_PAIRED for an unknown peer.
    /// </summary>
    public bool SyncNow(string peerId = "")
    {
        var p = new Dictionary<string, object?>();
        if (peerId.Length > 0) p["peer_id"] = peerId;
        return JsonRead.Bool(_core.Call("notes.sync_now", p), "started");
    }

    public NoteConflictPage Conflicts(int limit = 50, string cursor = "")
    {
        var p = new Dictionary<string, object?> { ["limit"] = limit };
        if (cursor.Length > 0) p["cursor"] = cursor;
        return NoteConflictPage.FromJson(_core.Call("notes.sync_conflicts", p));
    }

    /// <summary>Settles a conflict copy (<see cref="ConflictActions"/>); returns the original note's revision id.</summary>
    public string Resolve(string noteId, string conflictNoteId, string action) =>
        JsonRead.Str(_core.Call("notes.sync_resolve", new { note_id = noteId, conflict_note_id = conflictNoteId, action }), "rev_id");
}
