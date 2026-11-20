package cloud.moonl.password

import android.content.Context
import android.os.Handler
import android.os.Looper
import android.util.Log
import android.widget.Toast
import cloud.moonl.password.core.CoreApi
import cloud.moonl.password.core.CoreEvent
import cloud.moonl.password.core.CoreHost
import cloud.moonl.password.platform.Notifications
import kotlinx.coroutines.launch

/**
 * Turns core events into user-visible effects. It is the one place that posts notifications, so it
 * works whenever the process is alive, whether or not a screen exists.
 */
class EventRouter(private val app: MoonPasswordApp) {
    private val tag = "MoonPassword"

    fun start() {
        app.appScope.launch {
            CoreHost.events.collect { ev ->
                try {
                    handle(ev)
                } catch (e: Exception) {
                    Log.w(tag, "handling ${ev.type} failed: ${e.javaClass.simpleName}")
                }
            }
        }
    }

    private suspend fun handle(ev: CoreEvent) {
        when (ev) {
            is CoreEvent.UpdateReady ->
                Notifications.postUpdate(app, app.getString(R.string.notif_update_ready_title), app.getString(R.string.notif_update_ready_text, ev.versionName))
            is CoreEvent.UpdateAvailable -> {
                // With automatic download on, "ready" follows by itself; otherwise ask the user.
                val auto = CoreApi.updateStatus().okOrNull?.auto ?: true
                if (!auto) Notifications.postUpdate(app, app.getString(R.string.notif_update_available_title), app.getString(R.string.notif_update_available_text, ev.versionName))
            }
            else -> Unit
        }
    }
}

/** Shows a short message from any thread. */
fun Context.toastOnMain(text: String) {
    Handler(Looper.getMainLooper()).post { Toast.makeText(applicationContext, text, Toast.LENGTH_SHORT).show() }
}
