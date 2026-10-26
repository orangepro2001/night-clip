package com.nightclip.app.ui.vault

import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.view.WindowManager
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.AccountCircle
import androidx.compose.material.icons.filled.Build
import androidx.compose.material.icons.filled.Create
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import com.nightclip.app.R
import com.nightclip.app.logic.MasterPasswordRules
import com.nightclip.app.logic.PasswordStrengthUi
import com.nightclip.app.logic.VaultTexts
import com.nightclip.app.platform.VaultFiles
import com.nightclip.app.platform.VaultSession
import kotlinx.coroutines.launch

/** Finds the Activity behind a Compose context (a ContextWrapper chain). */
fun Context.findActivity(): Activity? {
    var c: Context? = this
    while (c is ContextWrapper) {
        if (c is Activity) return c
        c = c.baseContext
    }
    return null
}

/**
 * Keeps FLAG_SECURE on the window for as long as this is in the composition (docs/vault-api.md §4 rule 2): no screenshots,
 * no screen recording, a blank thumbnail in the recent-apps list. Every activity instance sets and clears its own window,
 * so a rotation (new activity) is covered by the new composition.
 */
@Composable
fun SecureWindowEffect() {
    val activity = LocalContext.current.findActivity()
    DisposableEffect(activity) {
        activity?.window?.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
        onDispose { activity?.window?.clearFlags(WindowManager.LayoutParams.FLAG_SECURE) }
    }
}

/** A key file read from storage: its name for display and its bytes (<= 1 MiB). */
class KeyFile(val name: String, val bytes: ByteArray) {
    override fun toString() = "KeyFile($name)"
}

/** Returns a function that opens the system file chooser for a key file; [onPicked] gets null for a file that cannot be used. */
@Composable
fun rememberKeyFilePicker(onPicked: (KeyFile?) -> Unit, onError: (String) -> Unit): () -> Unit {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) {
            scope.launch {
                val bytes = VaultFiles.readLimited(context, uri, MasterPasswordRules.MAX_KEY_FILE_BYTES)
                if (bytes == null) {
                    onError(VaultTexts.KEY_FILE_TOO_BIG)
                    onPicked(null)
                } else {
                    onPicked(KeyFile(VaultFiles.displayName(context, uri) ?: "key", bytes))
                }
            }
        }
    }
    return {
        VaultSession.expectSystemUi()
        launcher.launch(arrayOf("*/*"))
    }
}

/** A password input: dots by default, "show" toggle, no suggestions, no autofill, password keyboard. */
@Composable
fun SecretTextField(
    value: String,
    onValueChange: (String) -> Unit,
    label: String,
    modifier: Modifier = Modifier,
    imeAction: ImeAction = ImeAction.Next,
    onDone: (() -> Unit)? = null,
    isError: Boolean = false,
    supporting: String? = null,
    enabled: Boolean = true,
) {
    var shown by remember { mutableStateOf(false) }
    OutlinedTextField(
        value = value,
        onValueChange = onValueChange,
        label = { Text(label) },
        modifier = modifier.fillMaxWidth(),
        singleLine = true,
        enabled = enabled,
        isError = isError,
        supportingText = supporting?.let { { Text(it) } },
        visualTransformation = if (shown) VisualTransformation.None else PasswordVisualTransformation(),
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, autoCorrectEnabled = false, imeAction = imeAction),
        keyboardActions = androidx.compose.foundation.text.KeyboardActions(onDone = { onDone?.invoke() }),
        trailingIcon = {
            TextButton(onClick = { shown = !shown }) { Text(stringResource(if (shown) R.string.hide else R.string.show)) }
        },
    )
}

/** Master password plus the optional key file; used by unlock, confirm dialogs, import, export and change-password. */
@Composable
fun CredentialsInput(
    password: String,
    onPassword: (String) -> Unit,
    keyFile: KeyFile?,
    onKeyFile: (KeyFile?) -> Unit,
    passwordLabel: String,
    onMessage: (String) -> Unit,
    modifier: Modifier = Modifier,
    imeAction: ImeAction = ImeAction.Done,
    onDone: (() -> Unit)? = null,
    enabled: Boolean = true,
    keyFileLabel: String = stringResource(R.string.vault_key_file_pick),
    showKeyFile: Boolean = true,
) {
    val pick = rememberKeyFilePicker(onPicked = { if (it != null) onKeyFile(it) }, onError = onMessage)
    Column(modifier, verticalArrangement = Arrangement.spacedBy(8.dp)) {
        SecretTextField(password, onPassword, passwordLabel, imeAction = imeAction, onDone = onDone, enabled = enabled)
        if (showKeyFile) Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            if (keyFile == null) {
                OutlinedButton(onClick = pick, enabled = enabled) { Text(keyFileLabel) }
            } else {
                Text(stringResource(R.string.vault_key_file_chosen, keyFile.name), Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium)
                TextButton(onClick = { onKeyFile(null) }) { Text(stringResource(R.string.vault_key_file_remove)) }
            }
        }
    }
}

/** Strength bar with the label; [label] null shows nothing (nothing typed yet). */
@Composable
fun StrengthBar(label: String?, modifier: Modifier = Modifier) {
    if (label == null) return
    val level = PasswordStrengthUi.level(label)
    val color = when (level) {
        0, 1 -> MaterialTheme.colorScheme.error
        2 -> Color(0xFFB26A00)
        else -> Color(0xFF2E7D32)
    }
    Column(modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(2.dp)) {
        LinearProgressIndicator(
            progress = { PasswordStrengthUi.fraction(label) },
            modifier = Modifier.fillMaxWidth().height(6.dp),
            color = color,
        )
        Text(
            stringResource(R.string.vault_strength, PasswordStrengthUi.text(label)),
            style = MaterialTheme.typography.bodySmall,
            color = color,
        )
    }
}

fun kindIcon(kind: String): ImageVector = when (kind) {
    VaultTexts.KIND_LOGIN -> Icons.Filled.AccountCircle
    VaultTexts.KIND_APIKEY -> Icons.Filled.Build
    VaultTexts.KIND_SECRET -> Icons.Filled.Lock
    VaultTexts.KIND_NOTE -> Icons.Filled.Create
    else -> Icons.Filled.Lock
}

/** Title bar of a vault sub-screen. [actions] go on the right. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun VaultTopBar(
    title: String,
    onBack: (() -> Unit)?,
    modifier: Modifier = Modifier,
    actions: @Composable androidx.compose.foundation.layout.RowScope.() -> Unit = {},
) {
    TopAppBar(
        title = { Text(title, maxLines = 1) },
        modifier = modifier,
        windowInsets = WindowInsets(0, 0, 0, 0),
        navigationIcon = {
            if (onBack != null) {
                IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.back)) }
            }
        },
        actions = actions,
    )
}

@Composable
fun HintText(text: String, modifier: Modifier = Modifier) {
    Text(text, modifier = modifier, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
}

@Composable
fun FormColumn(modifier: Modifier = Modifier, content: @Composable androidx.compose.foundation.layout.ColumnScope.() -> Unit) {
    Column(modifier.fillMaxWidth().padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp), content = content)
}
