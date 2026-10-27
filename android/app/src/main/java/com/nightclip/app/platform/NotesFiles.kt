package com.nightclip.app.platform

import android.content.Context
import android.net.Uri
import android.provider.OpenableColumns
import com.nightclip.app.logic.NotesTransfer
import java.io.File
import java.io.IOException
import java.util.UUID

/**
 * The files that pass between the system pickers (content Uris) and the Go core (which wants absolute paths): they live
 * for a moment in sub folders of the app's cache directory. Everything is streamed in small pieces, so a 256 MiB import
 * or a 32 MiB photo is never held in memory, and every copy is limited in size while it is being made.
 */
object NotesFiles {
    /** More bytes came than allowed. */
    class TooLargeException : IOException("file too large")

    /** What the system says about a picked file; [size] is -1 when it does not say. */
    class Picked(val name: String?, val size: Long, val mime: String?) {
        override fun toString() = "Picked(size=$size, mime=$mime)" // names of the user's files stay out of logs
    }

    private const val BUFFER = 64 * 1024
    private const val STALE_MS = 24L * 3600 * 1000
    private const val PICTURES_STALE_MS = 30L * 24 * 3600 * 1000

    fun query(context: Context, uri: Uri): Picked {
        var name: String? = null
        var size = -1L
        try {
            context.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)?.use { c ->
                if (c.moveToFirst()) {
                    val n = c.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                    val s = c.getColumnIndex(OpenableColumns.SIZE)
                    if (n >= 0 && !c.isNull(n)) name = c.getString(n)
                    if (s >= 0 && !c.isNull(s)) size = c.getLong(s)
                }
            }
        } catch (e: Exception) {
            // A provider that cannot answer: the copy will find out whether the file is readable.
        }
        return Picked(name, size, try { context.contentResolver.getType(uri) } catch (e: Exception) { null })
    }

    fun nonce(): String = UUID.randomUUID().toString().replace("-", "")

    /** A folder of its own inside cache/[name]; created if missing. */
    fun dir(context: Context, name: String): File = File(context.cacheDir, name).apply { mkdirs() }

    /** A new empty folder cache/[name]/<random>, so files with equal names never meet. */
    fun freshDir(context: Context, name: String): File = File(dir(context, name), nonce()).apply { mkdirs() }

    /** Copies the content of [uri] to [dest]. A file that turns out larger than [maxBytes] is deleted again and [TooLargeException] thrown. */
    fun copyIn(context: Context, uri: Uri, dest: File, maxBytes: Long): Long {
        val input = context.contentResolver.openInputStream(uri) ?: throw IOException("cannot open")
        var total = 0L
        try {
            input.use { src ->
                dest.outputStream().use { out ->
                    val buf = ByteArray(BUFFER)
                    while (true) {
                        val n = src.read(buf)
                        if (n < 0) break
                        total += n
                        if (total > maxBytes) throw TooLargeException()
                        out.write(buf, 0, n)
                    }
                }
            }
        } catch (e: Exception) {
            dest.delete()
            throw e
        }
        return total
    }

    /** Copies [src] into the document [uri] the user picked (overwriting what it holds). */
    fun copyOut(context: Context, src: File, uri: Uri): Long {
        // "wt" truncates what a document already holds; a provider that does not know the mode gets the plain one.
        val out = (try { context.contentResolver.openOutputStream(uri, "wt") } catch (e: Exception) { context.contentResolver.openOutputStream(uri) })
            ?: throw IOException("cannot open")
        return out.use { dst -> src.inputStream().use { it.copyTo(dst, BUFFER) } }
    }

    /** Deletes a file or folder, ignoring failures. */
    fun delete(f: File) {
        try {
            f.deleteRecursively()
        } catch (e: Exception) {
            // best effort: the sweep removes it later
        }
    }

    /** Removes what an interrupted import, export or attachment left behind, and pictures nobody looked at for a month. */
    fun sweep(context: Context, nowMs: Long = System.currentTimeMillis()) {
        for (name in listOf(NotesTransfer.DIR_EXPORT, NotesTransfer.DIR_IMPORT, NotesTransfer.DIR_ADD, NotesTransfer.DIR_OPEN)) {
            File(context.cacheDir, name).listFiles()?.forEach { if (nowMs - it.lastModified() > STALE_MS) delete(it) }
        }
        File(context.cacheDir, NotesTransfer.DIR_PICTURES).listFiles()?.forEach { if (nowMs - it.lastModified() > PICTURES_STALE_MS) delete(it) }
    }
}
