package com.nightclip.app.logic

import com.nightclip.app.core.NotesPeerStatus
import com.nightclip.app.core.NotesSyncStatus
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class NotesSyncLogicTest {
    private val now = 1_790_000_000_000L

    private fun peer(
        id: String = "dev_pc",
        name: String = "Tony-PC",
        connected: Boolean = true,
        state: String = "idle",
        lastSyncAt: Long = now - 3 * 60_000,
        behind: Int = 0,
    ) = NotesPeerStatus(id, name, connected, state, "", lastSyncAt, behind, 0)

    private fun status(vararg p: NotesPeerStatus, pending: Int = 0) = NotesSyncStatus(p.toList(), pending)

    // ---- summary line ------------------------------------------------------------------------

    @Test
    fun noPairedDeviceSaysNotesStayOnThisPhone() {
        val l = NotesSyncLogic.summary(NotesSyncStatus.EMPTY, now)
        assertEquals(NotesSyncLogic.NO_PEERS, l.text)
        assertEquals(SyncTone.WARN, l.tone)
    }

    @Test
    fun allQuietShowsWhenTheLastSyncWas() {
        val l = NotesSyncLogic.summary(status(peer()), now)
        assertEquals("已同步 · 上次同步 3 分钟前", l.text)
        assertEquals(SyncTone.OK, l.tone)
    }

    @Test
    fun syncingShowsTheDevice() {
        val l = NotesSyncLogic.summary(status(peer(state = "syncing")), now)
        assertEquals("正在与 Tony-PC 同步…", l.text)
        assertEquals(SyncTone.BUSY, l.tone)
    }

    @Test
    fun anOldPeerSaysToUpdateThisApp() {
        val l = NotesSyncLogic.summary(status(peer(state = "peer_old", lastSyncAt = 0)), now)
        assertTrue(l.text.contains("请更新本应用"))
        assertTrue(l.text.startsWith("Tony-PC"))
        assertEquals(SyncTone.ERROR, l.tone)
    }

    @Test
    fun theMostUrgentDeviceWins() {
        val s = status(peer(id = "a", name = "A", state = "syncing"), peer(id = "b", name = "B", state = "error"), peer(id = "c", name = "C", state = "peer_old"))
        assertTrue(NotesSyncLogic.summary(s, now).text.startsWith("C"))
        val s2 = status(peer(id = "a", name = "A", state = "syncing"), peer(id = "b", name = "B", state = "error"))
        assertEquals("B 同步出错，稍后会自动重试", NotesSyncLogic.summary(s2, now).text)
    }

    @Test
    fun behindAndPendingCountsAreShown() {
        assertEquals("还有 3 篇笔记待收取 · 上次同步 3 分钟前", NotesSyncLogic.summary(status(peer(behind = 3)), now).text)
        assertEquals("2 篇笔记待发送 · 上次同步 3 分钟前", NotesSyncLogic.summary(status(peer(), pending = 2), now).text)
    }

    @Test
    fun anOfflineDeviceIsAWarningNotAnError() {
        val l = NotesSyncLogic.summary(status(peer(connected = false)), now)
        assertEquals("对方设备未连接 · 上次同步 3 分钟前", l.text)
        assertEquals(SyncTone.WARN, l.tone)
    }

    @Test
    fun neverSyncedIsSaid() {
        assertEquals("已连接，还没有同步过", NotesSyncLogic.summary(status(peer(lastSyncAt = 0)), now).text)
        assertEquals("从未同步", NotesSyncLogic.lastSyncLabel(0, now))
    }

    @Test
    fun theNewestSyncOfAllDevicesIsTheOneShown() {
        val s = status(peer(id = "a", lastSyncAt = now - 2 * 3_600_000), peer(id = "b", lastSyncAt = now - 10_000))
        assertEquals("已同步 · 上次同步 刚刚", NotesSyncLogic.summary(s, now).text)
    }

    // ---- per device --------------------------------------------------------------------------

    @Test
    fun peerLineHasNameConnectionStateAndTime() {
        assertEquals("Tony-PC · 已连接 · 空闲 · 上次同步 3 分钟前", NotesSyncLogic.peerLine(peer(), now))
        assertEquals("Tony-PC · 离线 · 同步中 · 上次同步 3 分钟前 · 待收取 2 篇", NotesSyncLogic.peerLine(peer(connected = false, state = "syncing", behind = 2), now))
        assertEquals("Tony-PC · 已连接 · 需要更新本应用", NotesSyncLogic.peerLine(peer(state = "peer_old"), now))
        assertEquals("其他设备 · 已连接 · 空闲 · 从未同步", NotesSyncLogic.peerLine(peer(name = " ", lastSyncAt = 0), now))
    }

    // ---- events and decisions ----------------------------------------------------------------

    @Test
    fun aSyncUpdatedEventChangesOnlyThatDevice() {
        val s = status(peer(id = "a", name = "A"), peer(id = "b", name = "B"))
        val u = NotesSyncLogic.applyUpdated(s, "b", "syncing", 4)!!
        assertEquals("idle", u.peers[0].state)
        assertEquals("syncing", u.peers[1].state)
        assertEquals(4, u.peers[1].behind)
        assertEquals("B", u.peers[1].name)
    }

    @Test
    fun anEventForAnUnknownDeviceAsksForAReload() {
        assertNull(NotesSyncLogic.applyUpdated(status(peer(id = "a")), "zzz", "idle", 0))
    }

    @Test
    fun anEventWithoutAStateKeepsTheOldOne() {
        val u = NotesSyncLogic.applyUpdated(status(peer(state = "error")), "dev_pc", "", -2)!!
        assertEquals("error", u.peers[0].state)
        assertEquals(0, u.peers[0].behind)
    }

    @Test
    fun autoSyncIsThrottled() {
        assertTrue(NotesSyncLogic.shouldAutoSync(0, now))
        assertFalse(NotesSyncLogic.shouldAutoSync(now - 1_000, now))
        assertFalse(NotesSyncLogic.shouldAutoSync(now - 14_999, now))
        assertTrue(NotesSyncLogic.shouldAutoSync(now - 15_000, now))
        assertTrue("a clock that went backwards must not block syncing forever", NotesSyncLogic.shouldAutoSync(now + 60_000, now))
    }

    @Test
    fun syncNowNeedsADeviceAndOldPeersAreFlagged() {
        assertFalse(NotesSyncLogic.canSyncNow(NotesSyncStatus.EMPTY))
        assertTrue(NotesSyncLogic.canSyncNow(status(peer())))
        assertFalse(NotesSyncLogic.needsAppUpdate(status(peer())))
        assertTrue(NotesSyncLogic.needsAppUpdate(status(peer(), peer(id = "o", state = "peer_old"))))
    }
}
