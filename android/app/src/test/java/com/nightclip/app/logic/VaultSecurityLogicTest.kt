package com.nightclip.app.logic

import com.nightclip.app.core.DiffItem
import com.nightclip.app.core.MergeStats
import com.nightclip.app.core.RestorePreview
import com.nightclip.app.core.VaultBackup
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ClipClearPolicyTest {
    private val secret = "s3cr3t-value"
    private val hash = ClipClearPolicy.hash(secret)
    private val marker = ClipClearPolicy.MARKER_LABEL

    @Test
    fun readableAndStillOursIsCleared() {
        assertEquals(ClipClearPolicy.Decision.CLEAR, ClipClearPolicy.decide(ClipClearPolicy.Snapshot(secret, marker), hash))
        // the text is what counts, even if some app rewrote the label
        assertEquals(ClipClearPolicy.Decision.CLEAR, ClipClearPolicy.decide(ClipClearPolicy.Snapshot(secret, "other"), hash))
    }

    @Test
    fun somethingElseOnTheClipboardIsLeftAlone() {
        assertEquals(ClipClearPolicy.Decision.KEEP, ClipClearPolicy.decide(ClipClearPolicy.Snapshot("user copied this", "ClipBridge"), hash))
        // same label, different text: the user copied a second vault value in between? it is not what the timer was started for
        assertEquals(ClipClearPolicy.Decision.KEEP, ClipClearPolicy.decide(ClipClearPolicy.Snapshot("another", marker), hash))
    }

    @Test
    fun unreadableContentFallsBackToTheMarker() {
        assertEquals(ClipClearPolicy.Decision.CLEAR, ClipClearPolicy.decide(ClipClearPolicy.Snapshot(null, marker), hash))
        assertEquals(ClipClearPolicy.Decision.KEEP, ClipClearPolicy.decide(ClipClearPolicy.Snapshot(null, "Chrome"), hash))
    }

    @Test
    fun nothingReadableMeansClearUnverified() {
        assertEquals(ClipClearPolicy.Decision.CLEAR_UNVERIFIED, ClipClearPolicy.decide(ClipClearPolicy.Snapshot(null, null), hash))
    }

    @Test
    fun hashIsStableAndDistinct() {
        assertEquals(64, hash.length)
        assertEquals(hash, ClipClearPolicy.hash(secret))
        assertNotEquals(hash, ClipClearPolicy.hash(secret + " "))
        assertFalse(ClipClearPolicy.Snapshot(secret, marker).toString().contains(secret))
    }

    @Test
    fun delays() {
        assertNull(ClipClearPolicy.delayMs(0))
        assertNull(ClipClearPolicy.delayMs(-5))
        assertEquals(20_000L, ClipClearPolicy.delayMs(20))
        assertEquals(3_600_000L, ClipClearPolicy.delayMs(999_999))
    }

    @Test
    fun theMarkerIsNotTheLabelOfANormalCopy() {
        assertNotEquals("ClipBridge", ClipClearPolicy.MARKER_LABEL)
        assertEquals("ClipBridge vault", ClipClearPolicy.MARKER_LABEL)
    }
}

class QuickUnlockPolicyTest {
    private val hour = 3600_000L
    private val t0 = 1_000_000_000_000L
    private val ready = QuickUnlockState(enabled = true, hasSecret = true, lastMasterUnlockAtMs = t0, failures = 0, bootCountAtMasterUnlock = 7)

    @Test
    fun availableWithinTheRules() {
        assertEquals(QuickUnlockAvailability.AVAILABLE, QuickUnlockPolicy.check(ready, t0 + 71 * hour, 7))
        assertEquals(QuickUnlockAvailability.AVAILABLE, QuickUnlockPolicy.check(ready.copy(failures = 4), t0 + hour, 7))
    }

    @Test
    fun disabledOrWithoutSecret() {
        assertEquals(QuickUnlockAvailability.DISABLED, QuickUnlockPolicy.check(ready.copy(enabled = false), t0, 7))
        assertEquals(QuickUnlockAvailability.NO_SECRET, QuickUnlockPolicy.check(ready.copy(hasSecret = false), t0, 7))
        assertEquals(QuickUnlockAvailability.DISABLED, QuickUnlockPolicy.check(QuickUnlockState(), t0, 7))
    }

    @Test
    fun after72HoursItExpires() {
        assertEquals(QuickUnlockAvailability.EXPIRED, QuickUnlockPolicy.check(ready, t0 + 72 * hour, 7))
        assertEquals(QuickUnlockAvailability.EXPIRED, QuickUnlockPolicy.check(ready, t0 + 100 * hour, 7))
    }

    @Test
    fun aClockInThePastOrNeverUnlockedExpires() {
        assertEquals(QuickUnlockAvailability.EXPIRED, QuickUnlockPolicy.check(ready, t0 - 1, 7))
        assertEquals(QuickUnlockAvailability.EXPIRED, QuickUnlockPolicy.check(ready.copy(lastMasterUnlockAtMs = 0), t0, 7))
    }

    @Test
    fun fiveFailuresInARowStopIt() {
        assertEquals(QuickUnlockAvailability.TOO_MANY_FAILURES, QuickUnlockPolicy.check(ready.copy(failures = 5), t0 + hour, 7))
    }

    @Test
    fun aRebootEndsItWhenTheBootCountIsKnown() {
        assertEquals(QuickUnlockAvailability.REBOOTED, QuickUnlockPolicy.check(ready, t0 + hour, 8))
        // the rule is skipped when either count is unreadable
        assertEquals(QuickUnlockAvailability.AVAILABLE, QuickUnlockPolicy.check(ready, t0 + hour, null))
        assertEquals(QuickUnlockAvailability.AVAILABLE, QuickUnlockPolicy.check(ready.copy(bootCountAtMasterUnlock = null), t0 + hour, 9))
    }

    @Test
    fun purgeAndExplanations() {
        for (a in listOf(QuickUnlockAvailability.EXPIRED, QuickUnlockAvailability.TOO_MANY_FAILURES, QuickUnlockAvailability.REBOOTED)) {
            assertTrue(a.shouldPurge)
            assertTrue(a.explanation!!.contains("主密码"))
        }
        for (a in listOf(QuickUnlockAvailability.AVAILABLE, QuickUnlockAvailability.DISABLED, QuickUnlockAvailability.NO_SECRET)) {
            assertFalse(a.shouldPurge)
            assertNull(a.explanation)
        }
    }

    @Test
    fun masterUnlockRestartsTheClockQuickUnlockDoesNot() {
        val s = ready.copy(failures = 3)
        val m = QuickUnlockPolicy.afterMasterUnlock(s, t0 + 50 * hour, 9)
        assertEquals(t0 + 50 * hour, m.lastMasterUnlockAtMs)
        assertEquals(0, m.failures)
        assertEquals(9, m.bootCountAtMasterUnlock)
        val q = QuickUnlockPolicy.afterQuickSuccess(s)
        assertEquals(t0, q.lastMasterUnlockAtMs)
        assertEquals(0, q.failures)
    }

    @Test
    fun failuresAccumulateUntilTheLimit() {
        var s = ready
        repeat(4) { s = QuickUnlockPolicy.afterQuickFailure(s) }
        assertEquals(QuickUnlockAvailability.AVAILABLE, QuickUnlockPolicy.check(s, t0 + hour, 7))
        s = QuickUnlockPolicy.afterQuickFailure(s)
        assertEquals(QuickUnlockAvailability.TOO_MANY_FAILURES, QuickUnlockPolicy.check(s, t0 + hour, 7))
    }

    @Test
    fun wrongPasswordMasterChangeAndDisableInvalidate() {
        val s = QuickUnlockPolicy.afterInvalidate(ready.copy(failures = 2))
        assertFalse(s.enabled)
        assertFalse(s.hasSecret)
        assertEquals(0, s.failures)
        assertTrue(QuickUnlockPolicy.invalidatesOn("VAULT_WRONG_CREDENTIALS"))
        assertFalse(QuickUnlockPolicy.invalidatesOn("RATE_LIMITED"))
        val e = QuickUnlockPolicy.afterEnable(QuickUnlockState(lastMasterUnlockAtMs = t0))
        assertTrue(e.enabled && e.hasSecret)
        assertEquals(t0, e.lastMasterUnlockAtMs)
    }

    @Test
    fun constants() {
        assertEquals(72 * hour, QuickUnlockPolicy.MAX_AGE_MS)
        assertEquals(5, QuickUnlockPolicy.MAX_FAILURES)
    }
}

class AutoLockPolicyTest {
    @Test
    fun graceIsThirtySecondsUnlessASystemPickerIsOpen() {
        assertEquals(30_000L, AutoLockPolicy.BACKGROUND_GRACE_MS)
        assertEquals(30_000L, AutoLockPolicy.graceMs(false))
        assertEquals(AutoLockPolicy.PICKER_GRACE_MS, AutoLockPolicy.graceMs(true))
        assertTrue(AutoLockPolicy.PICKER_GRACE_MS > AutoLockPolicy.BACKGROUND_GRACE_MS)
    }

    @Test
    fun locksWhenAwayAtLeastTheGracePeriod() {
        val g = AutoLockPolicy.BACKGROUND_GRACE_MS
        assertFalse(AutoLockPolicy.shouldLockOnReturn(1_000, 1_000 + g - 1, g))
        assertTrue(AutoLockPolicy.shouldLockOnReturn(1_000, 1_000 + g, g))
        assertTrue(AutoLockPolicy.shouldLockOnReturn(1_000, 1_000 + 10 * g, g))
    }

    @Test
    fun aClockGoingBackwardsLocks() {
        assertTrue(AutoLockPolicy.shouldLockOnReturn(50_000, 10_000, AutoLockPolicy.BACKGROUND_GRACE_MS))
    }

    @Test
    fun screenOffLocksOnlyAnOpenVault() {
        assertTrue(AutoLockPolicy.shouldLockOnScreenOff(true))
        assertFalse(AutoLockPolicy.shouldLockOnScreenOff(false))
    }
}

class BackupLogicTest {
    private fun b(name: String, time: Long, reason: String) = VaultBackup(name, time, 1, reason)
    private fun d(id: String) = DiffItem(id, "T$id", 1)

    @Test
    fun reasonLabelsAreChinese() {
        val expected = mapOf(
            "save" to "保存前自动备份", "delete" to "删除前自动备份", "import" to "导入前自动备份", "restore" to "恢复前自动备份",
            "rekey" to "更换主密码前自动备份", "merge" to "合并前自动备份", "create" to "新建密码库时", "upgrade" to "升级前自动备份",
            "manual" to "手动备份", "previous" to "上一代文件（上次保存之前）",
        )
        for ((k, v) in expected) assertEquals(k, v, VaultTexts.backupReason(k))
        assertEquals("其他（weird）", VaultTexts.backupReason("weird"))
    }

    @Test
    fun oldPasswordHintFollowsTheLastMasterChange() {
        val list = listOf(
            b("new", 300, "save"), b("rekey", 200, "rekey"), b("old1", 150, "save"), b("old2", 100, "create"), b("prev", 90, "previous"),
        )
        assertFalse(BackupLogic.mayNeedOldPassword(list, list[0])) // taken after the change
        assertTrue(BackupLogic.mayNeedOldPassword(list, list[1])) // the rekey snapshot itself is old-password
        assertTrue(BackupLogic.mayNeedOldPassword(list, list[2]))
        assertTrue(BackupLogic.mayNeedOldPassword(list, list[3]))
        assertTrue(BackupLogic.mayNeedOldPassword(list, list[4]))
        assertFalse(BackupLogic.mayNeedOldPassword(listOf(b("a", 1, "save"), b("p", 0, "previous")), b("p", 0, "previous")))
    }

    private val preview = RestorePreview(
        onlyInBackup = listOf(d("a"), d("b")),
        onlyInCurrent = listOf(d("c")),
        changed = listOf(d("e")),
        same = 4,
    )

    @Test
    fun diffIsGroupedInDisplayOrderWithoutEmptyGroups() {
        val g = BackupLogic.group(preview)
        assertEquals(
            listOf(DiffSection.Kind.ONLY_IN_BACKUP, DiffSection.Kind.CHANGED, DiffSection.Kind.ONLY_IN_CURRENT),
            g.map { it.kind },
        )
        assertEquals(listOf(2, 1, 1), g.map { it.items.size })
        assertTrue(g[0].title.contains("（2）"))
        val onlyChanged = BackupLogic.group(RestorePreview(emptyList(), emptyList(), listOf(d("x")), 0))
        assertEquals(listOf(DiffSection.Kind.CHANGED), onlyChanged.map { it.kind })
        assertTrue(BackupLogic.group(RestorePreview(emptyList(), emptyList(), emptyList(), 3)).isEmpty())
    }

    @Test
    fun summaryText() {
        assertEquals("2 条会找回，1 条内容不同，1 条仅在当前库，4 条相同", BackupLogic.summary(preview))
        assertEquals("备份与当前库没有差别", BackupLogic.summary(RestorePreview(emptyList(), emptyList(), emptyList(), 0)))
    }

    @Test
    fun selectionOnlyOffersWhatARestoreCanDo() {
        assertEquals(listOf("a", "b", "e"), BackupLogic.selectableIds(preview))
        assertEquals(setOf("a", "b"), BackupLogic.defaultSelection(preview))
        assertEquals(setOf("a"), BackupLogic.toggle(setOf("a", "b"), "b"))
        assertEquals(setOf("a", "b", "e"), BackupLogic.toggle(setOf("a", "b"), "e"))
        // stale or not-restorable ids (c is only in the current vault) are dropped
        assertEquals(listOf("a", "e"), BackupLogic.cleanSelection(preview, setOf("a", "c", "zzz", "e")))
    }

    @Test
    fun restoreModesAndConfirmation() {
        assertFalse(BackupLogic.canRestore(VaultTexts.RESTORE_ENTRIES, 0))
        assertTrue(BackupLogic.canRestore(VaultTexts.RESTORE_ENTRIES, 1))
        assertTrue(BackupLogic.canRestore(VaultTexts.RESTORE_MERGE, 0))
        assertTrue(BackupLogic.canRestore(VaultTexts.RESTORE_REPLACE, 0))
        assertFalse(BackupLogic.canRestore("bogus", 5))
        assertTrue(BackupLogic.needsExplicitConfirm(VaultTexts.RESTORE_REPLACE))
        assertFalse(BackupLogic.needsExplicitConfirm(VaultTexts.RESTORE_MERGE))
        assertTrue(BackupLogic.SAFETY_NOTE.contains("快照"))
    }

    @Test
    fun staleBackupNudge() {
        val now = 100L * 86400
        assertTrue(BackupLogic.backupIsStale(0, now))
        assertTrue(BackupLogic.backupIsStale(now - 31 * 86400, now))
        assertFalse(BackupLogic.backupIsStale(now - 29 * 86400, now))
    }
}

class VaultTextsTest {
    @Test
    fun kindsFiltersAndExpiry() {
        assertEquals("账号", VaultTexts.kindLabel("login"))
        assertEquals("API 密钥", VaultTexts.kindLabel("apikey"))
        assertEquals("机密", VaultTexts.kindLabel("secret"))
        assertEquals("安全笔记", VaultTexts.kindLabel("note"))
        assertEquals("条目", VaultTexts.kindLabel("?"))
        assertEquals(listOf("全部", "账号", "API 密钥", "机密", "笔记", "即将到期", "回收站"), VaultFilter.entries.map(VaultTexts::filterLabel))
        assertEquals("", VaultTexts.expiryLabel(0, 100))
        assertEquals("已过期", VaultTexts.expiryLabel(50, 100))
        assertEquals("1 天内到期", VaultTexts.expiryLabel(100 + 3600, 100))
        assertEquals("5 天后到期", VaultTexts.expiryLabel(100 + 5 * 86400, 100))
    }

    @Test
    fun copyMessagesAndStats() {
        assertEquals("已复制，20 秒后自动清除", VaultTexts.copiedClears(20))
        assertEquals("已复制（不会自动清除）", VaultTexts.copiedClears(0))
        assertEquals("没有变化", VaultTexts.mergeStatsText(MergeStats()))
        assertEquals("新增 2 条，更新 1 条，新增分组 1 个", VaultTexts.mergeStatsText(MergeStats(entriesAdded = 2, entriesUpdated = 1, groupsAdded = 1)))
        assertEquals("", VaultTexts.dateTime(0))
        assertEquals("1970-01-01 00:01", VaultTexts.dateTime(60, java.time.ZoneId.of("UTC")))
        assertEquals("只恢复选中的条目", VaultTexts.restoreModeLabel("entries"))
    }
}
