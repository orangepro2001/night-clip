package com.nightclip.app

import android.app.Service
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import android.util.Log
import androidx.core.app.ServiceCompat
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.CoreApi
import com.nightclip.app.core.CoreEvent
import com.nightclip.app.core.CoreHost
import com.nightclip.app.platform.Actions
import com.nightclip.app.platform.Notifications
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.launch

/**
 * Real-time mode (docs/architecture.md §9): a `specialUse` foreground service whose only job is to
 * keep the process, and with it the Go core and its outbound connections, alive. It does not
 * consume core events for any purpose except refreshing the "connected N devices" count; the
 * [EventRouter] in the Application handles everything else. It never shuts the core down.
 */
class SyncForegroundService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private var watching = false
    @Volatile private var shownCount = -1

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == Actions.ACTION_STOP_REALTIME) {
            // "Disconnect": the choice is persisted, so the boot receiver will not bring it back.
            app.prefs.setRealtime(false)
            ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
            stopSelf()
            return START_NOT_STICKY
        }
        try {
            val notification = Notifications.connection(this, 0)
            if (Build.VERSION.SDK_INT >= 34) {
                startForeground(Notifications.ID_CONNECTION, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
            } else {
                startForeground(Notifications.ID_CONNECTION, notification)
            }
        } catch (e: Exception) {
            Log.w("ClipBridge", "foreground start refused: ${e.javaClass.simpleName}")
            Notifications.postInfo(this, "realtime-restore", getString(R.string.app_name), getString(R.string.notif_realtime_restore))
            stopSelf()
            return START_NOT_STICKY
        }
        if (!watching) {
            watching = true
            scope.launch { watchConnections() }
        }
        return START_STICKY
    }

    /** Keeps the "已连接 N 台设备" text current. */
    private suspend fun watchConnections() {
        CoreHost.awaitReady()
        refreshCount()
        CoreHost.events
            .filter { it is CoreEvent.PeerState || it is CoreEvent.PeerRevoked || it is CoreEvent.PairResult }
            .collect { refreshCount() }
    }

    private suspend fun refreshCount() {
        val peers = (CoreApi.peers() as? ApiResult.Ok)?.value ?: return
        val n = peers.count { it.isConnected }
        if (n != shownCount) {
            shownCount = n
            Notifications.updateConnection(this, n)
        }
    }

    override fun onDestroy() {
        scope.cancel()
        super.onDestroy()
    }
}
