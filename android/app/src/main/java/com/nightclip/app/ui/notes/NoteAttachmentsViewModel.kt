package com.nightclip.app.ui.notes

import android.app.Application
import android.content.ActivityNotFoundException
import android.content.ClipData
import android.content.Intent
import android.graphics.Bitmap
import android.net.Uri
import android.util.LruCache
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.core.content.FileProvider
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.AttachmentInfo
import com.nightclip.app.core.CoreEvent
import com.nightclip.app.core.CoreHost
import com.nightclip.app.core.NotesApi
import com.nightclip.app.logic.AttachmentFiles
import com.nightclip.app.logic.AttachmentState
import com.nightclip.app.logic.BlobAddress
import com.nightclip.app.logic.NotesErrors
import com.nightclip.app.logic.NotesTexts
import com.nightclip.app.logic.NotesTransfer
import com.nightclip.app.platform.AttachmentBitmaps
import com.nightclip.app.platform.NotesFiles
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import java.io.IOException

/** The user has to decide what happens to an attachment: a type that is not opened from a note, or a long press on a chip. */
data class AttachmentChoice(val blobId: String, val name: String, val mime: String, val blocked: Boolean)

/**
 * What the preview knows about the attachments (`blob:<id>`) of the notes on screen, and what can be done with them.
 * A picture is exported from the core into the cache once and decoded at a size the screen needs; a file is exported
 * under its real name into a folder that only the app's FileProvider shares, and opened, saved or shared from there.
 * A `blob:` address never reaches the web-link path: it only ever comes here.
 */
class NoteAttachmentsViewModel(application: Application) : AndroidViewModel(application) {
    private val app = application

    private val _states = MutableStateFlow<Map<String, AttachmentState>>(emptyMap())
    val states: StateFlow<Map<String, AttachmentState>> = _states.asStateFlow()

    private val _choices = MutableSharedFlow<AttachmentChoice>(extraBufferCapacity = 4)
    val choices: SharedFlow<AttachmentChoice> = _choices.asSharedFlow()

    private val _messages = MutableSharedFlow<String>(extraBufferCapacity = 8)
    val messages: SharedFlow<String> = _messages.asSharedFlow()

    private val resolving = HashSet<String>()

    private val bitmaps = object : LruCache<String, Bitmap>(BITMAP_CACHE_BYTES) {
        override fun sizeOf(key: String, value: Bitmap): Int = value.allocationByteCount
    }

    init {
        // A file that was missing has just synced in: ask again, the placeholder turns into the picture or the chip.
        viewModelScope.launch {
            CoreHost.events.collect { ev ->
                if (ev is CoreEvent.NotesAttachment && BlobAddress.isBlobId(ev.blobId)) {
                    val state = _states.value[ev.blobId]
                    if (state is AttachmentState.Waiting || state is AttachmentState.Failed) resolve(ev.blobId, hint = "")
                }
            }
        }
    }

    private fun say(text: String) {
        _messages.tryEmit(text)
    }

    private fun put(blobId: String, state: AttachmentState) {
        _states.update { it + (blobId to state) }
    }

    // ---- showing -------------------------------------------------------------------------------

    /** A preview wants to show [blobId]; [hint] is the name written in the note, used while nothing else is known. */
    fun request(blobId: String, hint: String) {
        if (!BlobAddress.isBlobId(blobId)) {
            put(blobId, AttachmentState.Failed(hint))
            return
        }
        when (val s = _states.value[blobId]) {
            is AttachmentState.Document, AttachmentState.Loading -> return
            is AttachmentState.Picture -> if (File(s.path).isFile) return
            else -> Unit // not asked yet, waiting for the sync, failed once: try (again)
        }
        resolve(blobId, hint)
    }

    private fun resolve(blobId: String, hint: String) {
        if (!resolving.add(blobId)) return
        if (_states.value[blobId] == null) put(blobId, AttachmentState.Loading)
        viewModelScope.launch {
            try {
                put(blobId, resolveNow(blobId, hint))
            } finally {
                resolving.remove(blobId)
            }
        }
    }

    private suspend fun resolveNow(blobId: String, hint: String): AttachmentState {
        val info = (NotesApi.attachmentGet(blobId) as? ApiResult.Ok)?.value ?: return AttachmentState.Failed(hint)
        return when (AttachmentFiles.kindOf(info)) {
            AttachmentFiles.Kind.ABSENT -> AttachmentState.Waiting
            AttachmentFiles.Kind.DOCUMENT -> AttachmentState.Document(info.name, info.mime, info.size)
            AttachmentFiles.Kind.PICTURE -> exportPicture(info)?.let { AttachmentState.Picture(it.absolutePath, info.name, info.size) } ?: AttachmentState.Failed(info.name)
        }
    }

    /** `cache/notes/<id>.<ext>`: exported once (a file of the right size is kept), written under a temporary name first. */
    private suspend fun exportPicture(info: AttachmentInfo): File? {
        val name = AttachmentFiles.pictureCacheName(info.blobId, info.name, info.mime) ?: return null
        val dir = withContext(Dispatchers.IO) { NotesFiles.dir(app, NotesTransfer.DIR_PICTURES) }
        val target = File(dir, name)
        if (withContext(Dispatchers.IO) { target.isFile && target.length() == info.size && target.setLastModified(System.currentTimeMillis()) }) return target
        val part = File(dir, "$name.part")
        return when (NotesApi.attachmentExport(info.blobId, part.absolutePath)) {
            is ApiResult.Ok -> withContext(Dispatchers.IO) { if (part.renameTo(target)) target else null.also { part.delete() } }
            is ApiResult.Fail -> withContext(Dispatchers.IO) { null.also { part.delete() } }
        }
    }

    /** The picture decoded for a view [targetWidthPx] wide; null if it cannot be decoded. Decoded pictures are cached (by size in bytes). */
    suspend fun bitmap(path: String, targetWidthPx: Int): ImageBitmap? {
        val key = "$path@$targetWidthPx"
        bitmaps.get(key)?.let { return it.asImageBitmap() }
        val bmp = withContext(Dispatchers.IO) { AttachmentBitmaps.decode(path, targetWidthPx) } ?: return null
        bitmaps.put(key, bmp)
        return bmp.asImageBitmap()
    }

    // ---- using ---------------------------------------------------------------------------------

    /** Tap on a chip: opens the file with another app, or asks what to do when its type is not opened from a note. */
    fun open(blobId: String, hint: String) {
        viewModelScope.launch {
            val info = present(blobId) ?: return@launch
            if (!AttachmentFiles.isSafeToOpen(info.name, info.mime)) {
                _choices.emit(AttachmentChoice(blobId, info.name, info.mime, blocked = true))
                return@launch
            }
            val file = exportForOpen(info) ?: return@launch say(NotesTexts.ATTACHMENT_FAILED)
            view(file, info.mime)
        }
    }

    /** Long press on a chip: save or share, whatever the type. */
    fun choose(blobId: String) {
        viewModelScope.launch {
            val info = present(blobId) ?: return@launch
            _choices.emit(AttachmentChoice(blobId, info.name, info.mime, blocked = !AttachmentFiles.isSafeToOpen(info.name, info.mime)))
        }
    }

    fun share(blobId: String) {
        viewModelScope.launch {
            val info = present(blobId) ?: return@launch
            val file = exportForOpen(info) ?: return@launch say(NotesTexts.ATTACHMENT_FAILED)
            val uri = uriFor(file)
            val send = Intent(Intent.ACTION_SEND)
                .setType(AttachmentFiles.openMime(info.mime))
                .putExtra(Intent.EXTRA_STREAM, uri)
                .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            send.clipData = ClipData.newRawUri(info.name, uri)
            val chooser = Intent.createChooser(send, info.name).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_GRANT_READ_URI_PERMISSION)
            try {
                app.startActivity(chooser)
            } catch (e: Exception) {
                say(NotesTexts.ATTACHMENT_NO_APP)
            }
        }
    }

    /** Copies the attachment into the document [target] the user picked. */
    fun saveTo(blobId: String, target: Uri) {
        viewModelScope.launch {
            val info = present(blobId) ?: return@launch
            val file = exportForOpen(info) ?: return@launch say(NotesTexts.ATTACHMENT_FAILED)
            val ok = withContext(Dispatchers.IO) {
                try {
                    NotesFiles.copyOut(app, file, target)
                    true
                } catch (e: IOException) {
                    false
                }
            }
            say(if (ok) NotesTexts.ATTACHMENT_SAVED else NotesTexts.ATTACHMENT_FAILED)
        }
    }

    /** The core's answer for a file that is here; says why not otherwise (still syncing, gone) and returns null. */
    private suspend fun present(blobId: String): AttachmentInfo? {
        if (!BlobAddress.isBlobId(blobId)) {
            say(NotesTexts.ATTACHMENT_FAILED)
            return null
        }
        return when (val r = NotesApi.attachmentGet(blobId)) {
            is ApiResult.Fail -> {
                say(NotesErrors.message(r.code, NotesErrors.Context.ATTACH))
                null
            }
            is ApiResult.Ok -> if (r.value.present) r.value else {
                say(NotesTexts.ATTACHMENT_SYNCING)
                null
            }
        }
    }

    /** `cache/notes-open/<16 hex>/<real name>`: the name the other app shows, in a folder only this attachment uses. */
    private suspend fun exportForOpen(info: AttachmentInfo): File? {
        val dir = withContext(Dispatchers.IO) { File(NotesFiles.dir(app, NotesTransfer.DIR_OPEN), AttachmentFiles.openFolder(info.blobId)).apply { mkdirs() } }
        val target = File(dir, AttachmentFiles.openName(info.name))
        return when (NotesApi.attachmentExport(info.blobId, target.absolutePath)) {
            is ApiResult.Ok -> target
            is ApiResult.Fail -> null
        }
    }

    private fun uriFor(file: File): Uri = FileProvider.getUriForFile(app, "${app.packageName}.updates", file)

    private fun view(file: File, mime: String) {
        val intent = Intent(Intent.ACTION_VIEW)
            .setDataAndType(uriFor(file), AttachmentFiles.openMime(mime))
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        try {
            app.startActivity(intent)
        } catch (e: ActivityNotFoundException) {
            say(NotesTexts.ATTACHMENT_NO_APP)
        } catch (e: SecurityException) {
            say(NotesTexts.ATTACHMENT_NO_APP)
        }
    }

    private companion object {
        /** Decoded pictures kept for scrolling back and forth. */
        const val BITMAP_CACHE_BYTES = 32 * 1024 * 1024
    }
}
