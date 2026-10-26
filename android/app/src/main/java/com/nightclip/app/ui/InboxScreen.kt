package com.nightclip.app.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Email
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.nightclip.app.R
import com.nightclip.app.core.InboxItem
import com.nightclip.app.logic.Formatters
import com.nightclip.app.logic.NotificationTexts
import com.nightclip.app.logic.PeerTexts
import com.nightclip.app.logic.Strings
import com.nightclip.app.platform.ClipboardAccess

@Composable
fun InboxScreen(vm: InboxViewModel, onMessage: (String) -> Unit, modifier: Modifier = Modifier) {
    val context = LocalContext.current
    val items by vm.items.collectAsStateWithLifecycle()
    var revealed by rememberSaveable { mutableStateOf(setOf<String>()) }
    var confirmClear by remember { mutableStateOf(false) }
    val copiedText = stringResource(R.string.toast_copied)
    val failedText = stringResource(R.string.toast_copy_failed)

    LifecycleRefresh { vm.refresh() }
    CollectWhileStarted(vm.messages) { onMessage(it) }

    LazyColumn(
        modifier.fillMaxWidth(),
        contentPadding = PaddingValues(horizontal = 16.dp, vertical = 2.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        item {
            PageHeader(
                stringResource(R.string.nav_inbox),
                subtitle = "${items.size} 条内容",
                actions = {
                    TextButton(onClick = { confirmClear = true }, enabled = items.isNotEmpty()) { Text(stringResource(R.string.clear)) }
                },
            )
        }
        if (items.isEmpty()) {
            item { EmptyState(Icons.Filled.Email, stringResource(R.string.inbox_empty), "配对的设备发来的内容会出现在这里。") }
        }
        items(items, key = { it.eventId }) { item ->
            val show = item.eventId in revealed
            InboxCard(
                item,
                revealed = show,
                onReveal = { revealed = if (show) revealed - item.eventId else revealed + item.eventId },
                onCopy = {
                    // The button press happens in the visible app, so the write is allowed.
                    if (ClipboardAccess.writeText(context, item.text, item.sensitive)) {
                        vm.markApplied(item.eventId)
                        onMessage(copiedText)
                    } else {
                        onMessage(failedText)
                    }
                },
                onDismiss = { vm.dismiss(item.eventId) },
            )
        }
    }
    if (confirmClear) {
        ConfirmDialog(
            title = stringResource(R.string.clear_inbox_title),
            text = stringResource(R.string.clear_inbox_text),
            confirmLabel = stringResource(R.string.clear),
            onConfirm = {
                vm.clear()
                confirmClear = false
            },
            onDismiss = { confirmClear = false },
        )
    }
}

@Composable
private fun InboxCard(item: InboxItem, revealed: Boolean, onReveal: () -> Unit, onCopy: () -> Unit, onDismiss: () -> Unit) {
    val now = System.currentTimeMillis() / 1000
    PanelCard {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(
                item.fromName.ifBlank { Strings.UNKNOWN_DEVICE },
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.primary,
                modifier = Modifier.weight(1f),
                maxLines = 1,
                overflow = androidx.compose.ui.text.style.TextOverflow.Ellipsis,
            )
            Text(
                Formatters.relativeTime(item.receivedAt, now) + " · " + PeerTexts.inboxStateLabel(item.state),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Text(
            NotificationTexts.inboxBody(item.text, item.sensitive, revealed),
            style = MaterialTheme.typography.bodyLarge,
            maxLines = if (revealed || !item.sensitive) 8 else 3,
            overflow = androidx.compose.ui.text.style.TextOverflow.Ellipsis,
        )
        Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
            if (item.sensitive) {
                TextButton(onClick = onReveal, modifier = Modifier.height(36.dp)) { Text(stringResource(if (revealed) R.string.hide else R.string.show)) }
            }
            if (item.state == "received" || item.state == "applied") {
                TextButton(onClick = onCopy, modifier = Modifier.height(36.dp)) { Text(stringResource(R.string.action_copy)) }
            }
            if (item.state == "received") {
                TextButton(onClick = onDismiss, modifier = Modifier.height(36.dp)) { Text(stringResource(R.string.action_dismiss)) }
            }
        }
    }
}
