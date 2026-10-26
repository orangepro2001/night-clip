package com.nightclip.app.ui.vault

import androidx.fragment.app.FragmentActivity
import android.app.Application
import androidx.lifecycle.viewModelScope
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.CoreHost
import com.nightclip.app.core.CoreState
import com.nightclip.app.core.PasswordStrength
import com.nightclip.app.core.VaultApi
import com.nightclip.app.core.VaultCreds
import com.nightclip.app.core.VaultStatus
import com.nightclip.app.logic.KdfChoice
import com.nightclip.app.logic.KdfLevel
import com.nightclip.app.logic.QuickUnlockAvailability
import com.nightclip.app.logic.QuickUnlockPolicy
import com.nightclip.app.logic.RateLimitCountdown
import com.nightclip.app.logic.VaultErrors
import com.nightclip.app.platform.QuickUnlock
import com.nightclip.app.platform.VaultPrefs
import com.nightclip.app.platform.VaultSession
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** Lock state, unlock / create, quick unlock, navigation inside the tab and the vault settings. */
class VaultViewModel(application: Application) : VaultBaseViewModel(application) {
    private val prefs = VaultPrefs(application)
    private val quick = QuickUnlock(prefs)

    private val _phase = MutableStateFlow(VaultPhase.LOADING)
    val phase: StateFlow<VaultPhase> = _phase.asStateFlow()

    private val _status = MutableStateFlow<VaultStatus?>(null)
    val status: StateFlow<VaultStatus?> = _status.asStateFlow()

    private val _busy = MutableStateFlow(false)
    val busy: StateFlow<Boolean> = _busy.asStateFlow()

    private val _unlockError = MutableStateFlow<String?>(null)
    val unlockError: StateFlow<String?> = _unlockError.asStateFlow()

    private val _rateLimit = MutableStateFlow(RateLimitCountdown.NONE)
    val rateLimit: StateFlow<RateLimitCountdown> = _rateLimit.asStateFlow()

    private val _quickAvailability = MutableStateFlow(QuickUnlockAvailability.DISABLED)
    val quickAvailability: StateFlow<QuickUnlockAvailability> = _quickAvailability.asStateFlow()

    /** Why quick unlock is not offered although it was enabled (72 hours, restart, failures); null otherwise. */
    private val _quickNotice = MutableStateFlow<String?>(null)
    val quickNotice: StateFlow<String?> = _quickNotice.asStateFlow()

    /** True while the "enable quick unlock?" question is on screen (after a master-password unlock). */
    private val _offerQuick = MutableStateFlow(false)
    val offerQuick: StateFlow<Boolean> = _offerQuick.asStateFlow()
    private var offerCreds: VaultCreds? = null

    /** Shown once after the vault was created: "make an encrypted backup now". */
    private val _exportNudge = MutableStateFlow(false)
    val exportNudge: StateFlow<Boolean> = _exportNudge.asStateFlow()

    private val _route = MutableStateFlow<List<VaultRoute>>(emptyList())
    val route: StateFlow<List<VaultRoute>> = _route.asStateFlow()

    /** The KDF preset the create form starts with (low on phones where the last create/unlock was slow). */
    val suggestedKdf: KdfLevel get() = KdfChoice.default(prefs.lastKdfMs)
    val slowNotice: String? get() = KdfChoice.slowDeviceNotice(prefs.lastKdfMs)

    init {
        viewModelScope.launch {
            awaitCore()
            refresh()
        }
        // The core's own idle lock reaches the screens through VaultSession.locks (base class -> wipe()).
    }

    override fun wipe() {
        offerCreds = null
        _offerQuick.value = false
        _exportNudge.value = false
        _route.value = emptyList()
        _unlockError.value = null
        _phase.value = VaultPhase.LOCKED
        refreshQuick()
        viewModelScope.launch { refresh() }
    }

    // ---- status --------------------------------------------------------------------------------

    fun refresh() {
        viewModelScope.launch {
            if (CoreHost.state.value !is CoreState.Ready) return@launch
            when (val r = VaultApi.status()) {
                is ApiResult.Ok -> applyStatus(r.value)
                is ApiResult.Fail -> if (_phase.value == VaultPhase.LOADING) say(VaultErrors.message(r.code, r.retryAfterS))
            }
        }
    }

    private fun applyStatus(s: VaultStatus) {
        _status.value = s
        _rateLimit.value = RateLimitCountdown.from(s.retryAfterS, System.currentTimeMillis())
        val next = when {
            !s.exists -> VaultPhase.NO_VAULT
            s.unlocked -> VaultPhase.UNLOCKED
            else -> VaultPhase.LOCKED
        }
        if (next == VaultPhase.UNLOCKED) VaultSession.markUnlocked()
        if (next != VaultPhase.UNLOCKED) _route.value = emptyList()
        _phase.value = next
        refreshQuick()
    }

    private fun refreshQuick() {
        val a = QuickUnlockPolicy.check(prefs.quickState(), System.currentTimeMillis(), prefs.currentBootCount())
        if (a.shouldPurge) {
            purgeQuick()
            _quickNotice.value = a.explanation
            return
        }
        _quickAvailability.value = a
    }

    // ---- unlock / create -----------------------------------------------------------------------

    /** The caller clears its password field right after this call. */
    fun unlock(password: String, keyFile: ByteArray?) {
        if (_busy.value) return
        val creds = VaultCreds(password, keyFile)
        viewModelScope.launch {
            _busy.value = true
            _unlockError.value = null
            try {
                when (val r = VaultApi.unlock(creds)) {
                    is ApiResult.Ok -> onMasterUnlocked(r.value, creds)
                    is ApiResult.Fail -> onUnlockFailed(r)
                }
            } finally {
                _busy.value = false
            }
        }
    }

    private fun onUnlockFailed(r: ApiResult.Fail) {
        _unlockError.value = VaultErrors.message(r.code, r.retryAfterS, VaultErrors.Context.UNLOCK)
        if (r.code == "RATE_LIMITED") _rateLimit.value = RateLimitCountdown.from(r.retryAfterS, System.currentTimeMillis())
        if (QuickUnlockPolicy.invalidatesOn(r.code)) {
            purgeQuick() // a wrong master password voids any stored quick-unlock secret
            refresh() // picks up a new retry_after_s
        }
    }

    private fun onMasterUnlocked(ms: Long, creds: VaultCreds) {
        prefs.lastKdfMs = ms
        _quickNotice.value = null
        val st = QuickUnlockPolicy.afterMasterUnlock(prefs.quickState(), System.currentTimeMillis(), prefs.currentBootCount())
        prefs.saveQuickState(st)
        VaultSession.markUnlocked()
        _phase.value = VaultPhase.UNLOCKED
        _rateLimit.value = RateLimitCountdown.NONE
        refresh()
        if (!st.enabled && !prefs.quickOfferDismissed) {
            offerCreds = creds
            _offerQuick.value = true
        }
    }

    fun create(password: String, keyFile: ByteArray?, kdf: KdfLevel) {
        if (_busy.value) return
        val creds = VaultCreds(password, keyFile)
        viewModelScope.launch {
            _busy.value = true
            try {
                when (val r = VaultApi.create(creds, kdf.apiName)) {
                    is ApiResult.Ok -> {
                        prefs.lastKdfMs = r.value
                        prefs.saveQuickState(QuickUnlockPolicy.afterMasterUnlock(prefs.quickState(), System.currentTimeMillis(), prefs.currentBootCount()))
                        VaultSession.markUnlocked()
                        _phase.value = VaultPhase.UNLOCKED
                        _exportNudge.value = true
                        refresh()
                        if (!prefs.quickOfferDismissed) {
                            offerCreds = creds
                            _offerQuick.value = true
                        }
                    }
                    is ApiResult.Fail -> fail(r, VaultErrors.Context.CREATE)
                }
            } finally {
                _busy.value = false
            }
        }
    }

    fun lock() {
        VaultSession.lockNow()
    }

    suspend fun strength(password: String): PasswordStrength? = VaultApi.strength(password).okOrNull

    // ---- quick unlock --------------------------------------------------------------------------

    fun quickUnlock(activity: FragmentActivity) {
        if (_busy.value) return
        refreshQuick()
        if (_quickAvailability.value != QuickUnlockAvailability.AVAILABLE) return
        _unlockError.value = null
        quick.unlock(activity) { result ->
            when (result) {
                is QuickUnlock.Result.Secret -> viewModelScope.launch { finishQuickUnlock(result.creds) }
                QuickUnlock.Result.Cancelled -> Unit
                QuickUnlock.Result.Invalidated -> {
                    purgeQuick()
                    _unlockError.value = "快速解锁已失效（指纹有变动），请输入主密码。"
                }
                is QuickUnlock.Result.Failed -> {
                    prefs.saveQuickState(QuickUnlockPolicy.afterQuickFailure(prefs.quickState()))
                    refreshQuick()
                    _unlockError.value = result.message
                }
            }
        }
    }

    private suspend fun finishQuickUnlock(creds: VaultCreds) {
        _busy.value = true
        try {
            when (val r = VaultApi.unlock(creds)) {
                is ApiResult.Ok -> {
                    prefs.lastKdfMs = r.value
                    prefs.saveQuickState(QuickUnlockPolicy.afterQuickSuccess(prefs.quickState()))
                    VaultSession.markUnlocked()
                    _phase.value = VaultPhase.UNLOCKED
                    refresh()
                }
                is ApiResult.Fail -> {
                    if (QuickUnlockPolicy.invalidatesOn(r.code)) {
                        purgeQuick()
                    } else if (r.code != "RATE_LIMITED") {
                        prefs.saveQuickState(QuickUnlockPolicy.afterQuickFailure(prefs.quickState()))
                    }
                    onUnlockFailed(r)
                    refreshQuick()
                }
            }
        } finally {
            _busy.value = false
        }
    }

    /** The user said yes to the offer shown after unlocking. */
    fun acceptQuickOffer(activity: FragmentActivity) {
        val creds = offerCreds
        _offerQuick.value = false
        offerCreds = null
        if (creds != null) enrollQuick(activity, creds)
    }

    /** Drops the offer without remembering an answer (the phone cannot do it right now). */
    fun skipQuickOffer() {
        offerCreds = null
        _offerQuick.value = false
    }

    fun declineQuickOffer() {
        prefs.quickOfferDismissed = true
        offerCreds = null
        _offerQuick.value = false
    }

    private fun enrollQuick(activity: FragmentActivity, creds: VaultCreds) {
        quick.enroll(activity, creds) { result ->
            when (result) {
                QuickUnlock.EnrollResult.Done -> {
                    prefs.saveQuickState(QuickUnlockPolicy.afterEnable(prefs.quickState()))
                    refreshQuick()
                    say("已启用快速解锁")
                }
                QuickUnlock.EnrollResult.Cancelled -> Unit
                is QuickUnlock.EnrollResult.Failed -> say(result.message)
            }
        }
    }

    /** Settings switch on: the master password is checked with the core first, then wrapped. */
    fun enableQuickFromSettings(activity: FragmentActivity, password: String, keyFile: ByteArray?) {
        val creds = VaultCreds(password, keyFile)
        viewModelScope.launch {
            when (val r = VaultApi.verifyMaster(creds)) {
                is ApiResult.Ok -> enrollQuick(activity, creds)
                is ApiResult.Fail -> fail(r, VaultErrors.Context.UNLOCK)
            }
        }
    }

    fun disableQuick() {
        purgeQuick()
        say("已关闭快速解锁")
    }

    private fun purgeQuick() {
        quick.purge()
        prefs.saveQuickState(QuickUnlockPolicy.afterInvalidate(prefs.quickState()))
        _quickAvailability.value = QuickUnlockAvailability.DISABLED
    }

    fun quickAvailableOnDevice(activity: FragmentActivity): Boolean = quick.available(activity)


    // ---- navigation ----------------------------------------------------------------------------

    fun push(r: VaultRoute) {
        _route.update { it + r }
    }

    fun pop() {
        _route.update { if (it.isEmpty()) it else it.dropLast(1) }
    }

    fun replaceTop(r: VaultRoute) {
        _route.update { if (it.isEmpty()) listOf(r) else it.dropLast(1) + r }
    }

    fun popToList() {
        _route.value = emptyList()
    }

    fun dismissExportNudge() {
        _exportNudge.value = false
    }

    // ---- settings, master password ------------------------------------------------------------

    fun setAutolock(seconds: Int) {
        viewModelScope.launch {
            when (val r = VaultApi.setAutolock(seconds)) {
                is ApiResult.Ok -> refresh()
                is ApiResult.Fail -> say(VaultErrors.message(r.code, r.retryAfterS))
            }
        }
    }

    fun setClipClear(seconds: Int) {
        viewModelScope.launch {
            when (val r = VaultApi.setClipClear(seconds)) {
                is ApiResult.Ok -> refresh()
                is ApiResult.Fail -> say(VaultErrors.message(r.code, r.retryAfterS))
            }
        }
    }

    /** Success pops the screen and drops the quick-unlock secret (it holds the OLD password). */
    fun changeMaster(current: VaultCreds, new: VaultCreds) {
        if (_busy.value) return
        viewModelScope.launch {
            _busy.value = true
            try {
                when (val r = VaultApi.changeMaster(current, new)) {
                    is ApiResult.Ok -> {
                        purgeQuick()
                        prefs.saveQuickState(QuickUnlockPolicy.afterMasterUnlock(prefs.quickState(), System.currentTimeMillis(), prefs.currentBootCount()))
                        prefs.quickOfferDismissed = false
                        say("主密码已更换。旧的备份仍需要旧主密码才能打开。")
                        popToList()
                        _exportNudge.value = true
                        refresh()
                    }
                    is ApiResult.Fail -> fail(r, VaultErrors.Context.UNLOCK)
                }
            } finally {
                _busy.value = false
            }
        }
    }

    /** Re-asks the core whether the master credentials are right (used before dangerous actions in other screens). */
    suspend fun verifyMaster(creds: VaultCreds): Boolean = when (val r = VaultApi.verifyMaster(creds)) {
        is ApiResult.Ok -> true
        is ApiResult.Fail -> {
            fail(r, VaultErrors.Context.UNLOCK)
            false
        }
    }
}
