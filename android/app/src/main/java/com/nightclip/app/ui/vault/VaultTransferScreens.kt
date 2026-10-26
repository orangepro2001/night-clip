package com.nightclip.app.ui.vault

import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.nightclip.app.R
import com.nightclip.app.core.VaultCreds
import com.nightclip.app.logic.AttachmentNames
import com.nightclip.app.logic.MasterPasswordRules
import com.nightclip.app.logic.PasswordStrengthUi
import com.nightclip.app.platform.VaultSession
import com.nightclip.app.ui.ConfirmDialog
import com.nightclip.app.ui.InfoCard
import com.nightclip.app.ui.SectionTitle
import java.time.LocalDate
import java.time.format.DateTimeFormatter

private fun today(): String = LocalDate.now().format(DateTimeFormatter.BASIC_ISO_DATE)

@Composable
fun ImportScreen(vm: VaultViewModel, data: VaultDataViewModel, onMessage: (String) -> Unit) {
    val busy by data.busy.collectAsStateWithLifecycle()
    val result by data.result.collectAsStateWithLifecycle()
    var askKdbxPassword by remember { mutableStateOf(false) }

    val kdbxPicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri: Uri? ->
        if (uri != null) {
            data.stageImport(uri)
            askKdbxPassword = true
        }
    }
    val csvPicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri: Uri? ->
        if (uri != null) data.importCsv(uri)
    }

    Column(Modifier.fillMaxSize()) {
        VaultTopBar(stringResource(R.string.vault_menu_import), onBack = { vm.pop() })
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            InfoCard { Text(stringResource(R.string.vault_import_note)) }
            SectionTitle(stringResource(R.string.vault_import_kdbx))
            HintText(stringResource(R.string.vault_import_kdbx_hint))
            Button(onClick = { VaultSession.expectSystemUi(); kdbxPicker.launch(arrayOf("*/*")) }, enabled = !busy) { Text(stringResource(R.string.vault_import_pick)) }
            SectionTitle(stringResource(R.string.vault_import_csv))
            HintText(stringResource(R.string.vault_import_csv_hint))
            Button(onClick = { VaultSession.expectSystemUi(); csvPicker.launch(arrayOf("text/*", "application/csv", "application/vnd.ms-excel", "*/*")) }, enabled = !busy) {
                Text(stringResource(R.string.vault_import_pick))
            }
            if (busy) CircularProgressIndicator()
        }
    }

    if (askKdbxPassword) {
        CredentialsDialog(
            title = stringResource(R.string.vault_import_kdbx),
            text = stringResource(R.string.vault_import_kdbx_password),
            confirmLabel = stringResource(R.string.vault_import_go),
            onMessage = onMessage,
            onConfirm = { creds ->
                askKdbxPassword = false
                data.importKdbx(creds)
            },
            onDismiss = {
                askKdbxPassword = false
                data.cancelImport()
            },
        )
    }
    result?.let { text ->
        AlertDialog(
            onDismissRequest = { data.dismissResult() },
            title = { Text(stringResource(R.string.vault_result_title)) },
            text = { Text(text) },
            confirmButton = { TextButton(onClick = { data.dismissResult() }) { Text(stringResource(R.string.done)) } },
        )
    }
}

/** Export: an encrypted KDBX copy with its own password, or a plaintext CSV behind a red warning. Both re-ask the master password. */
@Composable
fun ExportScreen(vm: VaultViewModel, data: VaultDataViewModel, onMessage: (String) -> Unit) {
    val busy by data.busy.collectAsStateWithLifecycle()
    val result by data.result.collectAsStateWithLifecycle()

    var newPw by remember { mutableStateOf("") }
    var newPw2 by remember { mutableStateOf("") }
    var newKey by remember { mutableStateOf<KeyFile?>(null) }
    var masterPw by remember { mutableStateOf("") }
    var masterKey by remember { mutableStateOf<KeyFile?>(null) }
    var strength by remember { mutableStateOf<String?>(null) }

    var csvMasterPw by remember { mutableStateOf("") }
    var csvMasterKey by remember { mutableStateOf<KeyFile?>(null) }
    var csvUnderstood by remember { mutableStateOf(false) }

    var confirmKdbx by remember { mutableStateOf(false) }
    var confirmCsv by remember { mutableStateOf(false) }

    val kdbxSaver = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/octet-stream")) { uri: Uri? ->
        if (uri == null) data.cancelExport() else data.finishExport(uri)
    }
    val csvSaver = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("text/csv")) { uri: Uri? ->
        if (uri == null) data.cancelExport() else data.finishExport(uri)
    }

    LaunchedEffect(newPw) {
        if (newPw.isEmpty()) {
            strength = null
        } else {
            kotlinx.coroutines.delay(250)
            strength = vm.strength(newPw)?.label
        }
    }
    val kdbxProblem = MasterPasswordRules.problem(newPw, newPw2)
    val kdbxReady = kdbxProblem == null && masterPw.isNotEmpty() && !busy

    Column(Modifier.fillMaxSize()) {
        VaultTopBar(stringResource(R.string.vault_menu_export), onBack = { vm.pop() })
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            SectionTitle(stringResource(R.string.vault_export_kdbx))
            HintText(stringResource(R.string.vault_export_kdbx_hint))
            SecretTextField(newPw, { newPw = it }, stringResource(R.string.vault_export_new_password), enabled = !busy)
            StrengthBar(strength)
            SecretTextField(
                newPw2, { newPw2 = it }, stringResource(R.string.vault_master_confirm),
                isError = newPw2.isNotEmpty() && kdbxProblem == MasterPasswordRules.Problem.MISMATCH,
                supporting = if (newPw2.isNotEmpty() && kdbxProblem == MasterPasswordRules.Problem.MISMATCH) stringResource(R.string.vault_mismatch) else null,
                enabled = !busy,
            )
            KeyFileRow(newKey, { newKey = it }, stringResource(R.string.vault_export_key_file), onMessage)
            Text(stringResource(R.string.vault_export_confirm_master), style = MaterialTheme.typography.titleSmall)
            CredentialsInput(masterPw, { masterPw = it }, masterKey, { masterKey = it }, stringResource(R.string.vault_master_password), onMessage, enabled = !busy)
            Button(onClick = { confirmKdbx = true }, enabled = kdbxReady) { Text(stringResource(R.string.vault_export_choose)) }

            SectionTitle(stringResource(R.string.vault_export_csv))
            InfoCard(container = MaterialTheme.colorScheme.errorContainer) {
                Text(stringResource(R.string.vault_export_csv_warning_title), fontWeight = FontWeight.Bold, color = MaterialTheme.colorScheme.onErrorContainer)
                Text(stringResource(R.string.vault_export_csv_warning), color = MaterialTheme.colorScheme.onErrorContainer)
            }
            Row(verticalAlignment = Alignment.CenterVertically) {
                Checkbox(checked = csvUnderstood, onCheckedChange = { csvUnderstood = it })
                Text(stringResource(R.string.vault_export_csv_understood), style = MaterialTheme.typography.bodyMedium)
            }
            CredentialsInput(csvMasterPw, { csvMasterPw = it }, csvMasterKey, { csvMasterKey = it }, stringResource(R.string.vault_master_password), onMessage, enabled = !busy)
            Button(
                onClick = { confirmCsv = true },
                enabled = csvUnderstood && csvMasterPw.isNotEmpty() && !busy,
                colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.error),
            ) { Text(stringResource(R.string.vault_export_csv_go)) }
            if (busy) CircularProgressIndicator()
        }
    }

    if (confirmKdbx) {
        ConfirmDialog(
            title = stringResource(R.string.vault_export_kdbx),
            text = stringResource(R.string.vault_export_kdbx_confirm),
            confirmLabel = stringResource(R.string.vault_export_choose),
            onConfirm = {
                confirmKdbx = false
                data.prepareExport(PendingExport.Kdbx(VaultCreds(masterPw, masterKey?.bytes), VaultCreds(newPw, newKey?.bytes), null))
                masterPw = ""
                masterKey = null
                newPw = ""
                newPw2 = ""
                newKey = null
                VaultSession.expectSystemUi()
                kdbxSaver.launch(AttachmentNames.exportName("密码库备份", today(), "kdbx"))
            },
            onDismiss = { confirmKdbx = false },
        )
    }
    if (confirmCsv) {
        ConfirmDialog(
            title = stringResource(R.string.vault_export_csv_warning_title),
            text = stringResource(R.string.vault_export_csv_confirm),
            confirmLabel = stringResource(R.string.vault_export_csv_go),
            onConfirm = {
                confirmCsv = false
                data.prepareExport(PendingExport.Csv(VaultCreds(csvMasterPw, csvMasterKey?.bytes)))
                csvMasterPw = ""
                csvMasterKey = null
                csvUnderstood = false
                VaultSession.expectSystemUi()
                csvSaver.launch(AttachmentNames.exportName("密码库明文", today(), "csv"))
            },
            onDismiss = { confirmCsv = false },
        )
    }
    result?.let { text ->
        AlertDialog(
            onDismissRequest = { data.dismissResult() },
            title = { Text(stringResource(R.string.vault_result_title)) },
            text = { Text(text) },
            confirmButton = { TextButton(onClick = { data.dismissResult() }) { Text(stringResource(R.string.done)) } },
        )
    }
}

@Composable
private fun KeyFileRow(keyFile: KeyFile?, onKeyFile: (KeyFile?) -> Unit, label: String, onMessage: (String) -> Unit) {
    val pick = rememberKeyFilePicker(onPicked = { if (it != null) onKeyFile(it) }, onError = onMessage)
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        if (keyFile == null) {
            OutlinedButton(onClick = pick) { Text(label) }
        } else {
            Text(stringResource(R.string.vault_key_file_chosen, keyFile.name), Modifier.weight(1f))
            TextButton(onClick = { onKeyFile(null) }) { Text(stringResource(R.string.vault_key_file_remove)) }
        }
    }
}

@Composable
fun ChangeMasterScreen(vm: VaultViewModel, onMessage: (String) -> Unit) {
    val busy by vm.busy.collectAsStateWithLifecycle()
    var current by remember { mutableStateOf("") }
    var currentKey by remember { mutableStateOf<KeyFile?>(null) }
    var newPw by remember { mutableStateOf("") }
    var newPw2 by remember { mutableStateOf("") }
    var newKey by remember { mutableStateOf<KeyFile?>(null) }
    var strength by remember { mutableStateOf<String?>(null) }
    var confirm by remember { mutableStateOf(false) }

    LaunchedEffect(newPw) {
        if (newPw.isEmpty()) {
            strength = null
        } else {
            kotlinx.coroutines.delay(250)
            strength = vm.strength(newPw)?.label
        }
    }
    val problem = MasterPasswordRules.problem(newPw, newPw2)
    val mismatch = newPw2.isNotEmpty() && problem == MasterPasswordRules.Problem.MISMATCH

    Column(Modifier.fillMaxSize()) {
        VaultTopBar(stringResource(R.string.vault_menu_change_master), onBack = { vm.pop() })
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            InfoCard(container = MaterialTheme.colorScheme.errorContainer) {
                Text(stringResource(R.string.vault_change_warning), color = MaterialTheme.colorScheme.onErrorContainer)
            }
            Text(stringResource(R.string.vault_change_current), style = MaterialTheme.typography.titleSmall)
            CredentialsInput(current, { current = it }, currentKey, { currentKey = it }, stringResource(R.string.vault_master_password), onMessage, enabled = !busy)
            Text(stringResource(R.string.vault_change_new), style = MaterialTheme.typography.titleSmall)
            SecretTextField(newPw, { newPw = it }, stringResource(R.string.vault_change_new_password), enabled = !busy)
            StrengthBar(strength)
            PasswordStrengthUi.masterWarning(strength ?: "strong", newPw.length)?.let {
                Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
            }
            SecretTextField(
                newPw2, { newPw2 = it }, stringResource(R.string.vault_master_confirm),
                isError = mismatch, supporting = if (mismatch) stringResource(R.string.vault_mismatch) else null, enabled = !busy,
            )
            KeyFileRow(newKey, { newKey = it }, stringResource(R.string.vault_change_new_key_file), onMessage)
            HintText(stringResource(R.string.vault_change_key_file_hint))
            Button(onClick = { confirm = true }, enabled = current.isNotEmpty() && problem == null && !busy) {
                if (busy) CircularProgressIndicator(Modifier.padding(end = 8.dp), strokeWidth = 2.dp)
                Text(stringResource(R.string.vault_change_go))
            }
        }
    }
    if (confirm) {
        ConfirmDialog(
            title = stringResource(R.string.vault_menu_change_master),
            text = stringResource(R.string.vault_change_confirm),
            confirmLabel = stringResource(R.string.vault_change_go),
            onConfirm = {
                confirm = false
                val cur = VaultCreds(current, currentKey?.bytes)
                val new = VaultCreds(newPw, newKey?.bytes)
                current = ""
                newPw = ""
                newPw2 = ""
                vm.changeMaster(cur, new)
            },
            onDismiss = { confirm = false },
        )
    }
}
