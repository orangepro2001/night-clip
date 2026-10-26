package com.nightclip.app.logic

/** Choices offered for the two vault settings and their labels (core limits: auto-lock 30–86400 s, clipboard clear 0 or 5–300 s). */
object VaultSettingOptions {
    val AUTOLOCK_SECONDS = listOf(30, 60, 120, 300, 600, 900, 1800, 3600)
    val CLIP_CLEAR_SECONDS = listOf(10, 20, 30, 60, 120, 0)

    fun autolockLabel(seconds: Int): String = when {
        seconds < 60 -> "$seconds 秒"
        seconds < 3600 -> "${seconds / 60} 分钟"
        else -> "${seconds / 3600} 小时"
    }

    fun clipClearLabel(seconds: Int): String = if (seconds <= 0) "不自动清除" else "$seconds 秒"

    /** Whether the core accepts [seconds] for `vault_autolock_s`. */
    fun validAutolock(seconds: Int): Boolean = seconds in 30..86400

    /** Whether the core accepts [seconds] for `vault_clip_clear_s`. */
    fun validClipClear(seconds: Int): Boolean = seconds == 0 || seconds in 5..300
}
