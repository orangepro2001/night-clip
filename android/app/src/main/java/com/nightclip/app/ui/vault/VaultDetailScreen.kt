package com.nightclip.app.ui.vault

import android.content.ActivityNotFoundException
import android.content.Intent
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import com.nightclip.app.R
import com.nightclip.app.core.TotpCode
import com.nightclip.app.core.VaultEntryDetail
import com.nightclip.app.logic.Formatters
import com.nightclip.app.logic.MaskPolicy
import com.nightclip.app.logic.PasswordStrengthUi
import com.nightclip.app.logic.TotpClock
import com.nightclip.app.logic.UrlGuard
import com.nightclip.app.logic.VaultTexts
import com.nightclip.app.platform.VaultSession
import com.nightclip.app.ui.ConfirmDialog
import com.nightclip.app.ui.LabeledRow
import com.nightclip.app.ui.SectionTitle
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

@Composable
fun EntryDetailScreen(id: String, vm: VaultViewModel, browse: VaultBrowseViewModel, onMessage: (String) -> Unit) {
    val detail by browse.detail.collectAsStateWithLifecycle()
    val groups by browse.groups.collectAsStateWithLifecycle()
    val history by browse.history.collectAsStateWithLifecycle()
    LaunchedEffect(id) {
        browse.openDetail(id)
        browse.loadHistory(id)
    }

    val d = detail?.takeIf { it.id == id }
    var menu by remember { mutableStateOf(false) }
    var confirmDelete by remember { mutableStateOf(false) }
    var moveDialog by remember { mutableStateOf(false) }
    var showHistory by remember { mutableStateOf(false) }
    var removeAttachment by remember { mutableStateOf<String?>(null) }

    Column(Modifier.fillMaxSize()) {
        VaultTopBar(d?.title.orEmpty().ifBlank { stringResource(R.string.vault_entry) }, onBack = { vm.pop() }) {
            if (d != null && !d.inTrash) {
                IconButton(onClick = { vm.push(VaultRoute.Editor(d.id, d.kind)) }) { Icon(Icons.Filled.Edit, contentDescription = stringResource(R.string.vault_edit)) }
            }
            if (d != null) {
                Box {
                    IconButton(onClick = { menu = true }) { Icon(Icons.Filled.MoreVert, contentDescription = stringResource(R.string.more)) }
                    DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                        if (d.inTrash) {
                            DropdownMenuItem(text = { Text(stringResource(R.string.vault_restore_entry)) }, onClick = {
                                menu = false
                                browse.restoreEntry(d.id) { vm.pop() }
                            })
                        } else {
                            DropdownMenuItem(text = { Text(stringResource(R.string.vault_move)) }, onClick = { menu = false; moveDialog = true })
                        }
                        DropdownMenuItem(
                            text = { Text(stringResource(if (d.inTrash) R.string.vault_delete_forever else R.string.vault_delete)) },
                            onClick = { menu = false; confirmDelete = true },
                        )
                    }
                }
            }
        }
        if (d == null) {
            VaultLoading(Modifier.weight(1f))
        } else {
            DetailBody(d, browse, groups.firstOrNull { it.id == d.groupId }?.path.orEmpty(), history, showHistory, { showHistory = !showHistory }, onMessage) { removeAttachment = it }
        }
    }

    if (confirmDelete && d != null) {
        ConfirmDialog(
            title = stringResource(if (d.inTrash) R.string.vault_delete_forever else R.string.vault_delete),
            text = stringResource(if (d.inTrash) R.string.vault_delete_forever_text else R.string.vault_delete_text, d.title),
            confirmLabel = stringResource(if (d.inTrash) R.string.vault_delete_forever else R.string.vault_delete),
            onConfirm = {
                confirmDelete = false
                browse.deleteEntry(d.id) { vm.pop() }
            },
            onDismiss = { confirmDelete = false },
        )
    }
    if (moveDialog && d != null) {
        GroupPickerDialog(
            groups = groups.filter { !it.isTrash },
            selected = d.groupId,
            onPick = {
                moveDialog = false
                browse.moveEntry(d.id, it)
            },
            onDismiss = { moveDialog = false },
        )
    }
    removeAttachment?.let { name ->
        ConfirmDialog(
            title = stringResource(R.string.vault_attachment_remove),
            text = stringResource(R.string.vault_attachment_remove_text, name),
            confirmLabel = stringResource(R.string.vault_attachment_remove),
            onConfirm = {
                removeAttachment = null
                browse.removeAttachment(id, name)
            },
            onDismiss = { removeAttachment = null },
        )
    }
}

@Composable
private fun DetailBody(
    d: VaultEntryDetail,
    browse: VaultBrowseViewModel,
    groupPath: String,
    history: List<com.nightclip.app.core.VaultHistoryVersion>,
    showHistory: Boolean,
    onToggleHistory: () -> Unit,
    onMessage: (String) -> Unit,
    onRemoveAttachment: (String) -> Unit,
) {
    val context = LocalContext.current
    val nowS = System.currentTimeMillis() / 1000
    val template = com.nightclip.app.logic.KindTemplate.of(d.kind)

    var pendingSave by remember { mutableStateOf<String?>(null) }
    val saveLauncher = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/octet-stream")) { uri: Uri? ->
        val name = pendingSave
        pendingSave = null
        if (uri != null && name != null) browse.saveAttachment(d.id, name, uri)
    }
    val addLauncher = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri: Uri? ->
        if (uri != null) browse.addAttachment(d.id, uri)
    }

    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Icon(kindIcon(d.kind), contentDescription = null, tint = MaterialTheme.colorScheme.primary)
            Column {
                Text(d.title.ifBlank { stringResource(R.string.vault_entry) }, style = MaterialTheme.typography.titleLarge)
                Text(
                    VaultTexts.kindLabel(d.kind) + if (groupPath.isNotBlank()) " · $groupPath" else "",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        if (d.inTrash) {
            Text(stringResource(R.string.vault_in_trash), color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium)
        }
        val expiry = VaultTexts.expiryLabel(d.expiresAt, nowS)
        if (expiry.isNotEmpty()) {
            Text(expiry, color = if (d.expiresAt <= nowS) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant)
        }

        if (d.username.isNotEmpty()) {
            PlainFieldRow(stringResource(R.string.vault_field_username), d.username, onCopy = { browse.copy(d.id, "username") })
        }
        if (d.hasPassword) {
            SecretFieldRow(
                label = template.passwordLabel.ifEmpty { stringResource(R.string.vault_field_password) },
                fetch = { browse.reveal(d.id, "password") },
                onCopy = { browse.copy(d.id, "password") },
            )
            if (d.kind == VaultTexts.KIND_LOGIN) {
                Text(
                    stringResource(R.string.vault_strength, PasswordStrengthUi.text(d.strength.label)),
                    style = MaterialTheme.typography.bodySmall,
                    color = if (PasswordStrengthUi.isWeak(d.strength.label)) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        if (d.url.isNotEmpty()) {
            val web = UrlGuard.webUrl(d.url)
            PlainFieldRow(stringResource(R.string.vault_field_url), d.url, onCopy = { browse.copy(d.id, "url") }) {
                if (web != null) {
                    TextButton(onClick = {
                        try {
                            VaultSession.expectSystemUi()
                            context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(web)).addCategory(Intent.CATEGORY_BROWSABLE))
                        } catch (e: ActivityNotFoundException) {
                            onMessage(VaultTexts.NO_BROWSER)
                        }
                    }) { Text(stringResource(R.string.vault_open_url)) }
                }
            }
            if (web == null) HintText(VaultTexts.NOT_A_WEB_URL)
        }
        if (d.hasTotp) TotpRow(d.id, browse)

        d.fields.forEach { f ->
            if (f.protected) {
                SecretFieldRow(label = f.name, fetch = { browse.reveal(d.id, "field:${f.name}") }, onCopy = { browse.copy(d.id, "field:${f.name}") }, empty = !f.hasValue)
            } else {
                PlainFieldRow(f.name, f.value, onCopy = { browse.copy(d.id, "field:${f.name}") })
            }
        }

        if (d.notes.isNotEmpty()) {
            if (MaskPolicy.notesAreSecret(d.kind)) {
                SecretFieldRow(label = stringResource(R.string.vault_field_notes), fetch = { browse.reveal(d.id, "notes") }, onCopy = { browse.copy(d.id, "notes") }, multiline = true)
            } else {
                Column {
                    Text(stringResource(R.string.vault_field_notes), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    Text(d.notes, style = MaterialTheme.typography.bodyMedium)
                }
            }
        }
        if (d.tags.isNotEmpty()) {
            LabeledRow(stringResource(R.string.vault_field_tags), d.tags.joinToString("、"))
        }

        SectionTitle(stringResource(R.string.vault_attachments))
        if (d.attachments.isEmpty()) HintText(stringResource(R.string.vault_attachments_none))
        d.attachments.forEach { a ->
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(a.name, style = MaterialTheme.typography.bodyMedium)
                    HintText(Formatters.fileSize(a.size))
                }
                TextButton(onClick = {
                    pendingSave = a.name
                    VaultSession.expectSystemUi()
                    saveLauncher.launch(com.nightclip.app.logic.AttachmentNames.sanitize(a.name))
                }) { Text(stringResource(R.string.vault_attachment_save)) }
                if (!d.inTrash) TextButton(onClick = { onRemoveAttachment(a.name) }) { Text(stringResource(R.string.vault_attachment_remove)) }
            }
        }
        if (!d.inTrash) {
            OutlinedButton(onClick = {
                VaultSession.expectSystemUi()
                addLauncher.launch(arrayOf("*/*"))
            }) { Text(stringResource(R.string.vault_attachment_add)) }
        }

        if (d.historyCount > 0) {
            SectionTitle(stringResource(R.string.vault_history, d.historyCount))
            TextButton(onClick = onToggleHistory) { Text(stringResource(if (showHistory) R.string.vault_history_hide else R.string.vault_history_show)) }
            if (showHistory) {
                history.reversed().forEach { v ->
                    Card(Modifier.fillMaxWidth()) {
                        Row(Modifier.padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
                            Column(Modifier.weight(1f)) {
                                Text(v.title.ifBlank { stringResource(R.string.vault_entry) }, style = MaterialTheme.typography.bodyMedium)
                                HintText(listOf(v.username, v.url).filter { it.isNotBlank() }.joinToString(" · "))
                                HintText(VaultTexts.dateTime(v.modifiedAt))
                            }
                            if (!d.inTrash) TextButton(onClick = { browse.restoreVersion(d.id, v.index) }) { Text(stringResource(R.string.vault_history_restore)) }
                        }
                    }
                }
            }
        }
        HintText(stringResource(R.string.vault_created_modified, VaultTexts.dateTime(d.createdAt), VaultTexts.dateTime(d.modifiedAt)))
        Box(Modifier.height(24.dp))
    }
}

/** Label + plain value + copy button; [extra] adds buttons such as "open". */
@Composable
private fun PlainFieldRow(label: String, value: String, onCopy: () -> Unit, extra: @Composable () -> Unit = {}) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text(label, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text(value, style = MaterialTheme.typography.bodyLarge)
        }
        extra()
        TextButton(onClick = onCopy) { Text(stringResource(R.string.vault_copy)) }
    }
}

/**
 * A secret shown as dots. "Show" fetches it with vault.reveal and keeps it only for [MaskPolicy.AUTO_MASK_MS],
 * until the screen is left or the app is stopped. Copying always goes through vault.copy.
 */
@Composable
private fun SecretFieldRow(label: String, fetch: suspend () -> String?, onCopy: () -> Unit, empty: Boolean = false, multiline: Boolean = false) {
    var shown by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    LaunchedEffect(shown) {
        if (shown != null) {
            delay(MaskPolicy.AUTO_MASK_MS)
            shown = null
        }
    }
    LifecycleEventEffect(Lifecycle.Event.ON_STOP) { shown = null }
    DisposableEffect(Unit) { onDispose { shown = null } }

    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text(label, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text(
                if (empty) stringResource(R.string.vault_empty_value) else MaskPolicy.display(shown),
                style = MaterialTheme.typography.bodyLarge,
                fontFamily = if (shown != null && !multiline) FontFamily.Monospace else FontFamily.Default,
            )
        }
        if (!empty) {
            TextButton(onClick = {
                if (shown != null) shown = null else scope.launch { shown = fetch() }
            }) { Text(stringResource(if (shown != null) R.string.hide else R.string.show)) }
            TextButton(onClick = onCopy) { Text(stringResource(R.string.vault_copy)) }
        }
    }
}

/** Live one-time code: fetched when missing or expired, counted down locally once a second (and only while the screen is visible). */
@Composable
private fun TotpRow(id: String, browse: VaultBrowseViewModel) {
    var code by remember { mutableStateOf<TotpCode?>(null) }
    var clock by remember { mutableStateOf<TotpClock?>(null) }
    var period by remember { mutableStateOf(30) }
    var failed by remember { mutableStateOf(false) }
    var nowMs by remember { mutableLongStateOf(System.currentTimeMillis()) }
    val lifecycle = LocalLifecycleOwner.current.lifecycle

    LaunchedEffect(id) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                val now = System.currentTimeMillis()
                val c = clock
                if (c == null || c.isExpired(now)) {
                    val t = browse.totp(id)
                    if (t == null) {
                        failed = true
                        break
                    }
                    failed = false
                    code = t
                    period = TotpClock.guessPeriod(t.secondsLeft)
                    clock = TotpClock(System.currentTimeMillis(), t.secondsLeft)
                }
                nowMs = System.currentTimeMillis()
                delay(1000)
            }
        }
    }
    DisposableEffect(Unit) { onDispose { code = null; clock = null } }

    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(stringResource(R.string.vault_field_totp), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            val c = code
            val ck = clock
            if (failed || c == null || ck == null) {
                Text(if (failed) stringResource(R.string.vault_totp_failed) else "······", style = MaterialTheme.typography.bodyLarge)
            } else {
                val left = ck.remainingSeconds(nowMs)
                Text(
                    TotpClock.format(c.code),
                    style = MaterialTheme.typography.headlineSmall,
                    fontFamily = FontFamily.Monospace,
                    color = if (TotpClock.isUrgent(left)) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurface,
                )
                LinearProgressIndicator(progress = { ck.fraction(nowMs, period) }, modifier = Modifier.fillMaxWidth(0.6f))
                HintText(stringResource(R.string.vault_totp_left, left))
            }
        }
        TextButton(onClick = { browse.copy(id, "totp") }, enabled = code != null) { Text(stringResource(R.string.vault_copy)) }
    }
}

@Composable
fun GroupPickerDialog(
    groups: List<com.nightclip.app.core.VaultGroup>,
    selected: String,
    onPick: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.vault_move)) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState())) {
                groups.forEach { g ->
                    TextButton(onClick = { onPick(g.id) }, modifier = Modifier.fillMaxWidth()) {
                        Text((if (g.id == selected) "✓ " else "") + g.path.ifBlank { g.name }, modifier = Modifier.fillMaxWidth())
                    }
                }
            }
        },
        confirmButton = {},
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.cancel)) } },
    )
}
