package com.nightclip.app.logic

import com.nightclip.app.core.MergeStats

/**
 * User-facing text of the password vault for code without a Context (pure logic, unit-tested).
 * Compose screens use res/values/strings_vault.xml; this object holds labels that the logic classes map codes to.
 */
object VaultTexts {
    // Kinds (vault-api §3.2)
    const val KIND_LOGIN = "login"
    const val KIND_APIKEY = "apikey"
    const val KIND_SECRET = "secret"
    const val KIND_NOTE = "note"
    val KINDS = listOf(KIND_LOGIN, KIND_APIKEY, KIND_SECRET, KIND_NOTE)

    fun kindLabel(kind: String): String = when (kind) {
        KIND_LOGIN -> "账号"
        KIND_APIKEY -> "API 密钥"
        KIND_SECRET -> "机密"
        KIND_NOTE -> "安全笔记"
        else -> "条目"
    }

    // List filters
    fun filterLabel(filter: VaultFilter): String = when (filter) {
        VaultFilter.ALL -> "全部"
        VaultFilter.LOGIN -> "账号"
        VaultFilter.APIKEY -> "API 密钥"
        VaultFilter.SECRET -> "机密"
        VaultFilter.NOTE -> "笔记"
        VaultFilter.EXPIRING -> "即将到期"
        VaultFilter.TRASH -> "回收站"
    }

    // Password strength (vault.strength labels)
    fun strengthLabel(label: String): String = when (label) {
        "very_weak" -> "很弱"
        "weak" -> "弱"
        "fair" -> "一般"
        "good" -> "较强"
        "strong" -> "强"
        else -> "未知"
    }

    // Backups (vault-api §3.5: reason)
    fun backupReason(reason: String): String = when (reason) {
        "save" -> "保存前自动备份"
        "delete" -> "删除前自动备份"
        "import" -> "导入前自动备份"
        "restore" -> "恢复前自动备份"
        "rekey" -> "更换主密码前自动备份"
        "merge" -> "合并前自动备份"
        "create" -> "新建密码库时"
        "upgrade" -> "升级前自动备份"
        "manual" -> "手动备份"
        "previous" -> "上一代文件（上次保存之前）"
        else -> "其他（$reason）"
    }

    // Restore modes
    const val RESTORE_ENTRIES = "entries"
    const val RESTORE_MERGE = "merge"
    const val RESTORE_REPLACE = "replace"

    fun restoreModeLabel(mode: String): String = when (mode) {
        RESTORE_ENTRIES -> "只恢复选中的条目"
        RESTORE_MERGE -> "合并到当前库"
        RESTORE_REPLACE -> "整库替换为备份"
        else -> mode
    }

    // Expiry
    fun expiryLabel(expiresAtS: Long, nowS: Long): String {
        if (expiresAtS <= 0) return ""
        if (expiresAtS <= nowS) return "已过期"
        val days = (expiresAtS - nowS + 86399) / 86400
        return if (days <= 1) "1 天内到期" else "$days 天后到期"
    }

    /** "2026-10-26 14:30" in the phone's time zone; "" for 0 (unknown). */
    fun dateTime(epochSeconds: Long, zone: java.time.ZoneId = java.time.ZoneId.systemDefault()): String =
        if (epochSeconds <= 0) "" else java.time.format.DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm").withZone(zone).format(java.time.Instant.ofEpochSecond(epochSeconds))

    // Result of a merge / import / restore
    fun mergeStatsText(s: MergeStats): String {
        val parts = mutableListOf<String>()
        if (s.entriesAdded > 0) parts += "新增 ${s.entriesAdded} 条"
        if (s.entriesUpdated > 0) parts += "更新 ${s.entriesUpdated} 条"
        if (s.entriesRemoved > 0) parts += "删除 ${s.entriesRemoved} 条"
        if (s.entriesMoved > 0) parts += "移动 ${s.entriesMoved} 条"
        if (s.groupsAdded > 0) parts += "新增分组 ${s.groupsAdded} 个"
        if (s.groupsRemoved > 0) parts += "删除分组 ${s.groupsRemoved} 个"
        return if (parts.isEmpty()) "没有变化" else parts.joinToString("，")
    }

    // Health report
    const val HEALTH_WEAK = "弱密码"
    const val HEALTH_REUSED = "重复使用的密码"
    const val HEALTH_OLD = "超过一年没换的密码"
    const val HEALTH_EXPIRED = "已过期"

    // Messages that are not errors
    const val COPIED = "已复制"
    const val COPY_FAILED = "无法写入剪贴板，请保持应用在前台后重试"
    fun copiedClears(seconds: Int) = if (seconds > 0) "已复制，$seconds 秒后自动清除" else "已复制（不会自动清除）"
    const val SAVED = "已保存"
    const val DELETED_TO_TRASH = "已移入回收站"
    const val DELETED_FOREVER = "已永久删除"
    const val RESTORED = "已恢复"
    const val LOCKED_NOTICE = "密码库已锁定"
    const val BACKUP_DONE = "已备份"
    const val NOT_A_WEB_URL = "只能打开 http 或 https 链接"
    const val NO_BROWSER = "没有可以打开链接的应用"
    const val KEY_FILE_TOO_BIG = "密钥文件不能超过 1 MiB"
    const val FILE_READ_FAILED = "无法读取所选文件"
    const val FILE_WRITE_FAILED = "无法写入所选位置"
    const val ATTACHMENT_TOO_BIG = "附件不能超过 5 MiB"
}
