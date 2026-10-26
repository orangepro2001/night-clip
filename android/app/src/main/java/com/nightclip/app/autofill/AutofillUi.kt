package com.nightclip.app.autofill

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import com.nightclip.app.R
import com.nightclip.app.logic.autofill.RateLimit
import kotlinx.coroutines.delay

/** A card over a dimmed window: the look of both autofill screens. Tapping outside cancels. */
@Composable
fun AutofillSheet(onDismiss: () -> Unit, content: @Composable () -> Unit) {
    Box(
        Modifier
            .fillMaxSize()
            .background(Color.Black.copy(alpha = 0.45f))
            .clickable(interactionSource = remember { MutableInteractionSource() }, indication = null, onClick = onDismiss),
        contentAlignment = Alignment.Center,
    ) {
        Surface(
            Modifier
                .padding(24.dp)
                .fillMaxWidth()
                // swallow taps on the card so they do not reach the dismiss layer
                .clickable(interactionSource = remember { MutableInteractionSource() }, indication = null, onClick = {}),
            shape = MaterialTheme.shapes.extraLarge,
            tonalElevation = 6.dp,
        ) {
            Column(Modifier.verticalScroll(rememberScrollState()).padding(20.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) { content() }
        }
    }
}

@Composable
fun WorkingContent() {
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        CircularProgressIndicator(Modifier.padding(4.dp))
        Text(stringResource(R.string.autofill_working))
    }
}

/**
 * Master password entry. The text lives only in this composable and is cleared on submit.
 * [retryAfterS] > 0 (RATE_LIMITED) disables the button and counts down.
 */
@Composable
fun PasswordContent(
    title: String,
    text: String?,
    confirmLabel: String,
    busy: Boolean,
    error: String?,
    retryAfterS: Int,
    onSubmit: (String) -> Unit,
    onCancel: () -> Unit,
) {
    var pw by remember { mutableStateOf("") }
    var remaining by remember(retryAfterS) { mutableIntStateOf(retryAfterS) }
    LaunchedEffect(retryAfterS) {
        val start = System.currentTimeMillis()
        while (remaining > 0) {
            delay(500)
            remaining = RateLimit.remainingS(retryAfterS, System.currentTimeMillis() - start)
        }
    }
    Text(title, style = MaterialTheme.typography.titleLarge)
    if (text != null) Text(text, style = MaterialTheme.typography.bodyMedium)
    OutlinedTextField(
        value = pw,
        onValueChange = { pw = it },
        label = { Text(stringResource(R.string.autofill_master_password)) },
        singleLine = true,
        visualTransformation = PasswordVisualTransformation(),
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, autoCorrectEnabled = false),
        enabled = !busy,
        modifier = Modifier.fillMaxWidth(),
    )
    if (error != null) Text(error, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End, verticalAlignment = Alignment.CenterVertically) {
        TextButton(onClick = onCancel) { Text(stringResource(R.string.autofill_cancel)) }
        Button(
            onClick = {
                val v = pw
                pw = ""
                onSubmit(v)
            },
            enabled = !busy && pw.isNotEmpty() && remaining <= 0,
        ) { Text(if (remaining > 0) "$confirmLabel（${remaining}s）" else confirmLabel) }
    }
}

@Composable
fun MessageContent(text: String, openAppLabel: String?, onOpenApp: () -> Unit, onClose: () -> Unit) {
    Text(text, style = MaterialTheme.typography.bodyLarge)
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
        if (openAppLabel != null) TextButton(onClick = onOpenApp) { Text(openAppLabel) }
        TextButton(onClick = onClose) { Text(stringResource(R.string.close)) }
    }
}

/** A plain text field for the save form. */
@Composable
fun FormField(label: String, value: String, modifier: Modifier = Modifier, secret: Boolean = false, onChange: (String) -> Unit) {
    OutlinedTextField(
        value = value,
        onValueChange = onChange,
        label = { Text(label) },
        singleLine = true,
        visualTransformation = if (secret) PasswordVisualTransformation() else VisualTransformation.None,
        keyboardOptions = if (secret) KeyboardOptions(keyboardType = KeyboardType.Password, autoCorrectEnabled = false) else KeyboardOptions(autoCorrectEnabled = false),
        modifier = modifier.fillMaxWidth(),
    )
}
