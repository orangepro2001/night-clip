package cloud.moonl.password

import android.app.Application
import android.content.Context
import cloud.moonl.password.core.CoreHost
import cloud.moonl.password.platform.ForegroundTracker
import cloud.moonl.password.platform.Notifications
import cloud.moonl.password.platform.ShellPrefs
import cloud.moonl.password.platform.VaultSession
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob

/**
 * Process-level owner of everything shared (shell-guide §4 item 1): the Go core, its single event
 * pump (inside [CoreHost]) and the [EventRouter] that turns events into notifications.
 */
class MoonPasswordApp : Application() {
    /** Outlives every activity; used for work that must finish after a screen closes. */
    val appScope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    lateinit var prefs: ShellPrefs
        private set

    override fun onCreate() {
        super.onCreate()
        prefs = ShellPrefs(this)
        Notifications.createChannels(this)
        Notifications.userEnabled = prefs.notifications.value
        registerActivityLifecycleCallbacks(ForegroundTracker)
        VaultSession.install(this)
        CoreHost.start(this)
        EventRouter(this).start()
    }
}

val Context.app: MoonPasswordApp get() = applicationContext as MoonPasswordApp
