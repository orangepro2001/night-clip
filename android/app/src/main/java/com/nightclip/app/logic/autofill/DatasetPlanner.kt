package com.nightclip.app.logic.autofill

enum class ValueSource { USERNAME, PASSWORD, TOTP }

/** How the value of a dataset reaches the page. */
enum class FillMode {
    /** Values are in the dataset when the dropdown is built. */
    INLINE,

    /** Empty dataset; the auth activity fetches the value when tapped (no prompt while the vault is unlocked). Used for TOTP codes. */
    DEFERRED_SILENT,

    /** Empty dataset; the auth activity asks for the master password first. */
    DEFERRED_VERIFY,
}

data class FieldFill(val ref: Int, val source: ValueSource)

data class DatasetPlan(val entry: MatchedEntry, val fills: List<FieldFill>, val mode: FillMode) {
    val usesTotp: Boolean get() = fills.any { it.source == ValueSource.TOTP }
}

data class DatasetPlans(val datasets: List<DatasetPlan>, val offerOpenVault: Boolean)

/** The "verify the master password before every fill" policy (setting `autofill_require_auth`). */
object AuthPolicy {
    fun mode(requireAuth: Boolean, embeddedWeb: Boolean, hasTotpFill: Boolean): FillMode = when {
        requireAuth || embeddedWeb -> FillMode.DEFERRED_VERIFY
        hasTotpFill -> FillMode.DEFERRED_SILENT
        else -> FillMode.INLINE
    }

    /** The master password was just typed to unlock: a verify-first dataset can hand out its values straight away. */
    fun afterFreshUnlock(mode: FillMode): FillMode = if (mode == FillMode.DEFERRED_VERIFY) FillMode.INLINE else mode
}

object DatasetPlanner {
    const val MAX_DATASETS = 5

    /** `matches` must already be filtered by [SchemePolicy]. Order is kept: the core sends the best score first. */
    fun plan(cluster: FormCluster, target: Target, matches: List<MatchedEntry>, requireAuth: Boolean): DatasetPlans {
        if (!cluster.wantsCredentials) return DatasetPlans(emptyList(), offerOpenVault = false) // e.g. a sign-up form
        val embedded = (target as? Target.Web)?.embedded == true
        val plans = ArrayList<DatasetPlan>()
        val seen = HashSet<String>()
        for (m in matches.sortedByDescending { it.score }) {
            if (plans.size >= MAX_DATASETS) break
            if (!seen.add(m.id)) continue
            val fills = fillsFor(cluster, m)
            if (fills.isEmpty()) continue
            val mode = AuthPolicy.mode(requireAuth, embedded, fills.any { it.source == ValueSource.TOTP })
            plans += DatasetPlan(m, fills, mode)
        }
        // "Open the vault" is only a way out when nothing could be offered, and never on a page that wants a code.
        return DatasetPlans(plans, offerOpenVault = plans.isEmpty() && cluster.kind != FormKind.OTP)
    }

    /** Only the fields the page asked for, and only what the entry can give. The password of a NEW_PASSWORD field is never touched. */
    fun fillsFor(cluster: FormCluster, m: MatchedEntry): List<FieldFill> {
        val out = ArrayList<FieldFill>()
        val user = cluster.username
        if (user != null && m.username.isNotBlank()) out += FieldFill(user.ref, ValueSource.USERNAME)
        if (m.kind != "note") {
            cluster.currentPasswords.firstOrNull()?.let { out += FieldFill(it.ref, ValueSource.PASSWORD) }
        }
        if (m.hasTotp) cluster.oneTimeCodes.firstOrNull()?.let { out += FieldFill(it.ref, ValueSource.TOTP) }
        return out
    }
}

/** A TOTP code about to expire is useless by the time the user has tapped; wait for the next one. */
object TotpWait {
    fun delayMs(secondsLeft: Int): Long = if (secondsLeft in 0..2) (secondsLeft * 1000L) + 400L else 0L
}
