package com.nightclip.app.logic

import java.util.Locale

/**
 * Turns one logcat line into "the user copied something".
 *
 * Android 10+ ClipboardService only delivers OnPrimaryClipChangedListener callbacks to apps that
 * may read (focused app / IME / system). For every registered listener it still calls its access
 * check, and when the listener's app is not focused it logs, at E level:
 *
 *   ClipboardService: Denying clipboard access to <pkg>, application is not in focus nor is it a
 *   system service for user 0
 *
 * (Android 10 says "neither" instead of "nor".) That line is the background copy signal; the
 * listener callback itself never arrives. Keep the match strict enough that unrelated log lines
 * mentioning our package cannot trigger a focus grab.
 */
object ClipboardLogTrigger {
    fun matches(line: String, packageName: String): Boolean {
        if (line.isBlank() || packageName.isBlank()) return false
        if (!line.contains(packageName, ignoreCase = true)) return false
        val lower = line.lowercase(Locale.ROOT)
        val mentionsClipboard = lower.contains("clipboard")
        val mentionsDenial = lower.contains("deny") || lower.contains("denied") || lower.contains("denying")
        // Accept the AOSP wording and close OEM variants without matching arbitrary app logs.
        return mentionsClipboard && mentionsDenial
    }

    /**
     * One copy can produce one denial line; a burst (or the same line re-read after a logcat
     * restart) must not make us steal focus repeatedly. 800 ms is the starting value; the
     * real-device pass may tune it.
     */
    fun isDebounced(nowMs: Long, lastMs: Long, windowMs: Long = 800): Boolean =
        lastMs > 0 && nowMs - lastMs < windowMs
}
