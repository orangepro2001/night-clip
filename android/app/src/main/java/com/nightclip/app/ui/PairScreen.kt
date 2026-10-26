package com.nightclip.app.ui

import android.Manifest
import android.content.pm.PackageManager
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import com.nightclip.app.PortraitCaptureActivity
import com.nightclip.app.R
import com.nightclip.app.logic.PairUi
import com.nightclip.app.logic.PairingFlow
import com.nightclip.app.platform.ClipboardAccess

private const val TAB_SCAN = 0
private const val TAB_PASTE = 1
private const val TAB_MANUAL = 2

@Composable
fun PairScreen(vm: PairViewModel, onClose: () -> Unit, modifier: Modifier = Modifier) {
    val ui by vm.ui.collectAsStateWithLifecycle()
    val error by vm.inputError.collectAsStateWithLifecycle()
    val prefill by vm.prefill.collectAsStateWithLifecycle()
    var tab by rememberSaveable { mutableIntStateOf(if (prefill != null) TAB_PASTE else TAB_SCAN) }

    fun close() {
        // Leaving while a join is running would hide the SAS; only finished attempts are reset.
        if (ui is PairUi.Accepted || ui is PairUi.Failed) vm.reset()
        vm.setPrefill(null)
        onClose()
    }
    BackHandler(onBack = ::close)

    Column(modifier.fillMaxSize().padding(16.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = ::close) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.back)) }
            Text(stringResource(R.string.pair_title), style = MaterialTheme.typography.headlineSmall)
        }
        when (val s = ui) {
            is PairUi.Idle -> InputTabs(vm, tab, { tab = it }, error, prefill)
            is PairUi.Joining -> StatusBlock {
                CircularProgressIndicator()
                Text(stringResource(R.string.pair_connecting), style = MaterialTheme.typography.titleMedium)
                Text(stringResource(R.string.pair_connecting_hint), color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = TextAlign.Center)
            }
            is PairUi.ShowSas -> StatusBlock {
                Text(stringResource(R.string.pair_check_sas), style = MaterialTheme.typography.titleMedium, textAlign = TextAlign.Center)
                Text(
                    PairingFlow.formatSas(s.sas),
                    fontSize = 56.sp,
                    fontWeight = FontWeight.Bold,
                    fontFamily = FontFamily.Monospace,
                    color = MaterialTheme.colorScheme.primary,
                )
                Text(stringResource(R.string.pair_sas_hint), color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = TextAlign.Center)
                CircularProgressIndicator()
            }
            is PairUi.Accepted -> StatusBlock {
                Text(stringResource(R.string.pair_success), style = MaterialTheme.typography.headlineSmall)
                if (s.name.isNotBlank()) Text(s.name, style = MaterialTheme.typography.titleMedium)
                Button(onClick = ::close) { Text(stringResource(R.string.done)) }
            }
            is PairUi.Failed -> StatusBlock {
                Text(stringResource(R.string.pair_failed), style = MaterialTheme.typography.headlineSmall, color = MaterialTheme.colorScheme.error)
                Text(s.message, textAlign = TextAlign.Center)
                OutlinedButton(onClick = { vm.reset() }) { Text(stringResource(R.string.retry)) }
            }
        }
    }
}

@Composable
private fun StatusBlock(content: @Composable () -> Unit) {
    Column(
        Modifier.fillMaxSize().padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) { content() }
}

@Composable
private fun InputTabs(vm: PairViewModel, tab: Int, onTab: (Int) -> Unit, error: String?, prefill: String?) {
    TabRow(selectedTabIndex = tab) {
        Tab(selected = tab == TAB_SCAN, onClick = { onTab(TAB_SCAN) }, text = { Text(stringResource(R.string.pair_tab_scan)) })
        Tab(selected = tab == TAB_PASTE, onClick = { onTab(TAB_PASTE) }, text = { Text(stringResource(R.string.pair_tab_paste)) })
        Tab(selected = tab == TAB_MANUAL, onClick = { onTab(TAB_MANUAL) }, text = { Text(stringResource(R.string.pair_tab_manual)) })
    }
    Column(Modifier.verticalScroll(rememberScrollState()).padding(top = 16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(stringResource(R.string.pair_intro), color = MaterialTheme.colorScheme.onSurfaceVariant)
        when (tab) {
            TAB_SCAN -> ScanTab(vm, onNeedManual = { onTab(TAB_MANUAL) })
            TAB_PASTE -> PasteTab(vm, prefill)
            else -> ManualTab(vm)
        }
        error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
    }
}

@Composable
private fun ScanTab(vm: PairViewModel, onNeedManual: () -> Unit) {
    val context = LocalContext.current
    var denied by remember { mutableStateOf(false) }
    val scanner = rememberLauncherForActivityResult(ScanContract()) { result ->
        val text = result.contents
        if (text != null) vm.joinWithText(text, fromQr = true)
    }
    fun launchScan() {
        scanner.launch(
            ScanOptions()
                .setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                .setPrompt(context.getString(R.string.pair_scan_prompt))
                .setBeepEnabled(false)
                .setCaptureActivity(PortraitCaptureActivity::class.java)
                .setOrientationLocked(false),
        )
    }
    val permission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted) {
            denied = false
            launchScan()
        } else {
            denied = true
            onNeedManual() // camera refused: fall back to typing the details
        }
    }
    Button(onClick = {
        if (ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) {
            launchScan()
        } else {
            permission.launch(Manifest.permission.CAMERA)
        }
    }, modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.pair_scan_button)) }
    if (denied) Text(stringResource(R.string.pair_camera_denied), color = MaterialTheme.colorScheme.error)
    Text(stringResource(R.string.pair_scan_note), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
}

@Composable
private fun PasteTab(vm: PairViewModel, prefill: String?) {
    val context = LocalContext.current
    var text by remember { mutableStateOf(prefill.orEmpty()) } // not saved: it holds the one-time key
    LaunchedEffect(prefill) {
        if (prefill != null) text = prefill
    }
    OutlinedTextField(
        value = text,
        onValueChange = {
            text = it
            vm.clearError()
        },
        label = { Text(stringResource(R.string.pair_link_label)) },
        modifier = Modifier.fillMaxWidth(),
        minLines = 3,
        maxLines = 6,
    )
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        OutlinedButton(onClick = {
            // A button press in the foreground: allowed to read the clipboard.
            ClipboardAccess.readText(context)?.let {
                text = it
                vm.clearError()
            }
        }) { Text(stringResource(R.string.pair_paste_button)) }
        Button(onClick = { vm.joinWithText(text) }, enabled = text.isNotBlank()) { Text(stringResource(R.string.pair_start)) }
    }
}

@Composable
private fun ManualTab(vm: PairViewModel) {
    var host by rememberSaveable { mutableStateOf("") }
    var port by rememberSaveable { mutableStateOf("47821") }
    var psk by remember { mutableStateOf("") } // not saved: one-time key
    var fp by rememberSaveable { mutableStateOf("") }
    val change: (String) -> Unit = { vm.clearError() }
    OutlinedTextField(host, { host = it; change(it) }, label = { Text(stringResource(R.string.pair_host)) }, singleLine = true, modifier = Modifier.fillMaxWidth())
    OutlinedTextField(
        port, { port = it.filter(Char::isDigit).take(5); change(it) },
        label = { Text(stringResource(R.string.pair_port)) }, singleLine = true, modifier = Modifier.fillMaxWidth(),
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
    )
    OutlinedTextField(psk, { psk = it; change(it) }, label = { Text(stringResource(R.string.pair_psk)) }, singleLine = true, modifier = Modifier.fillMaxWidth())
    OutlinedTextField(fp, { fp = it; change(it) }, label = { Text(stringResource(R.string.pair_fp)) }, singleLine = true, modifier = Modifier.fillMaxWidth())
    Text(stringResource(R.string.pair_manual_note), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    Button(onClick = { vm.joinManual(host, port, psk, fp) }, modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.pair_start)) }
}
