package com.nightclip.app

import android.os.Bundle
import androidx.activity.ComponentActivity
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.CoreApi
import com.nightclip.app.logic.PeerTexts
import com.nightclip.app.logic.Strings
import com.nightclip.app.platform.ClipboardAccess
import kotlinx.coroutines.launch

/**
 * Invisible activity behind the Quick Settings tile and the "发送剪贴板" notification action.
 * Android 10+ lets only the focused app read the clipboard, so the read happens in
 * onWindowFocusChanged(true); the activity finishes right after the read and the send
 * continues in the application scope.
 */
class SendClipboardActivity : ComponentActivity() {
    private var handled = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // No content view: a transparent window that only exists to get focus.
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (!hasFocus || handled) return
        handled = true
        val text = ClipboardAccess.readText(this)
        val app = this.app
        if (text == null) {
            toastOnMain(Strings.CLIPBOARD_EMPTY)
        } else {
            app.appScope.launch {
                val message = when (val r = CoreApi.sendClip(text)) {
                    is ApiResult.Ok -> PeerTexts.sendOutcome(r.value)
                    is ApiResult.Fail -> r.message
                }
                app.toastOnMain(message)
            }
        }
        finish()
    }
}
