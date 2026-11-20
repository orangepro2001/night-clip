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

    suspend fun setSetting(key: String, value: String) =
        run("settings.set", JSONObject().put("key", key).put("value", value)) { }

    suspend fun updateStatus() = run("update.status", parse = UpdateState::from)
    suspend fun updateCheck() = run("update.check") { }
    suspend fun updateDownload() = run("update.download") { }
    suspend fun setUpdateAuto(on: Boolean) = run("update.config", JSONObject().put("auto", on)) { }
}
