package com.nightclip.app.ui.vault

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Clear
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Badge
import androidx.compose.material3.BadgedBox
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.nightclip.app.R
import com.nightclip.app.core.VaultEntrySummary
import com.nightclip.app.logic.VaultFilter
import com.nightclip.app.logic.VaultListLogic
import com.nightclip.app.logic.VaultTexts
import com.nightclip.app.platform.VaultSession
import com.nightclip.app.ui.ConfirmDialog
import com.nightclip.app.ui.LifecycleRefresh

/** Search, filter chips, group picker and the entry list; top bar actions and the add button. */
@Composable
fun VaultListScreen(vm: VaultViewModel, browse: VaultBrowseViewModel) {
    val query by browse.query.collectAsStateWithLifecycle()
    val filter by browse.filter.collectAsStateWithLifecycle()
    val groupFilter by browse.groupFilter.collectAsStateWithLifecycle()
    val groups by browse.groups.collectAsStateWithLifecycle()
    val entries by browse.entries.collectAsStateWithLifecycle()
    val status by vm.status.collectAsStateWithLifecycle()
    val attention by VaultSession.attention.collectAsStateWithLifecycle()

    var menu by remember { mutableStateOf(false) }
    var addDialog by remember { mutableStateOf(false) }
    var groupMenu by remember { mutableStateOf(false) }
    var newGroupDialog by remember { mutableStateOf(false) }
    var confirmEmpty by remember { mutableStateOf(false) }

    LifecycleRefresh { browse.reloadList() }

    Box(Modifier.fillMaxSize()) {
        Column(Modifier.fillMaxSize()) {
            VaultTopBar(stringResource(R.string.nav_vault), onBack = null) {
                IconButton(onClick = { vm.lock() }) { Icon(Icons.Filled.Lock, contentDescription = stringResource(R.string.vault_lock_now)) }
                Box {
                    IconButton(onClick = { menu = true }) {
                        BadgedBox(badge = { if (attention.isNotEmpty()) Badge() }) {
                            Icon(Icons.Filled.MoreVert, contentDescription = stringResource(R.string.more))
                        }
                    }
                    DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                        MenuItem(R.string.vault_menu_health) { menu = false; vm.push(VaultRoute.Health) }
                        MenuItem(R.string.vault_menu_backups) { menu = false; vm.push(VaultRoute.Backups) }
                        MenuItem(R.string.vault_menu_import) { menu = false; vm.push(VaultRoute.Import) }
                        MenuItem(R.string.vault_menu_export) { menu = false; vm.push(VaultRoute.Export) }
                        MenuItem(R.string.vault_menu_change_master) { menu = false; vm.push(VaultRoute.ChangeMaster) }
                        DropdownMenuItem(
                            text = { Text(stringResource(R.string.vault_menu_sync) + if (attention.isNotEmpty()) " ●" else "") },
                            onClick = { menu = false; vm.openSync() },
                        )
                        MenuItem(R.string.vault_menu_settings) { menu = false; vm.push(VaultRoute.Settings) }
                    }
                }
            }
            OutlinedTextField(
                value = query,
                onValueChange = browse::setQuery,
                modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp),
                singleLine = true,
                placeholder = { Text(stringResource(R.string.vault_search)) },
                leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
                trailingIcon = {
                    if (query.isNotEmpty()) IconButton(onClick = { browse.setQuery("") }) { Icon(Icons.Filled.Clear, contentDescription = stringResource(R.string.clear)) }
                },
                keyboardOptions = androidx.compose.foundation.text.KeyboardOptions(imeAction = ImeAction.Search, autoCorrectEnabled = false),
            )
            LazyRow(Modifier.fillMaxWidth(), contentPadding = androidx.compose.foundation.layout.PaddingValues(horizontal = 16.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                items(VaultFilter.entries) { f ->
                    FilterChip(selected = filter == f, onClick = { browse.setFilter(f) }, label = { Text(VaultTexts.filterLabel(f)) })
                }
            }
            Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp), verticalAlignment = Alignment.CenterVertically) {
                Box {
                    val shown = groups.firstOrNull { it.id == groupFilter }?.path
                    TextButton(onClick = { groupMenu = true }) { Text(stringResource(R.string.vault_group_label, shown ?: stringResource(R.string.vault_group_all))) }
                    DropdownMenu(expanded = groupMenu, onDismissRequest = { groupMenu = false }) {
                        DropdownMenuItem(text = { Text(stringResource(R.string.vault_group_all)) }, onClick = { groupMenu = false; browse.setGroupFilter(null) })
                        groups.filter { !it.isTrash }.forEach { g ->
                            DropdownMenuItem(
                                text = { Text(g.path.ifBlank { g.name } + " (${g.entries})") },
                                onClick = { groupMenu = false; browse.setGroupFilter(g.id) },
                            )
                        }
                        DropdownMenuItem(text = { Text(stringResource(R.string.vault_group_new)) }, onClick = { groupMenu = false; newGroupDialog = true })
                    }
                }
                if (filter == VaultFilter.TRASH && entries.isNotEmpty()) {
                    TextButton(onClick = { confirmEmpty = true }) { Text(stringResource(R.string.vault_empty_trash)) }
                }
            }
            if (entries.isEmpty()) {
                EmptyState(filter, query, status?.entries ?: 0)
            } else {
                val nowS = System.currentTimeMillis() / 1000
                LazyColumn(Modifier.fillMaxSize(), contentPadding = androidx.compose.foundation.layout.PaddingValues(bottom = 88.dp)) {
                    items(entries, key = { it.id }) { e -> EntryRow(e, nowS) { vm.push(VaultRoute.Detail(e.id)) } }
                }
            }
        }
        if (filter != VaultFilter.TRASH) {
            FloatingActionButton(onClick = { addDialog = true }, modifier = Modifier.align(Alignment.BottomEnd).padding(16.dp)) {
                Icon(Icons.Filled.Add, contentDescription = stringResource(R.string.vault_add_entry))
            }
        }
    }

    if (addDialog) {
        AlertDialog(
            onDismissRequest = { addDialog = false },
            title = { Text(stringResource(R.string.vault_add_title)) },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    VaultTexts.KINDS.forEach { kind ->
                        Row(
                            Modifier.fillMaxWidth().clickable {
                                addDialog = false
                                vm.push(VaultRoute.Editor(null, kind))
                            }.padding(vertical = 10.dp),
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.spacedBy(12.dp),
                        ) {
                            Icon(kindIcon(kind), contentDescription = null)
                            Text(VaultTexts.kindLabel(kind), style = MaterialTheme.typography.bodyLarge)
                        }
                    }
                }
            },
            confirmButton = {},
            dismissButton = { TextButton(onClick = { addDialog = false }) { Text(stringResource(R.string.cancel)) } },
        )
    }
    if (newGroupDialog) {
        var name by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { newGroupDialog = false },
            title = { Text(stringResource(R.string.vault_group_new)) },
            text = { OutlinedTextField(name, { name = it }, singleLine = true, label = { Text(stringResource(R.string.vault_group_name)) }) },
            confirmButton = {
                TextButton(enabled = name.isNotBlank(), onClick = {
                    browse.addGroup(name)
                    newGroupDialog = false
                }) { Text(stringResource(R.string.save)) }
            },
            dismissButton = { TextButton(onClick = { newGroupDialog = false }) { Text(stringResource(R.string.cancel)) } },
        )
    }
    if (confirmEmpty) {
        ConfirmDialog(
            title = stringResource(R.string.vault_empty_trash),
            text = stringResource(R.string.vault_empty_trash_text),
            confirmLabel = stringResource(R.string.vault_empty_trash),
            onConfirm = {
                confirmEmpty = false
                browse.emptyTrash()
            },
            onDismiss = { confirmEmpty = false },
        )
    }
}

@Composable
private fun MenuItem(label: Int, onClick: () -> Unit) {
    DropdownMenuItem(text = { Text(stringResource(label)) }, onClick = onClick)
}

@Composable
private fun EmptyState(filter: VaultFilter, query: String, total: Int) {
    Column(Modifier.fillMaxSize().padding(32.dp), verticalArrangement = Arrangement.Center, horizontalAlignment = Alignment.CenterHorizontally) {
        val text = when {
            query.isNotBlank() -> stringResource(R.string.vault_empty_search)
            filter == VaultFilter.TRASH -> stringResource(R.string.vault_empty_trash_list)
            filter != VaultFilter.ALL -> stringResource(R.string.vault_empty_filter)
            total == 0 -> stringResource(R.string.vault_empty_all)
            else -> stringResource(R.string.vault_empty_filter)
        }
        Text(text, style = MaterialTheme.typography.bodyLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
private fun EntryRow(e: VaultEntrySummary, nowS: Long, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 16.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Icon(kindIcon(e.kind), contentDescription = VaultTexts.kindLabel(e.kind), tint = MaterialTheme.colorScheme.primary)
        Column(Modifier.weight(1f)) {
            Text(VaultListLogic.title(e), style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
            val sub = VaultListLogic.subtitle(e)
            if (sub.isNotEmpty()) {
                Text(sub, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            val expiry = if (VaultListLogic.isExpiring(e, nowS)) VaultTexts.expiryLabel(e.expiresAt, nowS) else ""
            if (expiry.isNotEmpty()) Text(expiry, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
        }
        if (e.hasTotp) Badge("TOTP")
    }
}

@Composable
private fun Badge(text: String) {
    Surface(color = MaterialTheme.colorScheme.secondaryContainer, shape = MaterialTheme.shapes.small) {
        Text(text, Modifier.padding(horizontal = 6.dp, vertical = 2.dp), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSecondaryContainer)
    }
}
