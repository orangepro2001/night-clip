package com.nightclip.app.ui

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import com.nightclip.app.R
import com.nightclip.app.platform.BatteryOptimization

/** First-run guide (shell-guide §4 item 3). Nothing here is mandatory: "开始使用" is always available. */
@Composable
fun OnboardingScreen(onDone: () -> Unit, modifier: Modifier = Modifier) {
    val context = LocalContext.current
    fun notificationsGranted() = Build.VERSION.SDK_INT < 33 ||
        ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED
    var notifOk by remember { mutableStateOf(notificationsGranted()) }
    val notifLauncher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { notifOk = it || notificationsGranted() }

    Column(modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
        Text(stringResource(R.string.onboarding_title), style = MaterialTheme.typography.headlineMedium)
        Text(stringResource(R.string.onboarding_intro))

        InfoCard {
            Text(stringResource(R.string.onboarding_tailscale_title), style = MaterialTheme.typography.titleMedium)
            Text(stringResource(R.string.onboarding_tailscale_text))
            OutlinedButton(onClick = {
                val launch = context.packageManager.getLaunchIntentForPackage("com.tailscale.ipn")
                if (launch != null) context.startActivity(launch.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            }) { Text(stringResource(R.string.onboarding_open_tailscale)) }
        }

        InfoCard {
            Text(stringResource(R.string.onboarding_notif_title), style = MaterialTheme.typography.titleMedium)
            Text(stringResource(R.string.onboarding_notif_text))
            if (notifOk) {
                Text(stringResource(R.string.onboarding_granted), color = MaterialTheme.colorScheme.primary)
            } else {
                OutlinedButton(onClick = { notifLauncher.launch(Manifest.permission.POST_NOTIFICATIONS) }) {
                    Text(stringResource(R.string.onboarding_notif_button))
                }
            }
        }

        InfoCard {
            Text(stringResource(R.string.onboarding_battery_title), style = MaterialTheme.typography.titleMedium)
            Text(stringResource(R.string.onboarding_battery_text))
            OutlinedButton(onClick = { BatteryOptimization.open(context) }) { Text(stringResource(R.string.onboarding_battery_button)) }
        }

        Button(onClick = onDone, modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.onboarding_start)) }
    }
}
