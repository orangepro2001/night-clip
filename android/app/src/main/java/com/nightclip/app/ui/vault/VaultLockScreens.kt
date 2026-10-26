package com.nightclip.app.ui.vault

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.fragment.app.FragmentActivity
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.nightclip.app.R
import com.nightclip.app.logic.KdfLevel
import com.nightclip.app.logic.MasterPasswordRules
import com.nightclip.app.logic.PasswordStrengthUi
import com.nightclip.app.logic.KeyFileHint
import com.nightclip.app.logic.RateLimitCountdown
import com.nightclip.app.logic.VaultSyncLogic
import com.nightclip.app.ui.InfoCard
import kotlinx.coroutines.delay

@Composable
fun VaultLoading(modifier: Modifier = Modifier) {
    Column(modifier.fillMaxSize(), verticalArrangement = Arrangement.Center, horizontalAlignment = Alignment.CenterHorizontally) {
        CircularProgressIndicator()
    }
}

/** The locked state: master password, optional key file, rate-limit countdown and the quick unlock button. */
@Composable
fun UnlockScreen(vm: VaultViewModel, onMessage: (String) -> Unit, modifier: Modifier = Modifier) {
    var password by remember { mutableStateOf("") }
    var keyFile by remember { mutableStateOf<KeyFile?>(null) }
    val busy by vm.busy.collectAsStateWithLifecycle()
    val error by vm.unlockError.collectAsStateWithLifecycle()
    val rate by vm.rateLimit.collectAsStateWithLifecycle()
    val quick by vm.quickAvailability.collectAsStateWithLifecycle()
    val quickNotice by vm.quickNotice.collectAsStateWithLifecycle()
    val status by vm.status.collectAsStateWithLifecycle()
    val keyHint = KeyFileHint.of(status?.keyFile)
    var showKeyPicker by remember { mutableStateOf(false) }
    val activity = LocalContext.current.findActivity() as? FragmentActivity

    var nowMs by remember { mutableLongStateOf(System.currentTimeMillis()) }
    LaunchedEffect(rate) {
        nowMs = System.currentTimeMillis()
        while (rate.isActive(nowMs)) {
            delay(500)
            nowMs = System.currentTimeMillis()
        }
    }
    val waitS = rate.remainingSeconds(nowMs)

    val submit = {
        if (password.isNotEmpty() && !busy && waitS == 0) {
            val pw = password
            password = "" // never kept after use
            vm.unlock(pw, keyFile?.bytes)
        }
    }

    Column(
        modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Icon(Icons.Filled.Lock, contentDescription = null, tint = MaterialTheme.colorScheme.primary, modifier = Modifier.padding(top = 24.dp))
        Text(stringResource(R.string.vault_locked_title), style = MaterialTheme.typography.headlineSmall)
        HintText(stringResource(R.string.vault_locked_hint))
        CredentialsInput(
            password = password,
            onPassword = { password = it },
            keyFile = keyFile,
            onKeyFile = { keyFile = it },
            passwordLabel = stringResource(R.string.vault_master_password),
            onMessage = onMessage,
            imeAction = ImeAction.Done,
            onDone = { submit() },
            enabled = !busy,
            keyFileLabel = stringResource(
                if (keyHint == KeyFileHint.EXPECTED) R.string.vault_key_file_pick_expected else R.string.vault_key_file_pick,
            ),
            showKeyFile = keyHint != KeyFileHint.NOT_USED || showKeyPicker || keyFile != null,
        )
        if (keyHint == KeyFileHint.NOT_USED && !showKeyPicker && keyFile == null) {
            TextButton(onClick = { showKeyPicker = true }) { Text(stringResource(R.string.vault_key_file_show)) }
        }
        if (waitS > 0) {
            InfoCard(container = MaterialTheme.colorScheme.errorContainer) {
                Text(stringResource(R.string.vault_rate_limited, RateLimitCountdown.label(waitS)), color = MaterialTheme.colorScheme.onErrorContainer)
            }
        } else if (error != null) {
            Text(error.orEmpty(), color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium)
        }
        Button(onClick = { submit() }, enabled = password.isNotEmpty() && !busy && waitS == 0, modifier = Modifier.fillMaxWidth()) {
            if (busy) CircularProgressIndicator(Modifier.padding(end = 8.dp), strokeWidth = 2.dp)
            Text(stringResource(if (busy) R.string.vault_unlocking else R.string.vault_unlock))
        }
        if (activity != null && quick == com.nightclip.app.logic.QuickUnlockAvailability.AVAILABLE) {
            OutlinedButton(onClick = { vm.quickUnlock(activity) }, enabled = !busy, modifier = Modifier.fillMaxWidth()) {
                Text(stringResource(R.string.vault_quick_unlock))
            }
        }
        quickNotice?.let { HintText(it) }
        HintText(stringResource(R.string.vault_forgot_hint))
        TextButton(onClick = { vm.openSync() }) { Text(stringResource(R.string.vault_sync_title)) }
    }
}

/** Create flow: password + confirmation, live strength, optional key file, KDF choice and the "cannot be recovered" warning. */
@Composable
fun CreateVaultScreen(vm: VaultViewModel, onMessage: (String) -> Unit, modifier: Modifier = Modifier) {
    var password by remember { mutableStateOf("") }
    var confirm by remember { mutableStateOf("") }
    var keyFile by remember { mutableStateOf<KeyFile?>(null) }
    var strengthLabel by remember { mutableStateOf<String?>(null) }
    var advanced by remember { mutableStateOf(false) }
    var kdf by remember { mutableStateOf(vm.suggestedKdf) }
    var understood by remember { mutableStateOf(false) }
    val busy by vm.busy.collectAsStateWithLifecycle()

    LaunchedEffect(password) {
        if (password.isEmpty()) {
            strengthLabel = null
        } else {
            delay(250)
            strengthLabel = vm.strength(password)?.label
        }
    }
    val problem = MasterPasswordRules.problem(password, confirm)
    val mismatch = confirm.isNotEmpty() && problem == MasterPasswordRules.Problem.MISMATCH
    val canCreate = problem == null && understood && !busy

    Column(modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(stringResource(R.string.vault_create_title), style = MaterialTheme.typography.headlineSmall)
        InfoCard {
            Text(VaultSyncLogic.HINT_NO_VAULT, style = MaterialTheme.typography.bodyMedium)
            TextButton(onClick = { vm.openSync() }) { Text(stringResource(R.string.vault_sync_title)) }
        }
        InfoCard(container = MaterialTheme.colorScheme.errorContainer) {
            Text(
                stringResource(R.string.vault_forgot_warning_title),
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onErrorContainer,
            )
            Text(stringResource(R.string.vault_forgot_warning), color = MaterialTheme.colorScheme.onErrorContainer)
        }
        SecretTextField(password, { password = it }, stringResource(R.string.vault_master_password), enabled = !busy)
        StrengthBar(strengthLabel)
        PasswordStrengthUi.masterWarning(strengthLabel ?: "strong", password.length)?.let {
            Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
        }
        SecretTextField(
            confirm, { confirm = it }, stringResource(R.string.vault_master_confirm),
            isError = mismatch, supporting = if (mismatch) stringResource(R.string.vault_mismatch) else null,
            imeAction = ImeAction.Done, enabled = !busy,
        )
        val pick = rememberKeyFilePicker(onPicked = { if (it != null) keyFile = it }, onError = onMessage)
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            if (keyFile == null) {
                OutlinedButton(onClick = pick, enabled = !busy) { Text(stringResource(R.string.vault_key_file_pick)) }
            } else {
                Text(stringResource(R.string.vault_key_file_chosen, keyFile?.name.orEmpty()), Modifier.weight(1f))
                TextButton(onClick = { keyFile = null }) { Text(stringResource(R.string.vault_key_file_remove)) }
            }
        }
        HintText(stringResource(R.string.vault_key_file_hint))

        TextButton(onClick = { advanced = !advanced }) {
            Text(stringResource(if (advanced) R.string.vault_advanced_hide else R.string.vault_advanced_show))
        }
        if (advanced) {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(stringResource(R.string.vault_kdf_title), style = MaterialTheme.typography.titleSmall)
                vm.slowNotice?.let { HintText(it) }
                KdfLevel.entries.forEach { level ->
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        RadioButton(selected = kdf == level, onClick = { kdf = level })
                        Text(level.label, style = MaterialTheme.typography.bodyMedium)
                    }
                }
                HintText(stringResource(R.string.vault_kdf_hint))
            }
        }
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Checkbox(checked = understood, onCheckedChange = { understood = it })
            Text(stringResource(R.string.vault_understood), style = MaterialTheme.typography.bodyMedium)
        }
        Button(
            onClick = {
                val pw = password
                password = ""
                confirm = ""
                vm.create(pw, keyFile?.bytes, kdf)
            },
            enabled = canCreate,
            modifier = Modifier.fillMaxWidth(),
        ) {
            if (busy) CircularProgressIndicator(Modifier.padding(end = 8.dp), strokeWidth = 2.dp)
            Text(stringResource(if (busy) R.string.vault_creating else R.string.vault_create))
        }
    }
}
