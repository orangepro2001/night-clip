package com.nightclip.app.platform

import android.Manifest
import android.content.ClipboardManager
import android.content.Context
import android.content.pm.PackageManager
import android.util.Log
import androidx.core.content.ContextCompat
import com.nightclip.app.logic.ClipboardLogTrigger
import com.nightclip.app.logic.LogcatBackoff
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import java.io.BufferedReader
import java.io.File
import java.io.InputStreamReader
import java.util.concurrent.atomic.AtomicBoolean

sealed interface WatcherState {
    data object Stopped : WatcherState
    data object Running : WatcherState
    data class Error(val code: String) : WatcherState
}

/**
 * Detects "the user copied something" while the app is in the background.
 *
 * Android 10+ does not deliver OnPrimaryClipChangedListener callbacks to a background app, but
 * ClipboardService still iterates every registered listener and logs an E-level denial naming the
 * listener's package. Registering a listener therefore produces one log line per copy; READ_LOGS lets
 * us read it. Its callback is normally not delivered in the background; a few ROMs do deliver it,
 * and then the callback is used as a fallback trigger. We filter the stream at logcat level
 * (`ClipboardService:E *:S`)
 * and never persist or forward log content.
 *
 * The actual clipboard read is done by [ClipboardOverlayReader], not here.
 */
class LogcatClipboardWatcher(private val context: Context) {
    private val tag = "ClipBridge"
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val _state = MutableStateFlow<WatcherState>(WatcherState.Stopped)
    val state: StateFlow<WatcherState> = _state.asStateFlow()

    private var job: Job? = null
    @Volatile private var process: Process? = null
    @Volatile private var listener: ClipboardManager.OnPrimaryClipChangedListener? = null
    @Volatile private var trigger: (() -> Unit)? = null

    @Volatile private var stopping = false

    fun start(onTrigger: () -> Unit) {
        if (job?.isActive == true) return
        if (!hasLogPermission()) {
            _state.value = WatcherState.Error("READ_LOGS_DENIED")
            return
        }
        trigger = onTrigger
        stopping = false
        try {
            registerListener()
            job = scope.launch { runLoop() }
        } catch (e: Exception) {
            Log.w(tag, "cannot start background read watcher: ${e.javaClass.simpleName}")
            unregisterListener()
            _state.value = WatcherState.Error("WATCHER_START_FAILED")
        }
    }

    fun stop() {
        stopping = true
        job?.cancel()
        job = null
        process?.destroy()
        process = null
        unregisterListener()
        _state.value = WatcherState.Stopped
    }

    private fun hasLogPermission(): Boolean =
        ContextCompat.checkSelfPermission(context, Manifest.permission.READ_LOGS) == PackageManager.PERMISSION_GRANTED

    /**
     * The callback is intentionally empty. On Android 10+ the callback is NOT delivered while the
     * app is in the background; registration is what makes the platform log the denial line for us.
     * (If the app is focused, the focus path in MainActivity handles the copy.)
     */
    private fun registerListener() {
        if (listener != null) return
        val cm = context.getSystemService(ClipboardManager::class.java) ?: return
        val l = ClipboardManager.OnPrimaryClipChangedListener {
            // Android 10+ normally does NOT deliver this callback to a background app. A few ROMs
            // treat a foreground service as focused and do deliver it; in that case the denial log
            // may be missing, so use the callback as a fallback. The controller de-duplicates.
            if (!ForegroundTracker.isForeground) trigger?.invoke()
        }
        cm.addPrimaryClipChangedListener(l)
        listener = l
    }

    private fun unregisterListener() {
        val cm = context.getSystemService(ClipboardManager::class.java) ?: return
        listener?.let { cm.removePrimaryClipChangedListener(it) }
        listener = null
    }

    private suspend fun runLoop() {
        var failures = 0
        while (!stopping && scope.isActive) {
            val p = try {
                startProcess()
            } catch (e: Exception) {
                failures++
                if (LogcatBackoff.shouldStop(failures)) {
                    fail("LOGCAT_START_FAILED")
                    return
                }
                delay(LogcatBackoff.delayMs(failures))
                continue
            }
            // stop() may have run between startProcess() and this assignment; do not leak the child.
            if (stopping || !scope.isActive) {
                p.destroy()
                process = null
                return
            }
            process = p
            Log.i(tag, "bg-read: logcat watcher running")
            _state.value = WatcherState.Running
            var sawLine = false
            val permissionError = AtomicBoolean(false)

            // Drain stderr concurrently: a full stderr pipe would block the child and hide the exit.
            val stderrJob = scope.launch {
                try {
                    BufferedReader(InputStreamReader(p.errorStream)).useLines { lines ->
                        for (line in lines) {
                            if (line.contains("Permission denied", ignoreCase = true) ||
                                line.contains("not allowed", ignoreCase = true)
                            ) {
                                permissionError.set(true)
                                break
                            }
                        }
                    }
                } catch (_: Exception) {
                    // process went away
                }
            }
            try {
                BufferedReader(InputStreamReader(p.inputStream)).useLines { lines ->
                    for (line in lines) {
                        if (stopping) break
                        sawLine = true
                        if (ClipboardLogTrigger.matches(line, context.packageName)) {
                            Log.i(tag, "bg-read: denial log line matched")
                            trigger?.invoke()
                        }
                    }
                }
            } catch (_: Exception) {
                // exit/cancel; handled below
            } finally {
                stderrJob.cancel()
                p.destroy()
                process = null
            }

            if (stopping) break
            if (permissionError.get()) {
                fail("READ_LOGS_DENIED")
                return
            }
            failures = if (sawLine) 1 else failures + 1
            if (LogcatBackoff.shouldStop(failures)) {
                fail("LOGCAT_STOPPED")
                return
            }
            delay(LogcatBackoff.delayMs(failures))
        }
    }

    private fun startProcess(): Process {
        val logcat = if (File("/system/bin/logcat").canExecute()) "/system/bin/logcat" else "logcat"
        // Explicit buffers: the denial line is written through Slog (system buffer); do not rely
        // on whatever the vendor logcat considers its default set.
        return ProcessBuilder(logcat, "-b", "main,system", "-T", "1", "ClipboardService:E", "*:S")
            .redirectErrorStream(false)
            .start()
    }

    private fun fail(code: String) {
        Log.w(tag, "background read watcher stopped: $code")
        stopping = true
        process?.destroy()
        process = null
        unregisterListener()
        _state.value = WatcherState.Error(code)
    }
}
