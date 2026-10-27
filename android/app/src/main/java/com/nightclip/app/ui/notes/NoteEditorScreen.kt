package com.nightclip.app.ui.notes

import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.VerticalDivider
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.nightclip.app.R
import com.nightclip.app.logic.EditorPhase
import com.nightclip.app.logic.EditorState
import com.nightclip.app.logic.MdAction
import com.nightclip.app.logic.NoteLinks
import com.nightclip.app.logic.NotesErrors
import com.nightclip.app.logic.NotesTexts
import com.nightclip.app.logic.SaveStatus
import com.nightclip.app.ui.CollectWhileStarted
import com.nightclip.app.ui.InfoCard

/** Opens a tapped link of a note, only if it is one the app allows (http, https, mailto). False = not opened. */
internal fun openNoteLink(context: Context, url: String): Boolean {
    val safe = NoteLinks.openable(url) ?: return false
    return try {
        context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(safe)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        true
    } catch (e: Exception) {
        false
    }
}

/**
 * One note: title, Markdown source with a formatting toolbar above the keyboard, or the preview drawn from the core's
 * render tree. Everything the user types is saved by [NoteEditorViewModel] (500 ms after the last edit, when leaving,
 * on onStop).
 */
@Composable
fun NoteEditorScreen(
    route: NotesRoute.Editor,
    notes: NotesViewModel,
    vm: NoteEditorViewModel,
    onMessage: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val state by vm.state.collectAsStateWithLifecycle()
    val mode by vm.mode.collectAsStateWithLifecycle()
    val preview by vm.preview.collectAsStateWithLifecycle()
    val leaveBlocked by vm.leaveBlocked.collectAsStateWithLifecycle()
    val history by vm.history.collectAsStateWithLifecycle()
    val conflicts by notes.conflicts.collectAsStateWithLifecycle()
    var menu by remember { mutableStateOf(false) }
    var titleFocused by remember { mutableStateOf(false) }
    val bodyFocus = remember { FocusRequester() }
    val linkFailed = stringResource(R.string.notes_open_link_failed)
    // A wide window (tablet, phone on its side) shows the preview next to the text, and it follows every change.
    val wide = LocalConfiguration.current.screenWidthDp >= WIDE_DP
    val pickFile = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri -> if (uri != null) vm.attach(uri) }
    val pickImage = rememberLauncherForActivityResult(ActivityResultContracts.PickVisualMedia()) { uri -> if (uri != null) vm.attach(uri) }

    LaunchedEffect(route.sessionId) { vm.start(route) }
    CollectWhileStarted(vm.messages, onMessage)
    LifecycleEventEffect(Lifecycle.Event.ON_STOP) { vm.flushNow() }
    DisposableEffect(Unit) { onDispose { vm.flushNow() } }
    // A new note opens with the keyboard on the text, so the first thing typed is the note.
    LaunchedEffect(route.sessionId, state.phase) {
        if (route.noteId == null && state.phase == EditorPhase.READY && vm.bodyField.text.isEmpty()) runCatching { bodyFocus.requestFocus() }
    }

    val leave = { vm.requestLeave { notes.pop() } }
    BackHandler(onBack = leave)
    val openLink: (String) -> Unit = { url -> if (!openNoteLink(context, url)) onMessage(linkFailed) }

    // The toolbar is the last child of this column: imePadding lifts it right above the keyboard.
    Column(modifier.fillMaxSize().imePadding()) {
        Row(Modifier.fillMaxWidth().padding(end = 4.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            IconButton(onClick = leave) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.back)) }
            SaveStatusText(state, Modifier.weight(1f))
            FilterChip(selected = mode == EditorMode.SOURCE, onClick = { vm.setMode(EditorMode.SOURCE) }, label = { Text(stringResource(R.string.notes_mode_source)) })
            FilterChip(selected = mode == EditorMode.PREVIEW, onClick = { vm.setMode(EditorMode.PREVIEW) }, label = { Text(stringResource(R.string.notes_mode_preview)) })
            Box {
                IconButton(onClick = { menu = true }) { Icon(Icons.Filled.MoreVert, contentDescription = stringResource(R.string.more)) }
                DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.notes_history)) },
                        enabled = state.phase == EditorPhase.READY && state.noteId != null,
                        onClick = { menu = false; vm.openHistory() },
                    )
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.notes_move_to_trash)) },
                        enabled = state.phase == EditorPhase.READY && !state.deleted,
                        onClick = { menu = false; vm.trashCurrent { id -> if (id != null) notes.trashed(id); notes.pop() } },
                    )
                }
            }
        }

        if (state.deleted) {
            InfoCard(Modifier.padding(horizontal = 12.dp, vertical = 4.dp), container = MaterialTheme.colorScheme.errorContainer) {
                Text(stringResource(R.string.notes_in_trash_banner), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onErrorContainer)
                TextButton(onClick = vm::restoreFromTrash) { Text(stringResource(R.string.notes_restore)) }
            }
        }
        state.saveError?.let { code ->
            InfoCard(Modifier.padding(horizontal = 12.dp, vertical = 4.dp), container = MaterialTheme.colorScheme.errorContainer) {
                Text(
                    stringResource(R.string.notes_save_failed_banner, NotesErrors.message(code, NotesErrors.Context.SAVE)),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onErrorContainer,
                )
            }
        }
        val copy = conflicts.firstOrNull { it.conflictNoteId == state.noteId }
        if (copy != null) {
            InfoCard(Modifier.padding(horizontal = 12.dp, vertical = 4.dp)) {
                Text(stringResource(R.string.notes_is_conflict_copy), style = MaterialTheme.typography.bodyMedium)
                TextButton(onClick = { notes.openConflict(copy) }) { Text(stringResource(R.string.notes_resolve_conflict)) }
            }
        }

        when (state.phase) {
            EditorPhase.IDLE, EditorPhase.LOADING -> Box(Modifier.weight(1f).fillMaxWidth(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
            EditorPhase.LOAD_FAILED -> Column(Modifier.weight(1f).fillMaxWidth().padding(24.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
                Text(NotesErrors.message(state.loadError.orEmpty(), NotesErrors.Context.OPEN), textAlign = TextAlign.Center)
                TextButton(onClick = { vm.retryOpen(route) }) { Text(stringResource(R.string.retry)) }
            }
            EditorPhase.READY -> {
                val editable = mode == EditorMode.SOURCE || wide
                TitleField(vm, readOnly = state.readOnly || !editable, onNext = { runCatching { bodyFocus.requestFocus() } }, onFocus = { titleFocused = it })
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                Box(Modifier.weight(1f).fillMaxWidth()) {
                    val previewPane = @Composable { PreviewPane(preview, onLink = openLink, onRetry = { vm.setMode(EditorMode.PREVIEW) }) }
                    when {
                        mode == EditorMode.SOURCE -> SourceField(vm, readOnly = state.readOnly, focusRequester = bodyFocus)
                        wide -> Row(Modifier.fillMaxSize()) {
                            Box(Modifier.weight(1f).fillMaxHeight()) { SourceField(vm, readOnly = state.readOnly, focusRequester = bodyFocus) }
                            VerticalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                            Box(Modifier.weight(1f).fillMaxHeight()) { previewPane() }
                        }
                        else -> previewPane()
                    }
                }
                if (editable && !state.readOnly && !titleFocused) {
                    FormatToolbar(
                        onAction = vm::apply,
                        onAttachFile = { pickFile.launch(arrayOf("*/*")) },
                        onAttachImage = { pickImage.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly)) },
                    )
                }
            }
        }
    }

    if (leaveBlocked) {
        AlertDialog(
            onDismissRequest = vm::keepEditing,
            title = { Text(stringResource(R.string.notes_leave_title)) },
            text = { Text(stringResource(R.string.notes_leave_text)) },
            confirmButton = { TextButton(onClick = vm::keepEditing) { Text(stringResource(R.string.notes_leave_keep)) } },
            dismissButton = { TextButton(onClick = { vm.discardAndLeave { notes.pop() } }) { Text(stringResource(R.string.notes_leave_discard)) } },
        )
    }
    if (history.open) NoteHistorySheet(history, vm)
}

@Composable
private fun SaveStatusText(state: EditorState, modifier: Modifier) {
    val text = when {
        state.phase == EditorPhase.LOADING -> stringResource(R.string.notes_loading)
        state.phase != EditorPhase.READY -> ""
        else -> when (state.status) {
            SaveStatus.CLEAN -> NotesTexts.SAVE_CLEAN
            SaveStatus.PENDING -> NotesTexts.SAVE_PENDING
            SaveStatus.SAVING -> NotesTexts.SAVE_SAVING
            SaveStatus.FAILED -> NotesTexts.SAVE_FAILED
            SaveStatus.READ_ONLY -> NotesTexts.SAVE_READ_ONLY
        }
    }
    val failed = state.status == SaveStatus.FAILED && state.phase == EditorPhase.READY
    Text(
        text,
        modifier.padding(horizontal = 4.dp),
        style = MaterialTheme.typography.labelMedium,
        color = if (failed) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
        maxLines = 1,
        overflow = TextOverflow.Ellipsis,
    )
}

@Composable
private fun TitleField(vm: NoteEditorViewModel, readOnly: Boolean, onNext: () -> Unit, onFocus: (Boolean) -> Unit) {
    val scheme = MaterialTheme.colorScheme
    BasicTextField(
        value = vm.titleField,
        onValueChange = vm::onTitleChange,
        modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 10.dp).onFocusChanged { onFocus(it.isFocused) },
        readOnly = readOnly,
        singleLine = true,
        textStyle = MaterialTheme.typography.titleLarge.copy(color = scheme.onSurface),
        cursorBrush = SolidColor(scheme.primary),
        keyboardOptions = KeyboardOptions(imeAction = ImeAction.Next),
        keyboardActions = KeyboardActions(onNext = { onNext() }),
        decorationBox = { inner ->
            Box {
                if (vm.titleField.text.isEmpty()) {
                    Text(stringResource(R.string.notes_title_hint), style = MaterialTheme.typography.titleLarge, color = scheme.onSurfaceVariant.copy(alpha = 0.6f))
                }
                inner()
            }
        },
    )
}

/**
 * The Markdown text, monospace, as it is stored. The field fills all the space (a tap below the last line puts the cursor
 * there) and scrolls inside itself, which is how a multi-line text field keeps the cursor in view above the keyboard.
 */
@Composable
private fun SourceField(vm: NoteEditorViewModel, readOnly: Boolean, focusRequester: FocusRequester) {
    val scheme = MaterialTheme.colorScheme
    val style = TextStyle(fontFamily = FontFamily.Monospace, fontSize = 14.sp, lineHeight = 22.sp, color = scheme.onSurface)
    BasicTextField(
        value = vm.bodyField,
        onValueChange = vm::onBodyChange,
        modifier = Modifier
            .fillMaxSize()
            .padding(horizontal = 16.dp, vertical = 10.dp)
            .focusRequester(focusRequester),
        readOnly = readOnly,
        textStyle = style,
        cursorBrush = SolidColor(scheme.primary),
        decorationBox = { inner ->
            Box {
                if (vm.bodyField.text.isEmpty()) {
                    Text(stringResource(R.string.notes_body_hint), style = style.copy(color = scheme.onSurfaceVariant.copy(alpha = 0.6f)))
                }
                inner()
            }
        },
    )
}

@Composable
private fun PreviewPane(preview: PreviewState, onLink: (String) -> Unit, onRetry: () -> Unit) {
    when (preview) {
        PreviewState.Idle, PreviewState.Loading -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
        is PreviewState.Failed -> Column(Modifier.fillMaxSize().padding(24.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
            Text(preview.message, textAlign = TextAlign.Center)
            TextButton(onClick = onRetry) { Text(stringResource(R.string.retry)) }
        }
        is PreviewState.Ready -> Column(Modifier.fillMaxSize()) {
            // The picture below is the last one that could be made.
            if (preview.failed) {
                Text(
                    stringResource(R.string.notes_preview_failed),
                    Modifier.padding(horizontal = 16.dp, vertical = 6.dp),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            if (preview.blocks.isEmpty()) {
                Box(Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
                    Text(stringResource(R.string.notes_preview_empty), color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            } else {
                MarkdownPreview(preview.blocks, onLink, Modifier.fillMaxSize())
            }
        }
    }
}

// ---- formatting toolbar ----------------------------------------------------------------------

private class Tool(val label: String, val description: Int, val action: MdAction, val style: TextStyle = TextStyle(fontSize = 15.sp, fontWeight = FontWeight.Medium))

private val TOOL_GROUPS: List<List<Tool>> = listOf(
    listOf(
        Tool("B", R.string.tool_bold, MdAction.Bold, TextStyle(fontSize = 16.sp, fontWeight = FontWeight.ExtraBold)),
        Tool("I", R.string.tool_italic, MdAction.Italic, TextStyle(fontSize = 16.sp, fontStyle = FontStyle.Italic, fontWeight = FontWeight.Medium)),
        Tool("S", R.string.tool_strike, MdAction.Strike, TextStyle(fontSize = 16.sp, textDecoration = TextDecoration.LineThrough, fontWeight = FontWeight.Medium)),
        Tool("</>", R.string.tool_code, MdAction.InlineCode, TextStyle(fontSize = 13.sp, fontFamily = FontFamily.Monospace, fontWeight = FontWeight.Medium)),
    ),
    listOf(
        Tool("H1", R.string.tool_h1, MdAction.Heading(1)),
        Tool("H2", R.string.tool_h2, MdAction.Heading(2)),
        Tool("H3", R.string.tool_h3, MdAction.Heading(3)),
    ),
    listOf(
        Tool("•", R.string.tool_bullet, MdAction.Bullet, TextStyle(fontSize = 20.sp)),
        Tool("1.", R.string.tool_numbered, MdAction.Numbered),
        Tool("☐", R.string.tool_task, MdAction.Task, TextStyle(fontSize = 18.sp)),
        Tool("⇥", R.string.tool_indent, MdAction.Indent, TextStyle(fontSize = 18.sp)),
        Tool("⇤", R.string.tool_outdent, MdAction.Outdent, TextStyle(fontSize = 18.sp)),
    ),
    listOf(
        Tool(">", R.string.tool_quote, MdAction.Quote, TextStyle(fontSize = 18.sp, fontWeight = FontWeight.Bold)),
        Tool("{ }", R.string.tool_code_block, MdAction.CodeBlock, TextStyle(fontSize = 14.sp, fontFamily = FontFamily.Monospace, fontWeight = FontWeight.Medium)),
        Tool("🔗", R.string.tool_link, MdAction.Link, TextStyle(fontSize = 16.sp)),
        Tool("―", R.string.tool_rule, MdAction.Rule, TextStyle(fontSize = 18.sp)),
    ),
)

/** One row of buttons that scrolls sideways; each button is a 44 dp target and acts on the text field's selection. */
@Composable
private fun FormatToolbar(onAction: (MdAction) -> Unit, onAttachFile: () -> Unit, onAttachImage: () -> Unit) {
    val scheme = MaterialTheme.colorScheme
    Surface(Modifier.fillMaxWidth(), color = scheme.surface, border = BorderStroke(0.5.dp, scheme.outlineVariant)) {
        Row(Modifier.horizontalScroll(rememberScrollState()).padding(horizontal = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            TOOL_GROUPS.forEachIndexed { g, tools ->
                if (g > 0) VerticalDivider(Modifier.padding(horizontal = 4.dp).height(22.dp), color = scheme.outlineVariant)
                tools.forEach { tool -> ToolButton(tool.label, stringResource(tool.description), tool.style) { onAction(tool.action) } }
            }
            VerticalDivider(Modifier.padding(horizontal = 4.dp).height(22.dp), color = scheme.outlineVariant)
            ToolButton("📎", stringResource(R.string.tool_attach_file), TextStyle(fontSize = 17.sp), onAttachFile)
            ToolButton("🖼", stringResource(R.string.tool_attach_image), TextStyle(fontSize = 17.sp), onAttachImage)
        }
    }
}

@Composable
private fun ToolButton(label: String, description: String, style: TextStyle, onClick: () -> Unit) {
    Box(
        Modifier
            .size(width = 44.dp, height = 44.dp)
            .semantics { contentDescription = description }
            .clickable(role = Role.Button, onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        Text(label, style = style, color = MaterialTheme.colorScheme.onSurface)
    }
}

/** From this window width (dp) on, the preview sits next to the text instead of replacing it. */
private const val WIDE_DP = 720
