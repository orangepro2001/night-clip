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
import cloud.moonl.password.core.PairStatus
import cloud.moonl.password.core.SyncStatus
import cloud.moonl.password.platform.VaultClipboard
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

class SettingsViewModel(application: Application) : AndroidViewModel(application) {
    private val _update = MutableStateFlow(UpdateState())
    val update: StateFlow<UpdateState> = _update.asStateFlow()

    private val _account = MutableStateFlow(AccountStatus())
    val account = _account.asStateFlow()
    private val _accountBusy = MutableStateFlow(false)
    val accountBusy = _accountBusy.asStateFlow()
    private val _sync = MutableStateFlow(SyncStatus())
    val sync = _sync.asStateFlow()
    private val _syncBusy = MutableStateFlow(false)
    val syncBusy = _syncBusy.asStateFlow()
    private val _pair = MutableStateFlow<PairStatus?>(null)
    val pair = _pair.asStateFlow()
    private var pairJob: Job? = null
    var pairRequester = false
        private set

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
                    is CoreEvent.SyncChanged -> refreshSync()
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
        refreshSync()
    }

    private fun refreshAccount() {
        viewModelScope.launch { CoreApi.accountStatus().okOrNull?.let { _account.value = it } }
    }

    private fun refreshSync() {
        viewModelScope.launch { CoreApi.syncStatus().okOrNull?.let { _sync.value = it } }
    }

    fun createSync() = runSync { CoreApi.syncEnable("create", "") }
    fun joinSync(key: String) = runSync { CoreApi.syncEnable("join", key) }
    fun syncNow() = runSync { CoreApi.syncNow() }
    fun disconnectSync() = runSync { CoreApi.syncDisconnect() }

    fun beginPair(requester: Boolean) {
        if (_pair.value != null) return
        pairRequester = requester
        _pair.value = PairStatus("starting")
        pairJob = viewModelScope.launch {
            val start = CoreApi.pairBegin(requester)
            if (acceptPair(start)) pollPair()
        }
    }

    fun approvePair(code: String) {
        if (_pair.value?.state != "compare") return
        pairJob?.cancel()
        _pair.value = PairStatus("starting")
        pairJob = viewModelScope.launch {
            if (acceptPair(CoreApi.pairApprove(code))) pollPair()
        }
    }

    fun cancelPair() {
        if (_pair.value == null) return
        val finished = _pair.value?.state in setOf("sent", "connected")
        _pair.value = null
        pairJob?.cancel()
        if (!finished) viewModelScope.launch { CoreApi.pairCancel() }
    }

    private suspend fun pollPair() {
        while (true) {
            delay(2000)
            val result = CoreApi.pairStatus()
            if (result is ApiResult.Fail && result.code == "SYNC_NETWORK") continue
            if (!acceptPair(result)) return
        }
    }

    private fun acceptPair(result: ApiResult<PairStatus>): Boolean {
        return when (result) {
            is ApiResult.Ok -> {
                _pair.value = result.value
                if (result.value.state == "connected") refreshSync()
                result.value.state in setOf("waiting", "compare")
            }
            is ApiResult.Fail -> {
                _pair.value = PairStatus("error", error = result.code)
                false
            }
        }
    }

    private fun runSync(action: suspend () -> ApiResult<SyncStatus>) {
        if (_syncBusy.value) return
        _syncBusy.value = true
        viewModelScope.launch {
            try {
                when (val result = action()) {
                    is ApiResult.Ok -> _sync.value = result.value
                    is ApiResult.Fail -> _messages.tryEmit(getApplication<MoonPasswordApp>().getString(syncErrorText(result.code)))
                }
            } finally {
                _syncBusy.value = false
            }
        }
    }

    fun copySyncKey() {
        if (_syncBusy.value) return
        _syncBusy.value = true
        viewModelScope.launch {
            try {
                val app = getApplication<MoonPasswordApp>()
                when (val result = CoreApi.syncKey()) {
                    is ApiResult.Ok -> {
                        val message = if (VaultClipboard.copy(app, result.value, 30)) R.string.sync_key_copied else R.string.toast_copy_failed
                        _messages.tryEmit(app.getString(message))
                    }
                    is ApiResult.Fail -> _messages.tryEmit(app.getString(syncErrorText(result.code)))
                }
            } finally {
                _syncBusy.value = false
            }
        }
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

internal fun syncErrorText(code: String): Int = when (code) {
    "VAULT_LOCKED" -> R.string.sync_locked
    "SYNC_SIGN_IN" -> R.string.sync_sign_in
    "SYNC_ACCOUNT" -> R.string.sync_account_mismatch
    "SYNC_EXISTS" -> R.string.sync_exists
    "SYNC_MISSING" -> R.string.sync_missing
    "SYNC_KEY" -> R.string.sync_bad_key
    "SYNC_CONFLICT" -> R.string.sync_conflict
    "SYNC_DATA" -> R.string.sync_invalid
    "PAIR_MISSING" -> R.string.pair_missing
    "PAIR_BUSY" -> R.string.pair_busy
    "PAIR_INVALID", "PAIR_STATE" -> R.string.pair_expired
    "PAIR_CONFIRM" -> R.string.pair_check_code
    else -> R.string.sync_network_error
}
