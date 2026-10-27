package com.nightclip.app.logic

import com.nightclip.app.core.NoteDoc

enum class EditorPhase { IDLE, LOADING, READY, LOAD_FAILED }

/** What the label next to the editor says. */
enum class SaveStatus { CLEAN, PENDING, SAVING, FAILED, READ_ONLY }

/** A thing the user must be told after the editor changed on its own. */
sealed interface EditorNotice {
    val text: String

    /** The edit was merged into newer content from another device; the editor shows the merged text. */
    data object Merged : EditorNotice { override val text = NotesTexts.NOTICE_MERGED }

    /** Both sides touched the same block: the older side became the note [conflictNoteId]. */
    data class ConflictCopy(val conflictNoteId: String) : EditorNotice { override val text = NotesTexts.NOTICE_CONFLICT_COPY }

    /** The note was gone when the text was saved: it was saved as a new note instead. */
    data object SavedAsNew : EditorNotice { override val text = NotesTexts.NOTICE_SAVED_AS_NEW }

    /** The note went to the trash while it was open. */
    data object Trashed : EditorNotice { override val text = NotesTexts.NOTICE_TRASHED }
}

/** Work the host (the view model) must do for the editor and answer with one of the `on…` functions of [NoteEditorLogic]. */
sealed interface EditorCommand {
    /** `notes.get` of [noteId]; answer with [NoteEditorLogic.onLoaded] / [NoteEditorLogic.onLoadFailed] and the same [ticket]. */
    data class Load(val noteId: String, val ticket: Long) : EditorCommand

    /**
     * [noteId] null: `notes.create(title, body)`. Otherwise `notes.update(noteId, baseRevId, title, body)` where a null
     * field is left out of the request. Answer with [NoteEditorLogic.onSaved] / [NoteEditorLogic.onSaveFailed] and the same [ticket].
     */
    data class Save(val ticket: Long, val noteId: String?, val baseRevId: String, val title: String?, val body: String?) : EditorCommand

    data class Notify(val notice: EditorNotice) : EditorCommand
}

/** Snapshot of the editing session for the screen. */
data class EditorState(
    val phase: EditorPhase = EditorPhase.IDLE,
    /** Null while the note only exists in the editor (a new note that has not been saved yet). */
    val noteId: String? = null,
    val baseRevId: String = "",
    val title: String = "",
    val body: String = "",
    /** The text differs from what the core was last given. */
    val dirty: Boolean = false,
    /** A save request is out. */
    val saving: Boolean = false,
    /** A load request (open or reload) is out. */
    val loading: Boolean = false,
    /** The note is in the trash: it cannot be edited until it is restored. */
    val deleted: Boolean = false,
    val conflictOf: String = "",
    /** Core error code of the last failed save; null after a save worked. */
    val saveError: String? = null,
    val loadError: String? = null,
) {
    val readOnly: Boolean get() = deleted || phase != EditorPhase.READY

    val status: SaveStatus
        get() = when {
            deleted -> SaveStatus.READ_ONLY
            saving -> SaveStatus.SAVING
            saveError != null -> SaveStatus.FAILED
            dirty -> SaveStatus.PENDING
            else -> SaveStatus.CLEAN
        }

    /** Leaving now would lose text. */
    val hasUnsavedText: Boolean get() = dirty || saving

    /** Nothing is on its way and no save is waiting: what is on screen is what the core has (or a save failed for good). */
    val settled: Boolean get() = !saving && !loading && (!dirty || saveError != null || deleted)
}

/**
 * The editing state machine of one note (notes/api-spec.md `notes.update`, docs editor-and-markdown §7 item 3).
 * It owns no threads and no clock: the host executes the returned [EditorCommand]s, feeds the answers back and asks
 * [nextDueAt] when to call [tick].
 *
 * The rules:
 *  - The loaded revision is the base of every save; after a save the returned revision is the new base, so the
 *    next save is a plain successor. Only the fields that changed since the last save are sent.
 *  - A save starts [debounceMs] after the LAST edit. That also keeps an IME composition (many edits in a row) from
 *    producing a revision per keystroke.
 *  - One save is out at a time. Text typed meanwhile is sent by the next save.
 *  - If the core merged the edit or made a conflict copy, the editor is reloaded to show the newest text, but only if
 *    nothing was typed meanwhile. Typed text is never replaced: with unsaved text a newer revision is merged by the
 *    next save through its (older) base revision.
 *  - A `notes.changed` for the open note with a revision other than the base reloads silently when the text is clean.
 *  - [flush] (leaving the screen, onStop, switching note) starts a save at once.
 *
 * Not thread-safe: call it from one thread (the main thread).
 */
class NoteEditorLogic(private val now: () -> Long, private val debounceMs: Long = DEBOUNCE_MS) {
    companion object {
        const val DEBOUNCE_MS = 500L
    }

    private class Inflight(val ticket: Long, val title: String, val body: String)

    private var phase = EditorPhase.IDLE
    private var noteId: String? = null
    private var baseRevId = ""
    private var title = ""
    private var body = ""
    private var savedTitle = ""
    private var savedBody = ""
    private var deleted = false
    private var conflictOf = ""
    private var saveError: String? = null
    private var loadError: String? = null

    private var editSeq = 0L
    private var dueAt: Long? = null
    private var ticketCounter = 0L
    private var inflight: Inflight? = null
    private var loadTicket: Long? = null
    private var loadSeq = 0L
    private var pendingExternalRev: String? = null
    private var flushAfterSave = false
    private var reloadWhenClean = false

    /** A save was merged into newer content: tell the user once the merged text is on screen. */
    private var mergeNoticePending = false

    val state: EditorState
        get() = EditorState(
            phase = phase, noteId = noteId, baseRevId = baseRevId, title = title, body = body,
            dirty = isDirty(), saving = inflight != null, loading = loadTicket != null, deleted = deleted, conflictOf = conflictOf,
            saveError = saveError, loadError = loadError,
        )

    /** A new note that only exists in the editor is saved (created) once it has any text; an empty one never is. */
    private fun isDirty(): Boolean =
        if (noteId == null) (title.isNotBlank() || body.isNotBlank()) && (title != savedTitle || body != savedBody)
        else title != savedTitle || body != savedBody

    // ---- starting and ending a session -------------------------------------------------------

    /** Starts editing an existing note; ask the host for its text. Any earlier session is dropped (flush it first). */
    fun open(noteId: String): List<EditorCommand> {
        reset()
        this.noteId = noteId
        phase = EditorPhase.LOADING
        return listOf(requestLoad())
    }

    /** Starts a new note. It is created by the first save that has text. */
    fun openDraft(title: String = "", body: String = ""): List<EditorCommand> {
        reset()
        phase = EditorPhase.READY
        this.title = title
        this.body = body
        if (isDirty()) dueAt = now() + debounceMs
        return emptyList()
    }

    /** Forgets the session. Only call it when [EditorState.hasUnsavedText] is false or the user chose to discard. */
    fun close() = reset()

    private fun reset() {
        phase = EditorPhase.IDLE
        noteId = null
        baseRevId = ""
        title = ""
        body = ""
        savedTitle = ""
        savedBody = ""
        deleted = false
        conflictOf = ""
        saveError = null
        loadError = null
        dueAt = null
        inflight = null
        loadTicket = null
        pendingExternalRev = null
        flushAfterSave = false
        reloadWhenClean = false
        mergeNoticePending = false
        editSeq++
    }

    // ---- loading -----------------------------------------------------------------------------

    private fun requestLoad(): EditorCommand.Load {
        val id = checkNotNull(noteId) { "a draft has nothing to load" }
        ticketCounter++
        loadTicket = ticketCounter
        loadSeq = editSeq
        return EditorCommand.Load(id, ticketCounter)
    }

    fun onLoaded(ticket: Long, doc: NoteDoc): List<EditorCommand> {
        if (ticket != loadTicket) return emptyList()
        loadTicket = null
        val initial = phase == EditorPhase.LOADING
        if (!initial) {
            // A reload never replaces text the user typed since the request, nor one that is being saved.
            if (editSeq != loadSeq || inflight != null) {
                reloadWhenClean = true
                return emptyList()
            }
            // Already at this revision: the text on screen may differ only by normalisation; leave it alone.
            if (doc.revId == baseRevId) {
                deleted = doc.deleted
                return emptyList()
            }
        }
        baseRevId = doc.revId
        title = doc.title
        body = doc.body
        savedTitle = doc.title
        savedBody = doc.body
        deleted = doc.deleted
        conflictOf = doc.conflictOf
        saveError = null
        loadError = null
        phase = EditorPhase.READY
        dueAt = null
        editSeq++
        if (!mergeNoticePending) return emptyList()
        mergeNoticePending = false
        return listOf(EditorCommand.Notify(EditorNotice.Merged))
    }

    /** The open failed (then the screen shows [code]); a failed reload is ignored: the text on screen stays. */
    fun onLoadFailed(ticket: Long, code: String): List<EditorCommand> {
        if (ticket != loadTicket) return emptyList()
        loadTicket = null
        if (phase == EditorPhase.LOADING) {
            phase = EditorPhase.LOAD_FAILED
            loadError = code
        }
        return emptyList()
    }

    // ---- typing and saving -------------------------------------------------------------------

    /** The user changed the text. Ignored for a note that cannot be edited (still loading, in the trash). */
    fun edit(newTitle: String, newBody: String) {
        if (phase != EditorPhase.READY || deleted) return
        if (newTitle == title && newBody == body) return
        title = newTitle
        body = newBody
        editSeq++
        dueAt = if (isDirty()) now() + debounceMs else null
    }

    /**
     * When [tick] should be called next; null = no save is waiting for its time. While a request is out it is null too:
     * the answer decides what happens next, and the host asks again after every call.
     */
    fun nextDueAt(): Long? = if (inflight != null) null else dueAt

    /** Call at [nextDueAt]: starts the save if the quiet period is over. */
    fun tick(): List<EditorCommand> {
        val due = dueAt ?: return emptyList()
        if (now() < due || inflight != null) return emptyList()
        dueAt = null
        return startSave()
    }

    /** Save right now (leaving the screen, onStop, switching note, before a preview or a restore). */
    fun flush(): List<EditorCommand> {
        dueAt = null
        if (inflight != null) {
            if (isDirty()) flushAfterSave = true
            return emptyList()
        }
        return startSave()
    }

    private fun startSave(): List<EditorCommand> {
        if (phase != EditorPhase.READY || deleted || !isDirty()) return emptyList()
        ticketCounter++
        inflight = Inflight(ticketCounter, title, body)
        val id = noteId
        return listOf(
            if (id == null) EditorCommand.Save(ticketCounter, null, "", title, body)
            else EditorCommand.Save(ticketCounter, id, baseRevId, title.takeIf { it != savedTitle }, body.takeIf { it != savedBody }),
        )
    }

    /**
     * The core accepted the save. [noteId] is the note's id (the new one after a create). [changed] false = the edit
     * changed nothing once normalised (white space only): no revision was made and [revId] is just the note's current
     * one, which after a stale base can be another device's revision.
     */
    fun onSaved(ticket: Long, noteId: String, revId: String, merged: Boolean, conflictNoteId: String?, changed: Boolean = true): List<EditorCommand> {
        val sent = inflight?.takeIf { it.ticket == ticket } ?: return emptyList()
        inflight = null
        val out = mutableListOf<EditorCommand>()
        this.noteId = noteId
        savedTitle = sent.title
        savedBody = sent.body
        saveError = null
        val clean = title == savedTitle && body == savedBody
        // Nothing changed and the note is somewhere else than the base: the editor does not show its text.
        val diverged = merged || conflictNoteId != null || (!changed && revId != baseRevId)
        var reloading = false
        if (!diverged) {
            // The text sent is exactly what the newest revision holds: it is the next base.
            baseRevId = revId
        } else {
            // The newest revision differs from what was sent. The base stays the old one until the newest text
            // is loaded; text typed meanwhile is merged by the next save through that base.
            if (conflictNoteId != null) out += EditorCommand.Notify(EditorNotice.ConflictCopy(conflictNoteId)) else if (merged) mergeNoticePending = true
            if (clean) {
                out += requestLoad()
                reloading = true
            } else {
                reloadWhenClean = true
            }
        }
        val external = pendingExternalRev
        pendingExternalRev = null
        if (!reloading && clean && (reloadWhenClean || (external != null && external != revId)) && loadTicket == null) {
            out += requestLoad()
            reloading = true
        }
        if (reloading) reloadWhenClean = false
        if (flushAfterSave) {
            flushAfterSave = false
            out += startSave()
        }
        return out
    }

    /** The save failed with the core error [code]. The text stays in the editor and stays unsaved. */
    fun onSaveFailed(ticket: Long, code: String): List<EditorCommand> {
        if (inflight?.ticket != ticket) return emptyList()
        inflight = null
        flushAfterSave = false
        pendingExternalRev = null
        val out = mutableListOf<EditorCommand>()
        when {
            code == "NOT_FOUND" && noteId != null -> {
                // The note (or the revision the edit was based on) is gone: keep the text as a new note.
                noteId = null
                baseRevId = ""
                savedTitle = ""
                savedBody = ""
                saveError = null
                reloadWhenClean = false
                out += EditorCommand.Notify(EditorNotice.SavedAsNew)
                out += startSave()
            }
            code == "CONFLICT" -> {
                deleted = true
                saveError = null
                dueAt = null
                out += EditorCommand.Notify(EditorNotice.Trashed)
            }
            else -> saveError = code
        }
        return out
    }

    // ---- changes made elsewhere --------------------------------------------------------------

    /** A `notes.changed` event. */
    fun onExternalChange(eventNoteId: String, revId: String): List<EditorCommand> {
        if (phase != EditorPhase.READY || eventNoteId != noteId || revId == baseRevId) return emptyList()
        if (inflight != null) {
            pendingExternalRev = revId
            return emptyList()
        }
        // Typing: the next save merges against the newest revision through its (older) base revision.
        if (isDirty() || dueAt != null) return emptyList()
        if (loadTicket != null) return emptyList()
        return listOf(requestLoad())
    }

    /** The user restored the note from the trash. Unsaved text stays and is saved; otherwise the note is loaded again. */
    fun onRestored(): List<EditorCommand> {
        if (!deleted) return emptyList()
        deleted = false
        saveError = null
        if (isDirty()) {
            dueAt = now() + debounceMs
            return emptyList()
        }
        return if (noteId != null && loadTicket == null) listOf(requestLoad()) else emptyList()
    }
}
