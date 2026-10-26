package com.nightclip.app

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log
import com.nightclip.app.logic.Strings
import com.nightclip.app.platform.Notifications
import com.nightclip.app.platform.RealtimeMode

/**
 * Restores real-time mode after a reboot or an app update, but only if the user left it on.
 * If Android refuses to start the foreground service from here, the user gets a notification
 * that opens the app instead.
 */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val action = intent.action
        if (action != Intent.ACTION_BOOT_COMPLETED && action != Intent.ACTION_MY_PACKAGE_REPLACED) return
        if (!context.app.prefs.realtimeNow) return
        try {
            RealtimeMode.start(context)
        } catch (e: Exception) { // e.g. ForegroundServiceStartNotAllowedException
            Log.w("ClipBridge", "cannot start real-time service from ${action}: ${e.javaClass.simpleName}")
            Notifications.postInfo(context, "realtime-restore", context.getString(R.string.app_name), context.getString(R.string.notif_realtime_restore))
        }
    }
}
