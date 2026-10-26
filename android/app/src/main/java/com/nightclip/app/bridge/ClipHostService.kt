package com.nightclip.app.bridge

import android.content.ClipData
import android.content.Context
import android.os.IBinder
import android.os.Process
import android.util.Log
import com.nightclip.app.platform.VaultClipboard
import java.lang.reflect.Method
import kotlin.system.exitProcess

/**
 * Runs in a separate process **as the shell user**, started by Shizuku (`bindUserService`).
 *
 * Android 10+ only lets the focused app read the clipboard, but the shell user may read it at any time
 * (measured on Android 15: docs/plan-android-background-read.md section 7). This host therefore needs no
 * READ_LOGS, no overlay window and never takes focus: it reads the clipboard through the hidden
 * `IClipboard` service and reports every change to the app through [IClipCallback].
 *
 * It only ever reports text. It is not an Android component; Shizuku creates it by class name, with
 * either no arguments or a [Context].
 */
class ClipHostService() : IClipHost.Stub() {
    @Suppress("UNUSED_PARAMETER")
    constructor(context: Context) : this()

    @Volatile private var callback: IClipCallback? = null
    @Volatile private var running = true
    private var worker: Thread? = null

    override fun destroy() {
        running = false
        worker?.interrupt()
        // Shizuku expects the process to end; do it off the binder thread so the reply goes out first.
        Thread {
            runCatching { Thread.sleep(100) }
            exitProcess(0)
        }.start()
    }

    @Synchronized
    override fun setCallback(callback: IClipCallback?) {
        this.callback = callback
        if (callback != null && worker == null) {
            worker = Thread(::watch, "clip-host-watch").apply { isDaemon = true; start() }
        }
    }

    private fun watch() {
        val reader = try {
            ClipboardReader()
        } catch (e: Throwable) {
            Log.w(TAG, "clipboard service unavailable: ${e.javaClass.simpleName}")
            return
        }
        var last: String? = null
        var failures = 0
        while (running) {
            try {
                val clip = reader.read()
                failures = 0
                val text = clip?.first
                if (text != null && text != last) {
                    last = text
                    callback?.onClip(text, clip.second)
                }
            } catch (e: Throwable) {
                // The callback's process went away or a read failed; back off instead of spinning.
                failures++
                if (e is android.os.DeadObjectException) callback = null
                Log.w(TAG, "read failed: ${e.javaClass.simpleName}")
            }
            try {
                Thread.sleep(if (failures == 0) POLL_MS else minOf(POLL_MS * (1L shl minOf(failures, 5)), 10_000L))
            } catch (_: InterruptedException) {
                return
            }
        }
    }

    /** Reads the primary clip through the hidden IClipboard binder, as the shell user. */
    private class ClipboardReader {
        private val service: Any
        private val get: Method
        private val args: Array<Any?>

        init {
            val sm = Class.forName("android.os.ServiceManager")
            val binder = sm.getMethod("getService", String::class.java).invoke(null, "clipboard") as IBinder
            val stub = Class.forName("android.content.IClipboard\$Stub")
            service = stub.getMethod("asInterface", IBinder::class.java).invoke(null, binder)!!
            get = service.javaClass.methods.first { it.name == "getPrimaryClip" }
            // getPrimaryClip(callingPackage, attributionTag, userId[, deviceId]): the package must match our uid.
            val pkg = if (Process.myUid() == 0) "root" else "com.android.shell"
            args = get.parameterTypes.mapIndexed { i, t ->
                when {
                    t == String::class.java -> if (i == 0) pkg else null
                    t == Int::class.javaPrimitiveType -> 0
                    else -> null
                }
            }.toTypedArray()
        }

        /** (text, copiedAtMs) of the current clip, or null when there is no plain text. */
        fun read(): Pair<String, Long>? {
            val clip = get.invoke(service, *args) as ClipData? ?: return null
            if (clip.itemCount == 0) return null
            if (clip.description?.label == VaultClipboard.MARKER_LABEL) return null // a vault copy is never a user copy
            val text = clip.getItemAt(0).text?.toString()?.takeIf { it.isNotEmpty() } ?: return null
            return text to (clip.description?.timestamp ?: 0L)
        }
    }

    private companion object {
        const val TAG = "ClipBridgeHost"
        const val POLL_MS = 400L
    }
}
