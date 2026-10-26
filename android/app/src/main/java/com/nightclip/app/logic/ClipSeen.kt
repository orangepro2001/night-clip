package com.nightclip.app.logic

/**
 * Compare-and-set for "the clipboard text we have already looked at" (SHA-256 hex, never the text).
 *
 * The focus path (opening the app) and the background path (logcat trigger) can observe the same
 * copy almost simultaneously. Both capture the previous hash, decide, then claim; only one claim
 * can win, so a copy is sent once. A failed send rolls the claim back only if nobody claimed a
 * newer hash in the meantime.
 */
class ClipSeen(initial: String?) {
    var current: String? = initial
        private set

    fun reset(value: String?) {
        current = value
    }

    fun claim(expected: String?, next: String): Boolean {
        if (current != expected) return false
        current = next
        return true
    }

    fun rollback(claimed: String, previous: String?): Boolean {
        if (current != claimed) return false
        current = previous
        return true
    }
}
