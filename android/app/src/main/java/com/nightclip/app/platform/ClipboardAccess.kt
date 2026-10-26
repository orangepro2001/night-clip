package com.nightclip.app.platform

import android.content.ClipData
import android.content.ClipDescription
import android.content.ClipboardManager
import android.content.Context
import android.os.Build
import android.os.PersistableBundle

/**
 * Clipboard reads/writes. Call only from the main thread while one of our windows has focus:
 * Android 10+ ignores or blocks clipboard access for apps that are not in the foreground.
 * [ClipboardOverlayReader] creates such a window for background reads; writes do not need focus.
 */
object ClipboardAccess {
    fun readText(context: Context): String? = readClip(context)?.text

    /** The text plus when it was copied (ms since epoch, 0 = unknown). */
    data class ClipRead(val text: String, val copiedAtMs: Long) {
        override fun toString() = "ClipRead(copiedAtMs=$copiedAtMs)" // never print clipboard text
    }

    fun readClip(context: Context): ClipRead? {
        val cm = context.getSystemService(ClipboardManager::class.java) ?: return null
        val clip = cm.primaryClip ?: return null
        if (clip.itemCount == 0) return null
        if (clip.description?.label == VaultClipboard.MARKER_LABEL) return null // a vault copy is never a user copy (auto send, manual send)
        val text = clip.getItemAt(0).coerceToText(context)?.toString()?.takeIf { it.isNotEmpty() } ?: return null
        return ClipRead(text, clip.description?.timestamp ?: 0L)
    }

    /** Returns false if the platform refused the write. [sensitive] hides the preview on Android 13+. */
    fun writeText(context: Context, text: String, sensitive: Boolean = false): Boolean {
        val cm = context.getSystemService(ClipboardManager::class.java) ?: return false
        val clip = ClipData.newPlainText("ClipBridge", text)
        if (sensitive && Build.VERSION.SDK_INT >= 33) {
            clip.description.extras = PersistableBundle().apply { putBoolean(ClipDescription.EXTRA_IS_SENSITIVE, true) }
        }
        // Mark before setPrimaryClip: the platform may notify listeners during the write.
        ClipWriteGuard.noteWrite(context, text)
        return try {
            cm.setPrimaryClip(clip)
            true
        } catch (e: SecurityException) {
            false
        }
    }
}
