package com.nightclip.app

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.net.Uri
import android.os.Bundle
import android.provider.DocumentsContract
import android.util.Log
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.core.content.FileProvider
import androidx.lifecycle.lifecycleScope
import com.nightclip.app.logic.ReceivedImages
import com.nightclip.app.ui.ClipBridgeTheme
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import java.io.IOException

/**
 * A received image held in `<cacheDir>/received-images/<transfer>/` (logic/ReceivedImages.kt): preview,
 * "复制到剪贴板" (a content Uri through the app's FileProvider) and "保存到…" (the system's save screen, then the
 * saved location is shown). Leaving the screen discards the held copy; nothing reaches Downloads unless saved.
 */
class ImagePreviewActivity : ComponentActivity() {
    private lateinit var file: File
    private lateinit var name: String

    // The save screen gets the image's own type (so it keeps the extension), not a generic image/*.
    private val saver = registerForActivityResult(object : ActivityResultContracts.CreateDocument("image/*") {
        override fun createIntent(context: Context, input: String): Intent =
            super.createIntent(context, input).setType(ReceivedImages.mimeOf(input))
    }) { uri ->
        if (uri != null) save(uri)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val path = intent.getStringExtra(EXTRA_PATH)
        val held = path?.let { File(it) }?.takeIf { isHeld(this, it) && it.isFile }
        if (held == null) {
            Toast.makeText(this, R.string.image_gone, Toast.LENGTH_LONG).show()
            finish()
            return
        }
        file = held
        name = intent.getStringExtra(EXTRA_NAME).orEmpty().ifBlank { held.name }
        val from = intent.getStringExtra(EXTRA_FROM).orEmpty()
        setContent {
            ClipBridgeTheme {
                Surface(Modifier.fillMaxSize()) {
                    Preview(from)
                }
            }
        }
    }

    @Composable
    private fun Preview(from: String) {
        var bitmap by remember { mutableStateOf<Bitmap?>(null) }
        var failed by remember { mutableStateOf(false) }
        LaunchedEffect(file) {
            val decoded = withContext(Dispatchers.IO) { runCatching { decode(file, 2048) }.getOrNull() }
            if (decoded == null) failed = true else bitmap = decoded
        }
        Column(Modifier.safeDrawingPadding().padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text(stringResource(R.string.image_preview_title, from.ifBlank { "?" }), style = MaterialTheme.typography.titleMedium)
            Text(name, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Box(Modifier.weight(1f).fillMaxWidth(), contentAlignment = Alignment.Center) {
                val b = bitmap
                when {
                    b != null -> Image(b.asImageBitmap(), contentDescription = name, contentScale = ContentScale.Fit, modifier = Modifier.fillMaxSize())
                    failed -> Text(stringResource(R.string.image_load_failed), color = MaterialTheme.colorScheme.error)
                    else -> CircularProgressIndicator()
                }
            }
            Text(stringResource(R.string.image_not_saved), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = { copy() }) { Text(stringResource(R.string.image_copy)) }
                FilledTonalButton(onClick = {
                    runCatching { saver.launch(name) }.onFailure {
                        Log.w(TAG, "no save screen", it)
                        Toast.makeText(this@ImagePreviewActivity, R.string.image_save_failed, Toast.LENGTH_SHORT).show()
                    }
                }) { Text(stringResource(R.string.image_save_as)) }
                OutlinedButton(onClick = { finish() }) { Text(stringResource(R.string.image_close)) }
            }
        }
    }

    /**
     * The clipboard gets its own copy (`received-images/clipboard/`, replaced by the next copy), so pasting still
     * works after this screen closes and discards the held file. No text in the clip: it is never synced on.
     */
    private fun copy() {
        lifecycleScope.launch {
            val ok = runCatching {
                val clipFile = withContext(Dispatchers.IO) {
                    val dir = File(holdRoot(this@ImagePreviewActivity), CLIPBOARD_DIR)
                    dir.deleteRecursively()
                    dir.mkdirs()
                    File(dir, file.name).also { file.copyTo(it, overwrite = true) }
                }
                val uri = FileProvider.getUriForFile(this@ImagePreviewActivity, "$packageName.updates", clipFile)
                val cm = getSystemService(ClipboardManager::class.java)
                cm.setPrimaryClip(ClipData.newUri(contentResolver, CLIP_LABEL, uri))
            }.onFailure { Log.w(TAG, "copy failed", it) }.isSuccess
            Toast.makeText(this@ImagePreviewActivity, if (ok) R.string.image_copied else R.string.image_copy_failed, Toast.LENGTH_SHORT).show()
        }
    }

    /** Writes the image where the user chose, says where, and closes (which discards the held copy). */
    private fun save(target: Uri) {
        lifecycleScope.launch {
            val ok = withContext(Dispatchers.IO) {
                try {
                    contentResolver.openOutputStream(target, "wt")?.use { out -> file.inputStream().use { it.copyTo(out) } } != null
                } catch (e: IOException) {
                    Log.w(TAG, "save failed", e); false
                } catch (e: SecurityException) {
                    Log.w(TAG, "save refused", e); false
                }
            }
            if (!ok) {
                Toast.makeText(this@ImagePreviewActivity, R.string.image_save_failed, Toast.LENGTH_LONG).show()
                return@launch
            }
            val where = runCatching { ReceivedImages.displayLocation(DocumentsContract.getDocumentId(target)) }.getOrNull()
            val text = if (where != null) getString(R.string.image_saved_to, where) else getString(R.string.image_saved)
            Toast.makeText(this@ImagePreviewActivity, text, Toast.LENGTH_LONG).show()
            finish()
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        // Only when really leaving (not on rotation): the held copy and its transfer folder go.
        if (isFinishing && ::file.isInitialized) file.parentFile?.takeIf { isHeld(this, file) }?.deleteRecursively()
    }

    companion object {
        private const val TAG = "ClipBridge"
        private const val EXTRA_PATH = "path"
        private const val EXTRA_NAME = "name"
        private const val EXTRA_FROM = "from"
        private const val CLIPBOARD_DIR = "clipboard"
        const val CLIP_LABEL = "ClipBridge image"

        fun holdRoot(context: Context) = File(context.cacheDir, ReceivedImages.DIR)

        /** Inside the hold folder (and not the clipboard copy): the only files this screen opens or deletes. */
        fun isHeld(context: Context, f: File): Boolean {
            val root = holdRoot(context).canonicalFile
            val canon = runCatching { f.canonicalFile }.getOrNull() ?: return false
            val transferDir = canon.parentFile ?: return false
            return transferDir.parentFile == root && transferDir.name != CLIPBOARD_DIR
        }

        fun intent(context: Context, transferId: String, file: File, name: String, from: String): Intent =
            Intent(context, ImagePreviewActivity::class.java)
                .setData(Uri.parse("clipbridge://image/" + Uri.encode(transferId))) // distinct PendingIntents per image
                .putExtra(EXTRA_PATH, file.path)
                .putExtra(EXTRA_NAME, name)
                .putExtra(EXTRA_FROM, from)

        /** Decodes with a power-of-two sample size so the longer side is at most [maxSide]. */
        fun decode(f: File, maxSide: Int): Bitmap? {
            val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
            BitmapFactory.decodeFile(f.path, bounds)
            if (bounds.outWidth <= 0 || bounds.outHeight <= 0) return null
            var sample = 1
            while (maxOf(bounds.outWidth, bounds.outHeight) / sample > maxSide) sample *= 2
            return BitmapFactory.decodeFile(f.path, BitmapFactory.Options().apply { inSampleSize = sample })
        }
    }
}
