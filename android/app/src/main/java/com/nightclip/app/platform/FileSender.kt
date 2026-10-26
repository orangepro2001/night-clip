package com.nightclip.app.platform

import android.content.Context
import android.net.Uri
import android.provider.OpenableColumns
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.CoreApi
import com.nightclip.app.core.CoreHost
import com.nightclip.app.logic.FileNames
import com.nightclip.app.logic.Strings
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * File send path (shell-guide §4 item 9): ContentResolver -> ParcelFileDescriptor -> detachFd()
 * -> `file.offer {to, fd, name, size}`.
 *
 * OWNERSHIP: after detachFd() the raw descriptor belongs to the core. This code never closes it
 * again, not even when the call fails.
 */
object FileSender {
    /** A file whose descriptor has been detached and is waiting to be offered. */
    class Opened(val name: String, val size: Long, val fd: Int) {
        override fun toString() = "Opened(name=$name, size=$size)"
    }

    data class Outcome(val name: String, val transferId: String?, val error: String?)

    /** Opens and detaches descriptors while the caller's URI grant is still alive (share intents). */
    suspend fun open(context: Context, uris: List<Uri>): List<Pair<Opened?, String?>> {
        // Do not detach descriptors if nobody can take them.
        if (!CoreHost.awaitReady()) return uris.map { null to Strings.ERR_NOT_READY }
        return withContext(Dispatchers.IO) { uris.map { uri -> openOne(context, uri) } }
    }

    private fun openOne(context: Context, uri: Uri): Pair<Opened?, String?> {
        try {
            val (name, queried) = queryNameAndSize(context, uri)
            val pfd = context.contentResolver.openFileDescriptor(uri, "r") ?: return null to Strings.ERR_NO_FILE_ACCESS
            val size = if (queried >= 0) queried else pfd.statSize
            if (size < 0) {
                pfd.close() // still ours: we did not detach it
                return null to Strings.ERR_NO_FILE_SIZE
            }
            return Opened(name, size, pfd.detachFd()) to null // from here on the core owns the descriptor
        } catch (e: Exception) { // SecurityException, FileNotFoundException, …
            return null to Strings.ERR_NO_FILE_ACCESS
        }
    }

    suspend fun offer(to: String, files: List<Opened>): List<Outcome> = files.map { f ->
        when (val r = CoreApi.offerFile(to, f.fd, f.name, f.size)) {
            is ApiResult.Ok -> Outcome(f.name, r.value, null)
            is ApiResult.Fail -> Outcome(f.name, null, r.message)
        }
    }

    private fun queryNameAndSize(context: Context, uri: Uri): Pair<String, Long> {
        var name: String? = null
        var size = -1L
        context.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)?.use { c ->
            if (c.moveToFirst()) {
                val ni = c.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                val si = c.getColumnIndex(OpenableColumns.SIZE)
                if (ni >= 0 && !c.isNull(ni)) name = c.getString(ni)
                if (si >= 0 && !c.isNull(si)) size = c.getLong(si)
            }
        }
        return FileNames.sanitize(name ?: uri.lastPathSegment ?: "file") to size
    }
}
