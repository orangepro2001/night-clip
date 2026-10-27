package com.nightclip.app.ui.notes

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.nightclip.app.R
import com.nightclip.app.core.NoteConflict
import com.nightclip.app.core.NotesApi
import com.nightclip.app.logic.NotesTexts
import com.nightclip.app.ui.CollectWhileStarted
import com.nightclip.app.ui.EmptyState
import com.nightclip.app.ui.InfoCard

@Composable
private fun NotesTopBar(title: String, onBack: () -> Unit) {
    Row(Modifier.fillMaxWidth().padding(end = 12.dp), verticalAlignment = Alignment.CenterVertically) {
        IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.back)) }
        Text(title, style = MaterialTheme.typography.titleLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

/** Every conflict copy waiting for a decision. */
@Composable
fun NotesConflictsScreen(vm: NotesViewModel, modifier: Modifier = Modifier) {
    val conflicts by vm.conflicts.collectAsStateWithLifecycle()
    Column(modifier.fillMaxSize()) {
        NotesTopBar(stringResource(R.string.notes_conflicts_title), onBack = vm::pop)
        if (conflicts.isEmpty()) {
            EmptyState(Icons.Filled.CheckCircle, stringResource(R.string.notes_conflicts_none_title), stringResource(R.string.notes_conflicts_none_hint))
            OutlinedButton(onClick = vm::pop, modifier = Modifier.align(Alignment.CenterHorizontally)) { Text(stringResource(R.string.back)) }
        } else {
            LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                item { Text(stringResource(R.string.notes_conflict_intro), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                items(conflicts, key = { it.conflictNoteId }) { c ->
                    Surface(
                        Modifier.fillMaxWidth().clickable { vm.openConflict(c) },
                        shape = MaterialTheme.shapes.medium,
                        color = MaterialTheme.colorScheme.surface,
                        tonalElevation = 1.dp,
                    ) {
                        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                            Text(c.title.ifBlank { NotesTexts.UNTITLED }, style = MaterialTheme.typography.titleMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
                            Text(NotesTexts.dateTime(c.createdAt), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    }
                }
            }
        }
    }
}

/**
 * One conflict: the note as it stands and the copy of the older side, each drawn from the core's render tree, and the
 * three ways out of `notes.sync_resolve`. All of them keep the copy's text somewhere or move it to the trash; nothing is
 * erased.
 */
@Composable
fun NotesConflictDetailScreen(conflict: NoteConflict, notes: NotesViewModel, vm: NotesConflictViewModel, modifier: Modifier = Modifier) {
    val context = LocalContext.current
    val ui by vm.ui.collectAsStateWithLifecycle()
    var showCopy by rememberSaveable { mutableStateOf(false) }
    val linkFailed = stringResource(R.string.notes_open_link_failed)
    var notice by remember { mutableStateOf<String?>(null) }

    LaunchedEffect(conflict) { vm.load(conflict) }
    CollectWhileStarted(vm.messages) { notice = it }
    val side = if (showCopy) ui.copy else ui.main
    val mainTrashed = ui.main?.deleted == true

    Column(modifier.fillMaxSize()) {
        NotesTopBar(stringResource(R.string.notes_conflict_title), onBack = notes::pop)
        when {
            ui.loading -> Box(Modifier.weight(1f).fillMaxWidth(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
            ui.error != null -> Column(Modifier.weight(1f).fillMaxWidth().padding(24.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
                Text(ui.error.orEmpty(), textAlign = TextAlign.Center)
                TextButton(onClick = { vm.load(conflict, force = true) }) { Text(stringResource(R.string.retry)) }
            }
            else -> {
                Text(
                    stringResource(R.string.notes_conflict_intro),
                    Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Row(Modifier.padding(horizontal = 16.dp, vertical = 4.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    FilterChip(selected = !showCopy, onClick = { showCopy = false }, label = { Text(stringResource(R.string.notes_conflict_main)) })
                    FilterChip(selected = showCopy, onClick = { showCopy = true }, label = { Text(stringResource(R.string.notes_conflict_copy)) })
                }
                side?.let {
                    Text(
                        it.title.ifBlank { NotesTexts.UNTITLED },
                        Modifier.padding(horizontal = 16.dp, vertical = 2.dp),
                        style = MaterialTheme.typography.titleSmall,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                Box(Modifier.weight(1f).fillMaxWidth()) {
                    val blocks = side?.blocks.orEmpty()
                    if (blocks.isEmpty()) {
                        Text(stringResource(R.string.notes_preview_empty), Modifier.padding(24.dp), color = MaterialTheme.colorScheme.onSurfaceVariant)
                    } else {
                        MarkdownPreview(blocks, onLink = { url -> if (!openNoteLink(context, url)) notice = linkFailed }, modifier = Modifier.fillMaxSize())
                    }
                }
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                Column(Modifier.verticalScroll(rememberScrollState()).padding(horizontal = 16.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    if (mainTrashed) {
                        InfoCard(container = MaterialTheme.colorScheme.errorContainer) {
                            Text(stringResource(R.string.notes_conflict_main_trashed), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onErrorContainer)
                            TextButton(onClick = { vm.restoreMain(conflict) }) { Text(stringResource(R.string.notes_restore)) }
                        }
                    }
                    notice?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error) }
                    ResolveButton(NotesTexts.ACTION_KEEP_MAIN, NotesTexts.ACTION_KEEP_MAIN_HINT, primary = true, enabled = !ui.resolving) {
                        vm.resolve(conflict, NotesApi.KEEP_MAIN, notes::conflictResolved)
                    }
                    ResolveButton(NotesTexts.ACTION_KEEP_COPY, NotesTexts.ACTION_KEEP_COPY_HINT, primary = false, enabled = !ui.resolving && !mainTrashed) {
                        vm.resolve(conflict, NotesApi.KEEP_CONFLICT, notes::conflictResolved)
                    }
                    ResolveButton(NotesTexts.ACTION_MERGE, NotesTexts.ACTION_MERGE_HINT, primary = false, enabled = !ui.resolving && !mainTrashed) {
                        vm.resolve(conflict, NotesApi.MERGE, notes::conflictResolved)
                    }
                }
            }
        }
    }
}

@Composable
private fun ResolveButton(label: String, hint: String, primary: Boolean, enabled: Boolean, onClick: () -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
        if (primary) {
            Button(onClick = onClick, enabled = enabled, modifier = Modifier.fillMaxWidth()) { Text(label) }
        } else {
            FilledTonalButton(onClick = onClick, enabled = enabled, modifier = Modifier.fillMaxWidth()) { Text(label) }
        }
        Text(hint, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}
