package com.nightclip.app.ui.notes

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.NoteConflict
import com.nightclip.app.core.NotesApi
import com.nightclip.app.logic.FlatBlock
import com.nightclip.app.logic.NotesErrors
import com.nightclip.app.logic.NotesRenderSpans
import com.nightclip.app.logic.NotesTexts
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** One side of a conflict as the user sees it: the title and the picture of the text. */
data class ConflictSide(val title: String, val updatedAt: Long, val deleted: Boolean, val blocks: List<FlatBlock>)

data class ConflictUi(
    val loading: Boolean = true,
    /** The note that stays (the winner of the merge). */
    val main: ConflictSide? = null,
    /** The conflict copy: the older side, kept as a note of its own. */
    val copy: ConflictSide? = null,
    val error: String? = null,
    val resolving: Boolean = false,
)

/** The screen that settles one conflict copy with `notes.sync_resolve`. */
class NotesConflictViewModel(application: Application) : AndroidViewModel(application) {
    private val _ui = MutableStateFlow(ConflictUi())
    val ui: StateFlow<ConflictUi> = _ui.asStateFlow()

    private val _messages = MutableSharedFlow<String>(extraBufferCapacity = 8)
    val messages: SharedFlow<String> = _messages.asSharedFlow()

    private var shown: NoteConflict? = null

    /** Loads both notes; asking again for the conflict that is on screen does nothing. */
    fun load(c: NoteConflict, force: Boolean = false) {
        if (!force && shown == c) return
        shown = c
        _ui.value = ConflictUi()
        viewModelScope.launch {
            val main = async { side(c.noteId) }
            val copy = async { side(c.conflictNoteId) }
            val m = main.await()
            val k = copy.await()
            _ui.value = when {
                m is Side.Failed -> ConflictUi(loading = false, error = m.message)
                k is Side.Failed -> ConflictUi(loading = false, error = k.message)
                else -> ConflictUi(loading = false, main = (m as Side.Loaded).side, copy = (k as Side.Loaded).side)
            }
        }
    }

    private sealed interface Side {
        data class Loaded(val side: ConflictSide) : Side
        data class Failed(val message: String) : Side
    }

    private suspend fun side(noteId: String): Side {
        val doc = NotesApi.get(noteId)
        val rendered = NotesApi.render(noteId)
        val d = doc.okOrNull
        val r = rendered.okOrNull
        return when {
            doc is ApiResult.Fail -> Side.Failed(NotesErrors.message(doc.code, NotesErrors.Context.RESOLVE))
            rendered is ApiResult.Fail -> Side.Failed(NotesErrors.message(rendered.code, NotesErrors.Context.RESOLVE))
            d != null && r != null -> Side.Loaded(ConflictSide(d.title, d.updatedAt, d.deleted, NotesRenderSpans.flatten(r.blocks)))
            else -> Side.Failed(NotesErrors.message("INTERNAL"))
        }
    }

    /** [action]: `NotesApi.KEEP_MAIN`, `KEEP_CONFLICT` or `MERGE`. [onDone] runs after the core settled it. */
    fun resolve(c: NoteConflict, action: String, onDone: (String) -> Unit) {
        if (_ui.value.resolving) return
        _ui.update { it.copy(resolving = true) }
        viewModelScope.launch {
            when (val r = NotesApi.resolve(c.noteId, c.conflictNoteId, action)) {
                is ApiResult.Ok -> {
                    shown = null
                    onDone(action)
                }
                is ApiResult.Fail -> {
                    _ui.update { it.copy(resolving = false) }
                    _messages.tryEmit(NotesErrors.message(r.code, NotesErrors.Context.RESOLVE))
                }
            }
        }
    }

    /** The original note is in the trash and "use the copy" / "merge" cannot write into it: take it out first. */
    fun restoreMain(c: NoteConflict) {
        viewModelScope.launch {
            when (val r = NotesApi.restore(c.noteId)) {
                is ApiResult.Ok -> {
                    _messages.tryEmit(NotesTexts.RESTORED)
                    load(c, force = true)
                }
                is ApiResult.Fail -> _messages.tryEmit(NotesErrors.message(r.code))
            }
        }
    }
}
