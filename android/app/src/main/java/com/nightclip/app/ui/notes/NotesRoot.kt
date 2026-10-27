package com.nightclip.app.ui.notes

import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.nightclip.app.logic.AttachmentFiles
import com.nightclip.app.logic.NotesTexts
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
    val attachments: NoteAttachmentsViewModel = viewModel()
    val route by vm.route.collectAsStateWithLifecycle()
    val top = route.lastOrNull()

    CollectWhileStarted(vm.messages, onMessage)
    CollectWhileStarted(attachments.messages, onMessage)
    CollectWhileStarted(vm.undo) { req -> onUndo(req.message, undoLabel) { vm.restore(req.noteId) } }

    LaunchedEffect(top) { onFullScreen(top != null) }
    DisposableEffect(Unit) { onDispose { onFullScreen(false) } }
    // The editor handles its own back press: it saves first and may ask whether to lose text that could not be saved.
    BackHandler(enabled = top != null && top !is NotesRoute.Editor) { vm.pop() }

    // Every preview (editor, history, conflicts) shows pictures and files through the same store.
    CompositionLocalProvider(LocalNoteAttachments provides attachments) {
        when (top) {
            null -> NotesListScreen(vm, modifier)
            is NotesRoute.Editor -> NoteEditorScreen(top, vm, editor, onMessage, modifier)
            NotesRoute.Conflicts -> NotesConflictsScreen(vm, modifier)
            is NotesRoute.ConflictDetail -> NotesConflictDetailScreen(top.conflict, vm, conflict, modifier)
        }
        AttachmentChoiceDialog(attachments)
    }
}

/**
 * What to do with an attachment that is not opened from a note (installers, scripts, web pages) or that was long-pressed:
 * save it to a place the user picks, or share it. Never opened directly.
 */
@Composable
private fun AttachmentChoiceDialog(attachments: NoteAttachmentsViewModel) {
    var choice by remember { mutableStateOf<AttachmentChoice?>(null) }
    var saving by remember { mutableStateOf<AttachmentChoice?>(null) }
    CollectWhileStarted(attachments.choices) { choice = it }
    val saveLauncher = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/octet-stream")) { uri ->
        val target = saving
        saving = null
        if (uri != null && target != null) attachments.saveTo(target.blobId, uri)
    }
    choice?.let { c ->
        AlertDialog(
            onDismissRequest = { choice = null },
            title = { Text(if (c.blocked) NotesTexts.ATTACHMENT_NOT_OPENED_TITLE else c.name) },
            text = { if (c.blocked) Text(NotesTexts.ATTACHMENT_NOT_OPENED_TEXT + "\n\n" + c.name) },
            confirmButton = {
                TextButton(onClick = {
                    choice = null
                    saving = c
                    saveLauncher.launch(AttachmentFiles.openName(c.name))
                }) { Text(NotesTexts.ATTACHMENT_SAVE) }
            },
            dismissButton = {
                TextButton(onClick = {
                    choice = null
                    attachments.share(c.blobId)
                }) { Text(NotesTexts.ATTACHMENT_SHARE) }
            },
        )
    }
}
