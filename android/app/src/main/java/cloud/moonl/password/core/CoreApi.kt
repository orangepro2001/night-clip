package cloud.moonl.password.core

import cloud.moonl.password.UpdateState
import org.json.JSONObject

/** Typed result of a facade call: either a value or the core's error code with a user message. */
sealed interface ApiResult<out T> {
    data class Ok<T>(val value: T) : ApiResult<T>
    data class Fail(val code: String, val message: String, val retryAfterS: Int = 0) : ApiResult<Nothing>

    val okOrNull: T? get() = (this as? Ok<T>)?.value
}

/** What `status` reports (api-spec §7.3). */
data class CoreStatus(val deviceName: String = "", val platform: String = "", val eventsDropped: Long = 0) {
    companion object {
        fun from(o: JSONObject) = CoreStatus(
            deviceName = o.optString("device_name"),
            platform = o.optString("platform"),
            eventsDropped = o.optLong("events_dropped"),
        )
    }
}

data class AccountStatus(val state: String = "", val name: String = "", val username: String = "", val error: String = "") {
    companion object {
        fun from(o: JSONObject) = AccountStatus(o.optString("state"), o.optString("name"), o.optString("username"), o.optString("error"))
    }
}

data class SyncStatus(val enabled: Boolean = false, val lastSync: Long = 0, val error: String = "") {
    companion object {
        fun from(o: JSONObject) = SyncStatus(o.optBoolean("enabled"), o.optLong("last_sync"), o.optString("error"))
    }
}

data class PairStatus(val state: String, val code: String = "", val device: String = "", val error: String = "") {
    companion object {
        fun from(o: JSONObject) = PairStatus(o.optString("state"), o.optString("code"), o.optString("device"))
    }
}

/**
 * Type-safe wrapper over [CoreHost.call] (api-spec §7.3 and shell-guide §2) for everything that is not the vault
 * ([VaultApi]). All functions suspend off the main thread.
 */
object CoreApi {
    private suspend fun <T> run(method: String, params: JSONObject = JSONObject(), parse: (JSONObject) -> T): ApiResult<T> {
        val r = CoreHost.call(method, params)
        return if (r.ok) ApiResult.Ok(parse(r.result)) else ApiResult.Fail(r.errorCode, r.userMessage)
    }

    suspend fun status() = run("status", parse = CoreStatus::from)
    suspend fun diagnostics() = run("diagnostics.get") { it.toString(2) }

    suspend fun accountStatus() = run("account.status", parse = AccountStatus::from)
    suspend fun startAccountLogin() = run("account.login") { it.optString("authorization_url") }
    suspend fun completeAccountLogin(callback: String) = run("account.complete", JSONObject().put("callback", callback)) { }
    suspend fun cancelAccountLogin() = run("account.cancel") { }
    suspend fun accountLogout() = run("account.logout") { }
    suspend fun syncStatus() = run("sync.status", parse = SyncStatus::from)
    suspend fun syncEnable(mode: String, key: String) = run("sync.enable", JSONObject().put("mode", mode).put("key", key), parse = SyncStatus::from)
    suspend fun syncNow() = run("sync.now", parse = SyncStatus::from)
    suspend fun syncKey() = run("sync.recovery_key") { it.optString("key") }
    suspend fun syncDisconnect() = run("sync.disconnect", parse = SyncStatus::from)
    suspend fun pairBegin(requester: Boolean) = run(if (requester) "sync.pair_start" else "sync.pair_prepare", parse = PairStatus::from)
    suspend fun pairStatus() = run("sync.pair_status", parse = PairStatus::from)
    suspend fun pairApprove(code: String) = run("sync.pair_approve", JSONObject().put("code", code), PairStatus::from)
    suspend fun pairCancel() = run("sync.pair_cancel") { }

    suspend fun setSetting(key: String, value: String) =
        run("settings.set", JSONObject().put("key", key).put("value", value)) { }

    suspend fun updateStatus() = run("update.status", parse = UpdateState::from)
    suspend fun updateCheck() = run("update.check") { }
    suspend fun updateDownload() = run("update.download") { }
    suspend fun setUpdateAuto(on: Boolean) = run("update.config", JSONObject().put("auto", on)) { }
}
