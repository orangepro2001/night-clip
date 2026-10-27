package com.nightclip.app.ui.notes

import androidx.activity.compose.BackHandler
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.nightclip.app.ui.CollectWhileStarted

/** Id of the notes tab in the bottom navigation (see AppRoot). */
const val NOTES_TAB_ID = "notes"

/**
 * The "笔记" tab: the list, and over it the editor, the conflict list and a conflict. The view models are scoped to the
 * activity, so the stack and an editing session survive leaving the tab and rotating the phone.
 *
 * [onMessage] shows a plain snackbar; [onUndo] a snackbar with an "undo" button (message, action label, what undo does).
 * [onFullScreen] tells the host to hide the bottom navigation while a sub screen is open: the editor needs the room, and
 * its toolbar sits directly above the keyboard.
 */
@Composable
fun NotesRoot(
    onMessage: (String) -> Unit,
    onUndo: (message: String, label: String, undo: () -> Unit) -> Unit,
    onFullScreen: (Boolean) -> Unit,
    undoLabel: String,
    modifier: Modifier = Modifier,
) {
    val vm: NotesViewModel = viewModel()
    val editor: NoteEditorViewModel = viewModel()
    val conflict: NotesConflictViewModel = viewModel()
    val route by vm.route.collectAsStateWithLifecycle()
    val top = route.lastOrNull()

    CollectWhileStarted(vm.messages, onMessage)
    CollectWhileStarted(vm.undo) { req -> onUndo(req.message, undoLabel) { vm.restore(req.noteId) } }

    LaunchedEffect(top) { onFullScreen(top != null) }
    DisposableEffect(Unit) { onDispose { onFullScreen(false) } }
    // The editor handles its own back press: it saves first and may ask whether to lose text that could not be saved.
    BackHandler(enabled = top != null && top !is NotesRoute.Editor) { vm.pop() }

    when (top) {
        null -> NotesListScreen(vm, modifier)
        is NotesRoute.Editor -> NoteEditorScreen(top, vm, editor, onMessage, modifier)
        NotesRoute.Conflicts -> NotesConflictsScreen(vm, modifier)
        is NotesRoute.ConflictDetail -> NotesConflictDetailScreen(top.conflict, vm, conflict, modifier)
    }
}
