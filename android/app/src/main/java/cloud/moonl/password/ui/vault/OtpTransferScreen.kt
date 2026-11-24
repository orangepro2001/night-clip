package cloud.moonl.password.ui.vault

import android.content.ContentResolver
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import cloud.moonl.password.MoonPasswordApp
import cloud.moonl.password.PortraitCaptureActivity
import cloud.moonl.password.R
import cloud.moonl.password.core.ApiResult
import cloud.moonl.password.core.OtpExport
import cloud.moonl.password.core.VaultApi
import cloud.moonl.password.platform.VaultClipboard
import cloud.moonl.password.platform.VaultSession
import cloud.moonl.password.ui.InfoCard
import com.google.zxing.BarcodeFormat
import com.google.zxing.BinaryBitmap
import com.google.zxing.DecodeHintType
import com.google.zxing.RGBLuminanceSource
import com.google.zxing.ReaderException
import com.google.zxing.WriterException
import com.google.zxing.common.HybridBinarizer
import com.google.zxing.qrcode.QRCodeReader
import com.journeyapps.barcodescanner.BarcodeEncoder
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException
import java.io.ByteArrayOutputStream

private const val maxLinks = 4 shl 20

@Composable
private fun rememberOtpCamera(onScan: (String) -> Unit): () -> Unit {
    val scan = rememberLauncherForActivityResult(ScanContract()) { result -> result.contents?.let(onScan) }
    val prompt = stringResource(R.string.vault_otp_scan)
    return {
        VaultSession.expectSystemUi()
        scan.launch(ScanOptions().setDesiredBarcodeFormats(ScanOptions.QR_CODE).setPrompt(prompt)
            .setBeepEnabled(false).setOrientationLocked(false).setCaptureActivity(PortraitCaptureActivity::class.java))
    }
}

@Composable
fun OtpEditorScanButton(onLink: (String) -> Unit, onMessage: (String) -> Unit) {
    val invalid = stringResource(R.string.vault_otp_single_qr)
    val scan = rememberOtpCamera { text ->
        if (text.startsWith("otpauth://totp/", ignoreCase = true)) onLink(text) else onMessage(invalid)
    }
    OutlinedButton(onClick = scan) { Text(stringResource(R.string.vault_otp_scan)) }
}

@Composable
fun OtpImportScreen(vm: VaultViewModel, browse: VaultBrowseViewModel, groupId: String, onMessage: (String) -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var links by remember { mutableStateOf("") }
    var status by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    val payloads = remember(links) { links.trimStart('\uFEFF').lineSequence().map(String::trim).filter(String::isNotEmpty).toList() }

    fun add(payload: String) {
        val next = if (links.isBlank()) payload else links.trimEnd() + "\n" + payload
        if (next.length <= maxLinks) {
            links = next
            status = ""
        } else status = context.getString(R.string.vault_otp_read_failed)
    }

    val scan = rememberOtpCamera(::add)
    val open = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) scope.launch {
            busy = true
            val content = withContext(Dispatchers.IO) {
                try {
                    val resolver = context.contentResolver
                    if (resolver.getType(uri)?.startsWith("image/") == true) decodeQr(resolver, uri)
                    else resolver.openInputStream(uri)?.use {
                        val buffer = ByteArray(8192)
                        val out = ByteArrayOutputStream()
                        while (true) {
                            val n = it.read(buffer)
                            if (n < 0) break
                            if (out.size() + n > maxLinks) return@use null
                            out.write(buffer, 0, n)
                        }
                        out.toString("UTF-8").trimStart('\uFEFF')
                    }
                } catch (_: IOException) { null }
                catch (_: SecurityException) { null }
                catch (_: IllegalArgumentException) { null }
            }
            busy = false
            if (content.isNullOrBlank()) status = context.getString(R.string.vault_otp_read_failed) else add(content)
        }
    }

    Column(Modifier.fillMaxSize()) {
        VaultTopBar(stringResource(R.string.vault_otp_import), onBack = { vm.pop() })
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            InfoCard { Text(stringResource(R.string.vault_otp_import_hint)) }
            OutlinedButton(onClick = scan, enabled = !busy) { Text(stringResource(R.string.vault_otp_scan)) }
            OutlinedButton(onClick = { VaultSession.expectSystemUi(); open.launch(arrayOf("image/*", "text/plain")) }, enabled = !busy) {
                Text(stringResource(R.string.vault_otp_open))
            }
            OutlinedTextField(
                links, { if (it.length <= maxLinks) { links = it; status = "" } }, Modifier.fillMaxWidth(),
                enabled = !busy, label = { Text(stringResource(R.string.vault_otp_link)) },
                minLines = 3, maxLines = 5, visualTransformation = PasswordVisualTransformation(),
                keyboardOptions = KeyboardOptions(autoCorrectEnabled = false),
                supportingText = { Text(stringResource(R.string.vault_otp_count, payloads.size)) },
            )
            Button(enabled = !busy && payloads.isNotEmpty(), onClick = {
                scope.launch {
                    busy = true
                    when (val r = VaultApi.importOtp(payloads, groupId)) {
                        is ApiResult.Ok -> {
                            links = ""
                            val (added, duplicates, unsupported) = r.value
                            onMessage(context.getString(R.string.vault_otp_added, added, duplicates, unsupported))
                            browse.reloadList()
                            vm.pop()
                        }
                        is ApiResult.Fail -> status = if (r.code == "OTP_INCOMPLETE") context.getString(R.string.vault_otp_incomplete)
                            else r.message
                    }
                    busy = false
                }
            }) { Text(stringResource(R.string.vault_otp_import)) }
            if (status.isNotEmpty()) Text(status)
        }
    }
}

@Composable
fun OtpExportScreen(vm: VaultViewModel, entryId: String?, onMessage: (String) -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var output by remember { mutableStateOf<OtpExport?>(null) }
    var ask by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    var menu by remember { mutableStateOf(false) }
    var selected by remember { mutableStateOf(0) }
    val item = output?.entries?.getOrNull(selected)
    val qr = remember(item) {
        item?.let {
            try { BarcodeEncoder().encodeBitmap(it.link, BarcodeFormat.QR_CODE, 512, 512).asImageBitmap() }
            catch (_: WriterException) { null }
        }
    }
    val save = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("text/plain")) { uri ->
        val entries = output?.entries.orEmpty()
        if (uri != null && entries.isNotEmpty()) scope.launch {
            busy = true
            val ok = withContext(Dispatchers.IO) {
                try {
                    val writer = context.contentResolver.openOutputStream(uri, "wt")?.bufferedWriter()
                    writer?.use { out -> for (entry in entries) { out.write(entry.link); out.newLine() } }
                    writer != null
                } catch (_: IOException) { false }
                catch (_: SecurityException) { false }
            }
            busy = false
            onMessage(context.getString(if (ok) R.string.vault_otp_saved else R.string.vault_otp_save_failed))
        }
    }
    val title = stringResource(R.string.vault_otp_export)
    val warning = stringResource(R.string.vault_otp_export_warning)
    Column(Modifier.fillMaxSize()) {
        VaultTopBar(title, onBack = { vm.pop() })
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            InfoCard { Text(warning) }
            val current = output
            if (current == null) {
                Button(enabled = !busy, onClick = { ask = true }) { Text(stringResource(R.string.vault_otp_verify)) }
            } else if (item == null) {
                Text(stringResource(R.string.vault_otp_empty))
            } else {
                Box {
                    OutlinedButton(onClick = { menu = true }, enabled = current.entries.size > 1) {
                        Text(item.label)
                    }
                    DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                        current.entries.forEachIndexed { i, entry ->
                            DropdownMenuItem(text = { Text(entry.label) }, onClick = { selected = i; menu = false })
                        }
                    }
                }
                if (qr != null) Image(qr, stringResource(R.string.vault_otp_qr), Modifier.size(236.dp).align(Alignment.CenterHorizontally))
                OutlinedButton(onClick = {
                    val app = context.applicationContext as MoonPasswordApp
                    val copied = VaultClipboard.copy(app, item.link, 30)
                    onMessage(context.getString(if (copied) R.string.vault_otp_copied else R.string.vault_otp_copy_failed))
                }) { Text(stringResource(R.string.vault_otp_copy)) }
                Button(enabled = !busy, onClick = { VaultSession.expectSystemUi(); save.launch("moonpassword-otp.txt") }) {
                    Text(stringResource(R.string.vault_otp_save))
                }
            }
            if (current != null && current.skipped > 0) Text(stringResource(R.string.vault_otp_skipped, current.skipped))
        }
    }
    if (ask) CredentialsDialog(
        title, warning, stringResource(R.string.vault_otp_verify), onMessage = onMessage,
        onConfirm = { creds ->
            ask = false
            scope.launch {
                busy = true
                when (val r = VaultApi.exportOtp(entryId, creds)) {
                    is ApiResult.Ok -> output = r.value
                    is ApiResult.Fail -> onMessage(r.message)
                }
                busy = false
            }
        },
        onDismiss = { ask = false },
    )
}

private fun decodeQr(resolver: ContentResolver, uri: Uri): String? {
    val options = BitmapFactory.Options().apply { inJustDecodeBounds = true }
    resolver.openInputStream(uri)?.use { BitmapFactory.decodeStream(it, null, options) }
    if (options.outWidth < 1 || options.outHeight < 1 || options.outWidth > 16_384 || options.outHeight > 16_384) return null
    options.inJustDecodeBounds = false
    options.inPreferredConfig = Bitmap.Config.ARGB_8888
    while (options.outWidth.toLong() * options.outHeight / (options.inSampleSize * options.inSampleSize).coerceAtLeast(1) > 6_000_000) {
        options.inSampleSize = (options.inSampleSize * 2).coerceAtLeast(2)
    }
    val bmp = resolver.openInputStream(uri)?.use { BitmapFactory.decodeStream(it, null, options) } ?: return null
    val source = try {
        val pixels = IntArray(bmp.width * bmp.height)
        bmp.getPixels(pixels, 0, bmp.width, 0, 0, bmp.width, bmp.height)
        RGBLuminanceSource(bmp.width, bmp.height, pixels)
    } finally {
        bmp.recycle()
    }
    val hints = mapOf(DecodeHintType.TRY_HARDER to true)
    val reader = QRCodeReader()
    for (candidate in listOf(source, source.invert())) {
        try { return reader.decode(BinaryBitmap(HybridBinarizer(candidate)), hints).text }
        catch (_: ReaderException) { reader.reset() }
    }
    return null
}
