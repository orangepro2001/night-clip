package com.nightclip.app.logic

/**
 * Countdown of one TOTP code between two vault.totp calls. The core answers with the code and the seconds
 * it stays valid ("seconds_left"); the screen asks again when this reaches zero. Pure arithmetic on ms timestamps.
 */
class TotpClock(private val fetchedAtMs: Long, private val secondsLeftAtFetch: Int) {
    /** Whole seconds still left at [nowMs], rounded up, never negative. A clock that went backwards counts as just fetched. */
    fun remainingSeconds(nowMs: Long): Int {
        val elapsed = (nowMs - fetchedAtMs).coerceAtLeast(0)
        val leftMs = secondsLeftAtFetch * 1000L - elapsed
        if (leftMs <= 0) return 0
        return ((leftMs + 999) / 1000).toInt()
    }

    fun isExpired(nowMs: Long): Boolean = remainingSeconds(nowMs) <= 0

    /** 1.0 right after a fetch, 0.0 when expired; [periodS] is the TOTP period (see [guessPeriod]). */
    fun fraction(nowMs: Long, periodS: Int): Float {
        if (periodS <= 0) return 0f
        return (remainingSeconds(nowMs).toFloat() / periodS).coerceIn(0f, 1f)
    }

    companion object {
        /** The core does not report the period; the usual ones (RFC 6238 apps use 30, some services 60) are enough for a progress bar. */
        fun guessPeriod(secondsLeft: Int): Int = when {
            secondsLeft <= 30 -> 30
            secondsLeft <= 60 -> 60
            secondsLeft <= 120 -> 120
            else -> secondsLeft
        }

        /** "123456" -> "123 456", "12345678" -> "1234 5678"; other lengths are left alone. */
        fun format(code: String): String = when (code.length) {
            6 -> code.substring(0, 3) + " " + code.substring(3)
            8 -> code.substring(0, 4) + " " + code.substring(4)
            else -> code
        }

        /** True when the countdown should be shown as urgent. */
        fun isUrgent(remainingS: Int): Boolean = remainingS in 0..5
    }
}
