package com.nightclip.app.logic

/** When the shell locks the vault by itself (docs/vault-api.md §4 rule 4). Times are monotonic milliseconds. */
object AutoLockPolicy {
    /** Time the app may stay in the background before the vault is locked. */
    const val BACKGROUND_GRACE_MS = 30_000L

    /**
     * Longer grace while a system picker (file chooser, biometric prompt) that we started is on screen: our own
     * activity is stopped then although the user is still working with the vault.
     */
    const val PICKER_GRACE_MS = 5 * 60_000L

    fun graceMs(systemUiExpected: Boolean): Long = if (systemUiExpected) PICKER_GRACE_MS else BACKGROUND_GRACE_MS

    /**
     * The app came back after [backgroundedAtMs]. Lock if it was away for at least the grace period; a clock that went
     * backwards (negative elapsed time) is not trusted and locks.
     */
    fun shouldLockOnReturn(backgroundedAtMs: Long, nowMs: Long, graceMs: Long): Boolean {
        val away = nowMs - backgroundedAtMs
        return away < 0 || away >= graceMs
    }

    /** The screen turned off: lock if the vault is open. */
    fun shouldLockOnScreenOff(unlocked: Boolean): Boolean = unlocked
}
