package com.nightclip.app.ui.notes

import android.app.Application
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.CoreApi
import com.nightclip.app.core.CoreEvent
import com.nightclip.app.core.CoreHost
import com.nightclip.app.core.NoteDoc
import com.nightclip.app.core.NotesApi
import com.nightclip.app.core.RevisionInfo
import com.nightclip.app.logic.EditorCommand
import com.nightclip.app.logic.EditorState
import com.nightclip.app.logic.FlatBlock
import com.nightclip.app.logic.MarkdownEdit
import com.nightclip.app.logic.MdAction
import com.nightclip.app.logic.NoteEditorLogic
import com.nightclip.app.logic.NotesErrors
import com.nightclip.app.logic.NotesRenderSpans
import com.nightclip.app.logic.NotesTexts
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
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
import kotlinx.coroutines.withTimeoutOrNull

/** What the preview tab shows. */
sealed interface PreviewState {
    data object Idle : PreviewState
    data object Loading : PreviewState

    /** A note without any text yet. */
    data object Empty : PreviewState

    /** [stale]: there is text that could not be saved, so the preview shows the last saved version. */
    data class Ready(val blocks: List<FlatBlock>, val stale: Boolean) : PreviewState
    data class Failed(val message: String) : PreviewState
}

data class HistoryRow(val rev: RevisionInfo, val origin: String, val current: Boolean)

/** One old revision opened read-only. [doc] and [blocks] are null while loading. */
data class RevisionView(
    val rev: RevisionInfo,
    val origin: String,
    val doc: NoteDoc? = null,
    val blocks: List<FlatBlock>? = null,
    val error: String? = null,
)

data class HistoryUi(
    val open: Boolean = false,
    val loading: Boolean = false,
    val rows: List<HistoryRow> = emptyList(),
    val nextCursor: String? = null,
    val error: String? = null,
    val viewing: RevisionView? = null,
)

/**
 * The editing session of one note on screen. [NoteEditorLogic] decides everything; this class runs its commands
 * against the core, holds the text fields, and renders the preview and the history. It is scoped to the activity:
 * leaving the notes tab or rotating the phone keeps the session, so nothing typed is lost.
 *
 * Calls to the core are started in [scope], which is not cancelled with the screen: a save that began before the user
 * left finishes and is applied.
 */
class NoteEditorViewModel(application: Application) : AndroidViewModel(application) {
    private val logic = NoteEditorLogic(now = { System.currentTimeMillis() })

    /** Main thread, so the logic is only ever touched from there; never cancelled, so a save in flight completes. */
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)

    /** Compose snapshot state, not a flow: a flow can lose characters of a composition that is going on. */
    var titleField by mutableStateOf(TextFieldValue(""))
        private set
    var bodyField by mutableStateOf(TextFieldValue(""))
        private set

    private val _state = MutableStateFlow(logic.state)
    val state: StateFlow<EditorState> = _state.asStateFlow()

    private val _mode = MutableStateFlow(EditorMode.SOURCE)
    val mode: StateFlow<EditorMode> = _mode.asStateFlow()

    private val _preview = MutableStateFlow<PreviewState>(PreviewState.Idle)
    val preview: StateFlow<PreviewState> = _preview.asStateFlow()

    private val _history = MutableStateFlow(HistoryUi())
    val history: StateFlow<HistoryUi> = _history.asStateFlow()

    /** True while the "leave and lose the unsaved text?" question is on screen. */
    private val _leaveBlocked = MutableStateFlow(false)
    val leaveBlocked: StateFlow<Boolean> = _leaveBlocked.asStateFlow()

    private val _messages = MutableSharedFlow<String>(extraBufferCapacity = 16)
    val messages: SharedFlow<String> = _messages.asSharedFlow()

    private var currentSession = NO_SESSION
    private var timer: Job? = null
    private var previewJob: Job? = null
    private var historyJob: Job? = null
    private var deviceNames: Map<String, String> = emptyMap()
    private var selfDeviceId = ""

    init {
        viewModelScope.launch {
            CoreHost.events.collect { ev ->
                if (ev is CoreEvent.NotesChanged) run(logic.onExternalChange(ev.noteId, ev.revId))
            }
        }
    }

    private fun say(text: String) {
        _messages.tryEmit(text)
    }

    /** A message from a composable of this session (for example a link that could not be opened). */
    fun report(text: String) = say(text)

    // ---- running the logic's commands --------------------------------------------------------

    private fun run(commands: List<EditorCommand>) {
        for (c in commands) {
            when (c) {
                is EditorCommand.Load -> load(c)
                is EditorCommand.Save -> save(c)
                is EditorCommand.Notify -> say(c.notice.text)
            }
        }
        publish()
    }

    private fun load(c: EditorCommand.Load) {
        scope.launch {
            when (val r = NotesApi.get(c.noteId)) {
                is ApiResult.Ok -> run(logic.onLoaded(c.ticket, r.value))
                is ApiResult.Fail -> run(logic.onLoadFailed(c.ticket, r.code))
            }
        }
    }

    private fun save(c: EditorCommand.Save) {
        scope.launch {
            val noteId = c.noteId
            if (noteId == null) {
                when (val r = NotesApi.create(c.title.orEmpty(), c.body.orEmpty())) {
                    is ApiResult.Ok -> run(logic.onSaved(c.ticket, r.value.noteId, r.value.revId, false, null))
                    is ApiResult.Fail -> run(logic.onSaveFailed(c.ticket, r.code))
                }
            } else {
                when (val r = NotesApi.update(noteId, c.baseRevId, c.title, c.body)) {
                    is ApiResult.Ok -> run(logic.onSaved(c.ticket, noteId, r.value.revId, r.value.merged, r.value.conflictNoteId))
                    is ApiResult.Fail -> run(logic.onSaveFailed(c.ticket, r.code))
                }
            }
        }
    }

    /** Brings the text fields, the state flow and the save timer in line with the logic after every call into it. */
    private fun publish() {
        val s = logic.state
        // The logic is the truth: a load or a restore replaces the fields, an ignored edit (read-only) is taken back.
        if (titleField.text != s.title) titleField = TextFieldValue(s.title, TextRange(minOf(titleField.selection.end, s.title.length)))
        if (bodyField.text != s.body) bodyField = TextFieldValue(s.body, TextRange(minOf(bodyField.selection.end, s.body.length)))
        _state.value = s
        scheduleTimer()
    }

    private fun scheduleTimer() {
        timer?.cancel()
        timer = null
        val due = logic.nextDueAt() ?: return
        timer = viewModelScope.launch {
            delay((due - System.currentTimeMillis()).coerceAtLeast(0))
            timer = null
            run(logic.tick())
        }
    }

    /** Waits until nothing is on its way: a started save and a reload are answered, or a save failed for good. */
    private suspend fun awaitSettled() {
        withTimeoutOrNull(SETTLE_TIMEOUT_MS) { _state.first { it.settled } }
    }

    // ---- session -----------------------------------------------------------------------------

    /** Called when the editor route is shown: a new visit starts a session, a returning one carries on. */
    fun start(route: NotesRoute.Editor) {
        if (route.sessionId == currentSession) return
        currentSession = route.sessionId
        _mode.value = EditorMode.SOURCE
        _preview.value = PreviewState.Idle
        _history.value = HistoryUi()
        _leaveBlocked.value = false
        titleField = TextFieldValue("")
        bodyField = TextFieldValue("")
        run(if (route.noteId == null) logic.openDraft() else logic.open(route.noteId))
    }

    /** Opening the same note again after a failed open. */
    fun retryOpen(route: NotesRoute.Editor) {
        currentSession = NO_SESSION
        start(route)
    }

    /** The screen went away (back, another tab, rotation): save what was typed without waiting for the quiet period. */
    fun flushNow() {
        run(logic.flush())
    }

    /**
     * Back / up: saves, waits for the answer, then closes the session and calls [onDone]. If the text cannot be saved
     * the screen asks whether to stay or to lose it ([leaveBlocked]).
     */
    fun requestLeave(onDone: () -> Unit) {
        viewModelScope.launch {
            run(logic.flush())
            awaitSettled()
            if (logic.state.hasUnsavedText) {
                _leaveBlocked.value = true
                return@launch
            }
            endSession()
            onDone()
        }
    }

    fun keepEditing() {
        _leaveBlocked.value = false
    }

    fun discardAndLeave(onDone: () -> Unit) {
        endSession()
        onDone()
    }

    private fun endSession() {
        timer?.cancel()
        previewJob?.cancel()
        historyJob?.cancel()
        logic.close()
        currentSession = NO_SESSION
        _leaveBlocked.value = false
        _mode.value = EditorMode.SOURCE
        _preview.value = PreviewState.Idle
        _history.value = HistoryUi()
        titleField = TextFieldValue("")
        bodyField = TextFieldValue("")
        publish()
    }

    override fun onCleared() {
        // The activity is gone for good: send what is still unsaved; the request outlives this view model.
        run(logic.flush())
    }

    // ---- editing -----------------------------------------------------------------------------

    fun onTitleChange(v: TextFieldValue) {
        titleField = v
        logic.edit(v.text, bodyField.text)
        publish()
    }

    fun onBodyChange(v: TextFieldValue) {
        bodyField = v
        logic.edit(titleField.text, v.text)
        publish()
    }

    /** A toolbar button. */
    fun apply(action: MdAction) {
        if (logic.state.readOnly) return
        val v = bodyField
        val r = MarkdownEdit.apply(action, v.text, v.selection.min, v.selection.max)
        onBodyChange(TextFieldValue(r.text, TextRange(r.selStart, r.selEnd)))
    }

    // ---- trash -------------------------------------------------------------------------------

    /** The banner button of a note that is in the trash. */
    fun restoreFromTrash() {
        val id = logic.state.noteId ?: return
        scope.launch {
            when (val r = NotesApi.restore(id)) {
                is ApiResult.Ok -> {
                    run(logic.onRestored())
                    say(NotesTexts.RESTORED)
                }
                is ApiResult.Fail -> say(NotesErrors.message(r.code))
            }
        }
    }

    /** "Move to the trash": saves first so the text is in the history, then deletes and calls [onDone] with the note id. */
    fun trashCurrent(onDone: (String?) -> Unit) {
        viewModelScope.launch {
            run(logic.flush())
            awaitSettled()
            val id = logic.state.noteId
            if (id == null) {
                // A new note that was never saved: there is nothing to put in the trash.
                endSession()
                onDone(null)
                return@launch
            }
            when (val r = NotesApi.delete(id)) {
                is ApiResult.Ok -> {
                    endSession()
                    onDone(id)
                }
                is ApiResult.Fail -> say(NotesErrors.message(r.code))
            }
        }
    }

    // ---- preview -----------------------------------------------------------------------------

    fun setMode(mode: EditorMode) {
        _mode.value = mode
        previewJob?.cancel()
        if (mode == EditorMode.PREVIEW) loadPreview() else _preview.value = PreviewState.Idle
    }

    /** The core draws from a saved revision, so the pending text is saved first (nothing is rendered from the shell's own parsing). */
    private fun loadPreview() {
        previewJob = viewModelScope.launch {
            _preview.value = PreviewState.Loading
            run(logic.flush())
            awaitSettled()
            val s = logic.state
            val id = s.noteId
            if (id == null) {
                _preview.value = PreviewState.Empty
                return@launch
            }
            _preview.value = when (val r = NotesApi.render(id)) {
                is ApiResult.Ok -> PreviewState.Ready(NotesRenderSpans.flatten(r.value.blocks), stale = s.hasUnsavedText)
                is ApiResult.Fail -> PreviewState.Failed(NotesErrors.message(r.code, NotesErrors.Context.OPEN))
            }
        }
    }

    // ---- history -----------------------------------------------------------------------------

    fun openHistory() {
        historyJob?.cancel()
        _history.value = HistoryUi(open = true, loading = true)
        historyJob = viewModelScope.launch {
            run(logic.flush())
            awaitSettled()
            val id = logic.state.noteId
            if (id == null) {
                _history.value = HistoryUi(open = true, error = NotesTexts.HISTORY_NONE)
                return@launch
            }
            loadDeviceNames()
            when (val r = NotesApi.history(id, HISTORY_PAGE, null)) {
                is ApiResult.Ok -> _history.value = HistoryUi(open = true, rows = rows(r.value.items), nextCursor = r.value.nextCursor)
                is ApiResult.Fail -> _history.value = HistoryUi(open = true, error = NotesErrors.message(r.code))
            }
        }
    }

    fun loadMoreHistory() {
        val h = _history.value
        val cursor = h.nextCursor ?: return
        val id = logic.state.noteId ?: return
        if (h.loading) return
        _history.update { it.copy(loading = true) }
        viewModelScope.launch {
            when (val r = NotesApi.history(id, HISTORY_PAGE, cursor)) {
                is ApiResult.Ok -> _history.update { it.copy(loading = false, rows = it.rows + rows(r.value.items), nextCursor = r.value.nextCursor) }
                is ApiResult.Fail -> _history.update { it.copy(loading = false, error = NotesErrors.message(r.code)) }
            }
        }
    }

    private fun rows(items: List<RevisionInfo>): List<HistoryRow> {
        val base = logic.state.baseRevId
        return items.map { HistoryRow(it, NotesTexts.origin(it.originDevice, selfDeviceId, deviceNames), it.revId == base) }
    }

    private suspend fun loadDeviceNames() {
        selfDeviceId = CoreApi.identity().okOrNull?.first.orEmpty()
        deviceNames = CoreApi.peers().okOrNull.orEmpty().associate { it.deviceId to it.name }
    }

    fun closeHistory() {
        historyJob?.cancel()
        _history.value = HistoryUi()
    }

    /** Opens one old revision read-only: its text from `notes.get`, its picture from `notes.render`. */
    fun viewRevision(row: HistoryRow) {
        val id = logic.state.noteId ?: return
        _history.update { it.copy(viewing = RevisionView(row.rev, row.origin)) }
        viewModelScope.launch {
            val doc = NotesApi.get(id, row.rev.revId)
            val rendered = NotesApi.render(id, row.rev.revId)
            val view = when {
                doc is ApiResult.Fail -> RevisionView(row.rev, row.origin, error = NotesErrors.message(doc.code, NotesErrors.Context.OPEN))
                rendered is ApiResult.Fail -> RevisionView(row.rev, row.origin, error = NotesErrors.message(rendered.code, NotesErrors.Context.OPEN))
                else -> RevisionView(row.rev, row.origin, doc = doc.okOrNull, blocks = rendered.okOrNull?.let { NotesRenderSpans.flatten(it.blocks) })
            }
            // The user may have closed it or opened another one meanwhile.
            _history.update { if (it.viewing?.rev?.revId == row.rev.revId) it.copy(viewing = view) else it }
        }
    }

    fun closeRevision() {
        _history.update { it.copy(viewing = null) }
    }

    /**
     * "Restore this version" is an ordinary edit: the typed text is saved first (so it stays in the history), then the
     * old title and text become the content and are saved with `notes.update` on top of the newest revision.
     */
    fun restoreViewing() {
        val doc = _history.value.viewing?.doc ?: return
        viewModelScope.launch {
            run(logic.flush())
            awaitSettled()
            if (logic.state.readOnly) {
                say(NotesErrors.message("CONFLICT"))
                return@launch
            }
            logic.edit(doc.title, doc.body)
            run(logic.flush())
            _history.value = HistoryUi()
            say(NotesTexts.VERSION_RESTORED)
        }
    }

    private companion object {
        const val NO_SESSION = -1L
        const val SETTLE_TIMEOUT_MS = 10_000L
        const val HISTORY_PAGE = 50
    }
}
