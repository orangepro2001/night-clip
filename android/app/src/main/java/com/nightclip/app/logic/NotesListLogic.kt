package com.nightclip.app.logic

import com.nightclip.app.core.CoreEvent
import com.nightclip.app.core.NoteSummary
import com.nightclip.app.core.NotesPage

/** What the note list shows: the pages loaded so far of the live notes or of the trash. */
data class NotesListState(
    val items: List<NoteSummary> = emptyList(),
    /** Cursor of the next page; null = everything is loaded. */
    val nextCursor: String? = null,
    val trash: Boolean = false,
    /** True once the first page of this mode has arrived (an empty list before that is not "no notes"). */
    val loaded: Boolean = false,
) {
    val canLoadMore: Boolean get() = nextCursor != null
}

/** Cursor paging, live/trash modes and reaction to `notes.changed` for the note list. Pure. */
object NotesListLogic {
    const val PAGE_SIZE = 50

    /** The core's largest page (`notes.list` limit). */
    const val MAX_LIMIT = 500

    /** Switching between live notes and the trash starts from an empty list so the other mode's rows never flash. */
    fun switchMode(state: NotesListState, trash: Boolean): NotesListState =
        if (state.trash == trash) state else NotesListState(trash = trash)

    /** The first page replaces everything (initial load, refresh, reload after an event). */
    fun firstPage(state: NotesListState, page: NotesPage): NotesListState =
        state.copy(items = dedupe(page.items.filter { it.deleted == state.trash }), nextCursor = page.nextCursor, loaded = true)

    /** A following page is appended; a row that is already there (the list moved while paging) is not shown twice. */
    fun nextPage(state: NotesListState, page: NotesPage): NotesListState {
        val seen = state.items.mapTo(HashSet()) { it.noteId }
        val fresh = page.items.filter { it.deleted == state.trash && seen.add(it.noteId) }
        return state.copy(items = state.items + fresh, nextCursor = page.nextCursor, loaded = true)
    }

    /**
     * How many rows to ask for when reloading after a change, so the pages the user already scrolled through
     * stay in place: as many as are loaded, but at least one page.
     */
    fun reloadLimit(state: NotesListState): Int = state.items.size.coerceIn(PAGE_SIZE, MAX_LIMIT)

    /** Only `notes.changed` can change the rows; every other event is not about the list. */
    fun affectsList(ev: CoreEvent): Boolean = ev is CoreEvent.NotesChanged

    /** The row was deleted or restored here: it leaves this list at once (the reload that follows confirms it). */
    fun remove(state: NotesListState, noteId: String): NotesListState =
        state.copy(items = state.items.filterNot { it.noteId == noteId })

    /** Puts a row back in updated-at order (undo of a delete). */
    fun insert(state: NotesListState, item: NoteSummary): NotesListState {
        if (state.items.any { it.noteId == item.noteId }) return state
        val at = state.items.indexOfFirst { it.updatedAt < item.updatedAt }
        val items = state.items.toMutableList().apply { add(if (at < 0) size else at, item) }
        return state.copy(items = items)
    }

    private fun dedupe(items: List<NoteSummary>): List<NoteSummary> {
        val seen = HashSet<String>()
        return items.filter { seen.add(it.noteId) }
    }

    // ---- rows ----------------------------------------------------------------------------------

    fun title(item: NoteSummary): String = item.title.trim().ifEmpty { NotesTexts.UNTITLED }

    /**
     * The grey second line: the core's preview on one line. It is empty when it only repeats the title
     * (the core uses the first line of the text as the title of an untitled note).
     */
    fun previewLine(item: NoteSummary): String {
        val one = item.preview.replace(Regex("\\s+"), " ").trim()
        if (one.isEmpty()) return ""
        val title = item.title.replace(Regex("\\s+"), " ").trim()
        return if (title.isNotEmpty() && one == title) "" else one
    }

    fun updatedLabel(item: NoteSummary, nowMs: Long): String = NotesTexts.updatedLabel(item.updatedAt, nowMs)

    /** Conflict copies are shown first in a "needs attention" way elsewhere; here only the badge decision. */
    fun showsConflictBadge(item: NoteSummary): Boolean = item.isConflictCopy && !item.deleted
}
