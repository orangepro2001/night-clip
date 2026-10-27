package com.nightclip.app.logic

/** A value to work on and the number of the request that carries it; the answer is reported back with the same number. */
data class Pending<T>(val seq: Long, val value: T)

/**
 * "Run the latest value, but not on every keystroke": the live preview (150 ms) and the search box (250 ms) both use it.
 *
 *  - [submit] remembers the newest value and (re)starts the quiet period; the host calls [tick] at [nextDueAt].
 *  - One request is out at a time. A value that arrives meanwhile waits for the answer, then runs after its own
 *    quiet period. So a slow core call cannot pile requests up.
 *  - [finish] says whether the answer is still wanted: an answer for a request that was dropped by [reset] (another
 *    note, a closed search) is stale and must be ignored. A failed answer is reported too, so the host can keep the
 *    last good result on screen and flag the failure ([failed]).
 *  - The same value is not run twice in a row unless [invalidate] asks for it (an attachment arrived, so the same
 *    text now draws differently).
 *
 * Not thread-safe; the host calls it from one thread. The clock is injected so tests need no real time.
 */
class LatestWins<T : Any>(private val now: () -> Long, private val delayMs: Long) {
    private var wanted: T? = null
    private var lastRun: T? = null
    private var dueAt: Long? = null
    private var seq = 0L
    private var inflight: Long? = null

    /** The last answer was an error (the previous good result is still the one to show). */
    var failed: Boolean = false
        private set

    /** [immediate]: no quiet period (switching to the preview, opening a search with text already in it). */
    fun submit(value: T, immediate: Boolean = false) {
        wanted = value
        if (value == lastRun) {
            // Typed back to what is already shown (or running): nothing to do.
            dueAt = null
            return
        }
        dueAt = now() + if (immediate) 0 else delayMs
    }

    /** Runs the current value again after [afterMs], even though it is the same (the world changed, not the value). */
    fun invalidate(afterMs: Long = 0) {
        if (wanted == null) return
        lastRun = null
        dueAt = now() + afterMs
    }

    /** Forgets everything. Answers of requests sent before are ignored from now on. */
    fun reset() {
        wanted = null
        lastRun = null
        dueAt = null
        inflight = null
        failed = false
    }

    /** When to call [tick]; null while a request is out (the answer decides what happens next) or nothing is wanted. */
    fun nextDueAt(): Long? = if (inflight != null) null else dueAt

    /** The request to send now, if its time has come. */
    fun tick(): Pending<T>? {
        val due = dueAt ?: return null
        val value = wanted ?: return null
        if (inflight != null || now() < due) return null
        dueAt = null
        seq++
        inflight = seq
        lastRun = value
        return Pending(seq, value)
    }

    /** True = the answer belongs to the request in flight and should be applied (or, with [ok] false, flagged). */
    fun finish(seq: Long, ok: Boolean): Boolean {
        if (inflight != seq) return false
        inflight = null
        failed = !ok
        if (!ok) lastRun = null // the same value may be tried again by the next edit or an invalidate
        return true
    }
}
