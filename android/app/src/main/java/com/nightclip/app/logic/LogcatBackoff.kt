package com.nightclip.app.logic

/**
 * Restart policy for the logcat child process. A reader that dies immediately (permission revoked,
 * ROM restriction) must not spin: back off, then give up and let the UI show the error. A line read
 * successfully resets the failure count.
 */
object LogcatBackoff {
    fun delayMs(failures: Int): Long = when {
        failures <= 0 -> 0L
        failures == 1 -> 1_000L
        failures == 2 -> 2_000L
        failures == 3 -> 5_000L
        failures == 4 -> 10_000L
        else -> 30_000L
    }

    fun shouldStop(failures: Int): Boolean = failures >= 5
}
