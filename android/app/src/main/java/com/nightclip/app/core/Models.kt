package com.nightclip.app.core

import org.json.JSONArray
import org.json.JSONObject

/** Peer entry of `peers.list` / `status.peers` (api-spec §7.3). */
data class PeerInfo(
    val deviceId: String,
    val name: String,
    val platform: String,
    val state: String,
    val addrs: List<String> = emptyList(),
    val outboxPending: Int = 0,
    val lastSeenAt: Long = 0,
    val applyMode: String = "notify",
) {
    /** Clipboard/file sending only makes sense to peers that still trust us. */
    val canSend: Boolean get() = state != "revoked" && state != "unpaired"
    val isConnected: Boolean get() = state == "connected"
}

/** Entry of `inbox.list`. */
data class InboxItem(
    val eventId: String,
    val from: String,
    val fromName: String,
    val text: String,
    val sensitive: Boolean,
    val state: String,
    val receivedAt: Long,
    val expiresAt: Long = 0,
    val appliedAt: Long = 0,
) {
    override fun toString() = "InboxItem(eventId=$eventId, state=$state)" // never print clipboard text
}

/** Entry of `transfers.list`, also kept up to date from file.* events. */
data class TransferInfo(
    val transferId: String,
    val peerId: String = "",
    val peerName: String = "",
    val direction: String = "in",
    val name: String = "",
    val size: Long = 0,
    val sha256: String = "",
    val state: String = "offered",
    val offset: Long = 0,
    val rateBps: Long = 0,
    val error: String = "",
    val path: String = "",
    val createdAt: Long = 0,
    val expiresAt: Long = 0,
) {
    val incoming: Boolean get() = direction == "in"

    /** States in which the user can still cancel / the progress UI is shown. */
    val active: Boolean get() = state in ACTIVE_STATES
    val terminal: Boolean get() = state in TERMINAL_STATES
    val awaitingMyDecision: Boolean get() = incoming && state == "offered"

    companion object {
        val ACTIVE_STATES = setOf("hashing", "offered", "accepted", "transferring")
        val TERMINAL_STATES = setOf("done", "rejected", "cancelled", "failed", "expired")
    }
}

/** `settings.get` (all keys) in typed form. Values keep the core's defaults when a key is missing. */
data class CoreSettings(
    val capture: Boolean = false,
    val clipTtlS: Int = 900,
    val historyLimit: Int = 200,
    val sendSensitive: Boolean = true,
    val defaultApplyMode: String = "notify",
)

/** `clip.send` / `clip.local_changed` result. */
data class SendResult(val sentCount: Int, val suppressed: String = "")

/** `status` result, trimmed to what the shell shows. */
data class StatusInfo(
    val deviceId: String,
    val deviceName: String,
    val platform: String,
    val peers: List<PeerInfo>,
    val capture: Boolean,
    val eventsDropped: Long,
) {
    val connectedCount: Int get() = peers.count { it.isConnected }
}

object ModelParsers {
    private fun JSONArray?.objects(): List<JSONObject> =
        if (this == null) emptyList() else (0 until length()).mapNotNull { optJSONObject(it) }

    private fun JSONArray?.strings(): List<String> =
        if (this == null) emptyList() else (0 until length()).map { optString(it, "") }

    fun peer(o: JSONObject) = PeerInfo(
        deviceId = o.optString("device_id", ""),
        name = o.optString("name", ""),
        platform = o.optString("platform", ""),
        state = o.optString("state", "offline"),
        addrs = o.optJSONArray("tailnet_addrs").strings(),
        outboxPending = o.optInt("outbox_pending", 0),
        lastSeenAt = o.optLong("last_seen_at", 0),
        applyMode = o.optString("apply_mode", "notify"),
    )

    /** Parses the `result` of `peers.list`. */
    fun peers(result: JSONObject): List<PeerInfo> = result.optJSONArray("peers").objects().map(::peer)

    fun inboxItem(o: JSONObject) = InboxItem(
        eventId = o.optString("event_id", ""),
        from = o.optString("from", ""),
        fromName = o.optString("from_name", ""),
        text = o.optString("text", ""),
        sensitive = o.optBoolean("sensitive", false),
        state = o.optString("state", "received"),
        receivedAt = o.optLong("received_at", 0),
        expiresAt = o.optLong("expires_at", 0),
        appliedAt = o.optLong("applied_at", 0),
    )

    /** Parses the `result` of `inbox.list` (newest first, as the core returns it). */
    fun inbox(result: JSONObject): List<InboxItem> = result.optJSONArray("items").objects().map(::inboxItem)

    fun transfer(o: JSONObject) = TransferInfo(
        transferId = o.optString("transfer_id", ""),
        peerId = o.optString("peer_id", ""),
        peerName = o.optString("peer_name", ""),
        direction = o.optString("direction", "in"),
        name = o.optString("name", ""),
        size = o.optLong("size", 0),
        sha256 = o.optString("sha256", ""),
        state = o.optString("state", "offered"),
        offset = o.optLong("offset", 0),
        rateBps = o.optLong("rate_bps", 0),
        error = o.optString("error", ""),
        path = o.optString("path", ""),
        createdAt = o.optLong("created_at", 0),
        expiresAt = o.optLong("expires_at", 0),
    )

    /** Parses the `result` of `transfers.list`. */
    fun transfers(result: JSONObject): List<TransferInfo> = result.optJSONArray("transfers").objects().map(::transfer)

    fun settings(result: JSONObject): CoreSettings {
        val s = result.optJSONObject("settings") ?: result
        val d = CoreSettings()
        return CoreSettings(
            capture = s.optString("capture", "off") == "on",
            clipTtlS = s.optString("clip_ttl_s", "").toIntOrNull() ?: d.clipTtlS,
            historyLimit = s.optString("history_limit", "").toIntOrNull() ?: d.historyLimit,
            sendSensitive = s.optString("send_sensitive", "on") != "off",
            defaultApplyMode = s.optString("default_apply_mode", d.defaultApplyMode),
        )
    }

    fun sendResult(result: JSONObject): SendResult =
        SendResult(result.optJSONArray("sent")?.length() ?: 0, result.optString("suppressed", ""))

    fun status(result: JSONObject) = StatusInfo(
        deviceId = result.optString("device_id", ""),
        deviceName = result.optString("device_name", ""),
        platform = result.optString("platform", ""),
        peers = peers(result),
        capture = result.optString("capture", "off") == "on",
        eventsDropped = result.optLong("events_dropped", 0),
    )
}
