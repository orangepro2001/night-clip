package com.nightclip.app.ui.notes

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Create
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.KeyboardArrowUp
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.nightclip.app.R
import com.nightclip.app.core.NoteSummary
import com.nightclip.app.logic.NotesListLogic
import com.nightclip.app.logic.NotesSyncLogic
import com.nightclip.app.logic.NotesTexts
import com.nightclip.app.logic.SyncTone
import com.nightclip.app.ui.EmptyState
import com.nightclip.app.ui.InfoCard
import com.nightclip.app.ui.LifecycleRefresh
import com.nightclip.app.ui.PageHeader
import com.nightclip.app.ui.PanelCard
import com.nightclip.app.ui.StatusPill

/** The note list (or the trash): sync line, conflict and "deleted elsewhere" notices, rows, new-note button. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NotesListScreen(vm: NotesViewModel, modifier: Modifier = Modifier) {
    val list by vm.list.collectAsStateWithLifecycle()
    val sync by vm.sync.collectAsStateWithLifecycle()
    val conflicts by vm.conflicts.collectAsStateWithLifecycle()
    val notices by vm.notices.collectAsStateWithLifecycle()
    val refreshing by vm.refreshing.collectAsStateWithLifecycle()
    var syncOpen by rememberSaveable { mutableStateOf(false) }
    val listState = rememberLazyListState()
    val nowMs = System.currentTimeMillis()

    LifecycleRefresh { vm.onScreenStart() }

    // The next page is asked for when the end of the loaded rows comes into view.
    val nearEnd by remember {
        derivedStateOf {
            val info = listState.layoutInfo
            (info.visibleItemsInfo.lastOrNull()?.index ?: 0) >= info.totalItemsCount - 6
        }
    }
    LaunchedEffect(nearEnd, list.nextCursor) { if (nearEnd && list.canLoadMore) vm.loadMore() }

    Box(modifier.fillMaxSize()) {
        PullToRefreshBox(isRefreshing = refreshing, onRefresh = vm::pullRefresh, modifier = Modifier.fillMaxSize()) {
            LazyColumn(
                Modifier.fillMaxSize(),
                state = listState,
                contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 2.dp, bottom = 88.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                item(key = "header") {
                    PageHeader(
                        stringResource(if (list.trash) R.string.notes_trash else R.string.nav_notes),
                        actions = {
                            if (!list.trash) {
                                IconButton(onClick = vm::syncNow) { Icon(Icons.Filled.Refresh, contentDescription = stringResource(R.string.notes_sync_now)) }
                            }
                            TextButton(onClick = { vm.setTrash(!list.trash) }) {
                                Text(stringResource(if (list.trash) R.string.notes_back_to_notes else R.string.notes_trash))
                            }
                        },
                    )
                }
                if (!list.trash) {
                    item(key = "sync") {
                        val line = NotesSyncLogic.summary(sync, nowMs)
                        SyncLineRow(line.text, line.tone, syncOpen) { syncOpen = !syncOpen }
                        if (syncOpen) {
                            PanelCard(Modifier.padding(top = 6.dp)) {
                                if (sync.peers.isEmpty()) Text(NotesSyncLogic.NO_PEERS, style = MaterialTheme.typography.bodyMedium)
                                sync.peers.forEach { Text(NotesSyncLogic.peerLine(it, nowMs), style = MaterialTheme.typography.bodyMedium) }
                                if (NotesSyncLogic.needsAppUpdate(sync)) {
                                    Text(NotesSyncLogic.UPDATE_APP, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                                }
                                Button(onClick = vm::syncNow, enabled = NotesSyncLogic.canSyncNow(sync)) { Text(stringResource(R.string.notes_sync_now)) }
                            }
                        }
                    }
                    if (conflicts.isNotEmpty()) {
                        item(key = "conflicts") {
                            InfoCard(container = MaterialTheme.colorScheme.tertiaryContainer) {
                                Text(NotesTexts.conflictBanner(conflicts.size), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onTertiaryContainer)
                                TextButton(onClick = vm::openConflicts) { Text(stringResource(R.string.notes_conflict_view)) }
                            }
                        }
                    }
                    items(notices, key = { "notice-" + it.noteId }) { notice ->
                        InfoCard(container = MaterialTheme.colorScheme.secondaryContainer) {
                            Text(
                                if (notice.title.isBlank()) NotesTexts.DELETED_ELSEWHERE_UNKNOWN else NotesTexts.deletedElsewhere(notice.title),
                                style = MaterialTheme.typography.bodyMedium,
                                color = MaterialTheme.colorScheme.onSecondaryContainer,
                            )
                            Row {
                                TextButton(onClick = { vm.dismissNotice(notice.noteId) }) { Text(stringResource(R.string.notes_notice_dismiss)) }
                                TextButton(onClick = {
                                    vm.dismissNotice(notice.noteId)
                                    vm.openNote(notice.noteId)
                                }) { Text(stringResource(R.string.notes_notice_open)) }
                            }
                        }
                    }
                }
                if (!list.loaded) {
                    item(key = "loading") {
                        Box(Modifier.fillMaxWidth().padding(32.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
                    }
                } else if (list.items.isEmpty()) {
                    item(key = "empty") {
                        EmptyState(
                            if (list.trash) Icons.Filled.Delete else Icons.Filled.Create,
                            stringResource(if (list.trash) R.string.notes_trash_empty_title else R.string.notes_empty_title),
                            stringResource(if (list.trash) R.string.notes_trash_empty_hint else R.string.notes_empty_hint),
                        )
                    }
                }
                items(list.items, key = { it.noteId }) { item ->
                    NoteRow(
                        item = item,
                        trash = list.trash,
                        nowMs = nowMs,
                        onOpen = { vm.openNote(item.noteId) },
                        onDelete = { vm.delete(item) },
                        onRestore = { vm.restore(item.noteId, announce = true) },
                    )
                }
            }
        }
        if (!list.trash) {
            FloatingActionButton(onClick = vm::newNote, modifier = Modifier.align(Alignment.BottomEnd).padding(16.dp)) {
                Icon(Icons.Filled.Add, contentDescription = stringResource(R.string.notes_new))
            }
        }
    }
}

/** The one-line sync summary with a dot in the colour of its tone; tapping it opens the per-device details. */
@Composable
private fun SyncLineRow(text: String, tone: SyncTone, expanded: Boolean, onToggle: () -> Unit) {
    val scheme = MaterialTheme.colorScheme
    val dot: Color = when (tone) {
        SyncTone.OK -> scheme.secondary
        SyncTone.BUSY -> scheme.primary
        SyncTone.WARN -> scheme.outline
        SyncTone.ERROR -> scheme.error
    }
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onToggle).padding(vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Box(Modifier.size(8.dp).background(dot, CircleShape))
        Text(text, Modifier.weight(1f), style = MaterialTheme.typography.bodySmall, color = scheme.onSurfaceVariant, maxLines = 2, overflow = TextOverflow.Ellipsis)
        Icon(
            if (expanded) Icons.Filled.KeyboardArrowUp else Icons.Filled.KeyboardArrowDown,
            contentDescription = null,
            modifier = Modifier.size(18.dp),
            tint = scheme.onSurfaceVariant,
        )
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun NoteRow(item: NoteSummary, trash: Boolean, nowMs: Long, onOpen: () -> Unit, onDelete: () -> Unit, onRestore: () -> Unit) {
    var menu by remember { mutableStateOf(false) }
    val scheme = MaterialTheme.colorScheme
    Box {
        Surface(
            Modifier.fillMaxWidth().combinedClickable(
                onClick = onOpen,
                onLongClick = { menu = true },
                onLongClickLabel = stringResource(R.string.notes_more_actions),
            ),
            shape = MaterialTheme.shapes.medium,
            color = scheme.surface,
            border = BorderStroke(1.dp, scheme.outlineVariant.copy(alpha = 0.75f)),
        ) {
            Column(Modifier.padding(horizontal = 12.dp, vertical = 10.dp), verticalArrangement = Arrangement.spacedBy(3.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(NotesListLogic.title(item), Modifier.weight(1f), style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    if (NotesListLogic.showsConflictBadge(item)) StatusPill(NotesTexts.CONFLICT_BADGE, scheme.tertiary)
                }
                val preview = NotesListLogic.previewLine(item)
                if (preview.isNotEmpty()) {
                    Text(preview, style = MaterialTheme.typography.bodyMedium, color = scheme.onSurfaceVariant, maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
                Text(NotesListLogic.updatedLabel(item, nowMs), style = MaterialTheme.typography.labelSmall, color = scheme.onSurfaceVariant)
            }
        }
        DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
            if (trash) {
                DropdownMenuItem(text = { Text(stringResource(R.string.notes_restore)) }, onClick = { menu = false; onRestore() })
            } else {
                DropdownMenuItem(text = { Text(stringResource(R.string.notes_delete)) }, onClick = { menu = false; onDelete() })
            }
        }
    }
}
