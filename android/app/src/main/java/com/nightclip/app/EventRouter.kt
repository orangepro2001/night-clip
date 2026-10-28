package com.nightclip.app

import android.content.Context
import android.content.Intent
import android.os.Handler
import android.os.Looper
import android.util.Log
import android.widget.Toast
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.CoreApi
import com.nightclip.app.core.CoreEvent
import com.nightclip.app.core.CoreHost
import com.nightclip.app.core.CoreState
import com.nightclip.app.core.TransferInfo
import com.nightclip.app.logic.PathGuard
import com.nightclip.app.logic.ReceivedImages
import com.nightclip.app.logic.ReceiveDecider
import com.nightclip.app.logic.Strings
import com.nightclip.app.logic.TransferReducer
import com.nightclip.app.platform.ForegroundTracker
import com.nightclip.app.platform.MediaStoreSaver
import com.nightclip.app.platform.NotesSession
import com.nightclip.app.platform.Notifications
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.filterIsInstance
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import java.io.IOException
import java.util.concurrent.ConcurrentHashMap

/**
 * Turns core events into user-visible effects. It is the one place that posts notifications, so it
 * works whenever the process is alive, whether or not a screen or the foreground service exists.
 * The real-time service only keeps the process alive; it does not consume events.
 */
class EventRouter(private val app: ClipBridgeApp) {
    private val tag = "ClipBridge"
    private val dataDir get() = File(app.filesDir, "clipbridge")
    private val main = Handler(Looper.getMainLooper())

    @Volatile private var transfers: List<TransferInfo> = emptyList()
    private val saving = ConcurrentHashMap.newKeySet<String>()
    private val lastProgressAt = HashMap<String, Long>()

    fun start() {
        // Held images nobody opened within a day (the notification was swiped away): never saved, so they go.
        app.appScope.launch(Dispatchers.IO) {
            val cutoff = System.currentTimeMillis() - 24L * 60 * 60 * 1000
            ImagePreviewActivity.holdRoot(app).listFiles()?.filter { it.lastModified() < cutoff }?.forEach { it.deleteRecursively() }
        }
        app.appScope.launch {
            CoreHost.state.filterIsInstance<CoreState.Ready>().first()
            bootstrap()
        }
        app.appScope.launch {
            CoreHost.events.collect { ev ->
                try {
                    handle(ev)
                } catch (e: Exception) {
                    Log.w(tag, "handling ${ev.type} failed: ${e.javaClass.simpleName}")
                }
            }
        }
    }

    /** Reads transfers once and finishes saves that an earlier process did not complete. */
    private suspend fun bootstrap() {
        val list = (CoreApi.transfers(200) as? ApiResult.Ok)?.value ?: return
        transfers = list
        for (t in list) {
            if (t.incoming && t.state == "done" && t.path.isNotEmpty()) {
                app.appScope.launch { saveReceived(t.transferId, t.name, t.path) }
            }
        }
    }

    private suspend fun handle(ev: CoreEvent) {
        when (ev) {
            is CoreEvent.ClipReceived -> onClip(ev)
            is CoreEvent.FileOffered -> {
                transfers = TransferReducer.apply(transfers, ev)
                Notifications.postFileOffer(app, ev)
            }
            is CoreEvent.FileState, is CoreEvent.FileProgress, is CoreEvent.FileDone, is CoreEvent.FileFailed -> onTransferEvent(ev)
            // Notes only change screens in this version (no notifications); the session keeps what must survive a missing screen.
            is CoreEvent.NotesChanged, is CoreEvent.NotesSyncUpdated, is CoreEvent.NotesSyncConflict, is CoreEvent.NotesAttachment -> NotesSession.onEvent(ev)
            is CoreEvent.PeerRevoked ->
                Notifications.postInfo(app, "revoked-${ev.deviceId}", app.getString(R.string.notif_peer_revoked_title), app.getString(R.string.notif_peer_revoked_text, ev.name.ifBlank { Strings.UNKNOWN_DEVICE }))
            is CoreEvent.UpdateReady ->
                Notifications.postUpdate(app, app.getString(R.string.notif_update_ready_title), app.getString(R.string.notif_update_ready_text, ev.versionName))
            is CoreEvent.UpdateAvailable -> {
                // With automatic download on, "ready" follows by itself; otherwise ask the user.
                val auto = CoreApi.updateStatus().okOrNull?.auto ?: true
                if (!auto) Notifications.postUpdate(app, app.getString(R.string.notif_update_available_title), app.getString(R.string.notif_update_available_text, ev.versionName))
            }
            else -> Unit
        }
    }

    // ---- clipboard ---------------------------------------------------------------------------

    private suspend fun onClip(ev: CoreEvent.ClipReceived) {
        val outcome = ReceiveDecider.decide(ev, ForegroundTracker.isForeground)
        var degraded = outcome.degraded
        if (outcome.action == ReceiveDecider.Action.WRITE_NOW) {
            // Open the echo window before the clipboard write: the write itself can trigger a
            // clipboard notification that must not be sent back to the sender.
            if (CoreApi.beginApply(ev.eventId) is ApiResult.Ok) {
                val written = withContext(Dispatchers.Main) {
                    // Re-check on the main thread: the user may have left the app since the decision.
                    ForegroundTracker.isForeground && !ev.sensitive && com.nightclip.app.platform.ClipboardAccess.writeText(app, ev.text)
                }
                if (written) {
                    CoreApi.markApplied(ev.eventId)
                    toast(app.getString(R.string.toast_auto_copied, ev.fromName.ifBlank { Strings.UNKNOWN_DEVICE }))
                    return
                }
            }
            degraded = true
        }
        Notifications.postClip(app, ev, degraded)
    }

    // ---- files -------------------------------------------------------------------------------

    private suspend fun onTransferEvent(ev: CoreEvent) {
        if (TransferReducer.needsRefresh(transfers, ev)) {
            (CoreApi.transfers(200) as? ApiResult.Ok)?.let { transfers = it.value }
        }
        transfers = TransferReducer.apply(transfers, ev)
        val id = when (ev) {
            is CoreEvent.FileState -> ev.transferId
            is CoreEvent.FileProgress -> ev.transferId
            is CoreEvent.FileDone -> ev.transferId
            is CoreEvent.FileFailed -> ev.transferId
            else -> return
        }
        val t = transfers.firstOrNull { it.transferId == id } ?: return
        when (ev) {
            is CoreEvent.FileProgress -> {
                val now = System.currentTimeMillis()
                if (now - (lastProgressAt[id] ?: 0) >= 500) {
                    lastProgressAt[id] = now
                    Notifications.postTransferProgress(app, t)
                }
            }
            is CoreEvent.FileState -> when (ev.state) {
                "hashing", "accepted", "transferring" -> {
                    if (t.incoming) Notifications.cancelFileOffer(app, id)
                    Notifications.postTransferProgress(app, t)
                }
                "rejected" -> {
                    Notifications.cancelFileOffer(app, id)
                    if (!t.incoming) Notifications.postTransferResult(app, id, app.getString(R.string.notif_file_rejected_title), t.name)
                }
                "cancelled" -> { Notifications.cancelFileOffer(app, id); Notifications.cancelTransfer(app, id) }
                "expired" -> { Notifications.cancelFileOffer(app, id); Notifications.cancelTransfer(app, id) }
                else -> Unit
            }
            is CoreEvent.FileDone -> {
                lastProgressAt.remove(id)
                if (ev.direction == "out" || !t.incoming) {
                    Notifications.postTransferResult(app, id, app.getString(R.string.notif_file_sent_title), Strings.fileSentDone(t.name))
                } else {
                    // Heavy copy: do not hold up the event collector.
                    app.appScope.launch { saveReceived(id, ev.name.ifEmpty { t.name }, ev.path.ifEmpty { t.path }, t.peerName) }
                }
            }
            is CoreEvent.FileFailed -> {
                lastProgressAt.remove(id)
                Notifications.cancelFileOffer(app, id)
                Notifications.postTransferResult(app, id, Strings.FILE_FAILED_TITLE, "${t.name}\n${Notifications.fileFailedText(ev.code)}")
            }
            else -> Unit
        }
    }

    /**
     * A received image is held for the preview instead (logic/ReceivedImages.kt): copied into the hold folder, the
     * core's copy released, then the preview opens (app in front) or a notification with a thumbnail offers it.
     * False when it cannot be offered (no notifications while in the background): it is saved like any file.
     */
    private suspend fun holdImage(id: String, src: File, name: String, from: String): Boolean {
        val dir = File(ImagePreviewActivity.holdRoot(app), ReceivedImages.holdFolder(id))
        val held = File(dir, File(name).name.ifBlank { "image" })
        val foreground = ForegroundTracker.isForeground
        if (!foreground && !Notifications.canPost(app)) return false
        try {
            withContext(Dispatchers.IO) {
                dir.mkdirs()
                src.copyTo(held, overwrite = true)
            }
        } catch (e: IOException) {
            Log.w(tag, "holding image $id failed: ${e.javaClass.simpleName}")
            dir.deleteRecursively()
            return false
        }
        app.prefs.markSaved(id)
        CoreApi.releaseFile(id)
        val shown = foreground && runCatching {
            app.startActivity(ImagePreviewActivity.intent(app, id, held, name, from).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        }.isSuccess
        if (!shown) Notifications.postImage(app, id, held, name, from)
        return true
    }

    /** Copies a received file into Downloads, then tells the core it may delete its private copy. */
    private suspend fun saveReceived(id: String, name: String, path: String, from: String = "") {
        if (!saving.add(id)) return
        try {
            val src = File(path)
            if (app.prefs.wasSaved(id)) {
                // Saved earlier but `file.release` did not go through (crash, core error): only retry the release.
                CoreApi.releaseFile(id)
                return
            }
            if (!PathGuard.isInside(dataDir, src)) {
                Log.w(tag, "ignoring file outside the data directory for $id")
                return
            }
            if (ReceivedImages.isPreviewable(name, src.length()) && holdImage(id, src, name, from)) {
                Notifications.cancelTransfer(app, id)
                return
            }
            val saved = try {
                withContext(Dispatchers.IO) { MediaStoreSaver.save(app, src, name, null) }
            } catch (e: IOException) {
                Log.w(tag, "saving $id failed: ${e.javaClass.simpleName}")
                Notifications.postTransferResult(app, id, Strings.FILE_FAILED_TITLE, "$name\n${Strings.ERR_SAVE_FAILED}")
                return
            }
            app.prefs.markSaved(id)
            CoreApi.releaseFile(id)
            val text = if (saved.publicDownloads) Strings.fileSaved(saved.displayName) else Strings.fileSavedPrivate(saved.displayName, saved.location)
            Notifications.postTransferResult(app, id, app.getString(R.string.notif_file_saved_title), text)
        } finally {
            saving.remove(id)
        }
    }

    private fun toast(text: String) {
        main.post { Toast.makeText(app, text, Toast.LENGTH_SHORT).show() }
    }
}

/** Shows a short message from any thread. */
fun Context.toastOnMain(text: String) {
    Handler(Looper.getMainLooper()).post { Toast.makeText(applicationContext, text, Toast.LENGTH_SHORT).show() }
}
