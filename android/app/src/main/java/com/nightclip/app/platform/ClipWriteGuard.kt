package com.nightclip.app.platform

import android.content.Context
import com.nightclip.app.app
import com.nightclip.app.logic.ClipAutoSend

/**
 * Suppresses the background-read trigger for clipboard writes made by this app itself.
 *
 * Writing the clipboard sets it and (when we are not focused) makes ClipboardService log a denial
 * line for our registered listener, exactly like a user copy. Without this guard the app could
 * read back its own write, steal focus again, and — for content that was never marked as applied —
 * send it back as if the user had copied it. The window only needs to cover the trigger latency.
 */
object ClipWriteGuard {
    @Volatile private var lastWriteAtMs = 0L

    /** Records the write and marks the text as seen so the focus/background paths skip it. */
    fun noteWrite(context: Context, text: String) {
        lastWriteAtMs = System.currentTimeMillis()
        runCatching {
            // Hash only; the clipboard text itself never leaves ClipboardAccess.
            context.app.prefs.lastClipHash = ClipAutoSend.hash(text)
        }
    }

    fun isRecent(nowMs: Long = System.currentTimeMillis(), windowMs: Long = 2000): Boolean =
        shouldIgnore(nowMs, lastWriteAtMs, windowMs)

    /** Pure predicate so the timing rule can be unit-tested. */
    fun shouldIgnore(nowMs: Long, lastWriteAtMs: Long, windowMs: Long = 2000): Boolean =
        lastWriteAtMs > 0 && nowMs - lastWriteAtMs in 0 until windowMs
}
