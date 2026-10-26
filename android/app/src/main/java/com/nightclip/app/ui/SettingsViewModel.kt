package com.nightclip.app.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.nightclip.app.ClipBridgeApp
import com.nightclip.app.R
import com.nightclip.app.UpdateState
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.CoreApi
import com.nightclip.app.core.CoreEvent
import com.nightclip.app.core.CoreHost
import com.nightclip.app.core.CoreSettings
import com.nightclip.app.core.CoreState
import com.nightclip.app.platform.BackgroundReadController
import com.nightclip.app.platform.BackgroundReadRuntime
import com.nightclip.app.platform.ShizukuBridge
import com.nightclip.app.platform.ShizukuRuntime
import com.nightclip.app.platform.OverlayReadResult
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

    /** Runtime status/counters for the opt-in background read, shared with the settings card. */
    val backgroundRead: StateFlow<BackgroundReadRuntime> = BackgroundReadController.state
    val shizuku: StateFlow<ShizukuRuntime> = ShizukuBridge.state

    fun refreshShizuku() {
        ShizukuBridge.refresh()
        BackgroundReadController.refresh(getApplication<Application>())
    }

    fun requestShizukuPermission() = ShizukuBridge.requestPermission()

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
        refreshBackgroundRead()
    }

    fun setBackgroundRead(on: Boolean) {
        BackgroundReadController.setEnabled(getApplication<Application>(), on)
        BackgroundReadController.refresh(getApplication<Application>())
    }

    fun refreshBackgroundRead() {
        BackgroundReadController.refresh(getApplication<Application>())
    }

    /** Vault/autofill windows are excluded from screenshots by default; developers can turn it off temporarily. */
    fun setVaultPreventCapture(on: Boolean) {
        getApplication<ClipBridgeApp>().prefs.setVaultPreventCapture(on)
    }

    /** One-shot overlay read for the settings card; never sends and never changes the baseline. */
    fun testBackgroundRead() {
        viewModelScope.launch {
            val app = getApplication<Application>()
            val text = when (val r = BackgroundReadController.testRead(app)) {
                is OverlayReadResult.Ok -> app.getString(R.string.background_test_ok)
                is OverlayReadResult.NoPermission -> app.getString(R.string.background_test_no_permission)
                is OverlayReadResult.Locked -> app.getString(R.string.background_test_locked)
                is OverlayReadResult.NoFocus -> app.getString(R.string.background_test_no_focus)
                is OverlayReadResult.Empty -> app.getString(R.string.background_test_empty)
                is OverlayReadResult.Denied -> app.getString(R.string.background_test_denied)
                is OverlayReadResult.Busy -> app.getString(R.string.background_test_busy)
                is OverlayReadResult.Failed -> app.getString(R.string.background_test_failed, r.code)
            }
            _messages.tryEmit(text)
        }
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

    /** Pauses sending and automatic writing for [minutes]; 0 resumes. */
    fun pauseFor(minutes: Int) {
        val until = if (minutes > 0) System.currentTimeMillis() / 1000 + minutes * 60L else 0L
        set("paused_until", until.toString())
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
