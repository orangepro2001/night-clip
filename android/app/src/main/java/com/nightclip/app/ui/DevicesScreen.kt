package com.nightclip.app.ui

import android.net.Uri
import android.util.Log
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
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
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.nightclip.app.R
import com.nightclip.app.core.PeerInfo
import com.nightclip.app.core.TransferInfo
import com.nightclip.app.logic.Formatters
import com.nightclip.app.logic.PeerTexts
import com.nightclip.app.logic.Strings
import com.nightclip.app.logic.TailscaleHint
import com.nightclip.app.platform.ClipboardAccess

@Composable
fun DevicesScreen(vm: AppViewModel, onPair: () -> Unit, modifier: Modifier = Modifier) {
    val context = LocalContext.current
    val peers by vm.peers.collectAsStateWithLifecycle()
    val transfers by vm.transfers.collectAsStateWithLifecycle()
    val vpn by vm.vpnActive.collectAsStateWithLifecycle()

    var manualText by rememberSaveable { mutableStateOf("") }
    var fileTarget by rememberSaveable { mutableStateOf<String?>(null) }
    var renaming by remember { mutableStateOf<PeerInfo?>(null) }
    var changingMode by remember { mutableStateOf<PeerInfo?>(null) }
    var removing by remember { mutableStateOf<PeerInfo?>(null) }

    // GET_CONTENT is the most compatible picker across OEM file managers; OPEN_DOCUMENT is not
    // implemented by a few ROMs and launching it there ends in ActivityNotFoundException.
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.GetMultipleContents()) { uris: List<Uri> ->
        val target = fileTarget
        fileTarget = null
        if (target != null) vm.sendFiles(uris, target)
    }

    LifecycleRefresh { vm.refreshAll() }

    LazyColumn(modifier.fillMaxWidth(), contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        item { Text(stringResource(R.string.nav_devices), style = MaterialTheme.typography.headlineSmall) }
        TailscaleHint.message(vpn)?.let { hint ->
            item { InfoCard(container = MaterialTheme.colorScheme.errorContainer) { Text(hint, color = MaterialTheme.colorScheme.onErrorContainer) } }
        }
        item {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.fillMaxWidth()) {
                Button(onClick = {
                    // Reading the clipboard is only allowed while this screen is in the foreground (Android 10+).
                    vm.sendText(ClipboardAccess.readText(context).orEmpty())
                }, enabled = peers.any { it.canSend }) { Text(stringResource(R.string.send_clipboard)) }
                FilledTonalButton(onClick = onPair) { Text(stringResource(R.string.pair_new_device)) }
            }
        }
        item {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(
                    value = manualText,
                    onValueChange = { manualText = it },
                    label = { Text(stringResource(R.string.send_text_label)) },
                    modifier = Modifier.weight(1f),
                    maxLines = 4,
                    keyboardOptions = KeyboardOptions.Default,
                )
                Button(onClick = {
                    vm.sendText(manualText)
                    manualText = ""
                }, enabled = manualText.isNotBlank() && peers.any { it.canSend }) { Text(stringResource(R.string.send)) }
            }
        }
        item { SectionTitle(stringResource(R.string.paired_devices)) }
        if (peers.isEmpty()) {
            item { Text(stringResource(R.string.no_devices_hint), color = MaterialTheme.colorScheme.onSurfaceVariant) }
        }
        items(peers, key = { it.deviceId }) { peer ->
            PeerCard(
                peer,
                onSendClipboard = { vm.sendText(ClipboardAccess.readText(context).orEmpty(), listOf(peer.deviceId)) },
                onSendFile = {
                    fileTarget = peer.deviceId
                    runCatching { picker.launch("*/*") }.onFailure { e ->
                        fileTarget = null
                        Log.e("ClipBridge", "file picker failed", e)
                        vm.report(context.getString(R.string.msg_file_picker_failed) + " (" + e.javaClass.simpleName + ")")
                    }
                },
                onRename = { renaming = peer },
                onMode = { changingMode = peer },
                onToggleSend = { vm.setSend(peer.deviceId, !peer.sendEnabled) },
                onRemove = { removing = peer },
                onRepair = onPair,
            )
        }
        item { SectionTitle(stringResource(R.string.transfers)) }
        if (transfers.isEmpty()) {
            item { Text(stringResource(R.string.no_transfers), color = MaterialTheme.colorScheme.onSurfaceVariant) }
        }
        items(transfers.take(20), key = { it.transferId }) { t ->
            TransferRow(t, onAccept = { vm.accept(t.transferId) }, onReject = { vm.reject(t.transferId) }, onCancel = { vm.cancel(t.transferId) })
        }
    }

    renaming?.let { p ->
        var name by remember(p.deviceId) { mutableStateOf(p.name) }
        AlertDialog(
            onDismissRequest = { renaming = null },
            title = { Text(stringResource(R.string.rename_device)) },
            text = { OutlinedTextField(value = name, onValueChange = { name = it }, singleLine = true) },
            confirmButton = {
                TextButton(onClick = {
                    vm.rename(p.deviceId, name)
                    renaming = null
                }, enabled = name.isNotBlank()) { Text(stringResource(R.string.save)) }
            },
            dismissButton = { TextButton(onClick = { renaming = null }) { Text(stringResource(R.string.cancel)) } },
        )
    }
    changingMode?.let { p ->
        AlertDialog(
            onDismissRequest = { changingMode = null },
            title = { Text(stringResource(R.string.apply_mode_title, p.name)) },
            text = {
                Column {
                    PeerTexts.APPLY_MODES.forEach { mode ->
                        Row(
                            Modifier.fillMaxWidth().selectable(selected = p.applyMode == mode, role = Role.RadioButton, onClick = {
                                vm.setMode(p.deviceId, mode)
                                changingMode = null
                            }),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            RadioButton(selected = p.applyMode == mode, onClick = null)
                            Text(PeerTexts.applyModeLabel(mode), Modifier.padding(start = 8.dp, top = 12.dp, bottom = 12.dp))
                        }
                    }
                    Text(stringResource(R.string.apply_mode_note), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            },
            confirmButton = {},
            dismissButton = { TextButton(onClick = { changingMode = null }) { Text(stringResource(R.string.cancel)) } },
        )
    }
    removing?.let { p ->
        ConfirmDialog(
            title = stringResource(R.string.remove_device_title),
            text = stringResource(R.string.remove_device_text, p.name),
            confirmLabel = stringResource(R.string.remove),
            onConfirm = {
                vm.remove(p.deviceId)
                removing = null
            },
            onDismiss = { removing = null },
        )
    }
}

@Composable
private fun PeerCard(
    peer: PeerInfo,
    onSendClipboard: () -> Unit,
    onSendFile: () -> Unit,
    onRename: () -> Unit,
    onMode: () -> Unit,
    onToggleSend: () -> Unit,
    onRemove: () -> Unit,
    onRepair: () -> Unit,
) {
    var menu by remember { mutableStateOf(false) }
    val now = System.currentTimeMillis() / 1000
    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(peer.name.ifBlank { Strings.UNKNOWN_DEVICE }, style = MaterialTheme.typography.titleMedium)
                    Text(PeerTexts.platformLabel(peer.platform), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                IconButton(onClick = { menu = true }) { Icon(Icons.Filled.MoreVert, contentDescription = stringResource(R.string.more)) }
                DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                    DropdownMenuItem(text = { Text(stringResource(R.string.rename)) }, onClick = { menu = false; onRename() })
                    DropdownMenuItem(text = { Text(stringResource(R.string.apply_mode_menu, PeerTexts.applyModeLabel(peer.applyMode))) }, onClick = { menu = false; onMode() })
                    DropdownMenuItem(
                        text = { Text(stringResource(if (peer.sendEnabled) R.string.auto_send_on else R.string.auto_send_off)) },
                        onClick = { menu = false; onToggleSend() },
                    )
                    DropdownMenuItem(text = { Text(stringResource(R.string.remove)) }, onClick = { menu = false; onRemove() })
                }
            }
            Text(
                PeerTexts.stateLabel(peer),
                color = when (peer.state) {
                    "connected" -> MaterialTheme.colorScheme.primary
                    "unpaired", "revoked" -> MaterialTheme.colorScheme.error
                    else -> MaterialTheme.colorScheme.onSurfaceVariant
                },
            )
            Text(
                stringResource(R.string.last_seen, Formatters.relativeTime(peer.lastSeenAt, now)) + " · " + PeerTexts.applyModeLabel(peer.applyMode),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            if (peer.state == "unpaired" || peer.state == "revoked") {
                OutlinedButton(onClick = onRepair) { Text(stringResource(R.string.repair)) }
            } else {
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    FilledTonalButton(onClick = onSendClipboard) { Text(stringResource(R.string.send_clipboard)) }
                    FilledTonalButton(onClick = onSendFile) { Text(stringResource(R.string.send_file)) }
                }
            }
        }
    }
}

@Composable
private fun TransferRow(t: TransferInfo, onAccept: () -> Unit, onReject: () -> Unit, onCancel: () -> Unit) {
    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            val arrow = if (t.incoming) "↓" else "↑"
            Text("$arrow ${t.name}", style = MaterialTheme.typography.titleSmall)
            Text(
                listOf(t.peerName, Formatters.fileSize(t.size), PeerTexts.transferStateLabel(t)).filter { it.isNotBlank() }.joinToString(" · "),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            if (t.state == "transferring") {
                LinearProgressIndicator(progress = { Formatters.percent(t.offset, t.size) / 100f }, modifier = Modifier.fillMaxWidth())
                Text(Formatters.progressLine(t.offset, t.size, t.rateBps), style = MaterialTheme.typography.bodySmall)
            } else if (t.state == "hashing") {
                LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
            }
            if (t.awaitingMyDecision) {
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Button(onClick = onAccept) { Text(stringResource(R.string.action_accept)) }
                    OutlinedButton(onClick = onReject) { Text(stringResource(R.string.action_reject)) }
                }
            } else if (t.active) {
                OutlinedButton(onClick = onCancel) { Text(stringResource(R.string.action_cancel)) }
            }
        }
    }
}
