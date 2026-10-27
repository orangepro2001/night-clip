package com.nightclip.app.logic

import com.nightclip.app.core.NotesPeerStatus
import com.nightclip.app.core.NotesSyncStatus

/** How loudly the sync line is drawn. */
enum class SyncTone { OK, BUSY, WARN, ERROR }

/** The one-line summary above the note list. */
data class SyncLine(val text: String, val tone: SyncTone)

/** Texts and decisions of the notes sync status (notes/api-spec.md §3). Pure; times are Unix milliseconds. */
object NotesSyncLogic {
    const val STATE_IDLE = "idle"
    const val STATE_SYNCING = "syncing"
    const val STATE_ERROR = "error"
    const val STATE_PEER_OLD = "peer_old"

    /** The foreground / screen-open triggers never start a round more often than this. */
    const val MIN_AUTO_SYNC_INTERVAL_MS = 15_000L

    const val NO_PEERS = "还没有配对设备，笔记只保存在本机"
    const val UPDATE_APP = "对方的笔记格式更新，请更新本应用后再同步"

    fun shouldAutoSync(lastStartMs: Long, nowMs: Long): Boolean = lastStartMs <= 0 || nowMs - lastStartMs >= MIN_AUTO_SYNC_INTERVAL_MS || nowMs < lastStartMs

    /** The summary: the most urgent state of any device wins (peer_old > error > syncing > everything fine). */
    fun summary(status: NotesSyncStatus, nowMs: Long): SyncLine {
        val peers = status.peers
        if (peers.isEmpty()) return SyncLine(NO_PEERS, SyncTone.WARN)
        peers.firstOrNull { it.state == STATE_PEER_OLD }?.let { return SyncLine("${displayName(it)}：$UPDATE_APP", SyncTone.ERROR) }
        peers.firstOrNull { it.state == STATE_ERROR }?.let { return SyncLine("${displayName(it)} 同步出错，稍后会自动重试", SyncTone.ERROR) }
        peers.firstOrNull { it.state == STATE_SYNCING }?.let { return SyncLine("正在与 ${displayName(it)} 同步…", SyncTone.BUSY) }
        val behind = peers.sumOf { it.behind.coerceAtLeast(0) }
        val lastOk = peers.maxOf { it.lastSyncAt }
        val last = lastSyncLabel(lastOk, nowMs)
        val pending = status.pendingPush
        return when {
            behind > 0 -> SyncLine("还有 $behind 篇笔记待收取 · $last", SyncTone.BUSY)
            pending > 0 -> SyncLine("$pending 篇笔记待发送 · $last", SyncTone.BUSY)
            peers.none { it.connected } -> SyncLine("对方设备未连接 · $last", SyncTone.WARN)
            lastOk <= 0 -> SyncLine("已连接，还没有同步过", SyncTone.WARN)
            else -> SyncLine("已同步 · $last", SyncTone.OK)
        }
    }

    /** "上次同步 3 分钟前" / "从未同步". */
    fun lastSyncLabel(lastSyncAt: Long, nowMs: Long): String =
        if (lastSyncAt <= 0) "从未同步" else "上次同步 " + Formatters.relativeTime(lastSyncAt / 1000, nowMs / 1000)

    /** One line per device for the expanded panel. */
    fun peerLine(p: NotesPeerStatus, nowMs: Long): String {
        val parts = mutableListOf(displayName(p), if (p.connected) Strings.STATE_CONNECTED else Strings.STATE_OFFLINE, stateLabel(p))
        if (p.state != STATE_PEER_OLD) parts += lastSyncLabel(p.lastSyncAt, nowMs)
        if (p.behind > 0) parts += "待收取 ${p.behind} 篇"
        return parts.joinToString(" · ")
    }

    fun stateLabel(p: NotesPeerStatus): String = when (p.state) {
        STATE_IDLE -> "空闲"
        STATE_SYNCING -> "同步中"
        STATE_ERROR -> "同步出错"
        STATE_PEER_OLD -> "需要更新本应用"
        else -> p.state
    }

    fun displayName(p: NotesPeerStatus): String = p.name.ifBlank { Strings.UNKNOWN_DEVICE }

    /** Applies a `notes.sync.updated` event; null = the device is not in the list yet, ask the core again. */
    fun applyUpdated(status: NotesSyncStatus, peerId: String, state: String, behind: Int): NotesSyncStatus? {
        if (status.peers.none { it.peerId == peerId }) return null
        return status.copy(peers = status.peers.map { if (it.peerId == peerId) it.copy(state = state.ifEmpty { it.state }, behind = behind.coerceAtLeast(0)) else it })
    }

    /** The manual "sync now" button is only useful when there is somebody to sync with. */
    fun canSyncNow(status: NotesSyncStatus): Boolean = status.peers.isNotEmpty()

    /** Any device that needs this app to be updated. */
    fun needsAppUpdate(status: NotesSyncStatus): Boolean = status.peers.any { it.state == STATE_PEER_OLD }
}
