package com.nightclip.app.ui.notes

import com.nightclip.app.core.NoteConflict

/** Where the notes tab is. The list is the base; the stack lives in [NotesViewModel] and survives leaving the tab. */
sealed interface NotesRoute {
    /**
     * [noteId] null = a new note that is created by its first save. [sessionId] tells one visit of the editor from
     * the next, so coming back to the tab (or rotating the phone) continues the session instead of reloading it.
     */
    data class Editor(val sessionId: Long, val noteId: String?) : NotesRoute

    /** Every conflict copy waiting for a decision. */
    data object Conflicts : NotesRoute

    data class ConflictDetail(val conflict: NoteConflict) : NotesRoute
}

/** Source (the Markdown text) or preview (the core's render tree drawn natively). */
enum class EditorMode { SOURCE, PREVIEW }

/** A note another device deleted while this device had a newer edit: kept, and the user is told once. */
data class DeletedNotice(val noteId: String, val title: String)

/** "Moved to the trash" with an undo button: the screen shows it as a snackbar. */
data class UndoRequest(val message: String, val noteId: String)
