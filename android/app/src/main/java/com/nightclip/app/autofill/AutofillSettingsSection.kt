package com.nightclip.app.autofill

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
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
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.nightclip.app.R
import com.nightclip.app.logic.autofill.AutofillSetupLogic
import com.nightclip.app.platform.AutofillPrefs
import com.nightclip.app.platform.AutofillSetup

/**
 * Self-contained "自动填充" settings card: state of the system setting, the enable button and the
 * "每次填充前验证主密码" switch. The vault screens call `AutofillSettingsSection()` from their settings page
 * (it needs no view model); [AutofillSettingsActivity] shows the same card for the system's "settings" gear.
 */
@Composable
fun AutofillSettingsSection(modifier: Modifier = Modifier) {
    val context = LocalContext.current
    val prefs = remember { AutofillPrefs.get(context) }
    val requireAuth by prefs.requireAuth.collectAsStateWithLifecycle()
    var state by remember { mutableStateOf(AutofillSetup.state(context)) }
    // The user comes back from the system dialog: look again.
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) { state = AutofillSetup.state(context) }

    Card(modifier.fillMaxWidth()) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Text(stringResource(R.string.autofill_settings_title), style = MaterialTheme.typography.titleMedium)
            Text(
                stringResource(
                    when (state) {
                        AutofillSetupLogic.State.ENABLED -> R.string.autofill_status_enabled
                        AutofillSetupLogic.State.NOT_ENABLED -> R.string.autofill_status_disabled
                        AutofillSetupLogic.State.UNSUPPORTED -> R.string.autofill_status_unsupported
                    },
                ),
                style = MaterialTheme.typography.bodyMedium,
            )
            if (state == AutofillSetupLogic.State.NOT_ENABLED) {
                Button(onClick = { AutofillSetup.open(context) }) { Text(stringResource(R.string.autofill_enable)) }
            }
            if (state == AutofillSetupLogic.State.ENABLED) {
                OutlinedButton(onClick = { AutofillSetup.open(context) }) { Text(stringResource(R.string.autofill_open_system_settings)) }
            }
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceBetween) {
                Text(stringResource(R.string.autofill_require_auth), Modifier.weight(1f))
                Switch(checked = requireAuth, onCheckedChange = { prefs.setRequireAuth(it) })
            }
            Text(stringResource(R.string.autofill_require_auth_hint), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text(stringResource(R.string.autofill_browser_hint), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text(stringResource(R.string.autofill_untested_hint), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}
