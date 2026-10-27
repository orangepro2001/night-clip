package com.nightclip.app.platform

import android.content.Context
import android.net.Uri
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.AttachmentInfo
import com.nightclip.app.core.ImportResult
import com.nightclip.app.core.NotesApi
import com.nightclip.app.logic.AttachmentFiles
import com.nightclip.app.logic.NotesErrors
import com.nightclip.app.logic.NotesTransfer
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File
import java.io.IOException

/** Export, import and "add an attachment" between the system pickers and the core, through the cache folders. */
object NotesTransferRunner {
    sealed interface Outcome<out T> {
        data class Done<T>(val value: T) : Outcome<T>
        data class Failed(val message: String) : Outcome<Nothing>
    }

    private fun failed(code: String, context: NotesErrors.Context) = Outcome.Failed(NotesErrors.message(code, context))

    // ---- attachments ---------------------------------------------------------------------------

    /**
     * Stores the picked file as an attachment: it is copied (streamed, at most 32 MiB) to a folder of its own in the
     * cache under its display name, because the core takes the attachment's name from the file name; the core reads it,
     * and the copy is deleted again.
     */
    suspend fun addAttachment(context: Context, uri: Uri): Outcome<AttachmentInfo> = withContext(Dispatchers.IO) {
        val picked = NotesFiles.query(context, uri)
        if (!NotesTransfer.attachmentSizeOk(picked.size)) return@withContext failed("TOO_LARGE", NotesErrors.Context.ATTACH)
        val dir = NotesFiles.freshDir(context, NotesTransfer.DIR_ADD)
        try {
            val file = File(dir, AttachmentFiles.uploadName(picked.name))
            try {
                NotesFiles.copyIn(context, uri, file, NotesTransfer.MAX_ATTACHMENT_BYTES)
            } catch (e: NotesFiles.TooLargeException) {
                return@withContext failed("TOO_LARGE", NotesErrors.Context.ATTACH)
            } catch (e: Exception) {
                return@withContext failed("INVALID", NotesErrors.Context.ATTACH)
            }
            when (val r = NotesApi.attachmentAdd(file.absolutePath)) {
                is ApiResult.Ok -> Outcome.Done(r.value)
                is ApiResult.Fail -> failed(r.code, NotesErrors.Context.ATTACH)
            }
        } finally {
            NotesFiles.delete(dir)
        }
    }

    // ---- export --------------------------------------------------------------------------------

    /** Asks the core for a ZIP in a fresh cache file, copies it into the document the user chose, deletes the cache file. */
    suspend fun export(context: Context, target: Uri, includeTrash: Boolean): Outcome<String> = withContext(Dispatchers.IO) {
        val file = File(NotesFiles.dir(context, NotesTransfer.DIR_EXPORT), NotesTransfer.uniqueName("notes", NotesFiles.nonce(), "zip"))
        try {
            when (val r = NotesApi.export(file.absolutePath, includeTrash)) {
                is ApiResult.Fail -> failed(r.code, NotesErrors.Context.TRANSFER)
                is ApiResult.Ok -> try {
                    NotesFiles.copyOut(context, file, target)
                    Outcome.Done(NotesTransfer.exportSummary(r.value))
                } catch (e: IOException) {
                    Outcome.Failed(NotesTransfer.EXPORT_FAILED)
                }
            }
        } finally {
            NotesFiles.delete(file)
        }
    }

    // ---- import --------------------------------------------------------------------------------

    /** Copies the picked .zip / .md / .txt into the cache (streamed, at most 256 MiB, with the extension the core goes by), imports it, deletes the copy. */
    suspend fun import(context: Context, source: Uri): Outcome<ImportResult> = withContext(Dispatchers.IO) {
        val picked = NotesFiles.query(context, source)
        val extension = NotesTransfer.importExtension(picked.name, picked.mime) ?: return@withContext Outcome.Failed(NotesTransfer.IMPORT_UNSUPPORTED)
        if (!NotesTransfer.importSizeOk(picked.size)) return@withContext Outcome.Failed(NotesTransfer.IMPORT_TOO_LARGE)
        val file = File(NotesFiles.dir(context, NotesTransfer.DIR_IMPORT), NotesTransfer.uniqueName("import", NotesFiles.nonce(), extension))
        try {
            try {
                NotesFiles.copyIn(context, source, file, NotesTransfer.MAX_IMPORT_BYTES)
            } catch (e: NotesFiles.TooLargeException) {
                return@withContext Outcome.Failed(NotesTransfer.IMPORT_TOO_LARGE)
            } catch (e: Exception) {
                return@withContext Outcome.Failed(NotesTransfer.IMPORT_UNREADABLE)
            }
            when (val r = NotesApi.import(file.absolutePath)) {
                is ApiResult.Ok -> Outcome.Done(r.value)
                is ApiResult.Fail -> failed(r.code, NotesErrors.Context.TRANSFER)
            }
        } finally {
            NotesFiles.delete(file)
        }
    }
}
