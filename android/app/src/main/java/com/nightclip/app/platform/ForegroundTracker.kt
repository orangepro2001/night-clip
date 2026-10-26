package com.nightclip.app.platform

import android.app.Activity
import android.app.Application
import android.os.Bundle
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import java.util.concurrent.atomic.AtomicInteger

/**
 * True while at least one of our activities is resumed (visible and interactive). The auto-apply
 * decision and clipboard writes depend on it: Android only lets the foreground app touch the clipboard.
 */
object ForegroundTracker : Application.ActivityLifecycleCallbacks {
    private val resumed = AtomicInteger(0)
    private val _foreground = MutableStateFlow(false)
    val foreground: StateFlow<Boolean> = _foreground.asStateFlow()

    val isForeground: Boolean get() = _foreground.value

    override fun onActivityResumed(activity: Activity) {
        resumed.incrementAndGet()
        _foreground.value = true
    }

    override fun onActivityPaused(activity: Activity) {
        _foreground.value = resumed.decrementAndGet() > 0
    }

    override fun onActivityCreated(activity: Activity, savedInstanceState: Bundle?) = Unit
    override fun onActivityStarted(activity: Activity) = Unit
    override fun onActivityStopped(activity: Activity) = Unit
    override fun onActivitySaveInstanceState(activity: Activity, outState: Bundle) = Unit
    override fun onActivityDestroyed(activity: Activity) = Unit
}
