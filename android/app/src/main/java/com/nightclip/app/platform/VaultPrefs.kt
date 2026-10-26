package com.nightclip.app.platform

import android.content.Context
import android.content.SharedPreferences
import android.provider.Settings
import android.util.Base64
import com.nightclip.app.logic.QuickUnlockState

/**
 * Shell-side vault flags in the app's private preferences (no master password, no secret except the Keystore-wrapped
 * quick-unlock blob, which is useless without the biometric-bound key). The core keeps everything else.
 */
class VaultPrefs(context: Context) {
    private val appContext = context.applicationContext
    private val prefs: SharedPreferences = appContext.getSharedPreferences("clipbridge_vault", Context.MODE_PRIVATE)

    /** Duration of the last vault.create / vault.unlock (ms); drives the "slow phone" KDF default. null = unknown. */
    var lastKdfMs: Long?
        get() = if (prefs.contains(KEY_KDF_MS)) prefs.getLong(KEY_KDF_MS, 0) else null
        set(value) {
            prefs.edit().apply { if (value == null) remove(KEY_KDF_MS) else putLong(KEY_KDF_MS, value) }.apply()
        }

    /** The user said "not now" to the quick-unlock offer; it is only offered again from the vault settings. */
    var quickOfferDismissed: Boolean
        get() = prefs.getBoolean(KEY_OFFER_DISMISSED, false)
        set(value) {
            prefs.edit().putBoolean(KEY_OFFER_DISMISSED, value).apply()
        }

    /** Keystore-wrapped master credentials (QuickUnlock blob format), or null. */
    var quickBlob: ByteArray?
        get() = prefs.getString(KEY_BLOB, null)?.let { Base64.decode(it, Base64.NO_WRAP) }
        set(value) {
            prefs.edit().apply { if (value == null) remove(KEY_BLOB) else putString(KEY_BLOB, Base64.encodeToString(value, Base64.NO_WRAP)) }.apply()
        }

    fun quickState(): QuickUnlockState = QuickUnlockState(
        enabled = prefs.getBoolean(KEY_QUICK_ENABLED, false),
        hasSecret = prefs.contains(KEY_BLOB),
        lastMasterUnlockAtMs = prefs.getLong(KEY_LAST_MASTER, 0),
        failures = prefs.getInt(KEY_FAILURES, 0),
        bootCountAtMasterUnlock = if (prefs.contains(KEY_BOOT)) prefs.getInt(KEY_BOOT, 0) else null,
    )

    /** Persists everything of [s] except the blob itself (see [quickBlob]). */
    fun saveQuickState(s: QuickUnlockState) {
        prefs.edit().apply {
            putBoolean(KEY_QUICK_ENABLED, s.enabled)
            putLong(KEY_LAST_MASTER, s.lastMasterUnlockAtMs)
            putInt(KEY_FAILURES, s.failures)
            val boot = s.bootCountAtMasterUnlock
            if (boot == null) remove(KEY_BOOT) else putInt(KEY_BOOT, boot)
            if (!s.hasSecret) remove(KEY_BLOB)
        }.apply()
    }

    /** Settings.Global.BOOT_COUNT, or null where the system does not let us read it (the reboot rule is skipped then). */
    fun currentBootCount(): Int? = try {
        Settings.Global.getInt(appContext.contentResolver, Settings.Global.BOOT_COUNT)
    } catch (e: Exception) {
        null
    }

    private companion object {
        const val KEY_KDF_MS = "last_kdf_ms"
        const val KEY_OFFER_DISMISSED = "quick_offer_dismissed"
        const val KEY_BLOB = "quick_blob"
        const val KEY_QUICK_ENABLED = "quick_enabled"
        const val KEY_LAST_MASTER = "quick_last_master_unlock"
        const val KEY_FAILURES = "quick_failures"
        const val KEY_BOOT = "quick_boot_count"
    }
}
