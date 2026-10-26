package com.nightclip.app.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
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
import com.nightclip.app.ApkInstaller
import com.nightclip.app.BuildConfig
import com.nightclip.app.R
import com.nightclip.app.UpdateState
import com.nightclip.app.app
import com.nightclip.app.logic.Formatters
import com.nightclip.app.logic.PeerTexts
import com.nightclip.app.logic.UpdatePresenter
import com.nightclip.app.platform.BatteryOptimization
import com.nightclip.app.platform.ClipboardAccess
import com.nightclip.app.platform.RealtimeMode

private val TTL_OPTIONS = listOf(60, 300, 900, 3600, 86400)
private val HISTORY_OPTIONS = listOf(50, 200, 1000, 5000)

@Composable
fun SettingsScreen(vm: SettingsViewModel, onMessage: (String) -> Unit, modifier: Modifier = Modifier) {
    val context = LocalContext.current
    val settings by vm.settings.collectAsStateWithLifecycle()
    val update by vm.update.collectAsStateWithLifecycle()
    val identity by vm.identity.collectAsStateWithLifecycle()
    val diagnostics by vm.diagnostics.collectAsStateWithLifecycle()
    val realtime by context.app.prefs.realtime.collectAsStateWithLifecycle()

    LifecycleRefresh { vm.refresh() }
    CollectWhileStarted(vm.messages) { onMessage(it) }

    Column(modifier.verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(stringResource(R.string.nav_settings), style = MaterialTheme.typography.headlineSmall)

        SectionCard(stringResource(R.string.settings_realtime)) {
            SwitchRow(stringResource(R.string.settings_realtime_switch), realtime) { RealtimeMode.set(context, it) }
            Text(stringResource(R.string.settings_realtime_hint), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            OutlinedButton(onClick = { BatteryOptimization.open(context) }) { Text(stringResource(R.string.settings_battery)) }
        }

        SectionCard(stringResource(R.string.settings_sync)) {
            ChoiceRow(
                stringResource(R.string.settings_default_mode),
                PeerTexts.applyModeLabel(settings.defaultApplyMode),
                PeerTexts.APPLY_MODES.map { it to PeerTexts.applyModeLabel(it) },
            ) { vm.set("default_apply_mode", it) }
            ChoiceRow(
                stringResource(R.string.settings_ttl),
                Formatters.ttlLabel(settings.clipTtlS),
                TTL_OPTIONS.map { it.toString() to Formatters.ttlLabel(it) },
            ) { vm.set("clip_ttl_s", it) }
            ChoiceRow(
                stringResource(R.string.settings_history),
                stringResource(R.string.settings_history_value, settings.historyLimit),
                HISTORY_OPTIONS.map { it.toString() to context.getString(R.string.settings_history_value, it) },
            ) { vm.set("history_limit", it) }
            SwitchRow(stringResource(R.string.settings_send_sensitive), settings.sendSensitive) { vm.set("send_sensitive", if (it) "on" else "off") }
        }

        UpdateCard(update, vm, context, onMessage)

        SectionCard(stringResource(R.string.settings_about)) {
            LabeledRow(stringResource(R.string.about_version), "${BuildConfig.CB_VERSION_NAME} (${BuildConfig.CB_VERSION_CODE})")
            LabeledRow(stringResource(R.string.about_device_id), identity.first.ifBlank { "—" })
            LabeledRow(stringResource(R.string.about_fingerprint), identity.second.take(8).ifBlank { "—" })
        }

        SectionCard(stringResource(R.string.settings_diagnostics)) {
            Text(stringResource(R.string.settings_diagnostics_hint), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            OutlinedButton(onClick = { vm.loadDiagnostics() }) { Text(stringResource(R.string.settings_diagnostics_show)) }
        }
    }

    diagnostics?.let { text ->
        val copied = stringResource(R.string.diagnostics_copied)
        AlertDialog(
            onDismissRequest = { vm.closeDiagnostics() },
            title = { Text(stringResource(R.string.settings_diagnostics)) },
            text = {
                Column(Modifier.verticalScroll(rememberScrollState())) {
                    Text(text, fontFamily = FontFamily.Monospace, fontSize = 11.sp)
                }
            },
            confirmButton = {
                TextButton(onClick = {
                    if (ClipboardAccess.writeText(context, text)) onMessage(copied)
                }) { Text(stringResource(R.string.diagnostics_copy)) }
            },
            dismissButton = { TextButton(onClick = { vm.closeDiagnostics() }) { Text(stringResource(R.string.close)) } },
        )
    }
}

@Composable
private fun UpdateCard(update: UpdateState, vm: SettingsViewModel, context: android.content.Context, onMessage: (String) -> Unit) {
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
    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Text(title, style = MaterialTheme.typography.titleMedium)
            HorizontalDivider()
            content()
        }
    }
}

@Composable
private fun SwitchRow(label: String, checked: Boolean, onChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceBetween) {
        Text(label, Modifier.weight(1f))
        Switch(checked = checked, onCheckedChange = onChange)
    }
}

@Composable
private fun ChoiceRow(label: String, current: String, options: List<Pair<String, String>>, onPick: (String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceBetween) {
        Text(label, Modifier.weight(1f))
        Column {
            TextButton(onClick = { open = true }) { Text(current) }
            DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
                options.forEach { (value, text) ->
                    DropdownMenuItem(text = { Text(text) }, onClick = {
                        open = false
                        onPick(value)
                    })
                }
            }
        }
    }
}
