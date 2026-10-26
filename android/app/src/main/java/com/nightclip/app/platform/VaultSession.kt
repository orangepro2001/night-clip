package com.nightclip.app.platform

import android.app.Activity
import android.app.Application
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.os.Bundle
import android.os.SystemClock
import android.util.Log
import com.nightclip.app.ClipBridgeApp
import com.nightclip.app.core.CoreEvent
import com.nightclip.app.core.CoreHost
import com.nightclip.app.core.VaultApi
import com.nightclip.app.logic.AutoLockPolicy
import com.nightclip.app.logic.VaultSyncLogic
import com.nightclip.app.toastOnMain
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.getAndUpdate
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.util.concurrent.atomic.AtomicInteger

/**
 * Process-wide watcher of the vault's lock state (docs/vault-api.md §4 rule 4): locks it when the app stayed in the
 * background longer than [AutoLockPolicy.BACKGROUND_GRACE_MS] or the screen turns off, follows the core's
 * `vault.locked` event and tells the screens to forget everything. Installed once from [ClipBridgeApp.onCreate].
 */
object VaultSession : Application.ActivityLifecycleCallbacks {
    private const val TAG = "ClipBridge"
    private const val SYSTEM_UI_WINDOW_MS = 10_000L

    private lateinit var app: ClipBridgeApp

    private val startedActivities = AtomicInteger(0)
    private val _unlocked = MutableStateFlow(false)

    /** Whether the shell believes the vault is open (set by the vault screens, cleared by every lock). */
    val unlocked: StateFlow<Boolean> = _unlocked.asStateFlow()

    private val _locks = MutableSharedFlow<String>(extraBufferCapacity = 8)

    /** One value (the reason) per lock of an open vault: idle, user, screen_off, background … */
    val locks: SharedFlow<String> = _locks.asSharedFlow()

    @Volatile private var backgroundedAtMs = 0L
    @Volatile private var graceUsedMs = AutoLockPolicy.BACKGROUND_GRACE_MS
    @Volatile private var systemUiAskedAtMs = 0L
    private var lockJob: Job? = null
    private var screenOffReceiver: BroadcastReceiver? = null

    fun install(application: ClipBridgeApp) {
        app = application
        application.registerActivityLifecycleCallbacks(this)
        VaultFiles.sweep(application) // leftovers of an interrupted import/export
        application.appScope.launch {
            CoreHost.events.collect { ev ->
                when (ev) {
                    is CoreEvent.VaultLocked -> onLocked(ev.reason)
                    is CoreEvent.VaultSync -> {
                        _attention.update { VaultSyncLogic.applyStateEvent(it, ev.deviceId, ev.state) }
                        _syncVersion.update { it + 1 }
                    }
                    // Only an open vault is changed by a sync merge; the screens re-read from the core.
                    is CoreEvent.VaultChanged -> if (_unlocked.value) dataChanged()
                    is CoreEvent.VaultJoined -> {
                        _joined.value = VaultSyncLogic.joinedText(ev.name)
                        application.toastOnMain(VaultSyncLogic.joinedText(ev.name))
                        _syncVersion.update { it + 1 }
                    }
                    else -> Unit
                }
            }
        }
        application.appScope.launch(Dispatchers.Main) {
            _unlocked.collect { open -> if (open) registerScreenOff() else unregisterScreenOff() }
        }
    }

    private val _attention = MutableStateFlow<Set<String>>(emptySet())

    /** Devices whose vault sync waits for the user (needs_password / review): drives the dot on the sync entry point. */
    val attention: StateFlow<Set<String>> = _attention.asStateFlow()

    fun setAttention(ids: Set<String>) {
        _attention.value = ids
    }

    private val _syncVersion = MutableStateFlow(0)

    /** Bumped on every vault.sync / vault.joined event: the sync screen re-reads vault.sync_status. */
    val syncVersion: StateFlow<Int> = _syncVersion.asStateFlow()

    private val _joined = MutableStateFlow<String?>(null)

    /** Text of the "joined another device's vault" notice until the user dismisses it (the vault screens show it). */
    val joined: StateFlow<String?> = _joined.asStateFlow()

    fun dismissJoined() {
        _joined.value = null
    }

    fun markUnlocked() {
        _unlocked.value = true
    }

    /** A call came back VAULT_LOCKED although the shell thought the vault was open: same clean-up as the event. */
    fun coreReportsLocked() = onLocked("core")

    private val _dataVersion = MutableStateFlow(0)

    /** Bumped whenever something changed the vault behind the list's back (restore, import, history restore …). */
    val dataVersion: StateFlow<Int> = _dataVersion.asStateFlow()

    fun dataChanged() {
        _dataVersion.value += 1
    }

    /**
     * Call right before starting a system picker or prompt of ours: the app is stopped while the user chooses, which
     * must not count as "left the app". Only counts if the app really stops within [SYSTEM_UI_WINDOW_MS] afterwards.
     */
    fun expectSystemUi() {
        systemUiAskedAtMs = SystemClock.elapsedRealtime()
    }

    /** Asks the core to lock now; the resulting `vault.locked` event does the clean-up. */
    fun lockNow() {
        app.appScope.launch { VaultApi.lock() }
    }

    private fun onLocked(reason: String) {
        lockJob?.cancel()
        lockJob = null
        val wasOpen = _unlocked.getAndUpdate { false }
        VaultClipboard.clearNow(app)
        VaultFiles.sweep(app)
        if (wasOpen) _locks.tryEmit(reason) // a lock of a vault we never showed needs no screen reset
    }

    // ---- background grace period ---------------------------------------------------------------

    override fun onActivityStarted(activity: Activity) {
        if (startedActivities.incrementAndGet() != 1) return
        lockJob?.cancel()
        lockJob = null
        val away = backgroundedAtMs
        if (away != 0L && _unlocked.value) {
            if (AutoLockPolicy.shouldLockOnReturn(away, SystemClock.elapsedRealtime(), graceUsedMs)) lockNow()
        }
        backgroundedAtMs = 0L
    }

    override fun onActivityStopped(activity: Activity) {
        if (startedActivities.decrementAndGet() > 0) return
        if (activity.isChangingConfigurations) return // a rotation stops and restarts the activity
        val now = SystemClock.elapsedRealtime()
        backgroundedAtMs = now
        val grace = AutoLockPolicy.graceMs(now - systemUiAskedAtMs < SYSTEM_UI_WINDOW_MS)
        graceUsedMs = grace
        if (!_unlocked.value) return
        lockJob?.cancel()
        lockJob = app.appScope.launch {
            delay(grace)
            if (_unlocked.value) {
                Log.i(TAG, "vault: locking after the app stayed in the background")
                VaultApi.lock()
            }
        }
    }

    // ---- screen off ---------------------------------------------------------------------------

    private fun registerScreenOff() {
        if (screenOffReceiver != null) return
        val r = object : BroadcastReceiver() {
            override fun onReceive(context: Context, intent: Intent) {
                if (intent.action == Intent.ACTION_SCREEN_OFF && AutoLockPolicy.shouldLockOnScreenOff(_unlocked.value)) lockNow()
            }
        }
        app.registerReceiver(r, IntentFilter(Intent.ACTION_SCREEN_OFF))
        screenOffReceiver = r
    }

    private fun unregisterScreenOff() {
        val r = screenOffReceiver ?: return
        screenOffReceiver = null
        try {
            app.unregisterReceiver(r)
        } catch (e: IllegalArgumentException) {
            // already gone
        }
    }

    override fun onActivityCreated(activity: Activity, savedInstanceState: Bundle?) = Unit
    override fun onActivityResumed(activity: Activity) = Unit
    override fun onActivityPaused(activity: Activity) = Unit
    override fun onActivitySaveInstanceState(activity: Activity, outState: Bundle) = Unit
    override fun onActivityDestroyed(activity: Activity) = Unit
}
