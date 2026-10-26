package com.nightclip.app.platform

import android.content.Context
import android.content.SharedPreferences
import com.nightclip.app.logic.ClipSeen
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/** Shell-side persisted flags (the core keeps everything else). */
class ShellPrefs(context: Context) {
    private val prefs: SharedPreferences = context.getSharedPreferences("clipbridge_shell", Context.MODE_PRIVATE)

    private val _realtime = MutableStateFlow(prefs.getBoolean(KEY_REALTIME, false))
    val realtime: StateFlow<Boolean> = _realtime.asStateFlow()

    private val _onboarded = MutableStateFlow(prefs.getBoolean(KEY_ONBOARDED, false))
    val onboarded: StateFlow<Boolean> = _onboarded.asStateFlow()

    /** Opt-in background clipboard read (docs/adr/006-android-background-read.md); default off. */
    private val _backgroundRead = MutableStateFlow(prefs.getBoolean(KEY_BACKGROUND_READ, false))
    val backgroundRead: StateFlow<Boolean> = _backgroundRead.asStateFlow()

    /** Vault/autofill windows are excluded from screenshots by default; developers can turn it off. */
    private val _vaultPreventCapture = MutableStateFlow(prefs.getBoolean(KEY_VAULT_PREVENT_CAPTURE, true))
    val vaultPreventCapture: StateFlow<Boolean> = _vaultPreventCapture.asStateFlow()

    /** Seconds in the background before the vault locks; 0 = never. */
    private val _vaultBackgroundLock = MutableStateFlow(prefs.getInt(KEY_VAULT_BACKGROUND_LOCK, 30))
    val vaultBackgroundLock: StateFlow<Int> = _vaultBackgroundLock.asStateFlow()

    /** Whether turning the screen off locks an open vault. */
    private val _vaultLockOnScreenOff = MutableStateFlow(prefs.getBoolean(KEY_VAULT_SCREEN_OFF_LOCK, true))
    val vaultLockOnScreenOff: StateFlow<Boolean> = _vaultLockOnScreenOff.asStateFlow()

    /** Persisted "real-time mode" choice; the service and boot receiver follow this value. */
    fun setRealtime(on: Boolean) {
        prefs.edit().putBoolean(KEY_REALTIME, on).apply()
        _realtime.value = on
    }

    fun setOnboarded(done: Boolean) {
        prefs.edit().putBoolean(KEY_ONBOARDED, done).apply()
        _onboarded.value = done
    }

    fun setBackgroundRead(on: Boolean) {
        prefs.edit().putBoolean(KEY_BACKGROUND_READ, on).apply()
        _backgroundRead.value = on
    }

    fun setVaultPreventCapture(on: Boolean) {
        prefs.edit().putBoolean(KEY_VAULT_PREVENT_CAPTURE, on).apply()
        _vaultPreventCapture.value = on
    }

    fun setVaultBackgroundLock(seconds: Int) {
        val v = seconds.coerceAtLeast(0)
        prefs.edit().putInt(KEY_VAULT_BACKGROUND_LOCK, v).apply()
        _vaultBackgroundLock.value = v
    }

    fun setVaultLockOnScreenOff(on: Boolean) {
        prefs.edit().putBoolean(KEY_VAULT_SCREEN_OFF_LOCK, on).apply()
        _vaultLockOnScreenOff.value = on
    }

    /** Transfers already copied into Downloads, so a crash before `file.release` never saves twice. */
    @Synchronized
    fun markSaved(transferId: String) {
        val set = prefs.getStringSet(KEY_SAVED, emptySet()).orEmpty().toMutableSet()
        if (set.size > 500) set.clear()
        set += transferId
        prefs.edit().putStringSet(KEY_SAVED, set).apply()
    }

    @Synchronized
    fun wasSaved(transferId: String): Boolean = prefs.getStringSet(KEY_SAVED, emptySet()).orEmpty().contains(transferId)

    /**
     * SHA-256 of the clipboard text the focus-triggered auto send last looked at (never the text itself),
     * so opening the app twice with the same clipboard sends once. Null until the first look.
     */
    private val seen = ClipSeen(prefs.getString(KEY_LAST_CLIP, null))

    @get:Synchronized
    @set:Synchronized
    var lastClipHash: String?
        get() = seen.current
        set(value) {
            seen.reset(value)
            persistClipHash(value)
        }

    /**
     * Atomically replaces [lastClipHash] only if it still equals [expected]. The focus path and the
     * background path can see the same copy; only one may send it.
     */
    @Synchronized
    fun claimClipHash(expected: String?, next: String): Boolean {
        if (!seen.claim(expected, next)) return false
        persistClipHash(seen.current)
        return true
    }

    /** Rolls a claim back only if no newer claim replaced it meanwhile. */
    @Synchronized
    fun rollbackClipHash(claimed: String, previous: String?): Boolean {
        if (!seen.rollback(claimed, previous)) return false
        persistClipHash(seen.current)
        return true
    }

    private fun persistClipHash(value: String?) {
        prefs.edit().apply { if (value == null) remove(KEY_LAST_CLIP) else putString(KEY_LAST_CLIP, value) }.apply()
    }

    val realtimeNow: Boolean get() = _realtime.value
    val vaultPreventCaptureNow: Boolean get() = _vaultPreventCapture.value
    val vaultBackgroundLockNow: Int get() = _vaultBackgroundLock.value
    val vaultLockOnScreenOffNow: Boolean get() = _vaultLockOnScreenOff.value

    private companion object {
        const val KEY_REALTIME = "realtime_mode"
        const val KEY_SAVED = "saved_transfers"
        const val KEY_ONBOARDED = "onboarded"
        const val KEY_BACKGROUND_READ = "background_read_enabled"
        const val KEY_VAULT_PREVENT_CAPTURE = "vault_prevent_capture"
        const val KEY_VAULT_BACKGROUND_LOCK = "vault_background_lock_s"
        const val KEY_VAULT_SCREEN_OFF_LOCK = "vault_lock_on_screen_off"
        const val KEY_LAST_CLIP = "last_clip_hash"
    }
}
