package com.nightclip.app.platform

import android.content.Context
import android.content.Intent
import androidx.core.content.ContextCompat
import com.nightclip.app.SyncForegroundService
import com.nightclip.app.app

/** Turns real-time mode (the persisted flag plus the foreground service) on or off. */
object RealtimeMode {
    /** Call from a visible activity or the boot receiver: those are allowed to start a foreground service. */
    fun set(context: Context, on: Boolean) {
        context.app.prefs.setRealtime(on)
        if (on) start(context) else context.stopService(Intent(context, SyncForegroundService::class.java))
    }

    fun start(context: Context) {
        ContextCompat.startForegroundService(context, Intent(context, SyncForegroundService::class.java).setAction(Actions.ACTION_START_REALTIME))
    }
}
