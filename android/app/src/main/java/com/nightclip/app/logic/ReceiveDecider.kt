package com.nightclip.app.logic

import com.nightclip.app.core.CoreEvent

/**
 * "Event -> action" decision for `clip.received` (shell-guide §4 item 6). The core already
 * guarantees decision != auto for sensitive text; this repeats the check on purpose.
 */
object ReceiveDecider {
    enum class Action {
        /** Write the text to the clipboard now (app visible) and call clip.mark_applied. */
        WRITE_NOW,

        /** Post a notification with Copy / Dismiss. */
        NOTIFY,
    }

    /**
     * @property degraded the peer asked for auto-apply but the shell could not do it
     * (app in the background or the item is sensitive), so the user is notified instead.
     */
    data class Outcome(val action: Action, val degraded: Boolean)

    fun decide(event: CoreEvent.ClipReceived, appInForeground: Boolean): Outcome = when {
        event.decision != "auto" -> Outcome(Action.NOTIFY, degraded = false) // notify / manual / unknown
        event.sensitive -> Outcome(Action.NOTIFY, degraded = true)
        !appInForeground -> Outcome(Action.NOTIFY, degraded = true)
        else -> Outcome(Action.WRITE_NOW, degraded = false)
    }
}
