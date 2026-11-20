package com.nightclip.app.platform

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import com.nightclip.app.MainActivity
import com.nightclip.app.R

/**
 * Every notification the shell posts. Notifications are keyed by (tag, fixed id) so they never collide and can be
 * replaced or cancelled. All PendingIntents are FLAG_IMMUTABLE; intents that must stay distinct carry a unique data
 * URI (extras alone do not make them differ).
 */
object Notifications {
    const val CH_INFO = "info"
    const val CH_UPDATE = "updates"

    const val ID_UPDATE = 2
    private const val ID_INFO = 13

    private const val PI_FLAGS = PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT

    fun createChannels(context: Context) {
        val nm = context.getSystemService(NotificationManager::class.java)
        fun channel(id: String, nameRes: Int, descRes: Int, importance: Int) {
            nm.createNotificationChannel(
                NotificationChannel(id, context.getString(nameRes), importance).apply {
                    description = context.getString(descRes)
                },
            )
        }
        channel(CH_INFO, R.string.channel_info_name, R.string.channel_info_desc, NotificationManager.IMPORTANCE_HIGH)
        channel(CH_UPDATE, R.string.channel_update_name, R.string.channel_update_desc, NotificationManager.IMPORTANCE_DEFAULT)
    }

    private fun enabled(context: Context) = NotificationManagerCompat.from(context).areNotificationsEnabled()

    /** The in-app switch (Settings → 通知). Off: nothing is posted. */
    @Volatile
    var userEnabled: Boolean = true

    /** Applies the in-app switch; turning it off also clears what is already showing. */
    fun setUserEnabled(context: Context, on: Boolean) {
        userEnabled = on
        if (!on) clearAlerts(context)
    }

    @Suppress("MissingPermission") // checked through areNotificationsEnabled()
    private fun post(context: Context, tag: String?, id: Int, n: Notification) {
        if (!enabled(context) || !userEnabled) return
        try {
            NotificationManagerCompat.from(context).notify(tag, id, n)
        } catch (e: SecurityException) {
            // permission revoked between the check and the call: nothing useful to do
        }
    }

    /** Removes every dismissible notification this app has posted (what drives the launcher's count badge). */
    fun clearAlerts(context: Context) {
        val nm = context.getSystemService(NotificationManager::class.java)
        for (sbn in nm.activeNotifications) {
            val n = sbn.notification
            if (n.flags and (Notification.FLAG_ONGOING_EVENT or Notification.FLAG_FOREGROUND_SERVICE) != 0) continue
            nm.cancel(sbn.tag, sbn.id)
        }
    }

    fun canPost(context: Context): Boolean = userEnabled && enabled(context)

    private fun openApp(context: Context, tab: String): PendingIntent {
        val intent = Intent(context, MainActivity::class.java)
            .putExtra(Actions.EXTRA_TAB, tab)
            .setData(Uri.parse("app://tab/$tab"))
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP)
        return PendingIntent.getActivity(context, 0, intent, PI_FLAGS)
    }

    fun postInfo(context: Context, tag: String, title: String, text: String) {
        val n = NotificationCompat.Builder(context, CH_INFO)
            .setSmallIcon(R.drawable.ic_stat_app)
            .setContentTitle(title)
            .setContentText(text)
            .setStyle(NotificationCompat.BigTextStyle().bigText(text))
            .setAutoCancel(true)
            .setContentIntent(openApp(context, Actions.TAB_VAULT))
            .build()
        post(context, tag, ID_INFO, n)
    }

    fun postUpdate(context: Context, title: String, text: String) {
        val n = NotificationCompat.Builder(context, CH_UPDATE)
            .setSmallIcon(R.drawable.ic_stat_app)
            .setContentTitle(title)
            .setContentText(text)
            .setAutoCancel(true)
            .setContentIntent(openApp(context, Actions.TAB_SETTINGS))
            .build()
        post(context, null, ID_UPDATE, n)
    }
}
