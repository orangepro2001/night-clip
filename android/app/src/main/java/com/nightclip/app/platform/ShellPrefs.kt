package com.nightclip.app.platform

import android.content.Context
import android.content.SharedPreferences
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/** Shell-side persisted flags (the core keeps everything else). */
class ShellPrefs(context: Context) {
    private val prefs: SharedPreferences = context.getSharedPreferences("clipbridge_shell", Context.MODE_PRIVATE)

    /** The app's own on/off switch for its notifications (default on). */
    private val _notifications = MutableStateFlow(prefs.getBoolean(KEY_NOTIFICATIONS, true))
    val notifications: StateFlow<Boolean> = _notifications.asStateFlow()

    /** Vault/autofill windows are excluded from screenshots by default; developers can turn it off. */
    private val _vaultPreventCapture = MutableStateFlow(prefs.getBoolean(KEY_VAULT_PREVENT_CAPTURE, true))
    val vaultPreventCapture: StateFlow<Boolean> = _vaultPreventCapture.asStateFlow()

    /** Seconds in the background before the vault locks; 0 = never. */
    private val _vaultBackgroundLock = MutableStateFlow(prefs.getInt(KEY_VAULT_BACKGROUND_LOCK, 30))
    val vaultBackgroundLock: StateFlow<Int> = _vaultBackgroundLock.asStateFlow()

    /** Whether turning the screen off locks an open vault. */
    private val _vaultLockOnScreenOff = MutableStateFlow(prefs.getBoolean(KEY_VAULT_SCREEN_OFF_LOCK, true))
    val vaultLockOnScreenOff: StateFlow<Boolean> = _vaultLockOnScreenOff.asStateFlow()

    fun setNotifications(on: Boolean) {
        prefs.edit().putBoolean(KEY_NOTIFICATIONS, on).apply()
        _notifications.value = on
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

    val vaultPreventCaptureNow: Boolean get() = _vaultPreventCapture.value
    val vaultBackgroundLockNow: Int get() = _vaultBackgroundLock.value
    val vaultLockOnScreenOffNow: Boolean get() = _vaultLockOnScreenOff.value

    private companion object {
        const val KEY_NOTIFICATIONS = "notifications_enabled"
        const val KEY_VAULT_PREVENT_CAPTURE = "vault_prevent_capture"
        const val KEY_VAULT_BACKGROUND_LOCK = "vault_background_lock_s"
        const val KEY_VAULT_SCREEN_OFF_LOCK = "vault_lock_on_screen_off"
    }
}
