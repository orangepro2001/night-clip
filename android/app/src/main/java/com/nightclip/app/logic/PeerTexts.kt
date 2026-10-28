package com.nightclip.app.logic

import com.nightclip.app.core.PeerInfo
import com.nightclip.app.core.SendResult
import com.nightclip.app.core.TransferInfo
import com.nightclip.app.core.ErrorMessages

/** Label mapping for states and results. Pure Kotlin. */
object PeerTexts {
    fun stateLabel(p: PeerInfo): String = when (p.state) {
        "connected" -> Strings.STATE_CONNECTED
        "connecting" -> Strings.STATE_CONNECTING
        "offline" -> Strings.STATE_OFFLINE + if (p.outboxPending > 0) "（${Strings.queued(p.outboxPending)}）" else ""
        "unpaired" -> Strings.STATE_UNPAIRED
        "revoked" -> Strings.STATE_REVOKED
        else -> Strings.stateUnknown(p.state)
    }

    fun applyModeLabel(mode: String): String = when (mode) {
        "manual" -> Strings.MODE_MANUAL
        "auto" -> Strings.MODE_AUTO
        else -> Strings.MODE_NOTIFY
    }

    fun inboxStateLabel(state: String): String = when (state) {
        "received" -> Strings.INBOX_RECEIVED
        "applied" -> Strings.INBOX_APPLIED
        "ignored" -> Strings.INBOX_IGNORED
        "expired" -> Strings.INBOX_EXPIRED
        "error" -> Strings.INBOX_ERROR
        else -> state
    }

    val APPLY_MODES = listOf("notify", "manual", "auto")

    fun platformLabel(platform: String): String = when (platform) {
        "windows" -> "Windows"
        "android" -> "Android"
        "macos" -> "macOS"
        "" -> ""
        else -> platform
    }

    fun transferStateLabel(t: TransferInfo): String = when (t.state) {
        "hashing" -> Strings.TR_HASHING
        "offered" -> if (t.incoming) Strings.TR_OFFERED_IN else Strings.TR_OFFERED
        "accepted" -> Strings.TR_ACCEPTED
        "transferring" -> Strings.TR_TRANSFERRING
        "done" -> Strings.TR_DONE
        "rejected" -> Strings.TR_REJECTED
        "cancelled" -> Strings.TR_CANCELLED
        "expired" -> Strings.TR_EXPIRED
        "failed" -> if (t.error.isEmpty()) Strings.TR_FAILED else Strings.TR_FAILED + "：" + ErrorMessages.forCode(t.error)
        else -> t.state
    }

    /** Snackbar text after clip.send / clip.local_changed. */
    fun sendOutcome(r: SendResult): String = when {
        r.sentCount > 0 -> Strings.sentTo(r.sentCount)
        r.suppressed == "capture_off" -> Strings.SUPPRESSED_CAPTURE_OFF
        r.suppressed == "empty" -> Strings.SUPPRESSED_EMPTY
        r.suppressed == "echo" -> Strings.SUPPRESSED_ECHO
        r.suppressed == "duplicate" -> Strings.SUPPRESSED_DUPLICATE
        r.suppressed == "no_peers" -> Strings.SUPPRESSED_NO_PEERS
        r.suppressed == "too_large" -> Strings.SUPPRESSED_TOO_LARGE
        r.suppressed == "sensitive_blocked" -> Strings.SUPPRESSED_SENSITIVE
        r.suppressed.isNotEmpty() -> Strings.suppressedUnknown(r.suppressed)
        else -> Strings.SEND_NOTHING
    }
}
