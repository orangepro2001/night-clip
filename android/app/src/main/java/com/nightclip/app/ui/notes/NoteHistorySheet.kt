package com.nightclip.app.ui.notes

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.nightclip.app.R
import com.nightclip.app.logic.NotesTexts
import com.nightclip.app.ui.StatusPill

/**
 * The history of one note: its revisions (from `notes.history`), one of them opened read-only (text and picture from
 * the core), and "restore this version", which is an ordinary save of that version's text on top of the newest one.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NoteHistorySheet(history: HistoryUi, vm: NoteEditorViewModel) {
    val sheet = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    ModalBottomSheet(onDismissRequest = vm::closeHistory, sheetState = sheet) {
        val viewing = history.viewing
        if (viewing == null) {
            HistoryList(history, vm)
        } else {
            val current = history.rows.firstOrNull { it.rev.revId == viewing.rev.revId }?.current == true
            RevisionPage(viewing, current, vm)
        }
    }
}

@Composable
private fun HistoryList(history: HistoryUi, vm: NoteEditorViewModel) {
    Column(Modifier.fillMaxWidth().padding(bottom = 16.dp)) {
        Text(stringResource(R.string.notes_history), Modifier.padding(horizontal = 20.dp, vertical = 4.dp), style = MaterialTheme.typography.titleMedium)
        when {
            history.error != null && history.rows.isEmpty() ->
                Text(history.error, Modifier.padding(20.dp), color = MaterialTheme.colorScheme.onSurfaceVariant)
            history.loading && history.rows.isEmpty() ->
                Box(Modifier.fillMaxWidth().padding(32.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
            history.rows.isEmpty() ->
                Text(stringResource(R.string.notes_history_empty), Modifier.padding(20.dp), color = MaterialTheme.colorScheme.onSurfaceVariant)
            else -> LazyColumn(Modifier.fillMaxWidth().heightIn(max = 460.dp)) {
                items(history.rows, key = { it.rev.revId }) { row ->
                    HistoryRowItem(row, onClick = { vm.viewRevision(row) })
                    HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.6f))
                }
                if (history.nextCursor != null) {
                    item {
                        TextButton(onClick = vm::loadMoreHistory, enabled = !history.loading, modifier = Modifier.padding(horizontal = 12.dp)) {
                            Text(stringResource(R.string.notes_history_more))
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun HistoryRowItem(row: HistoryRow, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 20.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Column(Modifier.weight(1f)) {
            Text(NotesTexts.dateTime(row.rev.createdAt), style = MaterialTheme.typography.bodyLarge)
            val title = row.rev.title.trim().ifEmpty { NotesTexts.UNTITLED }
            Text(
                "${row.origin} · $title",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        if (row.rev.deleted) StatusPill(stringResource(R.string.notes_history_deleted), MaterialTheme.colorScheme.error)
        if (row.current) StatusPill(stringResource(R.string.notes_history_current))
    }
}

@Composable
private fun RevisionPage(view: RevisionView, isCurrent: Boolean, vm: NoteEditorViewModel) {
    val context = LocalContext.current
    val linkFailed = stringResource(R.string.notes_open_link_failed)
    Column(Modifier.fillMaxWidth().padding(bottom = 16.dp)) {
        Row(Modifier.fillMaxWidth().padding(horizontal = 8.dp), verticalAlignment = Alignment.CenterVertically) {
            TextButton(onClick = vm::closeRevision) { Text(stringResource(R.string.notes_history_back)) }
            Text(
                "${NotesTexts.dateTime(view.rev.createdAt)} · ${view.origin}",
                Modifier.weight(1f),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        val title = view.doc?.title?.trim().orEmpty()
        if (title.isNotEmpty()) Text(title, Modifier.padding(horizontal = 20.dp, vertical = 4.dp), style = MaterialTheme.typography.titleMedium)
        when {
            view.error != null -> Text(view.error, Modifier.padding(20.dp), color = MaterialTheme.colorScheme.error)
            view.blocks == null -> Box(Modifier.fillMaxWidth().padding(32.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
            view.blocks.isEmpty() -> Text(stringResource(R.string.notes_preview_empty), Modifier.padding(20.dp), color = MaterialTheme.colorScheme.onSurfaceVariant)
            else -> MarkdownPreview(
                view.blocks,
                onLink = { url -> if (!openNoteLink(context, url)) vm.report(linkFailed) },
                modifier = Modifier.fillMaxWidth().heightIn(max = 360.dp),
            )
        }
        Button(
            onClick = vm::restoreViewing,
            enabled = view.doc != null && !isCurrent,
            modifier = Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 8.dp),
        ) { Text(stringResource(if (isCurrent) R.string.notes_history_is_current else R.string.notes_history_restore)) }
    }
}
