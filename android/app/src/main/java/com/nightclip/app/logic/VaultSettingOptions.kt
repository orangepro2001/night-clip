package com.nightclip.app.logic

/** Choices offered for the vault settings and their labels (core limits: auto-lock 0 or 30–86400 s, clipboard clear 0 or 5–300 s). */
object VaultSettingOptions {
    val AUTOLOCK_SECONDS = listOf(0, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 21600, 43200, 86400)
    val CLIP_CLEAR_SECONDS = listOf(10, 20, 30, 60, 120, 300, 0)

    /** How long a biometric quick unlock stays usable after the last master-password unlock; 0 = never expires. */
    val QUICK_MAX_AGE_SECONDS = listOf(0, 300, 3600, 24 * 3600, 72 * 3600, 7 * 24 * 3600, 30 * 24 * 3600)

    fun autolockLabel(seconds: Int): String = when {
        seconds <= 0 -> "永不自动锁定"
        seconds < 60 -> "$seconds 秒"
        seconds < 3600 -> "${seconds / 60} 分钟"
        else -> "${seconds / 3600} 小时"
    }

    fun clipClearLabel(seconds: Int): String = if (seconds <= 0) "不自动清除" else "$seconds 秒"

    fun quickMaxAgeLabel(seconds: Int): String = when {
        seconds <= 0 -> "永不过期"
        seconds < 3600 -> "${seconds / 60} 分钟"
        seconds < 86400 -> "${seconds / 3600} 小时"
        else -> "${seconds / 86400} 天"
    }

    /** Whether the core accepts [seconds] for `vault_autolock_s`. */
    fun validAutolock(seconds: Int): Boolean = seconds == 0 || seconds in 30..86400

    /** Whether the core accepts [seconds] for `vault_clip_clear_s`. */
    fun validClipClear(seconds: Int): Boolean = seconds == 0 || seconds in 5..300
}
