package com.nightclip.app.core

import com.nightclip.app.logic.Strings

/** Maps core error codes (api-spec §1.4) to messages for people. Only the code is relied upon. */
object ErrorMessages {
    private val messages = mapOf(
        "NO_TAILNET" to "没有检测到 Tailscale 网络，请确认 Tailscale 已登录并连接",
        "LISTEN_FAILED" to "Tailscale 已连接，但无法监听端口（被占用或被系统保留）",
        "NOT_PAIRED" to "该设备尚未配对，或已被对方解除配对",
        "PAIRING_EXPIRED" to "配对已过期，请在电脑上重新生成配对码",
        "PAIRING_FAILED" to "配对失败，请确认配对码、地址和密钥无误后重试",
        "PEER_OFFLINE" to "对方设备当前不在线",
        "TARGET_MISMATCH" to "目标设备不匹配",
        "SOURCE_MISMATCH" to "来源设备不匹配",
        "EXPIRED" to "已过期",
        "HASH_MISMATCH" to Strings.FILE_HASH_MISMATCH,
        "SEQUENCE_GAP" to "消息出现缺口，正在重新同步",
        "TOO_LARGE" to "内容或文件太大",
        "NO_SPACE" to "存储空间不足",
        "RATE_LIMITED" to "操作太频繁，或已有一个配对正在进行，请稍后再试",
        "INVALID" to "参数无效，请检查输入",
        "DB_UNAVAILABLE" to "本地数据库无法打开，数据目录可能不可写或已损坏",
        "KEY_INVALID" to "数据库密钥与已有数据库不匹配，无法解密本机数据",
        "NOT_INITIALIZED" to Strings.ERR_NOT_READY,
        "METHOD_NOT_FOUND" to "核心不支持该功能，请更新应用",
        "UPDATE_DISABLED" to "此版本未启用自动更新",
        "UPDATE_FAILED" to "无法访问更新服务器，请确认已连接 Tailscale 后重试",
        "UPDATE_REJECTED" to "更新信息的签名不受信任，已拒绝（请联系开发者）",
        "INTERNAL" to "内部错误",
        "KEY_CORRUPT" to Strings.ERR_KEY_CORRUPT,
        "KEYSTORE_UNAVAILABLE" to Strings.ERR_KEY_STORE,
        // Password vault (docs/vault-api.md §2); logic/VaultErrors adds context and the rate-limit countdown.
        "VAULT_LOCKED" to "密码库已锁定，请先解锁",
        "VAULT_WRONG_CREDENTIALS" to "主密码或密钥文件不对，或文件已损坏。可以从备份恢复。",
        "VAULT_NOT_FOUND" to "找不到该条目、分组或备份",
        "VAULT_EXISTS" to "密码库或目标文件已存在",
        "VAULT_UNSUPPORTED" to "这个库的加密参数不受支持或不安全。",
        "VAULT_NOT_EMPTY" to "这个分组里还有条目或子分组，请先删除或移走它们",
        "VAULT_REVIEW" to "需要重新确认对方的文件，请刷新后再批准",
        "VAULT_BROKEN" to "密码库状态不一致，请先锁定再重新解锁。",
    )

    /** Known code -> its message; unknown code -> generic text plus the code; no code -> generic text. */
    fun forCode(code: String, @Suppress("UNUSED_PARAMETER") message: String = ""): String {
        if (code.isBlank()) return Strings.ERR_GENERIC
        return messages[code] ?: Strings.errUnknown(code)
    }

    /** Whether a core-start failure can be fixed by wiping the local data (offered on the error page). */
    fun isResettable(code: String): Boolean = code in setOf("KEY_INVALID", "DB_UNAVAILABLE", "KEY_CORRUPT")

    /** Extracts the code from an exception thrown by `Core.init`; the Go error text is "CODE: message". */
    fun codeFromInitFailure(message: String?): String {
        val m = Regex("^([A-Z][A-Z_]+):").find(message?.trim().orEmpty()) ?: return "INTERNAL"
        return m.groupValues[1]
    }
}
