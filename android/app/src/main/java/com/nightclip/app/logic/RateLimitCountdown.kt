package com.nightclip.app.logic

/** Countdown for RATE_LIMITED (details.retry_after_s / vault.status.retry_after_s): the unlock button stays disabled until it ends. */
class RateLimitCountdown private constructor(private val untilMs: Long) {
    fun remainingSeconds(nowMs: Long): Int {
        val left = untilMs - nowMs
        return if (left <= 0) 0 else ((left + 999) / 1000).toInt()
    }

    fun isActive(nowMs: Long): Boolean = remainingSeconds(nowMs) > 0

    companion object {
        /** An inactive countdown. */
        val NONE = RateLimitCountdown(0)

        /** [retryAfterS] <= 0 gives [NONE]; absurd values are capped at the core's own limit of five minutes plus a minute of slack. */
        fun from(retryAfterS: Int, nowMs: Long): RateLimitCountdown =
            if (retryAfterS <= 0) NONE else RateLimitCountdown(nowMs + retryAfterS.coerceAtMost(360) * 1000L)

        fun label(seconds: Int): String = when {
            seconds <= 0 -> ""
            seconds < 60 -> "$seconds 秒"
            else -> "${seconds / 60} 分 ${"%02d".format(seconds % 60)} 秒"
        }
    }
}
