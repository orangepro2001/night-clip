package com.nightclip.app.platform

import android.content.ComponentName
import android.content.Context
import android.content.ServiceConnection
import android.content.pm.PackageManager
import android.os.IBinder
import android.util.Log
import com.nightclip.app.BuildConfig
import com.nightclip.app.bridge.ClipHostService
import com.nightclip.app.bridge.IClipCallback
import com.nightclip.app.bridge.IClipHost
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import rikka.shizuku.Shizuku

/** What the UI and the controller need to know about the Shizuku side. No clipboard text. */
data class ShizukuRuntime(
    val installed: Boolean = false,
    val shizukuRunning: Boolean = false,
    val permitted: Boolean = false,
    val connected: Boolean = false,
    val error: String? = null,
    val received: Long = 0,
)

/**
 * Connects to the helper that Shizuku runs as the shell user ([ClipHostService]) and hands every
 * reported clipboard text to [onText]. Shizuku itself is started by the user (wireless debugging, once
 * per boot); this object only asks for permission, binds the helper and reports state.
 */
object ShizukuBridge {
    private const val TAG = "ClipBridge"
    private const val SHIZUKU_PACKAGE = "moe.shizuku.privileged.api"
    private const val REQUEST_CODE = 4711

    private val _state = MutableStateFlow(ShizukuRuntime())
    val state: StateFlow<ShizukuRuntime> = _state.asStateFlow()

    @Volatile private var appContext: Context? = null
    @Volatile private var onText: ((String, Long) -> Unit)? = null
    @Volatile private var wantRunning = false
    private var listenersAdded = false
    private var bound = false

    private val callback = object : IClipCallback.Stub() {
        override fun onClip(text: String?, copiedAtMs: Long) {
            if (text.isNullOrEmpty()) return
            _state.update { it.copy(received = it.received + 1) }
            onText?.invoke(text, copiedAtMs)
        }
    }

    private val connection = object : ServiceConnection {
        override fun onServiceConnected(name: ComponentName?, binder: IBinder?) {
            if (binder == null || !binder.pingBinder()) {
                _state.update { it.copy(connected = false, error = "HOST_BINDER_DEAD") }
                return
            }
            try {
                IClipHost.Stub.asInterface(binder).setCallback(callback)
                Log.i(TAG, "shizuku bridge: helper connected")
                _state.update { it.copy(connected = true, error = null) }
            } catch (e: Exception) {
                Log.w(TAG, "shizuku bridge: setCallback failed: ${e.javaClass.simpleName}")
                _state.update { it.copy(connected = false, error = "HOST_CALL_FAILED") }
            }
        }

        override fun onServiceDisconnected(name: ComponentName?) {
            Log.i(TAG, "shizuku bridge: helper disconnected")
            bound = false
            _state.update { it.copy(connected = false) }
            if (wantRunning) maybeBind()
        }
    }

    private val binderReceived = Shizuku.OnBinderReceivedListener {
        Log.i(TAG, "shizuku bridge: shizuku binder received")
        refresh()
        if (wantRunning) maybeBind()
    }
    private val binderDead = Shizuku.OnBinderDeadListener {
        Log.i(TAG, "shizuku bridge: shizuku binder dead")
        bound = false
        _state.update { it.copy(shizukuRunning = false, connected = false) }
    }
    private val permissionResult = Shizuku.OnRequestPermissionResultListener { _, _ ->
        refresh()
        if (wantRunning) maybeBind()
    }

    /** Call once from Application/Service; safe to call again. */
    @Synchronized
    fun init(context: Context) {
        appContext = context.applicationContext
        if (!listenersAdded) {
            listenersAdded = true
            Shizuku.addBinderReceivedListenerSticky(binderReceived)
            Shizuku.addBinderDeadListener(binderDead)
            Shizuku.addRequestPermissionResultListener(permissionResult)
        }
        refresh()
    }

    /** Re-reads what can change behind our back: installed, running, permitted. */
    fun refresh() {
        val ctx = appContext ?: return
        val installed = try {
            ctx.packageManager.getPackageInfo(SHIZUKU_PACKAGE, 0)
            true
        } catch (_: PackageManager.NameNotFoundException) {
            false
        }
        val running = try {
            Shizuku.pingBinder()
        } catch (_: Throwable) {
            false
        }
        val permitted = running && try {
            !Shizuku.isPreV11() && Shizuku.checkSelfPermission() == PackageManager.PERMISSION_GRANTED
        } catch (_: Throwable) {
            false
        }
        _state.update { it.copy(installed = installed, shizukuRunning = running, permitted = permitted) }
    }

    /** Asks Shizuku (it shows its own dialog) to let ClipBridge use it. */
    fun requestPermission() {
        refresh()
        try {
            if (Shizuku.pingBinder() && !Shizuku.isPreV11()) Shizuku.requestPermission(REQUEST_CODE)
        } catch (e: Throwable) {
            _state.update { it.copy(error = "PERMISSION_REQUEST_FAILED") }
        }
    }

    /** Starts (or keeps) the helper while [run] is true, stops it otherwise. */
    @Synchronized
    fun sync(context: Context, run: Boolean, handler: (String, Long) -> Unit) {
        init(context)
        onText = handler
        wantRunning = run
        if (run) maybeBind() else unbind()
    }

    @Synchronized
    private fun maybeBind() {
        val ctx = appContext ?: return
        refresh()
        val s = _state.value
        if (bound || !s.shizukuRunning || !s.permitted) return
        try {
            Shizuku.bindUserService(userServiceArgs(ctx), connection)
            bound = true
            _state.update { it.copy(error = null) }
        } catch (e: Throwable) {
            Log.w(TAG, "shizuku bridge: bind failed: ${e.javaClass.simpleName}")
            _state.update { it.copy(connected = false, error = "BIND_FAILED") }
        }
    }

    @Synchronized
    private fun unbind() {
        val ctx = appContext ?: return
        if (bound) {
            try {
                Shizuku.unbindUserService(userServiceArgs(ctx), connection, true)
            } catch (_: Throwable) {
                // Shizuku may already be gone; the helper dies with it.
            }
        }
        bound = false
        _state.update { it.copy(connected = false, error = null) }
    }

    private fun userServiceArgs(ctx: Context): Shizuku.UserServiceArgs =
        Shizuku.UserServiceArgs(ComponentName(ctx.packageName, ClipHostService::class.java.name))
            .daemon(false)
            .processNameSuffix("cliphost")
            .debuggable(BuildConfig.DEBUG)
            .version(BuildConfig.CB_VERSION_CODE.toInt().coerceAtLeast(1))
}
