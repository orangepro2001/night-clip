package com.nightclip.app.ui.vault

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.nightclip.app.R
import com.nightclip.app.core.VaultBackup
import com.nightclip.app.core.VaultCreds
import com.nightclip.app.logic.BackupLogic
import com.nightclip.app.logic.DiffSection
import com.nightclip.app.logic.Formatters
import com.nightclip.app.logic.VaultTexts
import com.nightclip.app.ui.ConfirmDialog
import com.nightclip.app.ui.InfoCard

/** Backup list, "back up now", verify, preview and restore (docs/plan-vault.md §8). */
@Composable
fun BackupsScreen(vm: VaultViewModel, data: VaultDataViewModel, onMessage: (String) -> Unit) {
    val backups by data.backups.collectAsStateWithLifecycle()
    val busy by data.busy.collectAsStateWithLifecycle()
    val status by vm.status.collectAsStateWithLifecycle()
    val verified by data.verified.collectAsStateWithLifecycle()
    val preview by data.preview.collectAsStateWithLifecycle()
    val result by data.result.collectAsStateWithLifecycle()

    var verifyTarget by remember { mutableStateOf<VaultBackup?>(null) }
    var previewTarget by remember { mutableStateOf<VaultBackup?>(null) }

    LaunchedEffect(Unit) { data.loadBackups() }

    val p = preview
    if (p != null) {
        RestorePreviewScreen(p, data, busy, onBack = { data.closePreview() })
        return
    }

    Column(Modifier.fillMaxSize()) {
        VaultTopBar(stringResource(R.string.vault_menu_backups), onBack = { vm.pop() })
        Column(Modifier.padding(horizontal = 16.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(onClick = { data.backupNow() }, enabled = !busy, modifier = Modifier.fillMaxWidth()) {
                if (busy) CircularProgressIndicator(Modifier.padding(end = 8.dp), strokeWidth = 2.dp)
                Text(stringResource(R.string.vault_backup_now))
            }
            val last = status?.lastManualBackupAt ?: 0
            val stale = BackupLogic.backupIsStale(last, System.currentTimeMillis() / 1000)
            HintText(
                if (last > 0) stringResource(R.string.vault_backup_last, VaultTexts.dateTime(last)) else stringResource(R.string.vault_backup_never),
            )
            if (stale) HintText(stringResource(R.string.vault_backup_stale))
            HintText(stringResource(R.string.vault_backup_auto_note))
        }
        if (backups.isEmpty()) {
            HintText(stringResource(R.string.vault_backup_empty), Modifier.padding(16.dp))
        } else {
            LazyColumn(Modifier.fillMaxSize(), contentPadding = androidx.compose.foundation.layout.PaddingValues(horizontal = 16.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                items(backups, key = { it.name }) { b ->
                    Card(Modifier.fillMaxWidth()) {
                        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                            Text(VaultTexts.backupReason(b.reason), style = MaterialTheme.typography.bodyLarge)
                            HintText(VaultTexts.dateTime(b.time) + " · " + Formatters.fileSize(b.size))
                            if (BackupLogic.mayNeedOldPassword(backups, b)) HintText(stringResource(R.string.vault_backup_old_password_hint))
                            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                TextButton(onClick = { verifyTarget = b }, enabled = !busy) { Text(stringResource(R.string.vault_backup_verify)) }
                                TextButton(onClick = { previewTarget = b }, enabled = !busy) { Text(stringResource(R.string.vault_backup_restore)) }
                            }
                        }
                    }
                }
            }
        }
    }

    verifyTarget?.let { b ->
        CredentialsDialog(
            title = stringResource(R.string.vault_backup_verify),
            text = stringResource(R.string.vault_backup_verify_text),
            oldPasswordHint = BackupLogic.mayNeedOldPassword(backups, b),
            confirmLabel = stringResource(R.string.vault_backup_verify),
            onMessage = onMessage,
            onConfirm = { creds ->
                verifyTarget = null
                data.verify(b, creds)
            },
            onDismiss = { verifyTarget = null },
        )
    }
    previewTarget?.let { b ->
        val maybeOld = BackupLogic.mayNeedOldPassword(backups, b)
        var useOld by remember(b) { mutableStateOf(false) }
        if (!maybeOld || !useOld) {
            AlertDialog(
                onDismissRequest = { previewTarget = null },
                title = { Text(stringResource(R.string.vault_backup_restore)) },
                text = {
                    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        Text(stringResource(R.string.vault_backup_preview_text))
                        if (maybeOld) {
                            Text(stringResource(R.string.vault_backup_old_password_hint), style = MaterialTheme.typography.bodySmall)
                            TextButton(onClick = { useOld = true }) { Text(stringResource(R.string.vault_backup_use_old)) }
                        }
                    }
                },
                confirmButton = {
                    TextButton(onClick = {
                        previewTarget = null
                        data.loadPreview(b, null)
                    }) { Text(stringResource(R.string.vault_backup_preview)) }
                },
                dismissButton = { TextButton(onClick = { previewTarget = null }) { Text(stringResource(R.string.cancel)) } },
            )
        } else {
            CredentialsDialog(
                title = stringResource(R.string.vault_backup_restore),
                text = stringResource(R.string.vault_backup_use_old_text),
                oldPasswordHint = true,
                confirmLabel = stringResource(R.string.vault_backup_preview),
                onMessage = onMessage,
                onConfirm = { creds ->
                    previewTarget = null
                    data.loadPreview(b, creds)
                },
                onDismiss = { previewTarget = null },
            )
        }
    }
    verified?.let { (name, check) ->
        AlertDialog(
            onDismissRequest = { data.clearVerified() },
            title = { Text(stringResource(R.string.vault_backup_verified_title)) },
            text = { Text(stringResource(R.string.vault_backup_verified_text, check.entries, check.groups, check.inTrash, VaultTexts.dateTime(check.modifiedAt).ifEmpty { "—" }, name)) },
            confirmButton = { TextButton(onClick = { data.clearVerified() }) { Text(stringResource(R.string.done)) } },
        )
    }
    result?.let { text ->
        AlertDialog(
            onDismissRequest = { data.dismissResult() },
            title = { Text(stringResource(R.string.vault_result_title)) },
            text = { Text(text) },
            confirmButton = { TextButton(onClick = { data.dismissResult() }) { Text(stringResource(R.string.done)) } },
        )
    }
}

/** Dialog that asks for a password (and optional key file) and hands them over once. */
@Composable
fun CredentialsDialog(
    title: String,
    text: String,
    confirmLabel: String,
    onMessage: (String) -> Unit,
    onConfirm: (VaultCreds) -> Unit,
    onDismiss: () -> Unit,
    oldPasswordHint: Boolean = false,
) {
    var password by remember { mutableStateOf("") }
    var keyFile by remember { mutableStateOf<KeyFile?>(null) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(text, style = MaterialTheme.typography.bodyMedium)
                CredentialsInput(
                    password = password, onPassword = { password = it }, keyFile = keyFile, onKeyFile = { keyFile = it },
                    passwordLabel = stringResource(if (oldPasswordHint) R.string.vault_password_maybe_old else R.string.vault_master_password),
                    onMessage = onMessage,
                )
            }
        },
        confirmButton = {
            TextButton(enabled = password.isNotEmpty(), onClick = {
                val creds = VaultCreds(password, keyFile?.bytes)
                password = ""
                onConfirm(creds)
            }) { Text(confirmLabel) }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.cancel)) } },
    )
}

@Composable
private fun RestorePreviewScreen(p: PreviewState, data: VaultDataViewModel, busy: Boolean, onBack: () -> Unit) {
    var mode by remember(p) { mutableStateOf(VaultTexts.RESTORE_ENTRIES) }
    var selected by remember(p) { mutableStateOf(BackupLogic.defaultSelection(p.preview)) }
    var confirmReplace by remember { mutableStateOf(false) }
    val sections = BackupLogic.group(p.preview)
    val selectable = BackupLogic.selectableIds(p.preview).toSet()
    val count = selected.count { it in selectable }

    androidx.activity.compose.BackHandler { onBack() }
    Column(Modifier.fillMaxSize()) {
        VaultTopBar(stringResource(R.string.vault_backup_preview), onBack = onBack)
        Column(Modifier.weight(1f).verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(VaultTexts.backupReason(p.backup.reason) + " · " + VaultTexts.dateTime(p.backup.time), style = MaterialTheme.typography.titleMedium)
            Text(BackupLogic.summary(p.preview), style = MaterialTheme.typography.bodyMedium)
            InfoCard { Text(BackupLogic.SAFETY_NOTE, style = MaterialTheme.typography.bodyMedium) }

            listOf(VaultTexts.RESTORE_ENTRIES, VaultTexts.RESTORE_MERGE, VaultTexts.RESTORE_REPLACE).forEach { m ->
                Row(verticalAlignment = Alignment.CenterVertically) {
                    RadioButton(selected = mode == m, onClick = { mode = m })
                    Column {
                        Text(VaultTexts.restoreModeLabel(m), style = MaterialTheme.typography.bodyLarge)
                        HintText(
                            stringResource(
                                when (m) {
                                    VaultTexts.RESTORE_ENTRIES -> R.string.vault_restore_mode_entries
                                    VaultTexts.RESTORE_MERGE -> R.string.vault_restore_mode_merge
                                    else -> R.string.vault_restore_mode_replace
                                },
                            ),
                        )
                    }
                }
            }

            sections.forEach { s ->
                Text(s.title, style = MaterialTheme.typography.titleSmall, modifier = Modifier.padding(top = 8.dp))
                HintText(s.hint)
                s.items.forEach { item ->
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                        if (mode == VaultTexts.RESTORE_ENTRIES && s.kind != DiffSection.Kind.ONLY_IN_CURRENT) {
                            Checkbox(checked = item.id in selected, onCheckedChange = { selected = BackupLogic.toggle(selected, item.id) })
                        }
                        Column {
                            Text(item.title.ifBlank { stringResource(R.string.vault_entry) }, style = MaterialTheme.typography.bodyMedium)
                            if (item.modifiedAt > 0) HintText(VaultTexts.dateTime(item.modifiedAt))
                        }
                    }
                }
            }
        }
        Button(
            onClick = { if (BackupLogic.needsExplicitConfirm(mode)) confirmReplace = true else data.restore(mode, selected) },
            enabled = !busy && BackupLogic.canRestore(mode, count),
            modifier = Modifier.fillMaxWidth().padding(16.dp),
        ) {
            if (busy) CircularProgressIndicator(Modifier.padding(end = 8.dp), strokeWidth = 2.dp)
            Text(if (mode == VaultTexts.RESTORE_ENTRIES) stringResource(R.string.vault_restore_go_entries, count) else VaultTexts.restoreModeLabel(mode))
        }
    }
    if (confirmReplace) {
        ConfirmDialog(
            title = stringResource(R.string.vault_restore_replace_title),
            text = stringResource(R.string.vault_restore_replace_text, p.preview.onlyInCurrent.size),
            confirmLabel = stringResource(R.string.vault_restore_replace_confirm),
            onConfirm = {
                confirmReplace = false
                data.restore(mode, selected)
            },
            onDismiss = { confirmReplace = false },
        )
    }
}
