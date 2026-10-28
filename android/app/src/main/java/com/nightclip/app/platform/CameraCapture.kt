package com.nightclip.app.platform

import android.content.Context
import android.net.Uri
import androidx.core.content.FileProvider
import com.nightclip.app.logic.CaptureFiles
import com.nightclip.app.logic.FileNames
import java.io.File

/**
 * The file the system camera app writes the photo into, shared through the app's FileProvider
 * (authority `<package>.updates`, folder `camera/` in res/xml/update_paths.xml; nothing else is shared).
 * No storage permission is involved; the photo does not go to the gallery.
 */
object CameraCapture {
    class Pending(val file: File, val uri: Uri)

    /** Cleans up old photos and reserves a new, unused file name. Nothing is created on disk yet. */
    fun prepare(context: Context, nowMillis: Long = System.currentTimeMillis()): Pending {
        val dir = File(context.cacheDir, CaptureFiles.DIR)
        if (!dir.isDirectory && !dir.mkdirs()) throw java.io.IOException("cannot create ${dir.path}")
        prune(dir, nowMillis)
        val name = FileNames.uniqueName(CaptureFiles.photoName(nowMillis)) { File(dir, it).exists() }
        val file = File(dir, name)
        return Pending(file, uriFor(context, file))
    }

    /** The content URI of a photo file (also used again after the camera returned, possibly in a new process). */
    fun uriFor(context: Context, file: File): Uri =
        FileProvider.getUriForFile(context, context.packageName + ".updates", file)

    private fun prune(dir: File, nowMillis: Long) {
        val files = dir.listFiles()?.filter { it.isFile } ?: return
        val old = CaptureFiles.expired(files.map { it.name to it.lastModified() }, nowMillis).toSet()
        files.filter { it.name in old }.forEach { it.delete() }
    }
}
