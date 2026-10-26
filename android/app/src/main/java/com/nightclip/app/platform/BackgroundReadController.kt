package com.nightclip.app.platform

import android.Manifest
import android.annotation.SuppressLint
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import android.provider.Settings
import android.util.Log
import androidx.core.content.ContextCompat
import com.nightclip.app.ClipBridgeApp
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.CoreApi
import com.nightclip.app.logic.BackgroundReadFacts
import com.nightclip.app.logic.BackgroundReadSetup
import com.nightclip.app.logic.ClipboardLogTrigger
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Everything the UI and the diagnostics need. Counters are diagnostics only; the clipboard text is
 * never stored here.
 */
data class BackgroundReadRuntime(
    val enabled: Boolean = false,
    val realtime: Boolean = false,
    val overlayGranted: Boolean = false,
    val logsGranted: Boolean = false,
    val serviceRunning: Boolean = false,
    val watcherRunning: Boolean = false,
    val supported: Boolean = true,
    val fatalError: Boolean = false,
    val triggers: Long = 0,
    val readsOk: Long = 0,
    val readsFailed: Long = 0,
    val sends: Long = 0,
    val lastError: String? = null,
) {
    fun facts(): BackgroundReadFacts = BackgroundReadFacts(
        enabled = enabled,
        realtime = realtime,
        overlayGranted = overlayGranted,
        logsGranted = logsGranted,
        serviceRunning = serviceRunning,
        watcherRunning = watcherRunning,
        hasError = fatalError,
        supported = supported,
    )
}

/**
 * Owns the opt-in background clipboard read (docs/adr/006-android-background-read.md).
 *
 * Detection: [LogcatClipboardWatcher] reads ClipboardService denial lines (READ_LOGS).
 * Read:       [ClipboardOverlayReader] briefly focuses a 1x1 overlay.
 * Send:       [AutoSend], shared with the focus layer.
 *
 * The controller only runs while the user enabled the feature, real-time mode is on, both
 * permissions are granted and the foreground service is alive. Everything else is a visible state,
 * never a silent failure.
 */
object BackgroundReadController {
    // Diagnostics only: stage names and error codes, never clipboard text or log lines.
    private const val TAG = "ClipBridge"

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val _state = MutableStateFlow(BackgroundReadRuntime())
    val state: StateFlow<BackgroundReadRuntime> = _state.asStateFlow()

    private var app: ClipBridgeApp? = null
    private val overlay = ClipboardOverlayReader()
    private val reading = AtomicBoolean(false)

    @Volatile private var serviceRunning = false
    // The watcher only holds the application context; lint cannot see through the singleton.
    @SuppressLint("StaticFieldLeak")
    @Volatile private var watcher: LogcatClipboardWatcher? = null
    private var watcherJob: Job? = null

    @Volatile private var lastTriggerAt = 0L
    @Volatile private var backoffUntil = 0L
    @Volatile private var consecutiveReadFailures = 0

    fun init(app: ClipBridgeApp) {
        if (this.app != null) return
        this.app = app
        refresh(app)
    }

    @Synchronized
    fun setEnabled(context: Context, on: Boolean) {
        val app = app ?: return
        app.prefs.setBackgroundRead(on)
        if (!on) {
            stopWatcher()
            _state.update {
                it.copy(enabled = false, watcherRunning = false, fatalError = false, lastError = null)
            }
            return
        }
        refresh(context)
    }

    /** Re-reads permissions/prefs; call from Activity.onResume and after a settings action. */
    @Synchronized
    fun refresh(context: Context) {
        val app = app ?: return
        val overlayGranted = Settings.canDrawOverlays(context)
        val logsGranted = ContextCompat.checkSelfPermission(context, Manifest.permission.READ_LOGS) ==
            PackageManager.PERMISSION_GRANTED
        val supported = Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q
        // The Shizuku-hosted helper does not depend on the overlay/READ_LOGS permissions below.
        ShizukuBridge.sync(
            context,
            run = app.prefs.backgroundRead.value && app.prefs.realtimeNow && serviceRunning,
        ) { text, copiedAt -> onBridgeText(text, copiedAt) }
        _state.update {
            it.copy(
                enabled = app.prefs.backgroundRead.value,
                realtime = app.prefs.realtimeNow,
                overlayGranted = overlayGranted,
                logsGranted = logsGranted,
                serviceRunning = serviceRunning,
                supported = supported,
            )
        }
        if (!supported) {
            stopWatcher()
            return
        }
        if (!overlayGranted || !logsGranted) {
            // Show the missing setup step, not a stale ERROR from before the permission changed.
            _state.update { it.copy(fatalError = false, lastError = null) }
        }
        val facts = _state.value.facts()
        if (!BackgroundReadSetup.canRun(facts)) {
            stopWatcher()
            return
        }
        startWatcherIfNeeded()
    }

    @Synchronized
    fun onServiceStarted(context: Context) {
        serviceRunning = true
        _state.update { it.copy(serviceRunning = true) }
        refresh(context)
    }

    @Synchronized
    fun onServiceStopped() {
        serviceRunning = false
        stopWatcher()
        app?.let { a -> ShizukuBridge.sync(a, run = false) { _, _ -> } }
        // A stopped service means "paused", not "broken"; a fresh start will try again.
        _state.update { it.copy(serviceRunning = false, watcherRunning = false, fatalError = false, lastError = null) }
    }

    /** One-shot test used by the settings card; does not send or change the baseline. */
    suspend fun testRead(context: Context): OverlayReadResult = overlay.read(context)

    @Synchronized
    private fun startWatcherIfNeeded() {
        val app = app ?: return
        if (watcher?.state?.value is WatcherState.Error) stopWatcher()
        if (watcher != null) return

        val w = LogcatClipboardWatcher(app)
        watcher = w
        _state.update { it.copy(watcherRunning = false, fatalError = false, lastError = null) }
        watcherJob = scope.launch {
            w.state.collect { s ->
                _state.update {
                    it.copy(
                        watcherRunning = s is WatcherState.Running,
                        fatalError = s is WatcherState.Error || it.fatalError,
                        lastError = when (s) {
                            is WatcherState.Error -> s.code
                            is WatcherState.Running -> null
                            is WatcherState.Stopped -> it.lastError
                        },
                    )
                }
            }
        }
        w.start { onTrigger() }
    }

    @Synchronized
    private fun stopWatcher() {
        watcherJob?.cancel()
        watcherJob = null
        watcher?.stop()
        watcher = null
        _state.update { it.copy(watcherRunning = false) }
    }

    private fun onTrigger() {
        val now = System.currentTimeMillis()
        if (ClipboardLogTrigger.isDebounced(now, lastTriggerAt)) return
        lastTriggerAt = now
        Log.i(TAG, "bg-read: trigger")
        _state.update { it.copy(triggers = it.triggers + 1) }
        if (!reading.compareAndSet(false, true)) return
        scope.launch {
            try {
                performRead()
            } finally {
                reading.set(false)
            }
        }
    }

    /**
     * A clipboard text reported by the Shizuku helper. It goes through the same gates as the focus-based
     * read (own writes, capture, pause, at least one peer) and then the shared AutoSend.
     */
    private fun onBridgeText(text: String, copiedAtMs: Long) {
        scope.launch {
            val app = app ?: return@launch
            _state.update { it.copy(triggers = it.triggers + 1) }
            val now = System.currentTimeMillis()
            if (!app.prefs.backgroundRead.value || !serviceRunning) return@launch skip("bridge-disabled")
            if (ClipWriteGuard.isRecent(now)) return@launch skip("own-write")
            val settings = (CoreApi.settings() as? ApiResult.Ok)?.value ?: return@launch skip("settings-unavailable")
            if (!settings.capture || settings.isPaused(now / 1000)) return@launch skip("capture-off-or-paused")
            val peers = (CoreApi.peers() as? ApiResult.Ok)?.value ?: return@launch skip("peers-unavailable")
            if (peers.none { it.canSend && it.sendEnabled }) return@launch skip("no-send-peer")
            if (!serviceRunning || !app.prefs.backgroundRead.value) return@launch
            if (ClipWriteGuard.isRecent()) return@launch skip("own-write")
            _state.update { it.copy(readsOk = it.readsOk + 1, lastError = null) }
            val started = AutoSend.handle(app, text, copiedAtMs, notify = ForegroundTracker.isForeground)
            Log.i(TAG, "bg-read: bridge text, send started=$started")
            if (started) _state.update { it.copy(sends = it.sends + 1) }
        }
    }

    private suspend fun performRead() {
        val app = app ?: return
        val now = System.currentTimeMillis()
        if (!app.prefs.backgroundRead.value || !serviceRunning) return skip("disabled")
        if (ClipWriteGuard.isRecent(now)) return skip("own-write")
        if (now < backoffUntil) return skip("backoff")

        val settings = (CoreApi.settings() as? ApiResult.Ok)?.value ?: return skip("settings-unavailable")
        if (!settings.capture || settings.isPaused(now / 1000)) return skip("capture-off-or-paused")
        val peers = (CoreApi.peers() as? ApiResult.Ok)?.value ?: return skip("peers-unavailable")
        if (peers.none { it.canSend && it.sendEnabled }) return skip("no-send-peer")
        if (!serviceRunning || !app.prefs.backgroundRead.value) return
        if (ClipWriteGuard.isRecent()) return // an app write may have landed during the facade calls

        val result = overlay.read(app)
        Log.i(TAG, "bg-read: overlay result=${result.javaClass.simpleName}")
        // The service/setting may have been turned off while the overlay was up.
        if (!serviceRunning || !app.prefs.backgroundRead.value) return
        when (result) {
            is OverlayReadResult.Ok -> {
                consecutiveReadFailures = 0
                backoffUntil = 0
                _state.update { it.copy(readsOk = it.readsOk + 1, lastError = null) }
                val started = AutoSend.handle(app, result.text, result.copiedAtMs, notify = ForegroundTracker.isForeground)
                Log.i(TAG, "bg-read: send started=$started")
                if (started) {
                    _state.update { it.copy(sends = it.sends + 1) }
                }
            }
            is OverlayReadResult.NoPermission -> fail("OVERLAY_DENIED")
            is OverlayReadResult.Locked -> _state.update { it.copy(lastError = "LOCKED") }
            is OverlayReadResult.NoFocus -> readFailed("NO_FOCUS", now)
            is OverlayReadResult.Denied -> readFailed("READ_DENIED", now)
            is OverlayReadResult.Empty -> _state.update { it.copy(lastError = "EMPTY") }
            is OverlayReadResult.Busy -> Unit
            is OverlayReadResult.Failed -> readFailed(result.code, now)
        }
    }

    private fun skip(reason: String) {
        Log.i(TAG, "bg-read: skipped ($reason)")
    }

    private fun readFailed(code: String, now: Long) {
        consecutiveReadFailures++
        if (consecutiveReadFailures >= 3) backoffUntil = now + 60_000
        _state.update { it.copy(readsFailed = it.readsFailed + 1, lastError = code) }
    }

    private fun fail(code: String) {
        stopWatcher()
        _state.update { it.copy(fatalError = true, lastError = code, watcherRunning = false) }
    }
}
