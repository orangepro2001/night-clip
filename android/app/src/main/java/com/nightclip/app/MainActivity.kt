package com.nightclip.app

import android.content.Intent
import android.os.Bundle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.fragment.app.FragmentActivity
import com.nightclip.app.platform.Actions
import com.nightclip.app.ui.AppRoot
import com.nightclip.app.ui.ClipBridgeTheme
import com.nightclip.app.ui.LaunchRequests

/** The single visible screen: the error page or the tabs (vault, settings). */
class MainActivity : FragmentActivity() {
    private val requests = LaunchRequests()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        // After a rotation or process restore the original intent would be replayed; only handle fresh launches.
        if (savedInstanceState == null) handle(intent)
        setContent { ClipBridgeTheme { AppRoot(requests) } }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        handle(intent)
    }

    private fun handle(intent: Intent?) {
        intent ?: return
        intent.getStringExtra(Actions.EXTRA_TAB)?.let { requests.tab.value = it }
    }
}
