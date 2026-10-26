package com.nightclip.app.logic

import com.nightclip.app.core.DiffItem
import com.nightclip.app.core.RestorePreview
import com.nightclip.app.core.VaultBackup

/** A group of differences in the restore preview. */
data class DiffSection(val kind: Kind, val title: String, val hint: String, val items: List<DiffItem>) {
    enum class Kind { ONLY_IN_BACKUP, CHANGED, ONLY_IN_CURRENT }
}

/** Pure helpers of the backups and restore screens. */
object BackupLogic {
    /**
     * A snapshot is encrypted with the master password of the time it was taken. A "rekey" snapshot (taken just before
     * a master-password change) and every older one therefore need the OLD password; so does the previous generation
     * file once a change happened. This is a hint for the verify dialog; the user can always say otherwise.
     */
    fun mayNeedOldPassword(backups: List<VaultBackup>, target: VaultBackup): Boolean {
        val newestRekey = backups.filter { it.reason == "rekey" }.maxOfOrNull { it.time } ?: return false
        return if (target.reason == "previous") true else target.time <= newestRekey
    }

    /** Preview grouped for display. Empty groups are left out; the order is "comes back", "differs", "would be lost". */
    fun group(p: RestorePreview): List<DiffSection> = buildList {
        if (p.onlyInBackup.isNotEmpty()) add(DiffSection(DiffSection.Kind.ONLY_IN_BACKUP, "备份里有、当前没有（${p.onlyInBackup.size}）", "恢复后会回来", p.onlyInBackup))
        if (p.changed.isNotEmpty()) add(DiffSection(DiffSection.Kind.CHANGED, "内容不同（${p.changed.size}）", "显示的是备份里的修改时间", p.changed))
        if (p.onlyInCurrent.isNotEmpty()) add(DiffSection(DiffSection.Kind.ONLY_IN_CURRENT, "当前有、备份没有（${p.onlyInCurrent.size}）", "整库替换会丢掉这些，但它们仍在恢复前自动拍的快照里", p.onlyInCurrent))
    }

    /** One line: "3 条会找回，2 条内容不同，1 条仅在当前库，4 条相同". */
    fun summary(p: RestorePreview): String {
        val parts = mutableListOf<String>()
        if (p.onlyInBackup.isNotEmpty()) parts += "${p.onlyInBackup.size} 条会找回"
        if (p.changed.isNotEmpty()) parts += "${p.changed.size} 条内容不同"
        if (p.onlyInCurrent.isNotEmpty()) parts += "${p.onlyInCurrent.size} 条仅在当前库"
        if (p.same > 0) parts += "${p.same} 条相同"
        return if (parts.isEmpty()) "备份与当前库没有差别" else parts.joinToString("，")
    }

    /** Ids that "restore selected entries" can act on: the ones the backup has and the current vault lacks or has differently. */
    fun selectableIds(p: RestorePreview): List<String> = (p.onlyInBackup + p.changed).map { it.id }.distinct()

    /** What is ticked when the list opens: entries that would come back. */
    fun defaultSelection(p: RestorePreview): Set<String> = p.onlyInBackup.map { it.id }.toSet()

    fun toggle(selected: Set<String>, id: String): Set<String> = if (id in selected) selected - id else selected + id

    /** The selection restricted to ids that exist in the preview (guards against stale ids). */
    fun cleanSelection(p: RestorePreview, selected: Set<String>): List<String> = selectableIds(p).filter { it in selected }

    /** "Restore entries" needs at least one entry; merge and replace need nothing. */
    fun canRestore(mode: String, selectedCount: Int): Boolean = when (mode) {
        VaultTexts.RESTORE_ENTRIES -> selectedCount > 0
        VaultTexts.RESTORE_MERGE, VaultTexts.RESTORE_REPLACE -> true
        else -> false
    }

    /** Whether the mode needs the explicit "this replaces the whole vault" confirmation. */
    fun needsExplicitConfirm(mode: String): Boolean = mode == VaultTexts.RESTORE_REPLACE

    const val SAFETY_NOTE = "恢复之前会先自动给当前密码库拍一张快照，所以这次恢复可以撤销（在备份列表里找“恢复前自动备份”）。"

    /** Days without a manual backup after which the screen nudges the user (plan-vault §8.1 L3). */
    const val STALE_BACKUP_DAYS = 30L

    fun backupIsStale(lastManualBackupAtS: Long, nowS: Long): Boolean =
        lastManualBackupAtS <= 0 || nowS - lastManualBackupAtS > STALE_BACKUP_DAYS * 86400
}
