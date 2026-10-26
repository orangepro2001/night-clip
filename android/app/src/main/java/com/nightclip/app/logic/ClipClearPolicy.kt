package com.nightclip.app.logic

import java.security.MessageDigest

/**
 * When a copied vault secret should be removed from the clipboard again (docs/vault-api.md §4 rule 1).
 *
 * The shell remembers only the SHA-256 of what it wrote and a fixed marker label on the ClipData. At the moment the
 * timer fires it takes a [Snapshot] of the clipboard and asks [decide]. What can be read depends on Android:
 * with window focus the text is readable; without focus (background) Android 10+ hides the content and usually the
 * description too, so the snapshot is empty. Pure logic, no Android classes.
 */
object ClipClearPolicy {
    /** Label of the ClipData the vault writes. Anything with this label is ours. */
    const val MARKER_LABEL = "ClipBridge vault"

    /** What a read of the primary clip returned. [text] is null when the content was not readable or empty; [label] likewise. */
    class Snapshot(val text: String?, val label: String?) {
        override fun toString() = "Snapshot(hasText=${text != null}, label=$label)" // never print the clipboard text
    }

    enum class Decision {
        /** The clipboard still holds what we wrote: clear it. */
        CLEAR,

        /** The user copied something else since: leave it alone. */
        KEEP,

        /**
         * Nothing could be read (no focus, or already empty). Clearing is harmless if it is empty and the right thing if our
         * secret is still there; the only cost is that something the user copied elsewhere in the meantime is cleared too.
         */
        CLEAR_UNVERIFIED,
    }

    fun hash(text: String): String =
        MessageDigest.getInstance("SHA-256").digest(text.toByteArray(Charsets.UTF_8)).joinToString("") { "%02x".format(it) }

    fun decide(snapshot: Snapshot, writtenHash: String): Decision {
        val text = snapshot.text
        if (text != null) return if (hash(text) == writtenHash) Decision.CLEAR else Decision.KEEP
        val label = snapshot.label
        return when {
            label == MARKER_LABEL -> Decision.CLEAR // readable description, still ours
            label != null -> Decision.KEEP // readable description of somebody else's clip
            else -> Decision.CLEAR_UNVERIFIED
        }
    }

    /** Delay before clearing; null when the core said 0 (never clear). Values above one hour are capped. */
    fun delayMs(clearAfterS: Int): Long? = if (clearAfterS <= 0) null else clearAfterS.coerceAtMost(3600) * 1000L
}
