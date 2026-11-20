package cloud.moonl.password.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import cloud.moonl.password.MoonPasswordApp
import cloud.moonl.password.R
import cloud.moonl.password.UpdateState
import cloud.moonl.password.core.AccountStatus
import cloud.moonl.password.core.ApiResult
import cloud.moonl.password.core.CoreApi
import cloud.moonl.password.core.CoreEvent
import cloud.moonl.password.core.CoreHost
import cloud.moonl.password.core.CoreState
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
    private val _update = MutableStateFlow(UpdateState())
    val update: StateFlow<UpdateState> = _update.asStateFlow()

    private val _account = MutableStateFlow(AccountStatus())
    val account = _account.asStateFlow()
    private val _accountBusy = MutableStateFlow(false)
    val accountBusy = _accountBusy.asStateFlow()

    private val _deviceName = MutableStateFlow("")
    val deviceName: StateFlow<String> = _deviceName.asStateFlow()

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
                    is CoreEvent.AccountChanged -> refreshAccount()
                    else -> Unit
                }
            }
        }
    }

    fun refresh() {
        viewModelScope.launch {
            (CoreApi.status() as? ApiResult.Ok)?.let { _deviceName.value = it.value.deviceName }
        }
        refreshUpdate()
        refreshAccount()
    }

    private fun refreshAccount() {
        viewModelScope.launch { CoreApi.accountStatus().okOrNull?.let { _account.value = it } }
    }

    fun accountAction(openBrowser: (String) -> Boolean) {
        if (_accountBusy.value) return
        _accountBusy.value = true
        viewModelScope.launch {
            try {
                val current = _account.value
                val result = when {
                    current.state == "signed_in" || current.error == "session_storage" -> CoreApi.accountLogout()
                    current.state == "authorizing" -> CoreApi.cancelAccountLogin()
                    current.state == "signed_out" -> {
                        val login = CoreApi.startAccountLogin()
                        if (login is ApiResult.Ok && !openBrowser(login.value)) {
                            CoreApi.cancelAccountLogin()
                            _messages.tryEmit(getApplication<MoonPasswordApp>().getString(R.string.account_browser_failed))
                        }
                        login
                    }
                    else -> return@launch
                }
                if (result is ApiResult.Fail) {
                    _messages.tryEmit(getApplication<MoonPasswordApp>().getString(R.string.account_action_failed))
                }
            } finally {
                CoreApi.accountStatus().okOrNull?.let { _account.value = it }
                _accountBusy.value = false
            }
        }
    }

    /** Vault/autofill windows are excluded from screenshots by default; developers can turn it off temporarily. */
    fun setVaultPreventCapture(on: Boolean) {
        getApplication<MoonPasswordApp>().prefs.setVaultPreventCapture(on)
    }

    fun refreshUpdate() {
        viewModelScope.launch { (CoreApi.updateStatus() as? ApiResult.Ok)?.let { _update.value = it.value } }
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
