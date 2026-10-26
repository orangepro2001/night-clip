package com.nightclip.app

import android.content.Intent
import android.os.Bundle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.fragment.app.FragmentActivity
import com.nightclip.app.platform.Actions
import com.nightclip.app.platform.BackgroundReadController
import com.nightclip.app.platform.FocusAutoSend
import com.nightclip.app.platform.RealtimeMode
import com.nightclip.app.ui.AppRoot
import com.nightclip.app.ui.ClipBridgeTheme
import com.nightclip.app.ui.LaunchRequests

/** The single visible screen: onboarding, error page or the three tabs (devices, inbox, settings). */
class MainActivity : FragmentActivity() {
    private val requests = LaunchRequests()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        // After a rotation or process restore the original intent would be replayed; only handle fresh launches.
        if (savedInstanceState == null) handle(intent)
        // Real-time mode is a persisted choice: if the service was lost (force stop, crash), bring it back now that we are visible.
        if (app.prefs.realtimeNow) {
            try {
                RealtimeMode.start(this)
            } catch (e: Exception) {
                // not allowed right now; the settings switch still shows the saved choice
            }
        }
        setContent { ClipBridgeTheme { AppRoot(requests) } }
    }

    override fun onResume() {
        super.onResume()
        // Permissions can be granted/revoked while we are away (overlay settings, adb).
        BackgroundReadController.refresh(this)
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        // Android only lets the focused app read the clipboard, so this is where a copy made
        // elsewhere is noticed and broadcast without any button press.
        if (hasFocus) FocusAutoSend.onFocus(this)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        handle(intent)
    }

    private fun handle(intent: Intent?) {
        intent ?: return
        intent.getStringExtra(Actions.EXTRA_TAB)?.let { requests.tab.value = it }
        val data = intent.data
        if (intent.action == Intent.ACTION_VIEW && data != null && data.scheme == "clipbridge" && data.host == "pair") {
            requests.pairLink.value = data.toString()
        }
    }
}
