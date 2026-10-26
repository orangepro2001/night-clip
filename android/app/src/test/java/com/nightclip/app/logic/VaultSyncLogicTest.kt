package com.nightclip.app.logic

import com.nightclip.app.core.CoreEvent
import com.nightclip.app.core.SyncPeer
import com.nightclip.app.core.VaultParsers
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private fun peer(
    id: String = "d1",
    name: String = "Tony-PC",
    enabled: Boolean = true,
    connected: Boolean = true,
    state: String = "idle",
    detail: String = "",
    lastOkAt: Long = 0,
    pending: Boolean = false,
) = SyncPeer(id, name, enabled, connected, state, detail, lastOkAt, pending)

class SyncParsersTest {
    @Test
    fun parsesSyncStatus() {
        val s = VaultParsers.syncStatus(
            JSONObject(
                """{"vault_exists":true,"peers":[
                {"device_id":"a","name":"PC","enabled":true,"connected":true,"state":"review","detail":"the other device has a different vault: approve","last_ok_at":1700,"pending_incoming":true,"incoming_sha256":"abc123"},
                {"device_id":"b","name":"Pad","enabled":false,"connected":false,"state":"idle","detail":"","last_ok_at":0,"pending_incoming":false}]}""",
            ),
        )
        assertTrue(s.vaultExists)
        assertEquals(2, s.peers.size)
        assertEquals("review", s.peers[0].state)
        assertTrue(s.peers[0].pendingIncoming)
        assertEquals("abc123", s.peers[0].incomingSha256)
        assertEquals(1700L, s.peers[0].lastOkAt)
        assertFalse(s.peers[1].enabled)
    }

    @Test
    fun emptyAndMissingKeysAreSafe() {
        assertTrue(VaultParsers.syncStatus(JSONObject("""{"peers":null}""")).peers.isEmpty())
        val p = VaultParsers.syncStatus(JSONObject("""{"peers":[{"device_id":"x"}]}""")).peers.single()
        assertEquals("idle", p.state)
        assertFalse(p.enabled)
    }

    @Test
    fun statusKeyFileIsNullableAndKdfKeysAreSnakeCase() {
        val unknown = VaultParsers.status(JSONObject("""{"exists":true,"unlocked":false}"""))
        assertNull(unknown.keyFile)
        assertEquals(true, VaultParsers.status(JSONObject("""{"key_file":true}""")).keyFile)
        assertEquals(false, VaultParsers.status(JSONObject("""{"key_file":false}""")).keyFile)
        val k = VaultParsers.status(JSONObject("""{"kdf":{"memory_mib":64,"iterations":3,"parallelism":2}}""")).kdf
        assertEquals(64L, k?.memoryMiB)
        assertEquals(2, k?.parallelism)
    }

    @Test
    fun syncEventsParse() {
        assertEquals(
            CoreEvent.VaultSync("d", "PC", "needs_password"),
            CoreEvent.parse("""{"type":"vault.sync","device_id":"d","name":"PC","state":"needs_password","detail":"x"}"""),
        )
        assertEquals(CoreEvent.VaultChanged("sync", "d"), CoreEvent.parse("""{"type":"vault.changed","reason":"sync","device_id":"d","stats":{"entries_added":1}}"""))
        assertEquals(CoreEvent.VaultJoined("d", "PC"), CoreEvent.parse("""{"type":"vault.joined","device_id":"d","name":"PC"}"""))
    }
}

class VaultSyncLogicTest {
    @Test
    fun peerOffTextMatchesTheSpec() {
        val p = peer(state = "peer_off")
        assertEquals("对方设备还没有允许同步。新版默认开启；如果对方是旧版本，请升级或在对方设备上打开密码库同步。", VaultSyncLogic.stateExplanation(p))
        assertEquals("对方未开启", VaultSyncLogic.stateLabel(p))
    }

    @Test
    fun everyStateHasLabelAndExplanation() {
        for (s in listOf("idle", "pending_unlock", "needs_password", "review", "peer_off", "error")) {
            val p = peer(state = s, detail = "boom")
            assertTrue(s, VaultSyncLogic.stateLabel(p).isNotBlank())
            assertTrue(s, VaultSyncLogic.stateExplanation(p).isNotBlank())
        }
        assertTrue(VaultSyncLogic.stateExplanation(peer(state = "needs_password")).contains("主密码"))
        assertTrue(VaultSyncLogic.stateExplanation(peer(state = "pending_unlock")).contains("解锁"))
        assertTrue(VaultSyncLogic.stateExplanation(peer(state = "error", detail = "x".repeat(500))).length < 200)
        assertEquals("已是最新", VaultSyncLogic.stateLabel(peer()))
        assertEquals("等待连接", VaultSyncLogic.stateLabel(peer(connected = false)))
    }

    @Test
    fun aSwitchedOffDeviceShowsOffWhateverTheCoreSays() {
        val p = peer(enabled = false, state = "review")
        assertEquals("未开启", VaultSyncLogic.stateLabel(p))
        assertFalse(VaultSyncLogic.needsAttention(p))
    }

    @Test
    fun reviewDetailIsClassified() {
        assertEquals(ReviewKind.DIFFERENT_VAULT, VaultSyncLogic.reviewKind("the other device has a different vault: approve to merge both"))
        assertEquals(ReviewKind.MANY_DELETIONS, VaultSyncLogic.reviewKind("the other device's copy would delete several entries: review and approve"))
        assertEquals(ReviewKind.UNKNOWN, VaultSyncLogic.reviewKind(""))
        assertTrue(VaultSyncLogic.reviewText(ReviewKind.DIFFERENT_VAULT).contains("不是同一个库"))
        assertTrue(VaultSyncLogic.reviewText(ReviewKind.MANY_DELETIONS).contains("5 个"))
        val p = peer(state = "review", detail = "the other device's copy would delete several entries")
        assertTrue(VaultSyncLogic.stateExplanation(p).contains("删除"))
    }

    @Test
    fun attentionOnlyForEnabledDevicesWaitingForTheUser() {
        val ps = listOf(
            peer("a", state = "needs_password"), peer("b", state = "review"), peer("c", state = "idle"),
            peer("d", state = "review", enabled = false), peer("e", state = "error"),
        )
        assertEquals(setOf("a", "b"), VaultSyncLogic.attentionIds(ps))
    }

    @Test
    fun eventsUpdateTheAttentionSet() {
        var a = emptySet<String>()
        a = VaultSyncLogic.applyStateEvent(a, "x", "needs_password")
        a = VaultSyncLogic.applyStateEvent(a, "y", "review")
        assertEquals(setOf("x", "y"), a)
        a = VaultSyncLogic.applyStateEvent(a, "x", "idle")
        assertEquals(setOf("y"), a)
        assertEquals(setOf("y"), VaultSyncLogic.applyStateEvent(a, "z", "peer_off"))
    }

    @Test
    fun resolvingNeedsAnOpenVault() {
        val p = peer(state = "review")
        assertTrue(VaultSyncLogic.canResolve(p, true))
        assertFalse(VaultSyncLogic.canResolve(p, false))
        assertFalse(VaultSyncLogic.canResolve(peer(state = "idle"), true))
    }

    @Test
    fun orderingAndSyncNow() {
        val ps = listOf(peer("1", "zeta", enabled = false), peer("2", "beta", connected = false), peer("3", "alpha"), peer("4", "Able"))
        assertEquals(listOf("Able", "alpha", "beta", "zeta"), VaultSyncLogic.sorted(ps).map { it.name })
        assertTrue(VaultSyncLogic.canSyncNow(ps))
        assertFalse(VaultSyncLogic.canSyncNow(listOf(peer(enabled = false))))
        assertFalse(VaultSyncLogic.canSyncNow(emptyList()))
    }

    @Test
    fun lastSyncAndConnectionLabels() {
        assertEquals("还没有成功同步过", VaultSyncLogic.lastSyncLabel(peer(), 1000))
        assertEquals("上次同步：5 分钟前", VaultSyncLogic.lastSyncLabel(peer(lastOkAt = 700), 1000))
        assertEquals("已连接", VaultSyncLogic.connectionLabel(peer()))
        assertEquals("未连接", VaultSyncLogic.connectionLabel(peer(connected = false)))
    }

    @Test
    fun textsMentionWhatTheSpecAsks() {
        assertTrue(VaultSyncLogic.EXPLAINER.contains("加密"))
        assertTrue(VaultSyncLogic.EXPLAINER.contains("两台设备"))
        assertTrue(VaultSyncLogic.EXPLAINER.contains("更换主密码"))
        assertTrue(VaultSyncLogic.EXPLAINER.contains("默认自动同步"))
        assertTrue(VaultSyncLogic.EXPLAINER.contains("完整"))
        assertTrue(VaultSyncLogic.HINT_NO_VAULT.contains("设备同步"))
        assertEquals("已加入 Tony-PC 的密码库，请用那个库的主密码解锁。", VaultSyncLogic.joinedText("Tony-PC"))
        assertTrue(VaultSyncLogic.joinedText("").contains("其他设备"))
    }

    @Test
    fun keyFileHintFollowsStatus() {
        assertEquals(KeyFileHint.OPTIONAL, KeyFileHint.of(null))
        assertEquals(KeyFileHint.EXPECTED, KeyFileHint.of(true))
        assertEquals(KeyFileHint.NOT_USED, KeyFileHint.of(false))
    }

    @Test
    fun syncContextHasItsOwnWrongPasswordText() {
        val m = VaultErrors.message("VAULT_WRONG_CREDENTIALS", context = VaultErrors.Context.SYNC)
        assertTrue(m.contains("对方"))
    }
}
