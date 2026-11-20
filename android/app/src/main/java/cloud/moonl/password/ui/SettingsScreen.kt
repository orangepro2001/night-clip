package cloud.moonl.password.ui

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import cloud.moonl.password.ApkInstaller
import cloud.moonl.password.BuildConfig
import cloud.moonl.password.R
import cloud.moonl.password.UpdateState
import cloud.moonl.password.app
import cloud.moonl.password.logic.UpdatePresenter
import cloud.moonl.password.platform.Notifications

@Composable
fun SettingsScreen(vm: SettingsViewModel, onMessage: (String) -> Unit, modifier: Modifier = Modifier) {
    val context = LocalContext.current
    val update by vm.update.collectAsStateWithLifecycle()
    val deviceName by vm.deviceName.collectAsStateWithLifecycle()
    val diagnostics by vm.diagnostics.collectAsStateWithLifecycle()
    val vaultPreventCapture by context.app.prefs.vaultPreventCapture.collectAsStateWithLifecycle()
    val notificationsOn by context.app.prefs.notifications.collectAsStateWithLifecycle()
    var confirmDisableVaultCapture by remember { mutableStateOf(false) }

    LifecycleRefresh { vm.refresh() }
    CollectWhileStarted(vm.messages) { onMessage(it) }

    Column(
        modifier.verticalScroll(rememberScrollState()).padding(horizontal = 16.dp, vertical = 0.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        PageHeader(
            stringResource(R.string.nav_settings),
            subtitle = stringResource(R.string.settings_subtitle),
        )

        SectionCard(stringResource(R.string.settings_vault)) {
            SwitchRow(stringResource(R.string.settings_vault_capture), vaultPreventCapture) { on ->
                if (on) vm.setVaultPreventCapture(true) else confirmDisableVaultCapture = true
            }
            Text(
                stringResource(R.string.settings_vault_capture_hint),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }

        SectionCard(stringResource(R.string.settings_notifications)) {
            SwitchRow(stringResource(R.string.settings_notifications_switch), notificationsOn) { on ->
                context.app.prefs.setNotifications(on)
                Notifications.setUserEnabled(context, on)
            }
        }

        UpdateCard(update, vm, context, onMessage)

        SectionCard(stringResource(R.string.settings_about)) {
            LabeledRow(stringResource(R.string.about_version), "${BuildConfig.CB_VERSION_NAME} (${BuildConfig.CB_VERSION_CODE})")
            LabeledRow(stringResource(R.string.about_device_name), deviceName.ifBlank { "—" })
        }

        SectionCard(stringResource(R.string.settings_diagnostics)) {
            Text(stringResource(R.string.settings_diagnostics_hint), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            OutlinedButton(onClick = { vm.loadDiagnostics() }) { Text(stringResource(R.string.settings_diagnostics_show)) }
        }
    }

    if (confirmDisableVaultCapture) {
        AlertDialog(
            onDismissRequest = { confirmDisableVaultCapture = false },
            title = { Text(stringResource(R.string.settings_vault_capture_off_title)) },
            text = { Text(stringResource(R.string.settings_vault_capture_off_text)) },
            confirmButton = {
                TextButton(onClick = {
                    confirmDisableVaultCapture = false
                    vm.setVaultPreventCapture(false)
                }) { Text(stringResource(R.string.settings_vault_capture_off_confirm)) }
            },
            dismissButton = {
                TextButton(onClick = { confirmDisableVaultCapture = false }) { Text(stringResource(R.string.cancel)) }
            },
        )
    }

    diagnostics?.let { text ->
        val copied = stringResource(R.string.diagnostics_copied)
        val failed = stringResource(R.string.toast_copy_failed)
        AlertDialog(
            onDismissRequest = { vm.closeDiagnostics() },
            title = { Text(stringResource(R.string.settings_diagnostics)) },
            text = {
                Column(Modifier.verticalScroll(rememberScrollState())) {
                    Text(text, fontFamily = FontFamily.Monospace, fontSize = 11.sp)
                }
            },
            confirmButton = {
                TextButton(onClick = { onMessage(if (copyPlain(context, text)) copied else failed) }) {
                    Text(stringResource(R.string.diagnostics_copy))
                }
            },
            dismissButton = { TextButton(onClick = { vm.closeDiagnostics() }) { Text(stringResource(R.string.close)) } },
        )
    }
}

/** Copies non-secret text (the diagnostics report); secrets go through VaultClipboard. */
private fun copyPlain(context: Context, text: String): Boolean = try {
    val cm = context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
    cm.setPrimaryClip(ClipData.newPlainText(context.getString(R.string.app_name), text))
    true
} catch (e: Exception) {
    false
}

@Composable
private fun UpdateCard(update: UpdateState, vm: SettingsViewModel, context: Context, onMessage: (String) -> Unit) {
    val needPermission = stringResource(R.string.update_need_permission)
    val missing = stringResource(R.string.update_missing_file)
    SectionCard(stringResource(R.string.settings_update)) {
        Text(UpdatePresenter.text(update))
        if (update.notes.isNotEmpty() && update.state in listOf("available", "ready")) Text(update.notes, style = MaterialTheme.typography.bodySmall)
        if (update.state == "downloading") LinearProgressIndicator(progress = { update.progress }, modifier = Modifier.fillMaxWidth())
        if (update.action != UpdateState.Action.NONE) {
            Button(onClick = {
                when (update.action) {
                    UpdateState.Action.CHECK -> vm.checkUpdate()
                    UpdateState.Action.DOWNLOAD -> vm.downloadUpdate()
                    UpdateState.Action.INSTALL -> when (ApkInstaller.install(context, update.readyPath)) {
                        ApkInstaller.Result.STARTED -> Unit
                        ApkInstaller.Result.NEEDS_PERMISSION -> onMessage(needPermission)
                        ApkInstaller.Result.MISSING_FILE -> onMessage(missing)
                    }
                    UpdateState.Action.NONE -> Unit
                }
            }) { Text(UpdatePresenter.buttonText(update)) }
        }
        if (update.enabled) SwitchRow(stringResource(R.string.update_auto), update.auto) { vm.setAutoUpdate(it) }
    }
}

@Composable
private fun SectionCard(title: String, content: @Composable () -> Unit) {
    PanelCard {
        Text(title, style = MaterialTheme.typography.titleSmall)
        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
        Column(verticalArrangement = Arrangement.spacedBy(8.dp)) { content() }
    }
}

@Composable
private fun SwitchRow(label: String, checked: Boolean, enabled: Boolean = true, onChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceBetween) {
        Text(label, Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium)
        Switch(checked = checked, onCheckedChange = onChange, enabled = enabled)
    }
}
