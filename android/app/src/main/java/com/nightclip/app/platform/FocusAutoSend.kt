package com.nightclip.app.platform

import android.app.Activity
import com.nightclip.app.app

/**
 * Layer 1 of the automatic broadcast: when one of our windows gets focus, look at the clipboard
 * once and, if it changed since the last look, send it through `clip.local_changed` (capture
 * switch, pause, echo, duplicate and size rules are the core's). Main thread only.
 *
 * Layer 2 (background) uses the same [AutoSend] path; the claim in ShellPrefs keeps the two from
 * sending the same copy twice.
 */
object FocusAutoSend {
    fun onFocus(activity: Activity) {
        val read = ClipboardAccess.readClip(activity) ?: return
        AutoSend.handle(activity.app, read.text, read.copiedAtMs, notify = true)
    }
}
