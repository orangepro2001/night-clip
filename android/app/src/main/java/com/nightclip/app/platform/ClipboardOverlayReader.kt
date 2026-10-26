package com.nightclip.app.platform

import android.app.KeyguardManager
import android.content.Context
import android.graphics.PixelFormat
import android.os.SystemClock
import android.provider.Settings
import android.view.Gravity
import android.view.View
import android.view.WindowManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.withContext

/** Result of one background read attempt. Never carries anything but the text/timestamp on success. */
sealed interface OverlayReadResult {
    data class Ok(val text: String, val copiedAtMs: Long) : OverlayReadResult

    /** SYSTEM_ALERT_WINDOW is missing (or the system refused to add the window). */
    data object NoPermission : OverlayReadResult

    /** The lock screen is up; overlays cannot reliably take focus there. */
    data object Locked : OverlayReadResult

    /** The window was added but never became focused (secure window, game, another overlay…). */
    data object NoFocus : OverlayReadResult

    /** Focus was acquired but the clipboard was empty or unreadable. */
    data object Empty : OverlayReadResult

    /** The platform rejected the clipboard read. */
    data object Denied : OverlayReadResult

    /** Another read is already in flight; the trigger is dropped rather than queued. */
    data object Busy : OverlayReadResult

    data class Failed(val code: String) : OverlayReadResult
}

/**
 * Reads the clipboard from a 1x1 transparent overlay window.
 *
 * Android 10+ only lets the focused app read the clipboard. A `TYPE_APPLICATION_OVERLAY` window
 * whose `FLAG_NOT_FOCUSABLE` is absent becomes the focused window for our UID for the few
 * milliseconds it exists, which is enough for `ClipboardAccess.readClip`. The window is always
 * removed in `finally`; it is not touchable beyond a 1x1 pixel and never draws anything.
 *
 * This is deliberately serialized: two concurrent focus grabs would fight the user's input focus.
 */
class ClipboardOverlayReader {
    private val mutex = Mutex()

    suspend fun read(context: Context): OverlayReadResult {
        if (!Settings.canDrawOverlays(context)) return OverlayReadResult.NoPermission
        val keyguard = context.getSystemService(KeyguardManager::class.java)
        if (keyguard?.isKeyguardLocked == true) return OverlayReadResult.Locked
        if (!mutex.tryLock()) return OverlayReadResult.Busy
        return try {
            withContext(Dispatchers.Main) { readOnMain(context) }
        } finally {
            mutex.unlock()
        }
    }

    private suspend fun readOnMain(context: Context): OverlayReadResult {
        val wm = context.getSystemService(WindowManager::class.java)
            ?: return OverlayReadResult.Failed("NO_WINDOW_SERVICE")
        val view = View(context)
        val params = WindowManager.LayoutParams(
            1,
            1,
            WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY,
            WindowManager.LayoutParams.FLAG_NOT_TOUCH_MODAL or WindowManager.LayoutParams.FLAG_ALT_FOCUSABLE_IM,
            PixelFormat.TRANSLUCENT,
        ).apply {
            gravity = Gravity.TOP or Gravity.START
            x = 0
            y = 0
            title = "ClipBridge background read"
        }
        var added = false
        return try {
            wm.addView(view, params)
            added = true
            view.isFocusable = true
            view.isFocusableInTouchMode = true
            view.requestFocus()

            // FLAG_ALT_FOCUSABLE_IM keeps the keyboard attached to the previous app when possible.
            // If the window still does not become focused, drop it and try the plain focusable form.
            if (!awaitFocus(view, 250)) {
                params.flags = params.flags and WindowManager.LayoutParams.FLAG_ALT_FOCUSABLE_IM.inv()
                wm.updateViewLayout(view, params)
                view.requestFocus()
                if (!awaitFocus(view, 250)) return OverlayReadResult.NoFocus
            }

            val first = try {
                ClipboardAccess.readClip(context)
            } catch (e: SecurityException) {
                return OverlayReadResult.Denied
            }
            if (first != null) return OverlayReadResult.Ok(first.text, first.copiedAtMs)

            // Focus may have arrived a frame late; one short retry, then treat it as empty.
            delay(50)
            val second = try {
                ClipboardAccess.readClip(context)
            } catch (e: SecurityException) {
                return OverlayReadResult.Denied
            }
            if (second != null) OverlayReadResult.Ok(second.text, second.copiedAtMs) else OverlayReadResult.Empty
        } catch (e: SecurityException) {
            OverlayReadResult.NoPermission
        } catch (e: WindowManager.BadTokenException) {
            OverlayReadResult.NoPermission
        } catch (e: Exception) {
            OverlayReadResult.Failed(e.javaClass.simpleName)
        } finally {
            if (added) {
                try {
                    wm.removeViewImmediate(view)
                } catch (_: Exception) {
                    // The system may already have removed it (process/display change).
                }
            }
        }
    }

    private suspend fun awaitFocus(view: View, timeoutMs: Long): Boolean {
        val deadline = SystemClock.uptimeMillis() + timeoutMs
        while (SystemClock.uptimeMillis() < deadline) {
            if (view.hasWindowFocus()) return true
            delay(16)
        }
        return view.hasWindowFocus()
    }
}
