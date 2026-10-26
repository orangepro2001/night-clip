package com.nightclip.app

import android.os.Bundle
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.lifecycle.lifecycleScope
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.CoreApi
import com.nightclip.app.core.InboxItem
import com.nightclip.app.platform.Actions
import com.nightclip.app.platform.ClipboardAccess
import com.nightclip.app.platform.Notifications
import kotlinx.coroutines.launch

/**
 * Invisible activity behind the notification's "复制" button. The clipboard may only be written by
 * the focused app, so the text is loaded first (from the core, never from the Intent) and written
 * once both the text and window focus are there. Then clip.mark_applied is sent and the activity ends.
 */
class CopyActivity : ComponentActivity() {
    private var eventId = ""
    private var item: InboxItem? = null
    private var failed = false
    private var focused = false
    private var done = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        eventId = intent.getStringExtra(Actions.EXTRA_EVENT_ID).orEmpty()
        if (eventId.isEmpty()) {
            finish()
            return
        }
        lifecycleScope.launch {
            when (val r = CoreApi.inboxItem(eventId)) {
                is ApiResult.Ok -> item = r.value
                is ApiResult.Fail -> failed = true
            }
            tryWrite()
        }
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        focused = hasFocus
        if (hasFocus) tryWrite()
    }

    private fun tryWrite() {
        if (done) return
        if (failed) {
            done = true
            Toast.makeText(this, R.string.toast_copy_failed, Toast.LENGTH_SHORT).show()
            finish()
            return
        }
        val it = item ?: return
        if (!focused) return
        done = true
        lifecycleScope.launch {
            if (CoreApi.beginApply(eventId) is ApiResult.Fail) {
                Toast.makeText(this@CopyActivity, R.string.toast_copy_failed, Toast.LENGTH_SHORT).show()
                finish()
                return@launch
            }
            val ok = ClipboardAccess.writeText(this@CopyActivity, it.text, it.sensitive)
            if (ok) {
                val app = this@CopyActivity.app
                Notifications.cancelClip(this@CopyActivity, eventId)
                // Not tied to this activity: it is about to finish.
                app.appScope.launch { CoreApi.markApplied(eventId) }
                Toast.makeText(this@CopyActivity, R.string.toast_copied, Toast.LENGTH_SHORT).show()
            } else {
                Toast.makeText(this@CopyActivity, R.string.toast_copy_failed, Toast.LENGTH_SHORT).show()
            }
            finish()
        }
    }
}
