package com.nightclip.app.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.CoreApi
import com.nightclip.app.core.CoreEvent
import com.nightclip.app.core.CoreHost
import com.nightclip.app.core.CoreState
import com.nightclip.app.core.InboxItem
import com.nightclip.app.platform.Notifications
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

class InboxViewModel(application: Application) : AndroidViewModel(application) {
    private val _items = MutableStateFlow<List<InboxItem>>(emptyList())
    val items: StateFlow<List<InboxItem>> = _items.asStateFlow()

    private val _messages = MutableSharedFlow<String>(extraBufferCapacity = 8)
    val messages: SharedFlow<String> = _messages.asSharedFlow()

    init {
        viewModelScope.launch {
            CoreHost.state.first { it is CoreState.Ready }
            refresh()
        }
        viewModelScope.launch {
            CoreHost.events.collect { ev ->
                if (ev is CoreEvent.ClipReceived || ev is CoreEvent.ClipAck || ev is CoreEvent.PeerRevoked) refresh()
            }
        }
    }

    fun refresh() {
        viewModelScope.launch {
            (CoreApi.inbox(200) as? ApiResult.Ok)?.let { _items.value = it.value }
        }
    }

    /** Opens the core's echo window before the caller writes the clipboard from a visible screen. */
    fun beginApply(eventId: String, onReady: (Boolean) -> Unit) {
        viewModelScope.launch {
            val ok = CoreApi.beginApply(eventId) is ApiResult.Ok
            withContext(Dispatchers.Main) { onReady(ok) }
        }
    }

    /** The caller already wrote the text to the clipboard from a visible screen; this reports it to the core. */
    fun markApplied(eventId: String) {
        viewModelScope.launch {
            CoreApi.markApplied(eventId)
            Notifications.cancelClip(getApplication(), eventId)
            refresh()
        }
    }

    fun dismiss(eventId: String) {
        viewModelScope.launch {
            val r = CoreApi.dismiss(eventId)
            if (r is ApiResult.Fail) _messages.tryEmit(r.message)
            Notifications.cancelClip(getApplication(), eventId)
            refresh()
        }
    }

    fun clear() {
        viewModelScope.launch {
            val r = CoreApi.clearInbox()
            if (r is ApiResult.Fail) _messages.tryEmit(r.message)
            refresh()
        }
    }
}
