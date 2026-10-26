package com.nightclip.app.ui

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.provider.Settings
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
import com.nightclip.app.logic.BackgroundReadSetup
import com.nightclip.app.logic.BackgroundReadStatus
import com.nightclip.app.logic.Formatters
import com.nightclip.app.logic.PeerTexts
import com.nightclip.app.logic.ShizukuFacts
import com.nightclip.app.logic.ShizukuSetup
import com.nightclip.app.logic.ShizukuStatus
import com.nightclip.app.logic.UpdatePresenter
import com.nightclip.app.platform.BatteryOptimization
import com.nightclip.app.platform.BackgroundReadRuntime
import com.nightclip.app.platform.ClipboardAccess
import com.nightclip.app.platform.RealtimeMode
import com.nightclip.app.platform.ShizukuRuntime

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
    val background by vm.backgroundRead.collectAsStateWithLifecycle()
    val vaultPreventCapture by context.app.prefs.vaultPreventCapture.collectAsStateWithLifecycle()
    var showBackgroundSetup by remember { mutableStateOf(false) }
    var confirmEnableBackground by remember { mutableStateOf(false) }
    var confirmDisableVaultCapture by remember { mutableStateOf(false) }

    LifecycleRefresh { vm.refresh() }
    CollectWhileStarted(vm.messages) { onMessage(it) }

    Column(modifier.verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(stringResource(R.string.nav_settings), style = MaterialTheme.typography.headlineSmall)

        SectionCard(stringResource(R.string.settings_realtime)) {
            SwitchRow(stringResource(R.string.settings_realtime_switch), realtime) { RealtimeMode.set(context, it) }
            Text(stringResource(R.string.settings_realtime_hint), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            OutlinedButton(onClick = { BatteryOptimization.open(context) }) { Text(stringResource(R.string.settings_battery)) }
        }

        ShizukuBackgroundCard(vm, realtime)

        // The READ_LOGS + overlay variant is only dependable below Android 13: from 13 on the system asks the user
        // to confirm every logcat connection and silently refuses it when the app is in the background.
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) SectionCard(stringResource(R.string.settings_background_read)) {
            SwitchRow(
                stringResource(R.string.settings_background_switch),
                background.enabled,
                enabled = background.supported,
            ) { on ->
                if (!on) {
                    showBackgroundSetup = false
                    vm.setBackgroundRead(false)
                } else if (!realtime) {
                    confirmEnableBackground = true
                } else {
                    vm.setBackgroundRead(true)
                    if (!background.overlayGranted || !background.logsGranted) showBackgroundSetup = true
                }
            }
            if (!background.supported) {
                Text(
                    stringResource(R.string.settings_background_status_unsupported),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            Text(
                stringResource(R.string.settings_background_hint),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            if (background.enabled) {
                StatusRow(
                    stringResource(R.string.settings_background_overlay),
                    if (background.overlayGranted) stringResource(R.string.settings_background_granted) else stringResource(R.string.settings_background_denied),
                    action = {
                        if (!background.overlayGranted) {
                            OutlinedButton(onClick = { openOverlaySettings(context) }) {
                                Text(stringResource(R.string.settings_background_open_overlay))
                            }
                        }
                    },
                )
                StatusRow(
                    stringResource(R.string.settings_background_logs),
                    if (background.logsGranted) stringResource(R.string.settings_background_granted) else stringResource(R.string.settings_background_denied),
                    action = {
                        if (!background.logsGranted) {
                            OutlinedButton(onClick = { showBackgroundSetup = true }) {
                                Text(stringResource(R.string.settings_background_show_setup))
                            }
                        }
                    },
                )
                StatusRow(
                    stringResource(R.string.settings_background_status),
                    backgroundStatusText(context, background),
                    action = {
                        when (BackgroundReadSetup.status(background.facts())) {
                            BackgroundReadStatus.REALTIME_OFF -> OutlinedButton(onClick = {
                                RealtimeMode.set(context, true)
                                vm.refreshBackgroundRead()
                            }) { Text(stringResource(R.string.settings_background_enable_realtime)) }
                            BackgroundReadStatus.WAITING_SERVICE -> OutlinedButton(onClick = {
                                try {
                                    RealtimeMode.start(context)
                                } catch (_: Exception) {
                                    // foreground service may be disallowed right now; status stays visible
                                }
                                vm.refreshBackgroundRead()
                            }) { Text(stringResource(R.string.settings_background_start_service)) }
                            BackgroundReadStatus.ERROR -> OutlinedButton(onClick = { vm.refreshBackgroundRead() }) {
                                Text(stringResource(R.string.settings_background_retry))
                            }
                            else -> Unit
                        }
                    },
                )
                Text(
                    stringResource(
                        R.string.settings_background_counts,
                        background.triggers,
                        background.readsOk,
                        background.readsFailed,
                        background.sends,
                    ),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedButton(onClick = { vm.refreshBackgroundRead() }) {
                        Text(stringResource(R.string.settings_background_retry))
                    }
                    OutlinedButton(onClick = { vm.testBackgroundRead() }) {
                        Text(stringResource(R.string.settings_background_test))
                    }
                }
                background.lastError?.let {
                    Text(
                        backgroundErrorText(context, it),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
                if (!realtime) {
                    Text(
                        stringResource(R.string.settings_background_status_realtime_off),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }

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

        SectionCard(stringResource(R.string.settings_sync)) {
            SwitchRow(stringResource(R.string.settings_capture), settings.capture) { vm.set("capture", if (it) "on" else "off") }
            Text(stringResource(R.string.settings_capture_hint), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            val nowSeconds = System.currentTimeMillis() / 1000
            ChoiceRow(
                stringResource(R.string.settings_pause),
                if (settings.isPaused(nowSeconds)) stringResource(R.string.settings_pause_until, Formatters.clockTime(settings.pausedUntil)) else stringResource(R.string.settings_pause_none),
                listOf(
                    "15" to stringResource(R.string.settings_pause_15), "60" to stringResource(R.string.settings_pause_60),
                    "240" to stringResource(R.string.settings_pause_240), "0" to stringResource(R.string.settings_pause_resume),
                ),
            ) { vm.pauseFor(it.toInt()) }
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
            SwitchRow(stringResource(R.string.settings_sensitive_detection), settings.sensitiveDetection) {
                vm.set("sensitive_detection", if (it) "on" else "off")
            }
            Text(
                stringResource(R.string.settings_sensitive_detection_hint),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
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

    if (confirmEnableBackground) {
        AlertDialog(
            onDismissRequest = { confirmEnableBackground = false },
            title = { Text(stringResource(R.string.settings_background_enable_title)) },
            text = { Text(stringResource(R.string.settings_background_realtime_needed)) },
            confirmButton = {
                TextButton(onClick = {
                    confirmEnableBackground = false
                    RealtimeMode.set(context, true)
                    vm.setBackgroundRead(true)
                    if (!background.overlayGranted || !background.logsGranted) showBackgroundSetup = true
                }) { Text(stringResource(R.string.settings_background_enable_confirm)) }
            },
            dismissButton = {
                TextButton(onClick = { confirmEnableBackground = false }) { Text(stringResource(R.string.cancel)) }
            },
        )
    }

    if (showBackgroundSetup) {
        AlertDialog(
            onDismissRequest = { showBackgroundSetup = false },
            title = { Text(stringResource(R.string.settings_background_setup_title)) },
            text = {
                Column(
                    Modifier.verticalScroll(rememberScrollState()),
                    verticalArrangement = Arrangement.spacedBy(10.dp),
                ) {
                    Text(stringResource(R.string.settings_background_setup_intro), style = MaterialTheme.typography.bodySmall)
                    Text(stringResource(R.string.settings_background_step_overlay), style = MaterialTheme.typography.bodySmall)
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Text(
                            if (background.overlayGranted) stringResource(R.string.settings_background_granted) else stringResource(R.string.settings_background_denied),
                            style = MaterialTheme.typography.bodySmall,
                        )
                        OutlinedButton(onClick = { openOverlaySettings(context) }) {
                            Text(stringResource(R.string.settings_background_open_overlay))
                        }
                    }
                    Text(stringResource(R.string.settings_background_step_logs), style = MaterialTheme.typography.bodySmall)
                    if (background.logsGranted) {
                        Text(stringResource(R.string.settings_background_logs_granted), style = MaterialTheme.typography.bodySmall)
                    } else {
                        Text(
                            BackgroundReadSetup.adbCommands(context.packageName),
                            fontFamily = FontFamily.Monospace,
                            fontSize = 11.sp,
                        )
                        OutlinedButton(onClick = { copyBackgroundCommands(context, onMessage) }) {
                            Text(stringResource(R.string.settings_background_copy_command))
                        }
                    }
                    Text(stringResource(R.string.settings_background_reopen), style = MaterialTheme.typography.bodySmall)
                    Text(
                        stringResource(R.string.settings_background_cost),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            },
            confirmButton = {
                TextButton(onClick = {
                    vm.refreshBackgroundRead()
                    showBackgroundSetup = false
                }) { Text(stringResource(R.string.done)) }
            },
            dismissButton = {
                TextButton(onClick = { showBackgroundSetup = false }) { Text(stringResource(R.string.close)) }
            },
        )
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
private fun StatusRow(label: String, value: String, action: (@Composable () -> Unit)? = null) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceBetween) {
        Text(label, Modifier.weight(1f))
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(value, style = MaterialTheme.typography.bodySmall)
            action?.invoke()
        }
    }
}

private fun backgroundStatusText(context: Context, r: BackgroundReadRuntime): String =
    context.getString(
        when (BackgroundReadSetup.status(r.facts())) {
            BackgroundReadStatus.OFF -> R.string.settings_background_status_off
            BackgroundReadStatus.UNSUPPORTED -> R.string.settings_background_status_unsupported
            BackgroundReadStatus.REALTIME_OFF -> R.string.settings_background_status_realtime_off
            BackgroundReadStatus.NEEDS_OVERLAY -> R.string.settings_background_status_needs_overlay
            BackgroundReadStatus.NEEDS_LOGS -> R.string.settings_background_status_needs_logs
            BackgroundReadStatus.WAITING_SERVICE -> R.string.settings_background_status_waiting
            BackgroundReadStatus.STARTING -> R.string.settings_background_status_starting
            BackgroundReadStatus.RUNNING -> R.string.settings_background_status_running
            BackgroundReadStatus.ERROR -> R.string.settings_background_status_error
        },
    )

private fun backgroundErrorText(context: Context, code: String): String = when (code) {
    "OVERLAY_DENIED" -> context.getString(R.string.background_error_overlay_denied)
    "READ_LOGS_DENIED" -> context.getString(R.string.background_error_logs_denied)
    "LOGCAT_START_FAILED", "LOGCAT_STOPPED", "WATCHER_START_FAILED" -> context.getString(R.string.background_error_logcat)
    "NO_FOCUS" -> context.getString(R.string.background_error_no_focus)
    "READ_DENIED" -> context.getString(R.string.background_error_read_denied)
    "EMPTY" -> context.getString(R.string.background_error_empty)
    "LOCKED" -> context.getString(R.string.background_error_locked)
    else -> context.getString(R.string.background_error_unknown, code)
}

private fun openOverlaySettings(context: Context) {
    val packageUri = Uri.parse("package:${context.packageName}")
    val intent = Intent(Settings.ACTION_MANAGE_OVERLAY_PERMISSION, packageUri)
        .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
    try {
        context.startActivity(intent)
    } catch (_: Exception) {
        try {
            context.startActivity(
                Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, packageUri)
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            )
        } catch (_: Exception) {
            // No settings screen to open; the ADB alternative is in the setup text.
        }
    }
}

private fun copyBackgroundCommands(context: Context, onMessage: (String) -> Unit) {
    val copied = ClipboardAccess.writeText(context, BackgroundReadSetup.adbCommands(context.packageName))
    onMessage(
        context.getString(
            if (copied) R.string.background_setup_copy_done else R.string.toast_copy_failed,
        ),
    )
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
private fun SwitchRow(label: String, checked: Boolean, enabled: Boolean = true, onChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceBetween) {
        Text(label, Modifier.weight(1f))
        Switch(checked = checked, onCheckedChange = onChange, enabled = enabled)
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


/** Silent background clipboard read through the helper that Shizuku runs as the shell user. */
@Composable
private fun ShizukuBackgroundCard(vm: SettingsViewModel, realtime: Boolean) {
    val context = LocalContext.current
    val background by vm.backgroundRead.collectAsStateWithLifecycle()
    val shizuku by vm.shizuku.collectAsStateWithLifecycle()
    val status = ShizukuSetup.status(shizuku.facts(background))

    LifecycleRefresh { vm.refreshShizuku() }

    SectionCard(stringResource(R.string.settings_shizuku_title)) {
        SwitchRow(stringResource(R.string.settings_shizuku_switch), background.enabled) { on ->
            if (on && !realtime) RealtimeMode.set(context, true) // the connection lives in the real-time service
            vm.setBackgroundRead(on)
            vm.refreshShizuku()
        }
        Text(
            stringResource(R.string.settings_shizuku_hint),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        if (background.enabled) {
            StatusRow(
                stringResource(R.string.settings_background_status),
                stringResource(shizukuStatusText(status)),
                action = {
                    when (status) {
                        ShizukuStatus.NOT_INSTALLED -> OutlinedButton(onClick = { openUrl(context, "https://shizuku.rikka.app/download/") }) {
                            Text(stringResource(R.string.settings_shizuku_get))
                        }
                        ShizukuStatus.NOT_RUNNING -> OutlinedButton(onClick = { openShizuku(context) }) {
                            Text(stringResource(R.string.settings_shizuku_open))
                        }
                        ShizukuStatus.NEEDS_PERMISSION -> Button(onClick = { vm.requestShizukuPermission() }) {
                            Text(stringResource(R.string.settings_shizuku_allow))
                        }
                        ShizukuStatus.WAITING_SERVICE -> OutlinedButton(onClick = {
                            try {
                                RealtimeMode.start(context)
                            } catch (_: Exception) {
                                // foreground service may be disallowed right now; the status stays visible
                            }
                            vm.refreshShizuku()
                        }) { Text(stringResource(R.string.settings_background_start_service)) }
                        ShizukuStatus.ERROR, ShizukuStatus.CONNECTING -> OutlinedButton(onClick = { vm.refreshShizuku() }) {
                            Text(stringResource(R.string.settings_background_retry))
                        }
                        else -> Unit
                    }
                },
            )
            Text(
                stringResource(R.string.settings_shizuku_counts, shizuku.received, background.sends),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            if (status == ShizukuStatus.NOT_INSTALLED || status == ShizukuStatus.NOT_RUNNING) {
                Text(
                    stringResource(R.string.settings_shizuku_steps),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            shizuku.error?.let {
                Text(
                    stringResource(R.string.settings_shizuku_error, it),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
        }
    }
}

private fun ShizukuRuntime.facts(background: BackgroundReadRuntime) = ShizukuFacts(
    enabled = background.enabled,
    realtime = background.realtime,
    installed = installed,
    shizukuRunning = shizukuRunning,
    permitted = permitted,
    serviceRunning = background.serviceRunning,
    connected = connected,
    hasError = error != null,
)

private fun shizukuStatusText(s: ShizukuStatus): Int = when (s) {
    ShizukuStatus.OFF -> R.string.settings_background_status_off
    ShizukuStatus.REALTIME_OFF -> R.string.settings_background_status_realtime_off
    ShizukuStatus.NOT_INSTALLED -> R.string.settings_shizuku_status_not_installed
    ShizukuStatus.NOT_RUNNING -> R.string.settings_shizuku_status_not_running
    ShizukuStatus.NEEDS_PERMISSION -> R.string.settings_shizuku_status_needs_permission
    ShizukuStatus.WAITING_SERVICE -> R.string.settings_background_status_waiting
    ShizukuStatus.CONNECTING -> R.string.settings_background_status_starting
    ShizukuStatus.RUNNING -> R.string.settings_background_status_running
    ShizukuStatus.ERROR -> R.string.settings_background_status_error
}

private fun openShizuku(context: Context) {
    val launch = context.packageManager.getLaunchIntentForPackage("moe.shizuku.privileged.api")
    try {
        if (launch != null) context.startActivity(launch.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        else openUrl(context, "https://shizuku.rikka.app/download/")
    } catch (_: Exception) {
        // nothing sensible to do; the status row stays as it is
    }
}

private fun openUrl(context: Context, url: String) {
    try {
        context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    } catch (_: Exception) {
        // no browser installed
    }
}
