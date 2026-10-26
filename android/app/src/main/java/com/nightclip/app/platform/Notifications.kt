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
import com.nightclip.app.CopyActivity
import com.nightclip.app.MainActivity
import com.nightclip.app.NotificationActionReceiver
import com.nightclip.app.R
import com.nightclip.app.SendClipboardActivity
import com.nightclip.app.SyncForegroundService
import com.nightclip.app.core.CoreEvent
import com.nightclip.app.core.ErrorMessages
import com.nightclip.app.core.TransferInfo
import com.nightclip.app.logic.Formatters
import com.nightclip.app.logic.NotificationTexts
import com.nightclip.app.logic.Strings

/**
 * Every notification the shell posts. Notifications are keyed by (tag = event/transfer id, fixed id)
 * so they never collide and can be replaced or cancelled by id. All PendingIntents are FLAG_IMMUTABLE;
 * intents that must stay distinct carry a unique data URI (extras alone do not make them differ).
 */
object Notifications {
    const val CH_CLIP = "clip_received"
    const val CH_FILES = "file_offers"
    const val CH_PROGRESS = "file_progress"
    const val CH_CONNECTION = "connection"
    const val CH_UPDATE = "updates"

    const val ID_CONNECTION = 1
    const val ID_UPDATE = 2
    private const val ID_CLIP = 10
    private const val ID_FILE_OFFER = 11
    private const val ID_TRANSFER = 12
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
        channel(CH_CLIP, R.string.channel_clip_name, R.string.channel_clip_desc, NotificationManager.IMPORTANCE_HIGH)
        channel(CH_FILES, R.string.channel_files_name, R.string.channel_files_desc, NotificationManager.IMPORTANCE_HIGH)
        channel(CH_PROGRESS, R.string.channel_progress_name, R.string.channel_progress_desc, NotificationManager.IMPORTANCE_LOW)
        channel(CH_CONNECTION, R.string.channel_connection_name, R.string.channel_connection_desc, NotificationManager.IMPORTANCE_LOW)
        channel(CH_UPDATE, R.string.channel_update_name, R.string.channel_update_desc, NotificationManager.IMPORTANCE_DEFAULT)
    }

    private fun enabled(context: Context) = NotificationManagerCompat.from(context).areNotificationsEnabled()

    @Suppress("MissingPermission") // checked through areNotificationsEnabled(); POST_NOTIFICATIONS is requested at onboarding
    private fun post(context: Context, tag: String?, id: Int, n: Notification) {
        if (!enabled(context)) return
        try {
            NotificationManagerCompat.from(context).notify(tag, id, n)
        } catch (e: SecurityException) {
            // permission revoked between the check and the call: nothing useful to do
        }
    }

    private fun cancel(context: Context, tag: String?, id: Int) = NotificationManagerCompat.from(context).cancel(tag, id)

    private fun openApp(context: Context, tab: String): PendingIntent {
        val intent = Intent(context, MainActivity::class.java)
            .putExtra(Actions.EXTRA_TAB, tab)
            .setData(Uri.parse("clipbridge://tab/$tab"))
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP)
        return PendingIntent.getActivity(context, 0, intent, PI_FLAGS)
    }

    private fun receiverIntent(context: Context, action: String, extra: String, id: String): PendingIntent {
        val intent = Intent(context, NotificationActionReceiver::class.java)
            .setAction(action)
            .putExtra(extra, id)
            .setData(Uri.parse("clipbridge://$action/$id")) // keeps one PendingIntent per item
        return PendingIntent.getBroadcast(context, 0, intent, PI_FLAGS)
    }

    // ---- clipboard -------------------------------------------------------------------------

    fun postClip(context: Context, ev: CoreEvent.ClipReceived, degraded: Boolean) {
        val base = NotificationTexts.clipBody(ev.text, ev.sensitive)
        val body = if (degraded && !ev.sensitive) base + Strings.DEGRADED_SUFFIX else base
        val copy = PendingIntent.getActivity(
            context, 0,
            Intent(context, CopyActivity::class.java)
                .putExtra(Actions.EXTRA_EVENT_ID, ev.eventId)
                .setData(Uri.parse("clipbridge://copy/${ev.eventId}"))
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_NO_ANIMATION),
            PI_FLAGS,
        )
        val publicVersion = NotificationCompat.Builder(context, CH_CLIP)
            .setSmallIcon(R.drawable.ic_stat_clip)
            .setContentTitle(Strings.PUBLIC_CLIPBOARD_TITLE)
            .build()
        val n = NotificationCompat.Builder(context, CH_CLIP)
            .setSmallIcon(R.drawable.ic_stat_clip)
            .setContentTitle(Strings.clipTitle(ev.fromName))
            .setContentText(body)
            .setStyle(NotificationCompat.BigTextStyle().bigText(body))
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setVisibility(NotificationCompat.VISIBILITY_PRIVATE)
            .setPublicVersion(publicVersion)
            .setWhen(if (ev.receivedAt > 0) ev.receivedAt * 1000 else System.currentTimeMillis())
            .setAutoCancel(true)
            .setContentIntent(openApp(context, Actions.TAB_INBOX))
            .addAction(0, context.getString(R.string.action_copy), copy)
            .addAction(0, context.getString(R.string.action_dismiss), receiverIntent(context, Actions.ACTION_DISMISS, Actions.EXTRA_EVENT_ID, ev.eventId))
            .build()
        post(context, ev.eventId, ID_CLIP, n)
    }

    fun cancelClip(context: Context, eventId: String) = cancel(context, eventId, ID_CLIP)

    // ---- files -----------------------------------------------------------------------------

    fun postFileOffer(context: Context, ev: CoreEvent.FileOffered) {
        val n = NotificationCompat.Builder(context, CH_FILES)
            .setSmallIcon(R.drawable.ic_stat_clip)
            .setContentTitle(Strings.fileOfferTitle(ev.fromName))
            .setContentText(Strings.fileOfferBody(ev.name, ev.size))
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setAutoCancel(true)
            .setContentIntent(openApp(context, Actions.TAB_DEVICES))
            .addAction(0, context.getString(R.string.action_accept), receiverIntent(context, Actions.ACTION_FILE_ACCEPT, Actions.EXTRA_TRANSFER_ID, ev.transferId))
            .addAction(0, context.getString(R.string.action_reject), receiverIntent(context, Actions.ACTION_FILE_REJECT, Actions.EXTRA_TRANSFER_ID, ev.transferId))
            .build()
        post(context, ev.transferId, ID_FILE_OFFER, n)
    }

    fun cancelFileOffer(context: Context, transferId: String) = cancel(context, transferId, ID_FILE_OFFER)

    /** Ongoing progress notification with a cancel action. */
    fun postTransferProgress(context: Context, t: TransferInfo) {
        val title = when {
            t.state == "hashing" -> Strings.fileHashing(t.name)
            t.incoming -> Strings.fileReceiving(t.name)
            else -> Strings.fileSending(t.name)
        }
        val determinate = t.size > 0 && t.state == "transferring"
        val builder = NotificationCompat.Builder(context, CH_PROGRESS)
            .setSmallIcon(R.drawable.ic_stat_clip)
            .setContentTitle(title)
            .setContentText(if (determinate) Formatters.progressLine(t.offset, t.size, t.rateBps) else "")
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setSilent(true)
            .setCategory(NotificationCompat.CATEGORY_PROGRESS)
            .setProgress(100, Formatters.percent(t.offset, t.size), !determinate)
            .setContentIntent(openApp(context, Actions.TAB_DEVICES))
            .addAction(0, context.getString(R.string.action_cancel), receiverIntent(context, Actions.ACTION_FILE_CANCEL, Actions.EXTRA_TRANSFER_ID, t.transferId))
        post(context, t.transferId, ID_TRANSFER, builder.build())
    }

    /** Replaces the progress notification with a final, dismissible one. */
    fun postTransferResult(context: Context, transferId: String, title: String, text: String) {
        val n = NotificationCompat.Builder(context, CH_FILES)
            .setSmallIcon(R.drawable.ic_stat_clip)
            .setContentTitle(title)
            .setContentText(text)
            .setStyle(NotificationCompat.BigTextStyle().bigText(text))
            .setAutoCancel(true)
            .setContentIntent(openApp(context, Actions.TAB_DEVICES))
            .build()
        post(context, transferId, ID_TRANSFER, n)
    }

    fun cancelTransfer(context: Context, transferId: String) = cancel(context, transferId, ID_TRANSFER)

    fun fileFailedText(code: String): String =
        if (code == "HASH_MISMATCH") Strings.FILE_HASH_MISMATCH else ErrorMessages.forCode(code)

    // ---- misc ------------------------------------------------------------------------------

    fun postInfo(context: Context, tag: String, title: String, text: String) {
        val n = NotificationCompat.Builder(context, CH_FILES)
            .setSmallIcon(R.drawable.ic_stat_clip)
            .setContentTitle(title)
            .setContentText(text)
            .setStyle(NotificationCompat.BigTextStyle().bigText(text))
            .setAutoCancel(true)
            .setContentIntent(openApp(context, Actions.TAB_DEVICES))
            .build()
        post(context, tag, ID_INFO, n)
    }

    fun postUpdate(context: Context, title: String, text: String) {
        val n = NotificationCompat.Builder(context, CH_UPDATE)
            .setSmallIcon(R.drawable.ic_stat_clip)
            .setContentTitle(title)
            .setContentText(text)
            .setAutoCancel(true)
            .setContentIntent(openApp(context, Actions.TAB_SETTINGS))
            .build()
        post(context, null, ID_UPDATE, n)
    }

    /** The persistent real-time mode notification (shell-guide §4 item 7). */
    fun connection(context: Context, connected: Int): Notification {
        val send = PendingIntent.getActivity(
            context, 0,
            Intent(context, SendClipboardActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_NO_ANIMATION),
            PI_FLAGS,
        )
        val stop = PendingIntent.getService(
            context, 0,
            Intent(context, SyncForegroundService::class.java).setAction(Actions.ACTION_STOP_REALTIME),
            PI_FLAGS,
        )
        return NotificationCompat.Builder(context, CH_CONNECTION)
            .setSmallIcon(R.drawable.ic_stat_clip)
            .setContentTitle(context.getString(R.string.app_name))
            .setContentText(Strings.connectedDevices(connected))
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setSilent(true)
            .setCategory(NotificationCompat.CATEGORY_SERVICE)
            .setContentIntent(openApp(context, Actions.TAB_DEVICES))
            .addAction(0, context.getString(R.string.action_send_clipboard), send)
            .addAction(0, context.getString(R.string.action_disconnect), stop)
            .build()
    }

    fun updateConnection(context: Context, connected: Int) = post(context, null, ID_CONNECTION, connection(context, connected))
}
