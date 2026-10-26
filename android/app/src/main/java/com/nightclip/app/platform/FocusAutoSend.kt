package com.nightclip.app.platform

import android.app.Activity
import com.nightclip.app.app
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.CoreApi
import com.nightclip.app.logic.ClipAutoSend
import com.nightclip.app.logic.Strings
import com.nightclip.app.toastOnMain
import kotlinx.coroutines.launch

/**
 * Layer 1 of the automatic broadcast: when one of our windows gets focus, look at the clipboard once and,
 * if it changed since the last look, send it through `clip.local_changed` (capture switch, pause, echo,
 * duplicate and size rules are the core's). Main thread only.
 */
object FocusAutoSend {
    fun onFocus(activity: Activity) {
        val app = activity.app
        val read = ClipboardAccess.readClip(activity)
        val prefs = app.prefs
        val previous = prefs.lastClipHash
        val verdict = ClipAutoSend.decide(read?.text, read?.copiedAtMs ?: 0L, System.currentTimeMillis(), previous)
        if (read == null || verdict == ClipAutoSend.Verdict.SKIP) return
        // Claim the text before the asynchronous send so a quick second focus event cannot send it twice.
        prefs.lastClipHash = ClipAutoSend.hash(read.text)
        if (verdict == ClipAutoSend.Verdict.BASELINE) return
        app.appScope.launch {
            when (val r = CoreApi.localChanged(read.text)) {
                is ApiResult.Ok -> if (r.value.sentCount > 0) app.toastOnMain(Strings.sentTo(r.value.sentCount))
                is ApiResult.Fail -> prefs.lastClipHash = previous // not looked at after all: try again next time
            }
        }
    }
}
