package com.nightclip.app.platform

import android.content.Context
import android.net.Uri
import android.provider.DocumentsContract
import android.provider.OpenableColumns
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File
import java.io.IOException
import java.io.RandomAccessFile
import java.util.UUID

/**
 * Files that pass between the vault and the Storage Access Framework. The core reads and writes plain paths, SAF gives
 * content:// URIs, so every transfer goes through a temp file in the app's private cache that is deleted (zeroed first,
 * best effort on flash storage) as soon as the transfer ended, whether it worked or not.
 */
object VaultFiles {
    private const val DIR = "vault-tmp"

    sealed interface Copied {
        data class Ok(val file: File, val size: Long) : Copied
        data object TooLarge : Copied
        data object Failed : Copied
    }

    private fun dir(context: Context): File = File(context.cacheDir, DIR).apply { mkdirs() }

    /** A path in the temp directory that does NOT exist yet (the core refuses to overwrite). Random name, never the user's. */
    fun newTempPath(context: Context, extension: String): File =
        File(dir(context), UUID.randomUUID().toString() + "." + extension.filter { it.isLetterOrDigit() }.take(8))

    /** Display name of a SAF document, or null. Only used as a suggestion for attachment names. */
    fun displayName(context: Context, uri: Uri): String? = try {
        context.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c ->
            if (c.moveToFirst()) c.getString(0) else null
        }
    } catch (e: Exception) {
        null
    }

    /** Copies [uri] into a fresh temp file; refuses (and deletes the partial copy) when it is larger than [maxBytes]. */
    suspend fun copyToTemp(context: Context, uri: Uri, maxBytes: Long, extension: String = "tmp"): Copied = withContext(Dispatchers.IO) {
        val out = newTempPath(context, extension)
        try {
            val input = context.contentResolver.openInputStream(uri) ?: return@withContext Copied.Failed
            var total = 0L
            input.use { ins ->
                out.outputStream().use { os ->
                    val buf = ByteArray(32 * 1024)
                    while (true) {
                        val n = ins.read(buf)
                        if (n < 0) break
                        total += n
                        if (total > maxBytes) {
                            wipeAndDelete(out)
                            return@withContext Copied.TooLarge
                        }
                        os.write(buf, 0, n)
                    }
                }
            }
            Copied.Ok(out, total)
        } catch (e: Exception) {
            wipeAndDelete(out)
            Copied.Failed
        }
    }

    /** Reads a whole (small) document such as a key file; null when it is unreadable, empty or larger than [maxBytes]. */
    suspend fun readLimited(context: Context, uri: Uri, maxBytes: Int): ByteArray? = withContext(Dispatchers.IO) {
        try {
            context.contentResolver.openInputStream(uri)?.use { ins ->
                val out = java.io.ByteArrayOutputStream()
                val buf = ByteArray(16 * 1024)
                while (true) {
                    val n = ins.read(buf)
                    if (n < 0) break
                    if (out.size() + n > maxBytes) return@withContext null
                    out.write(buf, 0, n)
                }
                out.toByteArray().takeIf { it.isNotEmpty() }
            }
        } catch (e: Exception) {
            null
        }
    }

    /** Copies [file] to the SAF document [uri] (truncating it). */
    suspend fun copyToUri(context: Context, file: File, uri: Uri): Boolean = withContext(Dispatchers.IO) {
        try {
            val os = context.contentResolver.openOutputStream(uri, "wt") ?: return@withContext false
            os.use { o -> file.inputStream().use { it.copyTo(o) } }
            true
        } catch (e: IOException) {
            false
        } catch (e: SecurityException) {
            false
        }
    }

    /** Removes the empty document that a failed export left behind (the user picked a name, we have nothing to put there). */
    suspend fun deleteDocument(context: Context, uri: Uri) = withContext(Dispatchers.IO) {
        try {
            DocumentsContract.deleteDocument(context.contentResolver, uri)
        } catch (e: Exception) {
            // not deletable: an empty file stays, harmless
        }
    }

    /** Overwrites with zeros, then deletes. Zeroing is best effort (flash wear levelling may keep old blocks). */
    fun wipeAndDelete(file: File) {
        try {
            if (file.isFile) {
                RandomAccessFile(file, "rw").use { raf ->
                    val zeros = ByteArray(8192)
                    var left = raf.length()
                    raf.seek(0)
                    while (left > 0) {
                        val n = minOf(left, zeros.size.toLong()).toInt()
                        raf.write(zeros, 0, n)
                        left -= n
                    }
                    raf.fd.sync()
                }
            }
        } catch (e: Exception) {
            // still try to delete
        }
        try {
            file.delete()
        } catch (e: Exception) {
            // nothing more to do
        }
    }

    /** Deletes leftovers of an interrupted transfer (crash, process death); called when the vault locks and at start. */
    fun sweep(context: Context) {
        dir(context).listFiles()?.forEach { wipeAndDelete(it) }
    }
}
