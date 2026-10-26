package com.nightclip.app.core

import org.json.JSONException
import org.json.JSONObject

/**
 * Typed form of the events returned by `Core.nextEvent` (api-spec §7.4, shell-guide §2).
 * Nothing here is ever logged except [type] and [logId]: texts and paths stay out of logs.
 */
sealed interface CoreEvent {
    val type: String

    /** Identifier that is safe to log (event or transfer id), or empty. */
    val logId: String get() = ""

    data class CoreStarted(val deviceId: String) : CoreEvent { override val type = "core.started" }
    data class CoreListening(val addrs: List<String>, val port: Int) : CoreEvent { override val type = "core.listening" }
    data class CoreWarning(val code: String) : CoreEvent { override val type = "core.warning" }
    data class PeerState(val deviceId: String, val state: String) : CoreEvent { override val type = "peer.state" }
    data class PeerRevoked(val deviceId: String, val name: String) : CoreEvent { override val type = "peer.revoked" }

    data class ClipReceived(
        val eventId: String,
        val from: String,
        val fromName: String,
        val text: String,
        val sensitive: Boolean,
        val decision: String,
        val receivedAt: Long,
    ) : CoreEvent {
        override val type = "clip.received"
        override val logId get() = eventId
        override fun toString() = "ClipReceived(eventId=$eventId, decision=$decision)" // no text
    }

    data class ClipAck(val eventId: String, val from: String, val result: String, val reason: String) : CoreEvent {
        override val type = "clip.ack"
        override val logId get() = eventId
    }

    data class OutboxExpired(val count: Int) : CoreEvent { override val type = "outbox.expired" }

    data class PairSas(val joinId: String, val sas: String) : CoreEvent {
        override val type = "pair.sas"
        override fun toString() = "PairSas(joinId=$joinId)"
    }

    data class PairResult(
        val state: String,
        val role: String,
        val pairId: String,
        val joinId: String,
        val code: String,
        val message: String,
        val deviceId: String,
        val name: String,
    ) : CoreEvent { override val type = "pair.result" }

    data class FileOffered(
        val transferId: String,
        val from: String,
        val fromName: String,
        val name: String,
        val size: Long,
        val sha256: String,
        val mimeType: String,
    ) : CoreEvent {
        override val type = "file.offered"
        override val logId get() = transferId
    }

    data class FileState(val transferId: String, val state: String) : CoreEvent {
        override val type = "file.state"
        override val logId get() = transferId
    }

    data class FileProgress(val transferId: String, val offset: Long, val size: Long, val rateBps: Long) : CoreEvent {
        override val type = "file.progress"
        override val logId get() = transferId
    }

    data class FileDone(
        val transferId: String,
        val direction: String,
        val path: String,
        val name: String,
        val size: Long,
        val sha256: String,
    ) : CoreEvent {
        override val type = "file.done"
        override val logId get() = transferId
    }

    data class FileFailed(val transferId: String, val code: String, val message: String) : CoreEvent {
        override val type = "file.failed"
        override val logId get() = transferId
    }

    data class UpdateAvailable(val versionCode: Long, val versionName: String, val mandatory: Boolean, val notes: String) : CoreEvent {
        override val type = "update.available"
    }

    data class UpdateProgress(val received: Long, val total: Long) : CoreEvent { override val type = "update.progress" }

    data class UpdateReady(val versionCode: Long, val versionName: String, val path: String, val mandatory: Boolean) : CoreEvent {
        override val type = "update.ready"
    }

    data class UpdateError(val code: String, val message: String) : CoreEvent { override val type = "update.error" }

    data class Unknown(override val type: String) : CoreEvent

    companion object {
        /** Parses one event line; returns null for text that is not a JSON object with a type. */
        fun parse(raw: String): CoreEvent? {
            val o = try {
                JSONObject(raw)
            } catch (e: JSONException) {
                return null
            }
            val type = o.optString("type", "")
            if (type.isEmpty()) return null
            return fromJson(type, o)
        }

        private fun fromJson(type: String, o: JSONObject): CoreEvent = when (type) {
            "core.started" -> CoreStarted(o.optString("device_id"))
            "core.listening" -> {
                val arr = o.optJSONArray("addrs")
                CoreListening(if (arr == null) emptyList() else (0 until arr.length()).map { arr.optString(it) }, o.optInt("port"))
            }
            "core.warning" -> CoreWarning(o.optString("code"))
            "peer.state" -> PeerState(o.optString("device_id"), o.optString("state"))
            "peer.revoked" -> PeerRevoked(o.optString("device_id"), o.optString("name"))
            "clip.received" -> ClipReceived(
                eventId = o.optString("event_id"),
                from = o.optString("from"),
                fromName = o.optString("from_name"),
                text = o.optString("text"),
                sensitive = o.optBoolean("sensitive", false),
                decision = o.optString("decision", "notify"),
                receivedAt = o.optLong("received_at", 0),
            )
            "clip.ack" -> ClipAck(o.optString("event_id"), o.optString("from"), o.optString("result"), o.optString("reason"))
            "outbox.expired" -> OutboxExpired(o.optInt("count"))
            "pair.sas" -> PairSas(o.optString("join_id"), o.optString("sas"))
            "pair.result" -> PairResult(
                state = o.optString("state"),
                role = o.optString("role"),
                pairId = o.optString("pair_id"),
                joinId = o.optString("join_id"),
                code = o.optString("code"),
                message = o.optString("message"),
                deviceId = o.optString("device_id"),
                name = o.optString("name"),
            )
            "file.offered" -> FileOffered(
                transferId = o.optString("transfer_id"),
                from = o.optString("from"),
                fromName = o.optString("from_name"),
                name = o.optString("name"),
                size = o.optLong("size"),
                sha256 = o.optString("sha256"),
                mimeType = o.optString("mime_type"),
            )
            "file.state" -> FileState(o.optString("transfer_id"), o.optString("state"))
            "file.progress" -> FileProgress(o.optString("transfer_id"), o.optLong("offset"), o.optLong("size"), o.optLong("rate_bps"))
            "file.done" -> FileDone(
                transferId = o.optString("transfer_id"),
                direction = o.optString("direction", "in"),
                path = o.optString("path"),
                name = o.optString("name"),
                size = o.optLong("size"),
                sha256 = o.optString("sha256"),
            )
            "file.failed" -> FileFailed(o.optString("transfer_id"), o.optString("code"), o.optString("message"))
            "update.available" -> UpdateAvailable(o.optLong("version_code"), o.optString("version_name"), o.optBoolean("mandatory"), o.optString("notes"))
            "update.progress" -> UpdateProgress(o.optLong("received"), o.optLong("total"))
            "update.ready" -> UpdateReady(o.optLong("version_code"), o.optString("version_name"), o.optString("path"), o.optBoolean("mandatory"))
            "update.error" -> UpdateError(o.optString("code"), o.optString("message"))
            else -> Unknown(type)
        }
    }
}
