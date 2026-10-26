package com.nightclip.app.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Email
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.Phone
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.NavigationBarItemDefaults
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.nightclip.app.R
import com.nightclip.app.app
import com.nightclip.app.core.CoreHost
import com.nightclip.app.core.CoreState
import com.nightclip.app.platform.Actions
import com.nightclip.app.ui.vault.VAULT_TAB_ID
import com.nightclip.app.ui.vault.VaultRoot
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch

/** Requests from outside the composition (notification taps, clipbridge:// links). */
class LaunchRequests {
    val tab = MutableStateFlow<String?>(null)
    val pairLink = MutableStateFlow<String?>(null)
}

@Composable
fun AppRoot(requests: LaunchRequests) {
    val context = LocalContext.current
    val coreState by CoreHost.state.collectAsStateWithLifecycle()
    val onboarded by context.app.prefs.onboarded.collectAsStateWithLifecycle()
    Surface(Modifier.fillMaxSize()) {
        when (val s = coreState) {
            is CoreState.Starting -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
            is CoreState.Failed -> ErrorScreen(s)
            is CoreState.Ready -> if (!onboarded) {
                OnboardingScreen(onDone = { context.app.prefs.setOnboarded(true) })
            } else {
                MainScaffold(requests)
            }
        }
    }
}

private data class TabSpec(val id: String, val label: Int, val icon: ImageVector)

@Composable
private fun MainScaffold(requests: LaunchRequests) {
    val appVm: AppViewModel = viewModel()
    val inboxVm: InboxViewModel = viewModel()
    val settingsVm: SettingsViewModel = viewModel()
    val pairVm: PairViewModel = viewModel()

    var tab by rememberSaveable { mutableStateOf(Actions.TAB_DEVICES) }
    var showPair by rememberSaveable { mutableStateOf(false) }
    val snackbar = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()
    val show: (String) -> Unit = { msg -> scope.launch { snackbar.showSnackbar(msg) } }

    val requestedTab by requests.tab.collectAsStateWithLifecycle()
    val requestedLink by requests.pairLink.collectAsStateWithLifecycle()
    LaunchedEffect(requestedTab) {
        requestedTab?.let {
            if (it in setOf(Actions.TAB_DEVICES, Actions.TAB_INBOX, Actions.TAB_SETTINGS)) {
                tab = it
                showPair = false
            }
            requests.tab.value = null
        }
    }
    LaunchedEffect(requestedLink) {
        requestedLink?.let {
            pairVm.setPrefill(it) // shown for the user to confirm; never joined automatically
            showPair = true
            requests.pairLink.value = null
        }
    }
    CollectWhileStarted(appVm.messages) { show(it) }

    val tabs = listOf(
        TabSpec(Actions.TAB_DEVICES, R.string.nav_devices, Icons.Filled.Phone),
        TabSpec(Actions.TAB_INBOX, R.string.nav_inbox, Icons.Filled.Email),
        TabSpec(VAULT_TAB_ID, R.string.nav_vault, Icons.Filled.Lock),
        TabSpec(Actions.TAB_SETTINGS, R.string.nav_settings, Icons.Filled.Settings),
    )

    Scaffold(
        containerColor = MaterialTheme.colorScheme.background,
        snackbarHost = { SnackbarHost(snackbar) },
        bottomBar = {
            if (!showPair) {
                NavigationBar(
                    containerColor = MaterialTheme.colorScheme.surface,
                    tonalElevation = 0.dp,
                    modifier = Modifier.height(66.dp),
                ) {
                    tabs.forEach { t ->
                        NavigationBarItem(
                            selected = tab == t.id,
                            onClick = { tab = t.id },
                            icon = { Icon(t.icon, contentDescription = null, modifier = Modifier.height(21.dp)) },
                            label = { Text(stringResource(t.label)) },
                            colors = NavigationBarItemDefaults.colors(
                                selectedIconColor = MaterialTheme.colorScheme.primary,
                                selectedTextColor = MaterialTheme.colorScheme.primary,
                                indicatorColor = MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.75f),
                                unselectedIconColor = MaterialTheme.colorScheme.onSurfaceVariant,
                                unselectedTextColor = MaterialTheme.colorScheme.onSurfaceVariant,
                            ),
                        )
                    }
                }
            }
        },
    ) { padding ->
        val mod = Modifier.padding(padding)
        when {
            showPair -> PairScreen(pairVm, onClose = {
                showPair = false
                appVm.refreshPeers()
            }, modifier = mod)
            tab == Actions.TAB_INBOX -> InboxScreen(inboxVm, onMessage = show, modifier = mod)
            tab == VAULT_TAB_ID -> VaultRoot(onMessage = show, modifier = mod)
            tab == Actions.TAB_SETTINGS -> SettingsScreen(settingsVm, onMessage = show, modifier = mod)
            else -> DevicesScreen(appVm, onPair = { showPair = true }, modifier = mod)
        }
    }
}
