package com.nightclip.app.platform

import android.Manifest
import android.content.ContentResolver
import android.content.ContentValues
import android.content.Context
import android.content.pm.PackageManager
import android.media.MediaScannerConnection
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import androidx.annotation.RequiresApi
import androidx.core.content.ContextCompat
import com.nightclip.app.logic.FileNames
import java.io.File
import java.io.IOException

/**
 * Moves a received file from the app-private folder into the public "Download/ClipBridge"
 * folder (shell-guide §4 item 9). API 29+: MediaStore.Downloads with IS_PENDING so other apps
 * never see a half-written file. API 26–28: the public Downloads directory when the legacy
 * storage permission is granted, otherwise the app's own external downloads folder.
 */
object MediaStoreSaver {
    const val SUBDIR = "ClipBridge"

    /** [location] is shown to the user; [publicDownloads] is false if only the app-private fallback was possible. */
    data class Saved(val displayName: String, val location: String, val publicDownloads: Boolean)

    @Throws(IOException::class)
    fun save(context: Context, source: File, rawName: String, mime: String?): Saved {
        val wanted = FileNames.sanitize(rawName)
        val type = FileNames.mimeFromName(wanted, mime)
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) saveQ(context, source, wanted, type) else saveLegacy(context, source, wanted, type)
    }

    @RequiresApi(Build.VERSION_CODES.Q)
    private fun saveQ(context: Context, source: File, wanted: String, mime: String): Saved {
        val resolver = context.contentResolver
        val relative = Environment.DIRECTORY_DOWNLOADS + "/" + SUBDIR + "/"
        val name = FileNames.uniqueName(wanted) { existsQ(resolver, relative, it) }
        val values = ContentValues().apply {
            put(MediaStore.MediaColumns.DISPLAY_NAME, name)
            put(MediaStore.MediaColumns.MIME_TYPE, mime)
            put(MediaStore.MediaColumns.RELATIVE_PATH, relative)
            put(MediaStore.MediaColumns.IS_PENDING, 1)
        }
        val uri = resolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values) ?: throw IOException("MediaStore insert failed")
        try {
            val out = resolver.openOutputStream(uri) ?: throw IOException("cannot open MediaStore output")
            val copied = out.use { o -> source.inputStream().use { it.copyTo(o, 64 * 1024) } }
            if (copied != source.length()) throw IOException("short copy")
            val done = ContentValues().apply { put(MediaStore.MediaColumns.IS_PENDING, 0) }
            if (resolver.update(uri, done, null, null) < 1) throw IOException("cannot publish file")
        } catch (e: Exception) {
            runCatching { resolver.delete(uri, null, null) }
            throw if (e is IOException) e else IOException(e)
        }
        return Saved(name, relative, publicDownloads = true)
    }

    @RequiresApi(Build.VERSION_CODES.Q)
    private fun existsQ(resolver: ContentResolver, relative: String, name: String): Boolean =
        resolver.query(
            MediaStore.Downloads.EXTERNAL_CONTENT_URI,
            arrayOf(MediaStore.MediaColumns._ID),
            "${MediaStore.MediaColumns.DISPLAY_NAME}=? AND ${MediaStore.MediaColumns.RELATIVE_PATH}=?",
            arrayOf(name, relative),
            null,
        )?.use { it.moveToFirst() } ?: false

    @Suppress("DEPRECATION")
    private fun saveLegacy(context: Context, source: File, wanted: String, mime: String): Saved {
        val granted = ContextCompat.checkSelfPermission(context, Manifest.permission.WRITE_EXTERNAL_STORAGE) == PackageManager.PERMISSION_GRANTED
        val base: File? = if (granted) {
            Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS)
        } else {
            context.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS)
        }
        val dir = File(base ?: throw IOException("external storage unavailable"), SUBDIR)
        if (!dir.isDirectory && !dir.mkdirs()) throw IOException("cannot create $dir")
        val name = FileNames.uniqueName(wanted) { File(dir, it).exists() }
        val tmp = File(dir, "$name.part")
        val target = File(dir, name)
        try {
            source.copyTo(tmp, overwrite = true)
            if (!tmp.renameTo(target)) throw IOException("cannot rename into place")
        } catch (e: Exception) {
            tmp.delete()
            throw if (e is IOException) e else IOException(e)
        }
        MediaScannerConnection.scanFile(context, arrayOf(target.path), arrayOf(mime), null)
        return Saved(name, target.parentFile?.path ?: dir.path, publicDownloads = granted)
    }
}
