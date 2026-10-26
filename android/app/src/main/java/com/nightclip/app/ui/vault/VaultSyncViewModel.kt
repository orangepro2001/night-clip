package com.nightclip.app.ui.vault

import android.app.Application
import androidx.lifecycle.viewModelScope
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.SyncPeer
import com.nightclip.app.core.VaultApi
import com.nightclip.app.core.VaultCreds
import com.nightclip.app.logic.RateLimitCountdown
import com.nightclip.app.logic.VaultErrors
import com.nightclip.app.logic.VaultSyncLogic
import com.nightclip.app.platform.VaultSession
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.drop
import kotlinx.coroutines.launch

/** Which dialog of the sync screen is open: the password of the other device's vault, or the approve/ignore question. */
sealed interface SyncDialog {
    val deviceId: String

    data class Password(override val deviceId: String) : SyncDialog
    data class Review(override val deviceId: String) : SyncDialog
}

/** Device sync screen state. Works while the vault is locked (status, switches, "sync now"); resolving needs it unlocked. */
class VaultSyncViewModel(application: Application) : VaultBaseViewModel(application) {
    private val _peers = MutableStateFlow<List<SyncPeer>>(emptyList())
    val peers: StateFlow<List<SyncPeer>> = _peers.asStateFlow()

    private val _vaultExists = MutableStateFlow(false)
    val vaultExists: StateFlow<Boolean> = _vaultExists.asStateFlow()

    private val _loaded = MutableStateFlow(false)
    val loaded: StateFlow<Boolean> = _loaded.asStateFlow()

    private val _busy = MutableStateFlow(false)
    val busy: StateFlow<Boolean> = _busy.asStateFlow()

    private val _dialog = MutableStateFlow<SyncDialog?>(null)
    val dialog: StateFlow<SyncDialog?> = _dialog.asStateFlow()

    /** Error shown inside the password dialog (it stays open on a wrong password). */
    private val _dialogError = MutableStateFlow<String?>(null)
    val dialogError: StateFlow<String?> = _dialogError.asStateFlow()

    private val _rateLimit = MutableStateFlow(RateLimitCountdown.NONE)
    val rateLimit: StateFlow<RateLimitCountdown> = _rateLimit.asStateFlow()

    init {
        viewModelScope.launch { VaultSession.syncVersion.drop(1).collect { refresh() } }
    }

    override fun wipe() {
        _dialog.value = null
        _dialogError.value = null
        _rateLimit.value = RateLimitCountdown.NONE
    }

    fun refresh() {
        viewModelScope.launch {
            awaitCore()
            when (val r = VaultApi.syncStatus()) {
                is ApiResult.Ok -> {
                    _peers.value = VaultSyncLogic.sorted(r.value.peers)
                    _vaultExists.value = r.value.vaultExists
                    _loaded.value = true
                    VaultSession.setAttention(VaultSyncLogic.attentionIds(r.value.peers))
                }
                is ApiResult.Fail -> fail(r)
            }
        }
    }

    fun setEnabled(deviceId: String, enabled: Boolean) {
        viewModelScope.launch {
            when (val r = VaultApi.syncEnable(deviceId, enabled)) {
                is ApiResult.Ok -> {
                    if (!enabled && _dialog.value?.deviceId == deviceId) _dialog.value = null
                    refresh()
                }
                is ApiResult.Fail -> fail(r)
            }
        }
    }

    fun syncNow() {
        viewModelScope.launch {
            when (val r = VaultApi.syncNow()) {
                is ApiResult.Ok -> {
                    say("已请求同步")
                    refresh()
                }
                is ApiResult.Fail -> fail(r)
            }
        }
    }

    fun openDialog(peer: SyncPeer) {
        _dialogError.value = null
        _dialog.value = when (peer.state) {
            VaultSyncLogic.STATE_NEEDS_PASSWORD -> SyncDialog.Password(peer.deviceId)
            VaultSyncLogic.STATE_REVIEW -> SyncDialog.Review(peer.deviceId)
            else -> null
        }
    }

    /** "忽略": leaves everything as it is; the request stays and is shown again. */
    fun closeDialog() {
        _dialog.value = null
        _dialogError.value = null
    }

    /** The other device's current master password. A wrong one keeps the dialog open. */
    fun resolveWithPassword(deviceId: String, creds: VaultCreds) {
        if (_busy.value) return
        viewModelScope.launch {
            _busy.value = true
            _dialogError.value = null
            try {
                when (val r = VaultApi.syncResolve(deviceId, false, creds)) {
                    is ApiResult.Ok -> {
                        _dialog.value = null
                        say("已用对方的主密码合并")
                        refresh()
                    }
                    is ApiResult.Fail -> {
                        if (VaultErrors.meansLocked(r.code)) {
                            _dialog.value = null
                            fail(r)
                        } else {
                            if (r.code == "RATE_LIMITED") _rateLimit.value = RateLimitCountdown.from(r.retryAfterS, System.currentTimeMillis())
                            _dialogError.value = VaultErrors.message(r.code, r.retryAfterS, VaultErrors.Context.SYNC)
                        }
                    }
                }
            } finally {
                _busy.value = false
            }
        }
    }

    fun approve(deviceId: String) {
        if (_busy.value) return
        viewModelScope.launch {
            _busy.value = true
            try {
                when (val r = VaultApi.syncResolve(deviceId, true, null)) {
                    is ApiResult.Ok -> {
                        _dialog.value = null
                        say("已批准并合并")
                        refresh()
                    }
                    is ApiResult.Fail -> {
                        _dialog.value = null
                        fail(r, VaultErrors.Context.SYNC)
                    }
                }
            } finally {
                _busy.value = false
            }
        }
    }
}
