package com.nightclip.app.ui

import android.Manifest
import android.content.ActivityNotFoundException
import android.content.pm.PackageManager
import android.net.Uri
import android.util.Log
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Phone
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.FilledTonalIconButton
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
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.nightclip.app.R
import com.nightclip.app.core.PeerInfo
import com.nightclip.app.core.TransferInfo
import com.nightclip.app.logic.CaptureFiles
import com.nightclip.app.logic.Formatters
import com.nightclip.app.logic.PeerTexts
import com.nightclip.app.logic.Strings
import com.nightclip.app.logic.TailscaleHint
import com.nightclip.app.platform.CameraCapture
import com.nightclip.app.platform.ClipboardAccess
import java.io.File

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

    // The photo being taken: where the camera app writes it, and who gets it. Saved state, because the camera
    // app is in front for a while and this screen may be recreated (even in a new process) before it returns.
    var capturePath by rememberSaveable { mutableStateOf<String?>(null) }
    var choosingCaptureTarget by remember { mutableStateOf(false) }
    var captureTargets by rememberSaveable { mutableStateOf("") }

    val sendPicked: (List<Uri>) -> Unit = { uris ->
        val target = fileTarget
        fileTarget = null
        if (target != null) vm.sendFiles(uris, target)
    }
    // GET_CONTENT is the most compatible picker across OEM file managers; OPEN_DOCUMENT is not
    // implemented by a few ROMs and launching it there ends in ActivityNotFoundException.
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.GetMultipleContents(), sendPicked)
    // The system photo picker (gallery: photos and videos, several at once, no storage permission).
    val mediaPicker = rememberLauncherForActivityResult(ActivityResultContracts.PickMultipleVisualMedia(), sendPicked)

    val camera = rememberLauncherForActivityResult(ActivityResultContracts.TakePicture()) { taken: Boolean ->
        val path = capturePath
        val targets = CaptureFiles.decodeTargets(captureTargets)
        capturePath = null
        captureTargets = ""
        if (path != null) {
            val file = File(path)
            if (taken) vm.sendCapture(file, targets) else file.delete()
        }
    }
    fun startCamera() {
        val pending = runCatching { CameraCapture.prepare(context) }.getOrElse { e ->
            Log.e("ClipBridge", "camera file failed", e)
            captureTargets = ""
            vm.report(context.getString(R.string.msg_file_send_failed) + " (" + e.javaClass.simpleName + ")")
            return
        }
        capturePath = pending.file.absolutePath
        runCatching { camera.launch(pending.uri) }.onFailure { e ->
            Log.e("ClipBridge", "camera launch failed", e)
            capturePath = null
            captureTargets = ""
            pending.file.delete()
            vm.report(
                context.getString(
                    when (e) {
                        is ActivityNotFoundException -> R.string.msg_camera_missing
                        is SecurityException -> R.string.msg_camera_denied
                        else -> R.string.msg_file_send_failed
                    },
                ),
            )
        }
    }
    val cameraPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted: Boolean ->
        if (granted) {
            startCamera()
        } else {
            captureTargets = ""
            vm.report(context.getString(R.string.msg_camera_denied))
        }
    }
    // The manifest declares CAMERA (for the pairing QR scanner), and with it declared the system camera app
    // may only be started once the permission is granted: ask first, then take the photo.
    fun takePhoto(targets: List<String>) {
        if (targets.isEmpty()) {
            vm.report(Strings.SEND_NOTHING)
            return
        }
        captureTargets = CaptureFiles.encodeTargets(targets)
        if (ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) {
            startCamera()
        } else {
            cameraPermission.launch(Manifest.permission.CAMERA)
        }
    }

    LifecycleRefresh { vm.refreshAll() }

    LazyColumn(
        modifier.fillMaxWidth(),
        contentPadding = PaddingValues(horizontal = 16.dp, vertical = 2.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        item {
            PageHeader(
                stringResource(R.string.nav_devices),
                subtitle = if (vpn) "Tailscale 已连接" else "Tailscale 未连接",
                actions = {
                    TextButton(onClick = onPair) { Text(stringResource(R.string.pair_new_device)) }
                },
            )
        }
        TailscaleHint.message(vpn)?.let { hint ->
            item { InfoCard(container = MaterialTheme.colorScheme.errorContainer) { Text(hint, color = MaterialTheme.colorScheme.onErrorContainer) } }
        }
        item {
            PanelCard {
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.fillMaxWidth()) {
                    Button(
                        onClick = {
                            // Reading the clipboard is only allowed while this screen is in the foreground (Android 10+).
                            vm.sendText(ClipboardAccess.readText(context).orEmpty())
                        },
                        enabled = peers.any { it.canSend },
                        modifier = Modifier.weight(1f).height(40.dp),
                    ) { Text(stringResource(R.string.send_clipboard)) }
                    FilledTonalButton(
                        onClick = onPair,
                        modifier = Modifier.weight(1f).height(40.dp),
                    ) { Text(stringResource(R.string.pair_new_device)) }
                    // Take a photo now and send it: straight away when only one device qualifies, otherwise ask which one.
                    FilledTonalIconButton(
                        onClick = {
                            val candidates = CaptureFiles.everyoneTargets(peers)
                            if (candidates.size > 1) choosingCaptureTarget = true else takePhoto(candidates)
                        },
                        enabled = peers.any { it.canSend },
                        modifier = Modifier.size(40.dp),
                    ) { Icon(painterResource(R.drawable.ic_photo_camera), contentDescription = stringResource(R.string.capture_photo)) }
                }
                Row(verticalAlignment = Alignment.Top, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedTextField(
                        value = manualText,
                        onValueChange = { manualText = it },
                        label = { Text(stringResource(R.string.send_text_label)) },
                        modifier = Modifier.weight(1f),
                        maxLines = 3,
                        keyboardOptions = KeyboardOptions.Default,
                    )
                    Button(
                        onClick = {
                            vm.sendText(manualText)
                            manualText = ""
                        },
                        enabled = manualText.isNotBlank() && peers.any { it.canSend },
                        modifier = Modifier.padding(top = 6.dp).height(40.dp),
                    ) { Text(stringResource(R.string.send)) }
                }
            }
        }
        item { SectionTitle(stringResource(R.string.paired_devices) + "  ${peers.size}") }
        if (peers.isEmpty()) {
            item {
                EmptyState(
                    Icons.Filled.Phone,
                    stringResource(R.string.no_devices_title),
                    stringResource(R.string.no_devices_hint),
                )
            }
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
                onSendMedia = {
                    fileTarget = peer.deviceId
                    runCatching {
                        mediaPicker.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageAndVideo))
                    }.onFailure { e ->
                        // Neither the photo picker nor ACTION_OPEN_DOCUMENT exists on this ROM: the plain file picker still lists the gallery.
                        Log.w("ClipBridge", "media picker unavailable, using the file picker", e)
                        runCatching { picker.launch("*/*") }.onFailure { e2 ->
                            fileTarget = null
                            Log.e("ClipBridge", "file picker failed", e2)
                            vm.report(context.getString(R.string.msg_file_picker_failed) + " (" + e2.javaClass.simpleName + ")")
                        }
                    }
                },
                onCapture = { takePhoto(listOf(peer.deviceId)) },
                onRename = { renaming = peer },
                onMode = { changingMode = peer },
                onToggleSend = { vm.setSend(peer.deviceId, !peer.sendEnabled) },
                onRemove = { removing = peer },
                onRepair = onPair,
            )
        }
        item { SectionTitle(stringResource(R.string.transfers) + "  ${transfers.size}") }
        if (transfers.isEmpty()) {
            item {
                Text(
                    stringResource(R.string.no_transfers),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(start = 2.dp, bottom = 8.dp),
                )
            }
        }
        items(transfers.take(20), key = { it.transferId }) { t ->
            TransferRow(t, onAccept = { vm.accept(t.transferId) }, onReject = { vm.reject(t.transferId) }, onCancel = { vm.cancel(t.transferId) })
        }
    }

    if (choosingCaptureTarget) {
        val candidates = CaptureFiles.everyoneTargets(peers)
        AlertDialog(
            onDismissRequest = { choosingCaptureTarget = false },
            title = { Text(stringResource(R.string.capture_target_title)) },
            text = {
                Column {
                    candidates.mapNotNull { id -> peers.firstOrNull { it.deviceId == id } }.forEach { p ->
                        TextButton(
                            onClick = { choosingCaptureTarget = false; takePhoto(listOf(p.deviceId)) },
                            modifier = Modifier.fillMaxWidth(),
                        ) { Text(p.name.ifBlank { Strings.UNKNOWN_DEVICE }) }
                    }
                    TextButton(
                        onClick = { choosingCaptureTarget = false; takePhoto(candidates) },
                        modifier = Modifier.fillMaxWidth(),
                    ) { Text(stringResource(R.string.capture_target_all)) }
                }
            },
            confirmButton = {},
            dismissButton = { TextButton(onClick = { choosingCaptureTarget = false }) { Text(stringResource(R.string.cancel)) } },
        )
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

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun PeerCard(
    peer: PeerInfo,
    onSendClipboard: () -> Unit,
    onSendFile: () -> Unit,
    onSendMedia: () -> Unit,
    onCapture: () -> Unit,
    onRename: () -> Unit,
    onMode: () -> Unit,
    onToggleSend: () -> Unit,
    onRemove: () -> Unit,
    onRepair: () -> Unit,
) {
    var menu by remember { mutableStateOf(false) }
    val now = System.currentTimeMillis() / 1000
    val stateColor = when (peer.state) {
        "connected" -> MaterialTheme.colorScheme.primary
        "unpaired", "revoked" -> MaterialTheme.colorScheme.error
        else -> MaterialTheme.colorScheme.onSurfaceVariant
    }
    PanelCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(
                        peer.name.ifBlank { Strings.UNKNOWN_DEVICE },
                        style = MaterialTheme.typography.titleMedium,
                        maxLines = 1,
                        overflow = androidx.compose.ui.text.style.TextOverflow.Ellipsis,
                    )
                    StatusPill(PeerTexts.stateLabel(peer), stateColor)
                }
                Text(
                    PeerTexts.platformLabel(peer.platform) + "  ·  " +
                        stringResource(R.string.last_seen, Formatters.relativeTime(peer.lastSeenAt, now)) + "  ·  " +
                        PeerTexts.applyModeLabel(peer.applyMode),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = androidx.compose.ui.text.style.TextOverflow.Ellipsis,
                )
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
        if (peer.state == "unpaired" || peer.state == "revoked") {
            OutlinedButton(onClick = onRepair, modifier = Modifier.height(36.dp)) { Text(stringResource(R.string.repair)) }
        } else {
            // Wraps onto a second line on a narrow screen instead of squeezing the labels.
            FlowRow(horizontalArrangement = Arrangement.spacedBy(4.dp), verticalArrangement = Arrangement.Center) {
                TextButton(onClick = onSendClipboard, modifier = Modifier.height(36.dp)) { Text(stringResource(R.string.send_clipboard)) }
                TextButton(onClick = onSendFile, modifier = Modifier.height(36.dp)) { Text(stringResource(R.string.send_file)) }
                TextButton(onClick = onSendMedia, modifier = Modifier.height(36.dp)) { Text(stringResource(R.string.send_media)) }
                IconButton(onClick = onCapture, modifier = Modifier.size(36.dp)) {
                    Icon(painterResource(R.drawable.ic_photo_camera), contentDescription = stringResource(R.string.capture_photo))
                }
            }
        }
    }
}

@Composable
private fun TransferRow(t: TransferInfo, onAccept: () -> Unit, onReject: () -> Unit, onCancel: () -> Unit) {
    PanelCard {
        val arrow = if (t.incoming) "↓" else "↑"
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(arrow, style = MaterialTheme.typography.titleMedium, color = MaterialTheme.colorScheme.primary)
            Text(t.name, style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f), maxLines = 1, overflow = androidx.compose.ui.text.style.TextOverflow.Ellipsis)
            Text(Formatters.fileSize(t.size), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Text(
            listOf(t.peerName, PeerTexts.transferStateLabel(t)).filter { it.isNotBlank() }.joinToString(" · "),
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
            Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                TextButton(onClick = onAccept, modifier = Modifier.height(36.dp)) { Text(stringResource(R.string.action_accept)) }
                TextButton(onClick = onReject, modifier = Modifier.height(36.dp)) { Text(stringResource(R.string.action_reject)) }
            }
        } else if (t.active) {
            TextButton(onClick = onCancel, modifier = Modifier.height(36.dp)) { Text(stringResource(R.string.action_cancel)) }
        }
    }
}
