package com.nightclip.app.core

import com.nightclip.app.UpdateState
import org.json.JSONArray
import org.json.JSONObject

/** Typed result of a facade call: either a value or the core's error code with a user message. */
sealed interface ApiResult<out T> {
    data class Ok<T>(val value: T) : ApiResult<T>
    data class Fail(val code: String, val message: String, val retryAfterS: Int = 0) : ApiResult<Nothing>

    val okOrNull: T? get() = (this as? Ok<T>)?.value
}

/** Type-safe wrapper over [CoreHost.call] (api-spec §7.3 and shell-guide §2). All functions suspend off the main thread. */
object CoreApi {
    private suspend fun <T> run(method: String, params: JSONObject = JSONObject(), parse: (JSONObject) -> T): ApiResult<T> {
        val r = CoreHost.call(method, params)
        return if (r.ok) ApiResult.Ok(parse(r.result)) else ApiResult.Fail(r.errorCode, r.userMessage)
    }

    suspend fun status() = run("status", parse = ModelParsers::status)
    suspend fun identity() = run("identity.get") { it.optString("device_id") to it.optString("fingerprint") }
    suspend fun diagnostics() = run("diagnostics.get") { it.toString(2) }

    suspend fun settings() = run("settings.get", parse = ModelParsers::settings)
    suspend fun setSetting(key: String, value: String) =
        run("settings.set", JSONObject().put("key", key).put("value", value)) { }

    suspend fun peers() = run("peers.list", parse = ModelParsers::peers)
    suspend fun renamePeer(id: String, name: String) =
        run("peers.update", JSONObject().put("device_id", id).put("name", name)) { }
    suspend fun setPeerSend(id: String, enabled: Boolean) =
        run("peers.update", JSONObject().put("device_id", id).put("send_enabled", enabled)) { }
    suspend fun setPeerMode(id: String, mode: String) =
        run("peers.update", JSONObject().put("device_id", id).put("apply_mode", mode)) { }
    suspend fun removePeer(id: String) = run("peers.remove", JSONObject().put("device_id", id)) { }

    /** Joiner side. Returns the join id; progress arrives as pair.sas / pair.result events. */
    suspend fun joinWithLink(link: String) =
        run("pair.join", JSONObject().put("link", link)) { it.optString("join_id") }
    suspend fun joinManual(host: String, port: Int, psk: String, fp: String?) =
        run("pair.join", JSONObject().put("host", host).put("port", port).put("psk", psk).apply { if (fp != null) put("fp", fp) }) {
            it.optString("join_id")
        }

    suspend fun sendClip(text: String, to: List<String>? = null) =
        run("clip.send", JSONObject().put("text", text).apply { if (!to.isNullOrEmpty()) put("to", JSONArray(to)) }, ModelParsers::sendResult)
    /** The user copied something (automatic path): the core applies capture, pause, echo and duplicate rules. */
    suspend fun localChanged(text: String) =
        run("clip.local_changed", JSONObject().put("text", text), ModelParsers::sendResult)
    /** Open the core's echo window BEFORE the shell writes the clipboard. */
    suspend fun beginApply(eventId: String) = run("clip.begin_apply", JSONObject().put("event_id", eventId)) { }

    suspend fun markApplied(eventId: String) = run("clip.mark_applied", JSONObject().put("event_id", eventId)) { }
    suspend fun dismiss(eventId: String) = run("clip.dismiss", JSONObject().put("event_id", eventId)) { }

    suspend fun inbox(limit: Int = 100) = run("inbox.list", JSONObject().put("limit", limit), ModelParsers::inbox)
    suspend fun inboxItem(eventId: String) =
        run("inbox.get", JSONObject().put("event_id", eventId)) { r -> ModelParsers.inboxItem(r.optJSONObject("item") ?: r) }
    suspend fun clearInbox() = run("inbox.clear") { }

    suspend fun transfers(limit: Int = 50) = run("transfers.list", JSONObject().put("limit", limit), ModelParsers::transfers)

    /** [fd] comes from ParcelFileDescriptor.detachFd(): the core owns it from this call on, success or not. */
    suspend fun offerFile(to: String, fd: Int, name: String, size: Long) =
        run("file.offer", JSONObject().put("to", to).put("fd", fd).put("name", name).put("size", size)) { it.optString("transfer_id") }
    suspend fun acceptFile(id: String) = run("file.accept", JSONObject().put("transfer_id", id)) { }
    suspend fun rejectFile(id: String) = run("file.reject", JSONObject().put("transfer_id", id)) { }
    suspend fun cancelFile(id: String) = run("file.cancel", JSONObject().put("transfer_id", id)) { }
    suspend fun releaseFile(id: String) = run("file.release", JSONObject().put("transfer_id", id)) { }

    suspend fun updateStatus() = run("update.status", parse = UpdateState::from)
    suspend fun updateCheck() = run("update.check") { }
    suspend fun updateDownload() = run("update.download") { }
    suspend fun setUpdateAuto(on: Boolean) = run("update.config", JSONObject().put("auto", on)) { }
}
