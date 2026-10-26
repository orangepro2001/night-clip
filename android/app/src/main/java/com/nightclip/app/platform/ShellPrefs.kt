package com.nightclip.app.platform

import android.content.Context
import android.content.SharedPreferences
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

    /** Persisted "real-time mode" choice; the service and boot receiver follow this value. */
    fun setRealtime(on: Boolean) {
        prefs.edit().putBoolean(KEY_REALTIME, on).apply()
        _realtime.value = on
    }

    fun setOnboarded(done: Boolean) {
        prefs.edit().putBoolean(KEY_ONBOARDED, done).apply()
        _onboarded.value = done
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
    var lastClipHash: String?
        get() = prefs.getString(KEY_LAST_CLIP, null)
        set(value) {
            prefs.edit().apply { if (value == null) remove(KEY_LAST_CLIP) else putString(KEY_LAST_CLIP, value) }.apply()
        }

    val realtimeNow: Boolean get() = _realtime.value

    private companion object {
        const val KEY_REALTIME = "realtime_mode"
        const val KEY_SAVED = "saved_transfers"
        const val KEY_ONBOARDED = "onboarded"
        const val KEY_LAST_CLIP = "last_clip_hash"
    }
}
