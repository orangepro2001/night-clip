package com.nightclip.app.logic.autofill

/**
 * Hook for a biometric quick unlock (docs/vault-api.md §4.6). The vault screens author may provide a real one
 * (Keystore + BiometricPrompt); the default does nothing, so the autofill screens always ask for the master password.
 */
interface QuickUnlockProvider {
    val available: Boolean

    /**
     * Shows the system prompt on [host] (an android.app.Activity, passed as Any to keep this interface JVM-pure)
     * and returns the stored master password, or null when cancelled / failed.
     */
    suspend fun obtainMasterPassword(host: Any): String?

    /** What the policy needs to know (see [AutofillQuickUnlockPolicy]); the owner of the provider keeps it up to date. */
    fun state(): AutofillQuickUnlockState = AutofillQuickUnlockState(0, 0, 0)

    /** Report how the last quick unlock ended, so the owner can count failures. */
    fun onResult(success: Boolean) {}
}

object NoQuickUnlock : QuickUnlockProvider {
    override val available: Boolean = false
    override suspend fun obtainMasterPassword(host: Any): String? = null
}

/** Process-wide slot: `QuickUnlockRegistry.provider = MyProvider()` once from Application.onCreate. */
object QuickUnlockRegistry {
    @Volatile var provider: QuickUnlockProvider = NoQuickUnlock
}

/** State the policy needs; the provider owner persists it (it is not secret). */
data class AutofillQuickUnlockState(
    /** When the master password was last typed (ms since epoch); 0 = never. */
    val lastMasterAtMs: Long,
    /** Value of SystemClock.elapsedRealtime() at that time, to notice a reboot. */
    val lastMasterElapsedMs: Long,
    val consecutiveFailures: Int,
)

object AutofillQuickUnlockPolicy {
    const val MAX_AGE_MS = 72L * 60 * 60 * 1000
    const val MAX_FAILURES = 5

    enum class Decision { USE_QUICK, ASK_PASSWORD }

    /** After [maxAgeMs] (0 = never expires), a restart, 5 failures in a row, or without a provider: type the master password. */
    fun decide(
        provider: QuickUnlockProvider,
        st: AutofillQuickUnlockState,
        nowMs: Long,
        nowElapsedMs: Long,
        maxAgeMs: Long = MAX_AGE_MS,
    ): Decision = when {
        !provider.available -> Decision.ASK_PASSWORD
        st.lastMasterAtMs <= 0 -> Decision.ASK_PASSWORD
        st.consecutiveFailures >= MAX_FAILURES -> Decision.ASK_PASSWORD
        maxAgeMs > 0 && (nowMs < st.lastMasterAtMs || nowMs - st.lastMasterAtMs > maxAgeMs) -> Decision.ASK_PASSWORD
        nowElapsedMs < st.lastMasterElapsedMs -> Decision.ASK_PASSWORD // the phone restarted since
        else -> Decision.USE_QUICK
    }
}
