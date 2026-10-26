package com.nightclip.app.logic

/**
 * When the biometric quick unlock may be offered (docs/vault-api.md §4 rule 6): only if the user enabled it, a wrapped
 * secret exists, the last master-password unlock is less than 72 hours old, fewer than 5 quick unlocks failed in a row
 * and the phone has not been restarted since that master-password unlock.
 */
data class QuickUnlockState(
    val enabled: Boolean = false,
    val hasSecret: Boolean = false,
    /** Wall-clock ms of the last successful MASTER-PASSWORD unlock (or vault creation); 0 = never. */
    val lastMasterUnlockAtMs: Long = 0,
    /** Consecutive quick-unlock failures since the last success. */
    val failures: Int = 0,
    /** Settings.Global.BOOT_COUNT at that master-password unlock; null when the system does not tell. */
    val bootCountAtMasterUnlock: Int? = null,
)

enum class QuickUnlockAvailability {
    AVAILABLE,
    DISABLED,
    NO_SECRET,
    EXPIRED,
    TOO_MANY_FAILURES,
    REBOOTED;

    /** The stored secret is useless from now on and should be deleted together with its key. */
    val shouldPurge: Boolean get() = this == EXPIRED || this == TOO_MANY_FAILURES || this == REBOOTED

    /** Text for the locked screen when quick unlock is NOT offered although the user enabled it; null when nothing needs saying. */
    val explanation: String?
        get() = when (this) {
            EXPIRED -> "距离上次输入主密码已超过 72 小时，请输入主密码。"
            TOO_MANY_FAILURES -> "快速解锁连续失败 5 次，请输入主密码。"
            REBOOTED -> "手机重启过，请输入主密码。"
            else -> null
        }
}

object QuickUnlockPolicy {
    const val MAX_AGE_MS = 72L * 3600 * 1000
    const val MAX_FAILURES = 5

    /**
     * [currentBootCount] is Settings.Global.BOOT_COUNT now, or null if unreadable. When either count is unknown the
     * reboot rule is skipped (documented limitation: only the 72 hour limit protects then).
     */
    fun check(state: QuickUnlockState, nowMs: Long, currentBootCount: Int?): QuickUnlockAvailability {
        if (!state.enabled) return QuickUnlockAvailability.DISABLED
        if (!state.hasSecret) return QuickUnlockAvailability.NO_SECRET
        val stored = state.bootCountAtMasterUnlock
        if (stored != null && currentBootCount != null && currentBootCount != stored) return QuickUnlockAvailability.REBOOTED
        val age = nowMs - state.lastMasterUnlockAtMs
        if (state.lastMasterUnlockAtMs <= 0 || age < 0 || age >= MAX_AGE_MS) return QuickUnlockAvailability.EXPIRED
        if (state.failures >= MAX_FAILURES) return QuickUnlockAvailability.TOO_MANY_FAILURES
        return QuickUnlockAvailability.AVAILABLE
    }

    /** A master-password unlock (or create) succeeded: the 72 hour clock restarts and failures are forgotten. */
    fun afterMasterUnlock(state: QuickUnlockState, nowMs: Long, bootCount: Int?): QuickUnlockState =
        state.copy(lastMasterUnlockAtMs = nowMs, failures = 0, bootCountAtMasterUnlock = bootCount)

    /** A quick unlock succeeded: failures reset, but the 72 hour clock is NOT restarted. */
    fun afterQuickSuccess(state: QuickUnlockState): QuickUnlockState = state.copy(failures = 0)

    fun afterQuickFailure(state: QuickUnlockState): QuickUnlockState = state.copy(failures = state.failures + 1)

    /** Enabling stores a fresh secret; the clocks come from the master-password unlock that provided it. */
    fun afterEnable(state: QuickUnlockState): QuickUnlockState = state.copy(enabled = true, hasSecret = true, failures = 0)

    /** Wrong master password, master-password change, expiry, too many failures or the user switching it off: nothing is kept. */
    fun afterInvalidate(state: QuickUnlockState): QuickUnlockState = state.copy(enabled = false, hasSecret = false, failures = 0)

    /** Whether a wrong-credentials reply must invalidate the stored secret: always, for either kind of unlock. */
    fun invalidatesOn(errorCode: String): Boolean = errorCode == "VAULT_WRONG_CREDENTIALS"
}
