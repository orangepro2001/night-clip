package com.nightclip.app.platform

import android.content.ClipData
import android.content.ClipDescription
import android.content.ClipboardManager
import android.content.Context
import android.os.Build
import android.os.PersistableBundle
import com.nightclip.app.ClipBridgeApp
import com.nightclip.app.logic.ClipClearPolicy
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * Clipboard handling of vault copies (docs/vault-api.md §4 rule 1). The text is written once, marked sensitive and
 * labelled [MARKER_LABEL]; only its SHA-256 is remembered. After `clear_after_s` seconds (or at once when the vault locks)
 * [ClipClearPolicy] decides whether it is still ours to remove. The timer lives in the application scope so it fires
 * even when the screen is closed. If the process is frozen or killed before it fires, nothing can clear the clipboard;
 * the core's `vault_origin` guard still keeps the text off the network.
 */
object VaultClipboard {
    const val MARKER_LABEL = ClipClearPolicy.MARKER_LABEL

    private val lock = Any()
    private var pendingHash: String? = null
    private var timer: Job? = null

    /** Main thread. Returns false if the platform refused the write. */
    fun copy(app: ClipBridgeApp, value: String, clearAfterS: Int): Boolean {
        val cm = app.getSystemService(ClipboardManager::class.java) ?: return false
        val clip = ClipData.newPlainText(MARKER_LABEL, value)
        if (Build.VERSION.SDK_INT >= 33) {
            clip.description.extras = PersistableBundle().apply { putBoolean(ClipDescription.EXTRA_IS_SENSITIVE, true) }
        }
        try {
            cm.setPrimaryClip(clip)
        } catch (e: SecurityException) {
            return false
        }
        val hash = ClipClearPolicy.hash(value)
        val delayMs = ClipClearPolicy.delayMs(clearAfterS)
        synchronized(lock) {
            timer?.cancel()
            timer = null
            pendingHash = hash
            if (delayMs != null) {
                timer = app.appScope.launch {
                    delay(delayMs)
                    withContext(Dispatchers.Main) { clearIfStillOurs(app) }
                }
            }
        }
        return true
    }

    /** The vault locked or the screen went off: do not wait for the timer. Safe to call from any thread. */
    fun clearNow(app: ClipBridgeApp) {
        app.appScope.launch(Dispatchers.Main) { clearIfStillOurs(app) }
    }

    /** Main thread. Takes the pending hash (so it fires once), reads what can be read and applies the policy. */
    private fun clearIfStillOurs(app: ClipBridgeApp) {
        val hash: String
        synchronized(lock) {
            hash = pendingHash ?: return
            pendingHash = null
            timer?.cancel()
            timer = null
        }
        val cm = app.getSystemService(ClipboardManager::class.java) ?: return
        when (ClipClearPolicy.decide(snapshot(app, cm), hash)) {
            ClipClearPolicy.Decision.KEEP -> Unit
            ClipClearPolicy.Decision.CLEAR, ClipClearPolicy.Decision.CLEAR_UNVERIFIED -> clear(cm)
        }
    }

    private fun snapshot(context: Context, cm: ClipboardManager): ClipClearPolicy.Snapshot {
        val clip = try {
            cm.primaryClip
        } catch (e: SecurityException) {
            null
        }
        val text = if (clip != null && clip.itemCount > 0) {
            clip.getItemAt(0).coerceToText(context)?.toString()?.takeIf { it.isNotEmpty() }
        } else {
            null
        }
        val label = clip?.description?.label?.toString()
            ?: try {
                cm.primaryClipDescription?.label?.toString()
            } catch (e: SecurityException) {
                null
            }
        return ClipClearPolicy.Snapshot(text, label)
    }

    private fun clear(cm: ClipboardManager) {
        try {
            if (Build.VERSION.SDK_INT >= 28) {
                cm.clearPrimaryClip()
            } else {
                cm.setPrimaryClip(ClipData.newPlainText("", ""))
            }
        } catch (e: SecurityException) {
            // the system refused (not in the foreground): nothing more we can do
        }
    }
}
