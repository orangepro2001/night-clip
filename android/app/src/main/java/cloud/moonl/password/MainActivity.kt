package cloud.moonl.password

import android.content.Intent
import android.os.Bundle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.fragment.app.FragmentActivity
import cloud.moonl.password.core.CoreApi
import cloud.moonl.password.platform.Actions
import cloud.moonl.password.ui.AppRoot
import cloud.moonl.password.ui.MoonPasswordTheme
import cloud.moonl.password.ui.LaunchRequests
import kotlinx.coroutines.launch

/** The single visible screen: the error page or the tabs (vault, settings). */
class MainActivity : FragmentActivity() {
    private val requests = LaunchRequests()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        // After a rotation or process restore the original intent would be replayed; only handle fresh launches.
        if (savedInstanceState == null) handle(intent)
        setContent { MoonPasswordTheme { AppRoot(requests) } }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        handle(intent)
    }

    private fun handle(intent: Intent?) {
        intent ?: return
        val callback = intent.data
        if (intent.action == Intent.ACTION_VIEW && callback?.scheme == "cloud.moonl.password" &&
            callback.encodedAuthority == null && callback.encodedPath == "/oauth2redirect") {
            intent.data = null
            requests.tab.value = Actions.TAB_SETTINGS
            app.appScope.launch { CoreApi.completeAccountLogin(callback.toString()) }
            return
        }
        intent.getStringExtra(Actions.EXTRA_TAB)?.let { requests.tab.value = it }
    }
}
