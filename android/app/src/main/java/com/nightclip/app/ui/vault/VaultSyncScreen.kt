package com.nightclip.app.ui.vault

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Switch
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
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import com.nightclip.app.R
import com.nightclip.app.core.SyncPeer
import com.nightclip.app.core.VaultCreds
import com.nightclip.app.logic.RateLimitCountdown
import com.nightclip.app.logic.VaultSyncLogic
import com.nightclip.app.ui.InfoCard
import kotlinx.coroutines.delay

/** "设备同步": per-device switches, state texts, resolve dialogs and "sync now". Shown over every phase of the vault tab. */
@Composable
fun VaultSyncScreen(vm: VaultViewModel, sync: VaultSyncViewModel, onMessage: (String) -> Unit) {
    val peers by sync.peers.collectAsStateWithLifecycle()
    val loaded by sync.loaded.collectAsStateWithLifecycle()
    val vaultExists by sync.vaultExists.collectAsStateWithLifecycle()
    val dialog by sync.dialog.collectAsStateWithLifecycle()
    val phase by vm.phase.collectAsStateWithLifecycle()
    val unlocked = phase == VaultPhase.UNLOCKED
    val lifecycle = LocalLifecycleOwner.current.lifecycle

    BackHandler { vm.closeSync() }
    // Connection and "last synced" are not events: read again every few seconds while the screen is visible.
    LaunchedEffect(Unit) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                sync.refresh()
                delay(8_000)
            }
        }
    }

    Column(Modifier.fillMaxSize()) {
        VaultTopBar(stringResource(R.string.vault_sync_title), onBack = { vm.closeSync() })
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            InfoCard { Text(VaultSyncLogic.EXPLAINER, style = MaterialTheme.typography.bodyMedium) }
            if (!vaultExists && loaded) HintText(stringResource(R.string.vault_sync_no_vault))
            if (loaded && peers.isEmpty()) HintText(stringResource(R.string.vault_sync_no_devices))
            val nowS = System.currentTimeMillis() / 1000
            peers.forEach { p ->
                PeerCard(p, nowS, unlocked, onToggle = { sync.setEnabled(p.deviceId, it) }, onResolve = { sync.openDialog(p) })
            }
            Button(onClick = { sync.syncNow() }, enabled = VaultSyncLogic.canSyncNow(peers), modifier = Modifier.fillMaxWidth()) {
                Text(stringResource(R.string.vault_sync_now))
            }
            HintText(stringResource(R.string.vault_sync_remove_hint))
        }
    }

    val d = dialog
    val target = peers.firstOrNull { it.deviceId == d?.deviceId }
    if (d != null && target != null) {
        when (d) {
            is SyncDialog.Password -> SyncPasswordDialog(target, sync, onMessage)
            is SyncDialog.Review -> SyncReviewDialog(target, sync)
        }
    }
    LaunchedEffect(d, target) { if (d != null && target == null) sync.closeDialog() }
}

@Composable
private fun PeerCard(p: SyncPeer, nowS: Long, unlocked: Boolean, onToggle: (Boolean) -> Unit, onResolve: () -> Unit) {
    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(p.name.ifBlank { stringResource(R.string.vault_sync_unknown_device) }, style = MaterialTheme.typography.titleMedium)
                    HintText(VaultSyncLogic.connectionLabel(p) + " · " + VaultSyncLogic.lastSyncLabel(p, nowS))
                }
                Switch(checked = p.enabled, onCheckedChange = onToggle)
            }
            Text(
                VaultSyncLogic.stateLabel(p),
                style = MaterialTheme.typography.labelLarge,
                color = if (VaultSyncLogic.needsAttention(p) || p.state == VaultSyncLogic.STATE_ERROR) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.primary,
            )
            Text(VaultSyncLogic.stateExplanation(p), style = MaterialTheme.typography.bodyMedium)
            if (VaultSyncLogic.needsAttention(p)) {
                if (VaultSyncLogic.canResolve(p, unlocked)) {
                    OutlinedButton(onClick = onResolve) {
                        Text(stringResource(if (p.state == VaultSyncLogic.STATE_REVIEW) R.string.vault_sync_review else R.string.vault_sync_enter_password))
                    }
                } else {
                    HintText(VaultSyncLogic.LOCKED_RESOLVE_HINT)
                }
            }
        }
    }
}

@Composable
private fun SyncPasswordDialog(peer: SyncPeer, sync: VaultSyncViewModel, onMessage: (String) -> Unit) {
    var password by remember { mutableStateOf("") }
    var keyFile by remember { mutableStateOf<KeyFile?>(null) }
    val error by sync.dialogError.collectAsStateWithLifecycle()
    val busy by sync.busy.collectAsStateWithLifecycle()
    val rate by sync.rateLimit.collectAsStateWithLifecycle()
    var nowMs by remember { mutableLongStateOf(System.currentTimeMillis()) }
    LaunchedEffect(rate) {
        nowMs = System.currentTimeMillis()
        while (rate.isActive(nowMs)) {
            delay(500)
            nowMs = System.currentTimeMillis()
        }
    }
    val waitS = rate.remainingSeconds(nowMs)

    AlertDialog(
        onDismissRequest = { sync.closeDialog() },
        title = { Text(stringResource(R.string.vault_sync_password_title, peer.name.ifBlank { stringResource(R.string.vault_sync_unknown_device) })) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(stringResource(R.string.vault_sync_password_text), style = MaterialTheme.typography.bodyMedium)
                CredentialsInput(
                    password = password, onPassword = { password = it }, keyFile = keyFile, onKeyFile = { keyFile = it },
                    passwordLabel = stringResource(R.string.vault_sync_password_label), onMessage = onMessage, enabled = !busy,
                )
                if (waitS > 0) {
                    Text(stringResource(R.string.vault_rate_limited, RateLimitCountdown.label(waitS)), color = MaterialTheme.colorScheme.error)
                } else if (error != null) {
                    Text(error.orEmpty(), color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium)
                }
            }
        },
        confirmButton = {
            TextButton(enabled = password.isNotEmpty() && !busy && waitS == 0, onClick = {
                val creds = VaultCreds(password, keyFile?.bytes)
                password = "" // the dialog stays open if the password was wrong
                sync.resolveWithPassword(peer.deviceId, creds)
            }) { Text(stringResource(R.string.vault_sync_merge)) }
        },
        dismissButton = { TextButton(onClick = { sync.closeDialog() }) { Text(stringResource(R.string.cancel)) } },
    )
}

@Composable
private fun SyncReviewDialog(peer: SyncPeer, sync: VaultSyncViewModel) {
    val busy by sync.busy.collectAsStateWithLifecycle()
    AlertDialog(
        onDismissRequest = { sync.closeDialog() },
        title = { Text(stringResource(R.string.vault_sync_review_title, peer.name.ifBlank { stringResource(R.string.vault_sync_unknown_device) })) },
        text = { Text(VaultSyncLogic.reviewText(VaultSyncLogic.reviewKind(peer.detail))) },
        confirmButton = { TextButton(enabled = !busy, onClick = { sync.approve(peer.deviceId) }) { Text(stringResource(R.string.vault_sync_approve)) } },
        dismissButton = { TextButton(onClick = { sync.closeDialog() }) { Text(stringResource(R.string.vault_sync_ignore)) } },
    )
}
