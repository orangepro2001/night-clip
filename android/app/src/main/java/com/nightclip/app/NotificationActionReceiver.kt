package com.nightclip.app

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.CoreApi
import com.nightclip.app.platform.Actions
import com.nightclip.app.platform.Notifications
import kotlinx.coroutines.launch

/**
 * Handles the notification buttons that need no screen: Dismiss, Accept/Reject a file, Cancel a
 * transfer. Not exported; the PendingIntents that reach it are immutable and created by this app.
 */
class NotificationActionReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val action = intent.action ?: return
        val pending = goAsync()
        val app = context.app
        app.appScope.launch {
            try {
                when (action) {
                    Actions.ACTION_DISMISS -> intent.getStringExtra(Actions.EXTRA_EVENT_ID)?.let { id ->
                        Notifications.cancelClip(app, id)
                        CoreApi.dismiss(id)
                    }
                    Actions.ACTION_FILE_ACCEPT -> intent.getStringExtra(Actions.EXTRA_TRANSFER_ID)?.let { id ->
                        Notifications.cancelFileOffer(app, id)
                        val r = CoreApi.acceptFile(id)
                        if (r is ApiResult.Fail) Notifications.postInfo(app, id, app.getString(R.string.notif_accept_failed), r.message)
                    }
                    Actions.ACTION_FILE_REJECT -> intent.getStringExtra(Actions.EXTRA_TRANSFER_ID)?.let { id ->
                        Notifications.cancelFileOffer(app, id)
                        CoreApi.rejectFile(id)
                    }
                    Actions.ACTION_FILE_CANCEL -> intent.getStringExtra(Actions.EXTRA_TRANSFER_ID)?.let { id ->
                        CoreApi.cancelFile(id)
                        Notifications.cancelTransfer(app, id)
                    }
                }
            } finally {
                pending.finish()
            }
        }
    }
}
