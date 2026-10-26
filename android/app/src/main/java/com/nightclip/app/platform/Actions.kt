package com.nightclip.app.platform

/** Intent actions and extras shared by notifications, receivers and activities. */
object Actions {
    const val EXTRA_EVENT_ID = "com.nightclip.app.extra.EVENT_ID"
    const val EXTRA_TRANSFER_ID = "com.nightclip.app.extra.TRANSFER_ID"
    const val EXTRA_TAB = "com.nightclip.app.extra.TAB"

    const val ACTION_DISMISS = "com.nightclip.app.action.DISMISS"
    const val ACTION_FILE_ACCEPT = "com.nightclip.app.action.FILE_ACCEPT"
    const val ACTION_FILE_REJECT = "com.nightclip.app.action.FILE_REJECT"
    const val ACTION_FILE_CANCEL = "com.nightclip.app.action.FILE_CANCEL"
    const val ACTION_STOP_REALTIME = "com.nightclip.app.action.STOP_REALTIME"
    const val ACTION_START_REALTIME = "com.nightclip.app.action.START_REALTIME"

    const val TAB_DEVICES = "devices"
    const val TAB_INBOX = "inbox"
    const val TAB_SETTINGS = "settings"
}
