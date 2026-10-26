package com.nightclip.app.ui.vault

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.imePadding
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.fragment.app.FragmentActivity
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.nightclip.app.R
import com.nightclip.app.logic.VaultTexts
import com.nightclip.app.platform.VaultSession
import com.nightclip.app.ui.CollectWhileStarted
import com.nightclip.app.ui.LifecycleRefresh

/** Id of the vault tab in the bottom navigation (see AppRoot). */
const val VAULT_TAB_ID = "vault"

/**
 * The "密码库" tab: locked / create / unlocked screens. While any of it is visible the window is FLAG_SECURE.
 * All three view models are scoped to the activity and wipe themselves when the vault locks.
 */
@Composable
fun VaultRoot(onMessage: (String) -> Unit, modifier: Modifier = Modifier) {
    val vm: VaultViewModel = viewModel()
    val browse: VaultBrowseViewModel = viewModel()
    val data: VaultDataViewModel = viewModel()

    SecureWindowEffect()
    CollectWhileStarted(vm.messages, onMessage)
    CollectWhileStarted(browse.messages, onMessage)
    CollectWhileStarted(data.messages, onMessage)
    CollectWhileStarted(VaultSession.locks) { reason -> if (reason != "user") onMessage(VaultTexts.LOCKED_NOTICE) }
    LifecycleRefresh { vm.refresh() }

    val phase by vm.phase.collectAsStateWithLifecycle()
    val body = modifier.imePadding()
    when (phase) {
        VaultPhase.LOADING -> VaultLoading(body)
        VaultPhase.NO_VAULT -> CreateVaultScreen(vm, onMessage, body)
        VaultPhase.LOCKED -> UnlockScreen(vm, onMessage, body)
        VaultPhase.UNLOCKED -> UnlockedHost(vm, browse, data, onMessage, body)
    }
}

@Composable
private fun UnlockedHost(
    vm: VaultViewModel,
    browse: VaultBrowseViewModel,
    data: VaultDataViewModel,
    onMessage: (String) -> Unit,
    modifier: Modifier,
) {
    val route by vm.route.collectAsStateWithLifecycle()
    val offerQuick by vm.offerQuick.collectAsStateWithLifecycle()
    val exportNudge by vm.exportNudge.collectAsStateWithLifecycle()
    val activity = LocalContext.current.findActivity() as? FragmentActivity

    BackHandler(enabled = route.isNotEmpty()) { vm.pop() }

    Box(modifier.fillMaxSize()) {
        Column(Modifier.fillMaxSize()) {
            when (val top = route.lastOrNull()) {
                null -> VaultListScreen(vm, browse)
                is VaultRoute.Detail -> EntryDetailScreen(top.id, vm, browse, onMessage)
                is VaultRoute.Editor -> EntryEditorScreen(top, vm, browse, onMessage)
                VaultRoute.Health -> HealthScreen(vm, browse)
                VaultRoute.Backups -> BackupsScreen(vm, data, onMessage)
                VaultRoute.Import -> ImportScreen(vm, data, onMessage)
                VaultRoute.Export -> ExportScreen(vm, data, onMessage)
                VaultRoute.ChangeMaster -> ChangeMasterScreen(vm, onMessage)
                VaultRoute.Settings -> VaultSettingsScreen(vm, onMessage)
            }
        }
    }

    val quickPossible = remember(activity, offerQuick) { activity != null && vm.quickAvailableOnDevice(activity) }
    LaunchedEffect(offerQuick, quickPossible) {
        if (offerQuick && !quickPossible) vm.skipQuickOffer() // no strong biometrics here: do not ask (and do not remember "no")
    }
    if (offerQuick && activity != null && quickPossible) {
        AlertDialog(
            onDismissRequest = { vm.declineQuickOffer() },
            title = { Text(stringResource(R.string.vault_quick_offer_title)) },
            text = { Text(stringResource(R.string.vault_quick_offer_text)) },
            confirmButton = { TextButton(onClick = { vm.acceptQuickOffer(activity) }) { Text(stringResource(R.string.vault_quick_offer_yes)) } },
            dismissButton = { TextButton(onClick = { vm.declineQuickOffer() }) { Text(stringResource(R.string.vault_quick_offer_no)) } },
        )
    }

    if (exportNudge && !offerQuick) {
        AlertDialog(
            onDismissRequest = { vm.dismissExportNudge() },
            title = { Text(stringResource(R.string.vault_export_nudge_title)) },
            text = { Text(stringResource(R.string.vault_export_nudge_text)) },
            confirmButton = {
                TextButton(onClick = {
                    vm.dismissExportNudge()
                    vm.push(VaultRoute.Export)
                }) { Text(stringResource(R.string.vault_export_nudge_yes)) }
            },
            dismissButton = { TextButton(onClick = { vm.dismissExportNudge() }) { Text(stringResource(R.string.vault_export_nudge_later)) } },
        )
    }
}
