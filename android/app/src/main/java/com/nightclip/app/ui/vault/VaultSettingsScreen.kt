package com.nightclip.app.ui.vault

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
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
import androidx.compose.ui.unit.dp
import androidx.fragment.app.FragmentActivity
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.nightclip.app.R
import com.nightclip.app.logic.QuickUnlockAvailability
import com.nightclip.app.logic.VaultSettingOptions
import com.nightclip.app.ui.SectionTitle

@Composable
fun VaultSettingsScreen(vm: VaultViewModel, onMessage: (String) -> Unit) {
    val status by vm.status.collectAsStateWithLifecycle()
    val quick by vm.quickAvailability.collectAsStateWithLifecycle()
    val activity = LocalContext.current.findActivity() as? FragmentActivity
    var askPassword by remember { mutableStateOf(false) }
    val quickOn = quick == QuickUnlockAvailability.AVAILABLE
    val quickPossible = remember(activity) { activity != null && vm.quickAvailableOnDevice(activity) }

    Column(Modifier.fillMaxSize()) {
        VaultTopBar(stringResource(R.string.vault_menu_settings), onBack = { vm.pop() })
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            val s = status
            SectionTitle(stringResource(R.string.vault_settings_lock))
            ChoiceRow(
                stringResource(R.string.vault_settings_autolock),
                VaultSettingOptions.autolockLabel(s?.autolockS ?: 300),
                VaultSettingOptions.AUTOLOCK_SECONDS.map { it to VaultSettingOptions.autolockLabel(it) },
            ) { vm.setAutolock(it) }
            HintText(stringResource(R.string.vault_settings_autolock_hint))
            ChoiceRow(
                stringResource(R.string.vault_settings_clip_clear),
                VaultSettingOptions.clipClearLabel(s?.clipClearS ?: 20),
                VaultSettingOptions.CLIP_CLEAR_SECONDS.map { it to VaultSettingOptions.clipClearLabel(it) },
            ) { vm.setClipClear(it) }
            HintText(stringResource(R.string.vault_settings_clip_hint))

            SectionTitle(stringResource(R.string.vault_settings_quick))
            if (!quickPossible) {
                HintText(stringResource(R.string.vault_settings_quick_unavailable))
            } else {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(stringResource(R.string.vault_settings_quick_switch), Modifier.weight(1f))
                    Switch(checked = quickOn, onCheckedChange = { on ->
                        if (on) askPassword = true else vm.disableQuick()
                    })
                }
                HintText(stringResource(R.string.vault_settings_quick_hint))
            }

            SectionTitle(stringResource(R.string.vault_settings_info))
            if (s != null) {
                val kdf = s.kdf
                if (kdf != null) HintText(stringResource(R.string.vault_settings_kdf, kdf.memoryMiB, kdf.iterations))
                s.entries?.let { HintText(stringResource(R.string.vault_settings_entries, it)) }
                HintText(stringResource(R.string.vault_settings_backups, s.backups))
            }
            OutlinedButton(onClick = { vm.lock() }) { Text(stringResource(R.string.vault_lock_now)) }
        }
    }

    if (askPassword && activity != null) {
        CredentialsDialog(
            title = stringResource(R.string.vault_settings_quick_switch),
            text = stringResource(R.string.vault_settings_quick_password),
            confirmLabel = stringResource(R.string.vault_settings_quick_enable),
            onMessage = onMessage,
            onConfirm = { creds ->
                askPassword = false
                vm.enableQuickFromSettings(activity, creds.password, creds.keyFile)
            },
            onDismiss = { askPassword = false },
        )
    }
}

@Composable
private fun ChoiceRow(label: String, current: String, options: List<Pair<Int, String>>, onPick: (Int) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Text(label, Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium)
        Box {
            OutlinedButton(onClick = { open = true }) { Text(current) }
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
