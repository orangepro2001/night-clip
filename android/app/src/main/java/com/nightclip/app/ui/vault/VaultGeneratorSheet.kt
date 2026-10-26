package com.nightclip.app.ui.vault

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Slider
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.nightclip.app.R
import com.nightclip.app.core.GeneratedSecret
import com.nightclip.app.logic.GeneratorOptions
import kotlin.math.roundToInt

/** Password / passphrase generator in a bottom sheet. The generated value only exists in this composition until "use". */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun GeneratorSheet(browse: VaultBrowseViewModel, onUse: (String) -> Unit, onDismiss: () -> Unit) {
    var opts by remember { mutableStateOf(GeneratorOptions()) }
    var nonce by remember { mutableIntStateOf(0) }
    var result by remember { mutableStateOf<GeneratedSecret?>(null) }

    LaunchedEffect(opts, nonce) { result = browse.generate(opts) }

    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(
            Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(horizontal = 16.dp).navigationBarsPadding(),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            Text(stringResource(R.string.vault_generate), style = MaterialTheme.typography.titleMedium)
            Text(
                result?.value.orEmpty().ifEmpty { "…" },
                style = MaterialTheme.typography.titleMedium,
                fontFamily = FontFamily.Monospace,
                modifier = Modifier.fillMaxWidth(),
            )
            StrengthBar(result?.strength?.label)
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                FilterChip(selected = !opts.passphrase, onClick = { opts = opts.copy(passphrase = false) }, label = { Text(stringResource(R.string.vault_gen_password)) })
                FilterChip(selected = opts.passphrase, onClick = { opts = opts.copy(passphrase = true) }, label = { Text(stringResource(R.string.vault_gen_passphrase)) })
            }
            if (!opts.passphrase) {
                Text(stringResource(R.string.vault_gen_length, opts.length))
                Slider(
                    value = opts.length.toFloat(),
                    onValueChange = { opts = opts.copy(length = it.roundToInt()) },
                    valueRange = 8f..128f,
                )
                ToggleRow(stringResource(R.string.vault_gen_lower), opts.lower) { opts = opts.withClass('l', it) }
                ToggleRow(stringResource(R.string.vault_gen_upper), opts.upper) { opts = opts.withClass('u', it) }
                ToggleRow(stringResource(R.string.vault_gen_digits), opts.digits) { opts = opts.withClass('d', it) }
                ToggleRow(stringResource(R.string.vault_gen_symbols), opts.symbols) { opts = opts.withClass('s', it) }
                ToggleRow(stringResource(R.string.vault_gen_ambiguous), opts.excludeAmbiguous) { opts = opts.copy(excludeAmbiguous = it) }
            } else {
                Text(stringResource(R.string.vault_gen_words, opts.words))
                Slider(
                    value = opts.words.toFloat(),
                    onValueChange = { opts = opts.copy(words = it.roundToInt()) },
                    valueRange = GeneratorOptions.MIN_WORDS.toFloat()..12f,
                )
                OutlinedTextField(
                    opts.separator, { opts = opts.copy(separator = it.take(4)) },
                    singleLine = true, label = { Text(stringResource(R.string.vault_gen_separator)) }, modifier = Modifier.fillMaxWidth(),
                )
                ToggleRow(stringResource(R.string.vault_gen_capitalize), opts.capitalize) { opts = opts.copy(capitalize = it) }
                ToggleRow(stringResource(R.string.vault_gen_number), opts.number) { opts = opts.copy(number = it) }
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.padding(bottom = 16.dp)) {
                OutlinedButton(onClick = { nonce += 1 }) { Text(stringResource(R.string.vault_gen_again)) }
                Button(onClick = { result?.value?.let(onUse) }, enabled = result != null) { Text(stringResource(R.string.vault_gen_use)) }
            }
        }
    }
}

@Composable
private fun ToggleRow(label: String, checked: Boolean, onChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Text(label, Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium)
        Switch(checked = checked, onCheckedChange = onChange)
    }
}
