package com.nightclip.app.autofill

import com.nightclip.app.core.ApiResult
import com.nightclip.app.logic.autofill.DatasetPlan
import com.nightclip.app.logic.autofill.TotpWait
import com.nightclip.app.logic.autofill.ValueSource
import kotlinx.coroutines.delay

/**
 * Fetches, at the moment a dataset is built, the values of exactly the fields its plan asks for. Only matched
 * entries reach this point (the plan comes from `vault.match`). The username is not a secret and comes from the
 * match summary; the password and the current TOTP code come from `vault.reveal` / `vault.totp`.
 */
object FillValues {
    /** ref -> value; empty values are dropped. Null when nothing at all could be fetched (e.g. the vault locked meanwhile). */
    suspend fun fetch(plan: DatasetPlan): Map<Int, String>? {
        val out = LinkedHashMap<Int, String>()
        for (fill in plan.fills) {
            val value = when (fill.source) {
                ValueSource.USERNAME -> plan.entry.username
                ValueSource.PASSWORD -> (AutofillVault.reveal(plan.entry.id, "password") as? ApiResult.Ok)?.value.orEmpty()
                ValueSource.TOTP -> totp(plan.entry.id)
            }
            if (value.isNotEmpty()) out[fill.ref] = value
        }
        return out.takeIf { it.isNotEmpty() }
    }

    private suspend fun totp(id: String): String {
        var r = AutofillVault.totp(id) as? ApiResult.Ok ?: return ""
        val wait = TotpWait.delayMs(r.value.second)
        if (wait > 0) {
            delay(wait) // the code is about to roll over: take the next one
            r = AutofillVault.totp(id) as? ApiResult.Ok ?: return ""
        }
        return r.value.first
    }
}
