package com.nightclip.app

import android.app.Application
import android.content.Context
import com.nightclip.app.core.CoreHost
import com.nightclip.app.platform.ForegroundTracker
import com.nightclip.app.platform.Notifications
import com.nightclip.app.platform.ShellPrefs
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob

/**
 * Process-level owner of everything shared (shell-guide §4 item 1): the Go core, its single event
 * pump (inside [CoreHost]) and the [EventRouter] that turns events into notifications.
 */
class ClipBridgeApp : Application() {
    /** Outlives every activity; used for work that must finish after a screen closes. */
    val appScope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    lateinit var prefs: ShellPrefs
        private set

    override fun onCreate() {
        super.onCreate()
        prefs = ShellPrefs(this)
        Notifications.createChannels(this)
        registerActivityLifecycleCallbacks(ForegroundTracker)
        CoreHost.start(this)
        EventRouter(this).start()
    }
}

val Context.app: ClipBridgeApp get() = applicationContext as ClipBridgeApp
