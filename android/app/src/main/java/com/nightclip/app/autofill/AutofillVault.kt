package com.nightclip.app.autofill

import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.CoreHost
import com.nightclip.app.logic.autofill.MatchedEntry
import com.nightclip.app.logic.autofill.Target
import com.nightclip.app.logic.autofill.TargetResolver
import com.nightclip.app.logic.autofill.VaultErrors
import com.nightclip.app.logic.autofill.VaultParsers
import com.nightclip.app.logic.autofill.VaultStatus
import kotlinx.coroutines.withTimeoutOrNull
import org.json.JSONObject

/**
 * Typed `vault.*` calls for the autofill screens (kept out of core/CoreApi.kt on purpose). The core runs in this
 * process: [CoreHost.call] waits for a cold start. Secrets pass through here only as call results and are never logged.
 */
object AutofillVault {
    /** A fill request must be answered within a few seconds; the system gives up after about five. */
    const val FILL_TIMEOUT_MS = 4_000L

    private suspend fun <T> run(method: String, params: JSONObject = JSONObject(), parse: (JSONObject) -> T): ApiResult<T> {
        val r = CoreHost.call(method, params)
        return if (r.ok) ApiResult.Ok(parse(r.result)) else ApiResult.Fail(r.errorCode, VaultErrors.message(r.errorCode))
    }

    suspend fun status(): ApiResult<VaultStatus> = run("vault.status", parse = VaultParsers::status)

    suspend fun unlock(password: String): ApiResult<Unit> =
        run("vault.unlock", JSONObject().put("password", password)) { }

    /** Re-checks the master password of an unlocked vault (`vault.verify_master`). */
    suspend fun verifyMaster(password: String): ApiResult<Unit> =
        run("vault.verify_master", JSONObject().put("password", password)) { }

    /** Strict match for the target; the caller still applies [com.nightclip.app.logic.autofill.SchemePolicy]. */
    suspend fun match(target: Target): ApiResult<List<MatchedEntry>> {
        val (url, pkg) = TargetResolver.matchParams(target)
        val p = JSONObject()
        if (url != null) p.put("url", url)
        if (pkg != null) p.put("package", pkg)
        return run("vault.match", p, VaultParsers::matches)
    }

    suspend fun reveal(id: String, field: String): ApiResult<String> =
        run("vault.reveal", JSONObject().put("id", id).put("field", field)) { it.optString("value") }

    /** Current code and the seconds it stays valid. */
    suspend fun totp(id: String): ApiResult<Pair<String, Int>> =
        run("vault.totp", JSONObject().put("id", id)) { it.optString("code") to it.optInt("seconds_left", 30) }

    suspend fun get(id: String): ApiResult<JSONObject> = run("vault.get", JSONObject().put("id", id)) { it }

    suspend fun add(entry: JSONObject): ApiResult<String> =
        run("vault.add", JSONObject().put("entry", entry)) { it.optString("id") }

    suspend fun update(id: String, entry: JSONObject): ApiResult<Unit> =
        run("vault.update", JSONObject().put("id", id).put("entry", entry)) { }

    /** Seconds before another unlock attempt is accepted (RATE_LIMITED), from `vault.status`. */
    suspend fun retryAfterS(): Int = status().okOrNull?.retryAfterS ?: 0

    /** Like [status] but gives up after [FILL_TIMEOUT_MS]; null on timeout or failure. */
    suspend fun statusOrNull(): VaultStatus? = withTimeoutOrNull(FILL_TIMEOUT_MS) { status().okOrNull }
}
