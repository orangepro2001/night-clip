package com.nightclip.app.ui

import android.app.Application
import android.net.Uri
import android.util.Log
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.nightclip.app.ClipBridgeApp
import com.nightclip.app.R
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.CoreApi
import com.nightclip.app.core.CoreEvent
import com.nightclip.app.core.CoreHost
import com.nightclip.app.core.CoreState
import com.nightclip.app.core.PeerInfo
import com.nightclip.app.core.TransferInfo
import com.nightclip.app.logic.PeerTexts
import com.nightclip.app.logic.Strings
import com.nightclip.app.logic.TransferReducer
import com.nightclip.app.platform.CameraCapture
import com.nightclip.app.platform.FileSender
import com.nightclip.app.platform.NetworkMonitor
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

/** State shared by the devices tab, the share sheet and the pairing result: peers, transfers, one-shot messages. */
class AppViewModel(application: Application) : AndroidViewModel(application) {
    private val app = application as ClipBridgeApp

    private val _peers = MutableStateFlow<List<PeerInfo>>(emptyList())
    val peers: StateFlow<List<PeerInfo>> = _peers.asStateFlow()

    private val _transfers = MutableStateFlow<List<TransferInfo>>(emptyList())
    val transfers: StateFlow<List<TransferInfo>> = _transfers.asStateFlow()

    private val _messages = MutableSharedFlow<String>(extraBufferCapacity = 16)
    val messages: SharedFlow<String> = _messages.asSharedFlow()

    /** Advisory only (shell-guide §4 item 4). */
    val vpnActive: StateFlow<Boolean> =
        NetworkMonitor.vpnActive(application).stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), true)

    init {
        viewModelScope.launch {
            CoreHost.state.first { it is CoreState.Ready }
            refreshPeers()
            refreshTransfers()
        }
        viewModelScope.launch { CoreHost.events.collect(::onEvent) }
    }

    private fun onEvent(ev: CoreEvent) {
        when (ev) {
            is CoreEvent.PeerState, is CoreEvent.PeerRevoked, is CoreEvent.PeerIntroduced, is CoreEvent.CoreStarted, is CoreEvent.OutboxExpired -> refreshPeers()
            is CoreEvent.PairResult -> if (ev.state == "accepted") refreshPeers()
            is CoreEvent.FileOffered, is CoreEvent.FileState, is CoreEvent.FileProgress, is CoreEvent.FileDone, is CoreEvent.FileFailed -> {
                _transfers.value = TransferReducer.apply(_transfers.value, ev)
                if (TransferReducer.needsRefresh(_transfers.value, ev) || ev is CoreEvent.FileDone || ev is CoreEvent.FileFailed) refreshTransfers()
            }
            else -> Unit
        }
    }

    fun refreshAll() {
        refreshPeers()
        refreshTransfers()
    }

    fun refreshPeers() {
        viewModelScope.launch {
            when (val r = CoreApi.peers()) {
                is ApiResult.Ok -> _peers.value = r.value
                is ApiResult.Fail -> Unit // the core may still be starting; the next event or resume retries
            }
        }
    }

    fun refreshTransfers() {
        viewModelScope.launch {
            (CoreApi.transfers(50) as? ApiResult.Ok)?.let { _transfers.value = it.value }
        }
    }

    private fun say(text: String) {
        _messages.tryEmit(text)
    }

    /** One-shot message from a screen that cannot reach [say] (for example a failed intent launch). */
    fun report(text: String) = say(text)

    // ---- clipboard sending -----------------------------------------------------------------

    /** [to] null = every paired device. The text comes from a user action in a visible screen. */
    fun sendText(text: String, to: List<String>? = null) {
        if (text.isEmpty()) {
            say(Strings.CLIPBOARD_EMPTY)
            return
        }
        viewModelScope.launch {
            when (val r = CoreApi.sendClip(text, to)) {
                is ApiResult.Ok -> say(PeerTexts.sendOutcome(r.value))
                is ApiResult.Fail -> say(r.message)
            }
            refreshPeers()
        }
    }

    // ---- devices ---------------------------------------------------------------------------

    fun rename(id: String, name: String) = peerCall { CoreApi.renamePeer(id, name.trim()) }
    fun setSend(id: String, enabled: Boolean) = peerCall { CoreApi.setPeerSend(id, enabled) }
    fun setMode(id: String, mode: String) = peerCall { CoreApi.setPeerMode(id, mode) }
    fun remove(id: String) = peerCall { CoreApi.removePeer(id) }

    private fun peerCall(block: suspend () -> ApiResult<Unit>) {
        viewModelScope.launch {
            val r = block()
            if (r is ApiResult.Fail) say(r.message)
            refreshPeers()
        }
    }

    // ---- files -----------------------------------------------------------------------------

    fun sendFiles(uris: List<Uri>, to: String) {
        if (uris.isEmpty()) return
        viewModelScope.launch {
            try {
                val opened = FileSender.open(app, uris)
                val files = opened.mapNotNull { it.first }
                opened.mapNotNull { it.second }.distinct().forEach(::say)
                if (files.isEmpty()) return@launch
                // The descriptors are detached: finish the hand-over even if this screen goes away.
                app.appScope.launch {
                    try {
                        val results = FileSender.offer(to, files)
                        val failed = results.firstOrNull { it.error != null }
                        _messages.tryEmit(failed?.error ?: app.getString(com.nightclip.app.R.string.msg_files_offered, results.size))
                        refreshTransfers()
                    } catch (e: Exception) {
                        Log.e("ClipBridge", "file offer failed", e)
                        say(app.getString(R.string.msg_file_send_failed) + " (" + e.javaClass.simpleName + ")")
                    }
                }
            } catch (e: Exception) {
                Log.e("ClipBridge", "opening files failed", e)
                say(app.getString(R.string.msg_file_send_failed) + " (" + e.javaClass.simpleName + ")")
            }
        }
    }

    /**
     * The photo the camera app has just written to [file], offered to every device in [targets]. Each device
     * gets its own descriptor (a descriptor can only be handed to the core once); the file stays in the cache
     * folder, see [com.nightclip.app.logic.CaptureFiles].
     */
    fun sendCapture(file: java.io.File, targets: List<String>) {
        if (!file.isFile || file.length() == 0L) {
            file.delete()
            say(app.getString(R.string.msg_capture_empty))
            return
        }
        if (targets.isEmpty()) {
            say(Strings.SEND_NOTHING)
            return
        }
        val uri = CameraCapture.uriFor(app, file)
        targets.forEach { sendFiles(listOf(uri), it) }
    }

    fun accept(id: String) = transferCall { CoreApi.acceptFile(id) }
    fun reject(id: String) = transferCall { CoreApi.rejectFile(id) }
    fun cancel(id: String) = transferCall { CoreApi.cancelFile(id) }

    private fun transferCall(block: suspend () -> ApiResult<Unit>) {
        viewModelScope.launch {
            val r = block()
            if (r is ApiResult.Fail) say(r.message)
            refreshTransfers()
        }
    }
}
