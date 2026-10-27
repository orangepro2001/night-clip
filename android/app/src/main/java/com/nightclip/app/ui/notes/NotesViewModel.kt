package com.nightclip.app.ui.notes

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.CoreEvent
import com.nightclip.app.core.CoreHost
import com.nightclip.app.core.CoreState
import com.nightclip.app.core.NoteConflict
import com.nightclip.app.core.NoteSummary
import com.nightclip.app.core.NotesApi
import com.nightclip.app.core.NotesSyncStatus
import com.nightclip.app.logic.NotesErrors
import com.nightclip.app.logic.NotesListLogic
import com.nightclip.app.logic.NotesListState
import com.nightclip.app.logic.NotesSyncLogic
import com.nightclip.app.logic.NotesTexts
import com.nightclip.app.platform.NotesSession
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/**
 * The notes tab outside the editor: the list (live notes or trash) with paging, the sync status line, conflict copies,
 * notices about notes deleted on another device, and the navigation stack. Scoped to the activity like the vault's
 * view models, so the stack (and an open editor) survives leaving the tab and rotating the phone.
 */
class NotesViewModel(application: Application) : AndroidViewModel(application) {
    private val _list = MutableStateFlow(NotesListState())
    val list: StateFlow<NotesListState> = _list.asStateFlow()

    private val _refreshing = MutableStateFlow(false)
    val refreshing: StateFlow<Boolean> = _refreshing.asStateFlow()

    private val _sync = MutableStateFlow(NotesSyncStatus.EMPTY)
    val sync: StateFlow<NotesSyncStatus> = _sync.asStateFlow()

    private val _conflicts = MutableStateFlow<List<NoteConflict>>(emptyList())
    val conflicts: StateFlow<List<NoteConflict>> = _conflicts.asStateFlow()

    private val _notices = MutableStateFlow<List<DeletedNotice>>(emptyList())
    val notices: StateFlow<List<DeletedNotice>> = _notices.asStateFlow()

    private val _route = MutableStateFlow<List<NotesRoute>>(emptyList())
    val route: StateFlow<List<NotesRoute>> = _route.asStateFlow()

    private val _messages = MutableSharedFlow<String>(extraBufferCapacity = 16)
    val messages: SharedFlow<String> = _messages.asSharedFlow()

    private val _undo = MutableSharedFlow<UndoRequest>(extraBufferCapacity = 4)
    val undo: SharedFlow<UndoRequest> = _undo.asSharedFlow()

    private var sessionCounter = 0L
    private var reloadScheduled = false
    private var loadingMore = false
    private val noticeTitles = HashMap<String, String>()

    init {
        viewModelScope.launch {
            CoreHost.state.first { it is CoreState.Ready }
            reloadNow()
            refreshSync()
            refreshConflicts()
        }
        viewModelScope.launch { CoreHost.events.collect(::onEvent) }
        viewModelScope.launch { NotesSession.deletedElsewhere.collect { ids -> _notices.value = ids.map { DeletedNotice(it, titleOf(it)) } } }
    }

    private fun say(text: String) {
        _messages.tryEmit(text)
    }

    private fun fail(r: ApiResult.Fail, context: NotesErrors.Context = NotesErrors.Context.GENERAL) {
        say(NotesErrors.message(r.code, context))
    }

    // ---- events ------------------------------------------------------------------------------

    private fun onEvent(ev: CoreEvent) {
        when (ev) {
            is CoreEvent.NotesChanged -> {
                scheduleReload()
                refreshConflictsSoon()
            }
            is CoreEvent.NotesSyncUpdated -> {
                val updated = NotesSyncLogic.applyUpdated(_sync.value, ev.peerId, ev.state, ev.behind)
                if (updated != null) _sync.value = updated
                // A finished round has a new "last synced" time, and an unknown device means the list changed.
                if (updated == null || ev.state != NotesSyncLogic.STATE_SYNCING) viewModelScope.launch { refreshSync() }
            }
            is CoreEvent.NotesSyncConflict -> if (ev.reason == "diverged") refreshConflictsSoon()
            else -> Unit
        }
    }

    // ---- list --------------------------------------------------------------------------------

    /** The tab became visible (or the app came back to it): everything is read again and a sync round is asked for. */
    fun onScreenStart() {
        viewModelScope.launch {
            reloadNow()
            refreshSync()
            refreshConflicts()
            NotesSession.syncIfDue()
        }
    }

    /** A burst of `notes.changed` (a sync brings many) becomes one reload shortly after the first event. */
    private fun scheduleReload() {
        if (reloadScheduled) return
        reloadScheduled = true
        viewModelScope.launch {
            delay(RELOAD_DELAY_MS)
            reloadScheduled = false
            reloadNow()
        }
    }

    private var conflictsScheduled = false

    private fun refreshConflictsSoon() {
        if (conflictsScheduled) return
        conflictsScheduled = true
        viewModelScope.launch {
            delay(RELOAD_DELAY_MS)
            conflictsScheduled = false
            refreshConflicts()
        }
    }

    private suspend fun reloadNow() {
        val before = _list.value
        val r = NotesApi.list(NotesListLogic.reloadLimit(before), null, before.trash)
        if (r is ApiResult.Ok && _list.value.trash == before.trash) _list.update { NotesListLogic.firstPage(it, r.value) }
        else if (r is ApiResult.Fail && !before.loaded) fail(r)
    }

    fun setTrash(trash: Boolean) {
        if (_list.value.trash == trash) return
        _list.update { NotesListLogic.switchMode(it, trash) }
        viewModelScope.launch { reloadNow() }
    }

    fun loadMore() {
        val state = _list.value
        val cursor = state.nextCursor ?: return
        if (loadingMore) return
        loadingMore = true
        viewModelScope.launch {
            try {
                val r = NotesApi.list(NotesListLogic.PAGE_SIZE, cursor, state.trash)
                // The list was reloaded or switched meanwhile: this page no longer belongs after it.
                if (r is ApiResult.Ok && _list.value.nextCursor == cursor && _list.value.trash == state.trash) {
                    _list.update { NotesListLogic.nextPage(it, r.value) }
                }
            } finally {
                loadingMore = false
            }
        }
    }

    /** Pull to refresh: ask the other devices for news and read the list again. */
    fun pullRefresh() {
        if (_refreshing.value) return
        _refreshing.value = true
        viewModelScope.launch {
            try {
                NotesSession.syncNow()
                reloadNow()
                refreshSync()
                refreshConflicts()
            } finally {
                _refreshing.value = false
            }
        }
    }

    // ---- delete and restore ------------------------------------------------------------------

    fun delete(item: NoteSummary) {
        viewModelScope.launch {
            when (val r = NotesApi.delete(item.noteId)) {
                is ApiResult.Ok -> {
                    _list.update { NotesListLogic.remove(it, item.noteId) }
                    _undo.tryEmit(UndoRequest(NotesTexts.TRASHED, item.noteId))
                    scheduleReload()
                }
                is ApiResult.Fail -> fail(r)
            }
        }
    }

    /** A note was moved to the trash from the editor. */
    fun trashed(noteId: String) {
        _undo.tryEmit(UndoRequest(NotesTexts.TRASHED, noteId))
        scheduleReload()
    }

    /** Undo of a delete, or "restore" in the trash. */
    fun restore(noteId: String, announce: Boolean = false) {
        viewModelScope.launch {
            when (val r = NotesApi.restore(noteId)) {
                is ApiResult.Ok -> {
                    _list.update { if (it.trash) NotesListLogic.remove(it, noteId) else it } // the live list gets it back with the reload
                    if (announce) say(NotesTexts.RESTORED)
                    scheduleReload()
                }
                is ApiResult.Fail -> fail(r)
            }
        }
    }

    // ---- sync status and conflicts -----------------------------------------------------------

    suspend fun refreshSync() {
        (NotesApi.syncStatus() as? ApiResult.Ok)?.let { _sync.value = it.value }
    }

    suspend fun refreshConflicts() {
        (NotesApi.conflicts() as? ApiResult.Ok)?.let { _conflicts.value = it.value.items }
    }

    /** The "sync now" button. */
    fun syncNow() {
        viewModelScope.launch {
            when (val r = NotesSession.syncNow()) {
                is ApiResult.Ok -> Unit
                is ApiResult.Fail -> fail(r, NotesErrors.Context.SYNC)
            }
            refreshSync()
        }
    }

    /** A conflict was settled: the lists change and the screen goes back. */
    fun conflictResolved(action: String) {
        say(NotesTexts.resolved(action))
        viewModelScope.launch {
            refreshConflicts()
            reloadNow()
        }
        pop()
    }

    // ---- notices -----------------------------------------------------------------------------

    private suspend fun titleOf(noteId: String): String {
        noticeTitles[noteId]?.let { return it }
        val title = (NotesApi.get(noteId) as? ApiResult.Ok)?.value?.title.orEmpty()
        noticeTitles[noteId] = title
        return title
    }

    fun dismissNotice(noteId: String) {
        NotesSession.dismissDeletedElsewhere(noteId)
        noticeTitles.remove(noteId)
    }

    // ---- navigation --------------------------------------------------------------------------

    fun openNote(noteId: String) = push(NotesRoute.Editor(++sessionCounter, noteId))

    fun newNote() = push(NotesRoute.Editor(++sessionCounter, null))

    fun openConflicts() {
        val all = _conflicts.value
        push(if (all.size == 1) NotesRoute.ConflictDetail(all.single()) else NotesRoute.Conflicts)
    }

    fun openConflict(c: NoteConflict) = push(NotesRoute.ConflictDetail(c))

    private fun push(r: NotesRoute) {
        _route.update { it + r }
    }

    fun pop() {
        _route.update { it.dropLast(1) }
    }

    private companion object {
        const val RELOAD_DELAY_MS = 250L
    }
}
