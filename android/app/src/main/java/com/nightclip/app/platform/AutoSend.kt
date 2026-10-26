package com.nightclip.app.platform

import com.nightclip.app.ClipBridgeApp
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.CoreApi
import com.nightclip.app.logic.ClipAutoSend
import com.nightclip.app.logic.Strings
import com.nightclip.app.toastOnMain
import kotlinx.coroutines.launch

/**
 * The one "clipboard text was observed" path shared by the focus layer and the background layer.
 * Owns the claim-before-send rule so the two layers cannot both send the same copy.
 *
 * @return true when a send was started (the core may still suppress it by policy); false for
 * skipped/baseline/duplicate observations.
 */
object AutoSend {
    fun handle(app: ClipBridgeApp, text: String, copiedAtMs: Long, notify: Boolean): Boolean {
        val prefs = app.prefs
        val previous = prefs.lastClipHash
        val verdict = ClipAutoSend.decide(text, copiedAtMs, System.currentTimeMillis(), previous)
        if (verdict == ClipAutoSend.Verdict.SKIP) return false

        val next = ClipAutoSend.hash(text)
        // Claim before the asynchronous send: another focus/background observation may be racing us.
        if (!prefs.claimClipHash(previous, next)) return false
        if (verdict == ClipAutoSend.Verdict.BASELINE) return false

        app.appScope.launch {
            when (val r = CoreApi.localChanged(text)) {
                is ApiResult.Ok -> if (notify && r.value.sentCount > 0) app.toastOnMain(Strings.sentTo(r.value.sentCount))
                is ApiResult.Fail -> prefs.rollbackClipHash(next, previous) // not looked at after all: retry next time
            }
        }
        return true
    }
}
