package com.nightclip.app.logic

import java.security.MessageDigest

/**
 * Decides what the focus-triggered auto send does with the clipboard it just read
 * (docs/plan-auto-broadcast.md §4.4, layer 1). Android only lets the focused app read the clipboard,
 * so "the user copied something" is noticed when one of our windows gets focus.
 */
object ClipAutoSend {
    /** A clip older than this was not copied "just now"; it is looked at once but never sent on its own. */
    const val MAX_AGE_MS = 5 * 60 * 1000L

    enum class Verdict {
        /** Nothing to do (empty, or the same text we already looked at). */
        SKIP,

        /** Remember it as seen but do not send: the first look ever, or a clip that is too old to be "just copied". */
        BASELINE,

        /** Hand it to the core (clip.local_changed). */
        SEND,
    }

    fun hash(text: String): String =
        MessageDigest.getInstance("SHA-256").digest(text.toByteArray(Charsets.UTF_8)).joinToString("") { "%02x".format(it) }

    /**
     * @param lastHash hash of the text looked at last time; null when never looked
     * @param copiedAtMs when the clip was copied, 0 when the platform does not say
     */
    fun decide(text: String?, copiedAtMs: Long, nowMs: Long, lastHash: String?): Verdict = when {
        text.isNullOrBlank() -> Verdict.SKIP
        lastHash == null -> Verdict.BASELINE
        hash(text) == lastHash -> Verdict.SKIP
        copiedAtMs > 0 && nowMs - copiedAtMs > MAX_AGE_MS -> Verdict.BASELINE
        else -> Verdict.SEND
    }
}
