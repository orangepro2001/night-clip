package com.nightclip.app.ui

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
import com.nightclip.app.R
import com.nightclip.app.core.CoreHost
import com.nightclip.app.core.CoreState

/** Visible error page for core start failures (shell-guide §4 item 1). */
@Composable
fun ErrorScreen(failure: CoreState.Failed, modifier: Modifier = Modifier) {
    val context = LocalContext.current
    var confirmReset by remember { mutableStateOf(false) }
    Column(modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
        Text(stringResource(R.string.error_title), style = MaterialTheme.typography.headlineMedium, color = MaterialTheme.colorScheme.error)
        Text(failure.message)
        Text(stringResource(R.string.error_code, failure.code), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Button(onClick = { CoreHost.start(context) }, modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.retry)) }
        if (failure.canReset) {
            Text(stringResource(R.string.error_reset_hint), style = MaterialTheme.typography.bodySmall)
            OutlinedButton(onClick = { confirmReset = true }, modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.error_reset)) }
        }
    }
    if (confirmReset) {
        ConfirmDialog(
            title = stringResource(R.string.error_reset_confirm_title),
            text = stringResource(R.string.error_reset_confirm_text),
            confirmLabel = stringResource(R.string.error_reset_confirm),
            onConfirm = {
                confirmReset = false
                CoreHost.resetData(context)
            },
            onDismiss = { confirmReset = false },
        )
    }
}
