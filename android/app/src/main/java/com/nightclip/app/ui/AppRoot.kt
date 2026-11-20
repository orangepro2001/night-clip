package com.nightclip.app.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Lock
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
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.nightclip.app.R
import com.nightclip.app.core.CoreHost
import com.nightclip.app.core.CoreState
import com.nightclip.app.platform.Actions
import com.nightclip.app.ui.vault.VaultRoot
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch

/** Requests from outside the composition (notification taps). */
class LaunchRequests {
    val tab = MutableStateFlow<String?>(null)
}

@Composable
fun AppRoot(requests: LaunchRequests) {
    val coreState by CoreHost.state.collectAsStateWithLifecycle()
    Surface(Modifier.fillMaxSize()) {
        when (val s = coreState) {
            is CoreState.Starting -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
            is CoreState.Failed -> ErrorScreen(s)
            is CoreState.Ready -> MainScaffold(requests)
        }
    }
}

private data class TabSpec(val id: String, val label: Int, val icon: ImageVector)

@Composable
private fun MainScaffold(requests: LaunchRequests) {
    val settingsVm: SettingsViewModel = viewModel()

    var tab by rememberSaveable { mutableStateOf(Actions.TAB_VAULT) }
    val snackbar = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()
    val show: (String) -> Unit = { msg -> scope.launch { snackbar.showSnackbar(msg) } }

    val requestedTab by requests.tab.collectAsStateWithLifecycle()
    LaunchedEffect(requestedTab) {
        requestedTab?.let {
            if (it in setOf(Actions.TAB_VAULT, Actions.TAB_SETTINGS)) tab = it
            requests.tab.value = null
        }
    }

    val tabs = listOf(
        TabSpec(Actions.TAB_VAULT, R.string.nav_vault, Icons.Filled.Lock),
        TabSpec(Actions.TAB_SETTINGS, R.string.nav_settings, Icons.Filled.Settings),
    )

    Scaffold(
        containerColor = MaterialTheme.colorScheme.background,
        snackbarHost = { SnackbarHost(snackbar) },
        bottomBar = {
            NavigationBar(
                containerColor = MaterialTheme.colorScheme.surface,
                tonalElevation = 0.dp,
            ) {
                tabs.forEach { t ->
                    NavigationBarItem(
                        selected = tab == t.id,
                        onClick = { tab = t.id },
                        icon = { Icon(t.icon, contentDescription = null, modifier = Modifier.size(21.dp)) },
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
        },
    ) { padding ->
        val mod = Modifier.padding(padding)
        when (tab) {
            Actions.TAB_SETTINGS -> SettingsScreen(settingsVm, onMessage = show, modifier = mod)
            else -> VaultRoot(onMessage = show, modifier = mod)
        }
    }
}
