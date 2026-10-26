package com.nightclip.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import android.util.Log

/**
 * Optional "real-time mode" service (docs/architecture.md §9). Keeps the Go
 * core running and drains its event queue on a worker thread. Event handling
 * (notifications, inbox) is added in Phase 0/1.
 */
class SyncForegroundService : Service() {
    @Volatile private var running = false
    private var worker: Thread? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (running) return START_STICKY
        startInForeground()
        CoreHost.start(applicationContext, Build.MODEL)
        running = true
        worker = Thread({
            while (running) {
                val ev = CoreHost.nextEvent(1000) ?: continue
                Log.d(TAG, "event type=${ev.optString("type")}")
            }
        }, "clipbridge-events").also { it.start() }
        return START_STICKY
    }

    override fun onDestroy() {
        running = false
        CoreHost.shutdown() // wakes the blocked nextEvent
        worker?.join(3000)
        super.onDestroy()
    }

    private fun startInForeground() {
        val nm = getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(
            NotificationChannel(CHANNEL_ID, "Connection", NotificationManager.IMPORTANCE_LOW)
        )
        val notification: Notification = Notification.Builder(this, CHANNEL_ID)
            .setContentTitle("ClipBridge")
            .setContentText("Connected mode is on")
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setOngoing(true)
            .build()
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(NOTIFICATION_ID, notification)
        }
    }

    private companion object {
        const val TAG = "ClipBridge"
        const val CHANNEL_ID = "connection"
        const val NOTIFICATION_ID = 1
    }
}
