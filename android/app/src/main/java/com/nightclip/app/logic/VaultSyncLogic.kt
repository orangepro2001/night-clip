package com.nightclip.app.logic

import com.nightclip.app.core.SyncPeer

/** Why a sync round stopped in the `review` state; the core only gives an English sentence, so it is classified here. */
enum class ReviewKind { DIFFERENT_VAULT, MANY_DELETIONS, UNKNOWN }

/** How the locked screen treats the key-file picker, from `vault.status.key_file`. */
enum class KeyFileHint {
    /** The last credentials on this device used a key file. */
    EXPECTED,

    /** Unknown (the vault arrived by sync and was never unlocked here): offer it, optional. */
    OPTIONAL,

    /** The last credentials had none: the picker is tucked behind a link. */
    NOT_USED,
    ;

    companion object {
        fun of(keyFile: Boolean?): KeyFileHint = when (keyFile) {
            true -> EXPECTED
            null -> OPTIONAL
            false -> NOT_USED
        }
    }
}

/** Texts, ordering and decisions of the device-sync screen (docs/vault-api.md §3.6). Pure. */
object VaultSyncLogic {
    const val STATE_IDLE = "idle"
    const val STATE_PENDING_UNLOCK = "pending_unlock"
    const val STATE_NEEDS_PASSWORD = "needs_password"
    const val STATE_REVIEW = "review"
    const val STATE_PEER_OFF = "peer_off"
    const val STATE_ERROR = "error"

    /** The core stops a batch that deletes this many entries or more until the user approves. */
    const val DELETION_REVIEW_THRESHOLD = 5

    const val HINT_NO_VAULT = "已有密码库在另一台设备上？到“设备同步”里开启，再回来用那个库的主密码解锁。"

    const val EXPLAINER = "同步只传输加密的密码库文件，对方没有主密码就读不了内容；两台设备都要为对方开启开关才会同步，默认什么都不共享。" +
        "关掉某台设备只是停止之后的同步，不会清除它已经拿到的内容——如果那台设备丢了或不再可信，请更换主密码。"

    fun reviewKind(detail: String): ReviewKind {
        val d = detail.lowercase()
        return when {
            "different vault" in d -> ReviewKind.DIFFERENT_VAULT
            "delete" in d -> ReviewKind.MANY_DELETIONS
            else -> ReviewKind.UNKNOWN
        }
    }

    fun reviewText(kind: ReviewKind): String = when (kind) {
        ReviewKind.DIFFERENT_VAULT ->
            "对方的库不是同一个库（它不是从这个库复制出来的）。批准后两个库的内容会合并到一起，之后互相同步。如果你不认识对方这个库，请选“忽略”。"
        ReviewKind.MANY_DELETIONS ->
            "对方这一批修改会一次删除 $DELETION_REVIEW_THRESHOLD 个或更多条目。如果这是你有意为之，请批准并合并；否则选“忽略”，并检查对方设备上发生了什么。"
        ReviewKind.UNKNOWN -> "对方的修改需要你确认后才会合并。如果不确定，请选“忽略”。"
    }

    fun stateLabel(p: SyncPeer): String = when {
        !p.enabled -> "未开启"
        else -> when (p.state) {
            STATE_IDLE -> if (p.connected) "已是最新" else "等待连接"
            STATE_PENDING_UNLOCK -> "等待解锁后合并"
            STATE_NEEDS_PASSWORD -> "需要对方的主密码"
            STATE_REVIEW -> "需要你批准"
            STATE_PEER_OFF -> "对方未开启"
            STATE_ERROR -> "同步出错"
            else -> p.state
        }
    }

    /** One or two sentences under the device name. */
    fun stateExplanation(p: SyncPeer): String = when {
        !p.enabled -> "打开右边的开关，并在对方设备上也为本机打开，两边就会开始同步。"
        else -> when (p.state) {
            STATE_IDLE -> if (p.pendingIncoming) "已收到对方的修改。" else "两边内容一致。"
            STATE_PENDING_UNLOCK -> "已收到对方的修改，解锁密码库后会自动合并。"
            STATE_NEEDS_PASSWORD -> "对方更换了主密码，需要输入对方现在的主密码才能合并。"
            STATE_REVIEW -> reviewText(reviewKind(p.detail))
            STATE_PEER_OFF -> "对方设备还没有为本机开启密码库同步"
            STATE_ERROR -> if (p.detail.isBlank()) "同步失败，稍后会自动重试。" else "同步失败：" + p.detail.take(120)
            else -> ""
        }
    }

    fun connectionLabel(p: SyncPeer): String = if (p.connected) "已连接" else "未连接"

    fun lastSyncLabel(p: SyncPeer, nowS: Long): String =
        if (p.lastOkAt <= 0) "还没有成功同步过" else "上次同步：" + Formatters.relativeTime(p.lastOkAt, nowS)

    /** States that wait for the user and make the sync entry point show a dot. */
    fun needsAttention(p: SyncPeer): Boolean = p.enabled && (p.state == STATE_NEEDS_PASSWORD || p.state == STATE_REVIEW)

    fun attentionIds(peers: List<SyncPeer>): Set<String> = peers.filter(::needsAttention).map { it.deviceId }.toSet()

    /** Applies a `vault.sync` event to the set of devices that need attention. */
    fun applyStateEvent(attention: Set<String>, deviceId: String, state: String): Set<String> =
        if (state == STATE_NEEDS_PASSWORD || state == STATE_REVIEW) attention + deviceId else attention - deviceId

    /** Enabled devices first, then connected ones, then by name. */
    fun sorted(peers: List<SyncPeer>): List<SyncPeer> =
        peers.sortedWith(compareByDescending<SyncPeer> { it.enabled }.thenByDescending { it.connected }.thenBy(String.CASE_INSENSITIVE_ORDER) { it.name })

    /** Resolving needs the vault open (the core merges into the unlocked vault). */
    fun canResolve(p: SyncPeer, vaultUnlocked: Boolean): Boolean = vaultUnlocked && needsAttention(p)

    /** "立即同步" makes sense when at least one device is switched on. */
    fun canSyncNow(peers: List<SyncPeer>): Boolean = peers.any { it.enabled }

    fun joinedText(name: String): String = "已加入 ${name.ifBlank { Strings.UNKNOWN_DEVICE }} 的密码库，请用那个库的主密码解锁。"

    const val LOCKED_RESOLVE_HINT = "先解锁密码库，才能处理这个请求。"
}
