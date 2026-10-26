package com.nightclip.app.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.nightclip.app.UpdateState
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.CoreApi
import com.nightclip.app.core.CoreEvent
import com.nightclip.app.core.CoreHost
import com.nightclip.app.core.CoreSettings
import com.nightclip.app.core.CoreState
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

class SettingsViewModel(application: Application) : AndroidViewModel(application) {
    private val _settings = MutableStateFlow(CoreSettings())
    val settings: StateFlow<CoreSettings> = _settings.asStateFlow()

    private val _update = MutableStateFlow(UpdateState())
    val update: StateFlow<UpdateState> = _update.asStateFlow()

    private val _identity = MutableStateFlow("" to "")
    val identity: StateFlow<Pair<String, String>> = _identity.asStateFlow()

    private val _diagnostics = MutableStateFlow<String?>(null)
    val diagnostics: StateFlow<String?> = _diagnostics.asStateFlow()

    private val _messages = MutableSharedFlow<String>(extraBufferCapacity = 8)
    val messages: SharedFlow<String> = _messages.asSharedFlow()

    init {
        viewModelScope.launch {
            CoreHost.state.first { it is CoreState.Ready }
            refresh()
        }
        // Updates are event-driven (shell-guide §4 item 1): read once, then follow update.* events.
        viewModelScope.launch {
            CoreHost.events.collect { ev ->
                when (ev) {
                    is CoreEvent.UpdateProgress -> _update.update { it.copy(state = "downloading", received = ev.received, total = ev.total) }
                    is CoreEvent.UpdateAvailable, is CoreEvent.UpdateReady, is CoreEvent.UpdateError -> refreshUpdate()
                    else -> Unit
                }
            }
        }
    }

    fun refresh() {
        viewModelScope.launch {
            (CoreApi.settings() as? ApiResult.Ok)?.let { _settings.value = it.value }
            (CoreApi.identity() as? ApiResult.Ok)?.let { _identity.value = it.value }
        }
        refreshUpdate()
    }

    fun refreshUpdate() {
        viewModelScope.launch { (CoreApi.updateStatus() as? ApiResult.Ok)?.let { _update.value = it.value } }
    }

    fun set(key: String, value: String) {
        viewModelScope.launch {
            val r = CoreApi.setSetting(key, value)
            if (r is ApiResult.Fail) _messages.tryEmit(r.message)
            (CoreApi.settings() as? ApiResult.Ok)?.let { _settings.value = it.value }
        }
    }

    fun checkUpdate() = updateCall { CoreApi.updateCheck() }
    fun downloadUpdate() = updateCall { CoreApi.updateDownload() }

    fun setAutoUpdate(on: Boolean) = updateCall { CoreApi.setUpdateAuto(on) }

    private fun updateCall(block: suspend () -> ApiResult<*>) {
        viewModelScope.launch {
            val r = block()
            if (r is ApiResult.Fail) _messages.tryEmit(r.message)
            (CoreApi.updateStatus() as? ApiResult.Ok)?.let { _update.value = it.value }
        }
    }

    fun loadDiagnostics() {
        viewModelScope.launch {
            _diagnostics.value = when (val r = CoreApi.diagnostics()) {
                is ApiResult.Ok -> r.value
                is ApiResult.Fail -> r.message
            }
        }
    }

    fun closeDiagnostics() {
        _diagnostics.value = null
    }
}
