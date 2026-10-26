package com.nightclip.app.logic

import com.nightclip.app.UpdateState
import com.nightclip.app.core.CoreEvent
import com.nightclip.app.core.PeerInfo
import com.nightclip.app.core.SendResult
import com.nightclip.app.core.TransferInfo
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File

class PairingFlowTest {
    private fun result(state: String, role: String = "joiner", join: String = "j1", code: String = "", name: String = "") =
        CoreEvent.PairResult(state, role, "", join, code, "", "", name)

    @Test
    fun happyPath() {
        var s: PairUi = PairUi.Joining(null)
        s = PairingFlow.withJoinId(s, "j1")
        assertEquals(PairUi.Joining("j1"), s)
        s = PairingFlow.reduce(s, CoreEvent.PairSas("j1", "482913"))
        assertEquals(PairUi.ShowSas("j1", "482913"), s)
        s = PairingFlow.reduce(s, result("accepted", name = "Tony-PC"))
        assertEquals(PairUi.Accepted("Tony-PC"), s)
    }

    @Test
    fun eventsMayArriveBeforeTheJoinIdIsKnown() {
        var s: PairUi = PairUi.Joining(null)
        s = PairingFlow.reduce(s, CoreEvent.PairSas("j9", "111222"))
        assertEquals(PairUi.ShowSas("j9", "111222"), s)
        assertEquals(s, PairingFlow.withJoinId(s, "j9"))
        // a late pair.join reply must not overwrite a progressed state
        var s2: PairUi = PairUi.Joining(null)
        s2 = PairingFlow.reduce(s2, CoreEvent.PairSas("", "111222"))
        s2 = PairingFlow.withJoinId(s2, "j9")
        assertEquals(PairUi.ShowSas("j9", "111222"), s2)
    }

    @Test
    fun otherJoinsAreIgnored() {
        val s = PairUi.Joining("j1")
        assertEquals(s, PairingFlow.reduce(s, CoreEvent.PairSas("other", "000000")))
        assertEquals(s, PairingFlow.reduce(s, result("failed", join = "other", code = "PAIRING_FAILED")))
        assertEquals(s, PairingFlow.reduce(s, result("accepted", role = "listener")))
    }

    @Test
    fun failuresMapToMessages() {
        val failed = PairingFlow.reduce(PairUi.ShowSas("j1", "123456"), result("failed", code = "PAIRING_FAILED")) as PairUi.Failed
        assertEquals("PAIRING_FAILED", failed.code)
        assertTrue(failed.message.isNotEmpty())
        val expired = PairingFlow.reduce(PairUi.Joining("j1"), result("expired")) as PairUi.Failed
        assertEquals("PAIRING_EXPIRED", expired.code)
        assertTrue(PairingFlow.reduce(PairUi.Joining("j1"), result("rejected")) is PairUi.Failed)
    }

    @Test
    fun idleAndFinishedStatesIgnoreEvents() {
        for (s in listOf(PairUi.Idle, PairUi.Accepted("x"), PairUi.Failed("c", "m"))) {
            assertEquals(s, PairingFlow.reduce(s, CoreEvent.PairSas("j", "1")))
            assertEquals(s, PairingFlow.reduce(s, result("accepted")))
        }
    }

    @Test
    fun sasIsGroupedForDisplay() {
        assertEquals("482 913", PairingFlow.formatSas("482913"))
        assertEquals("1234", PairingFlow.formatSas("1234"))
    }
}

class TransferReducerTest {
    private val base = TransferInfo("tr_1", peerName = "PC", direction = "in", name = "a.pdf", size = 1000, state = "accepted")

    @Test
    fun offerAddsOnlyOnce() {
        val ev = CoreEvent.FileOffered("tr_9", "dev_a", "PC", "b.zip", 5, "sha", "")
        val l = TransferReducer.apply(emptyList(), ev)
        assertEquals(1, l.size)
        assertTrue(l[0].awaitingMyDecision)
        assertEquals("PC", l[0].peerName)
        assertEquals(l, TransferReducer.apply(l, ev))
    }

    @Test
    fun progressMovesToTransferring() {
        val l = TransferReducer.apply(listOf(base), CoreEvent.FileProgress("tr_1", 250, 1000, 100))
        assertEquals("transferring", l[0].state)
        assertEquals(250L, l[0].offset)
        assertEquals(100L, l[0].rateBps)
    }

    @Test
    fun stateDoneAndFailed() {
        var l = TransferReducer.apply(listOf(base), CoreEvent.FileState("tr_1", "transferring"))
        assertEquals("transferring", l[0].state)
        l = TransferReducer.apply(l, CoreEvent.FileDone("tr_1", "in", "/p/a.pdf", "a.pdf", 1000, "sha"))
        assertEquals("done", l[0].state)
        assertEquals("/p/a.pdf", l[0].path)
        assertEquals(1000L, l[0].offset)
        l = TransferReducer.apply(listOf(base), CoreEvent.FileFailed("tr_1", "HASH_MISMATCH", ""))
        assertEquals("failed", l[0].state)
        assertEquals("HASH_MISMATCH", l[0].error)
    }

    @Test
    fun unknownIdsLeaveTheListAloneButAskForARefresh() {
        val ev = CoreEvent.FileProgress("tr_x", 1, 2, 3)
        assertEquals(listOf(base), TransferReducer.apply(listOf(base), ev))
        assertTrue(TransferReducer.needsRefresh(listOf(base), ev))
        assertFalse(TransferReducer.needsRefresh(listOf(base), CoreEvent.FileProgress("tr_1", 1, 2, 3)))
        assertFalse(TransferReducer.needsRefresh(listOf(base), CoreEvent.PeerState("a", "b")))
    }
}

class PeerTextsTest {
    private fun peer(state: String, pending: Int = 0) = PeerInfo("d", "n", "windows", state, outboxPending = pending)

    @Test
    fun peerStateLabelsFollowTheGuide() {
        assertEquals("已连接", PeerTexts.stateLabel(peer("connected")))
        assertEquals("正在连接", PeerTexts.stateLabel(peer("connecting")))
        assertEquals("离线", PeerTexts.stateLabel(peer("offline")))
        assertEquals("离线（排队 3 条）", PeerTexts.stateLabel(peer("offline", 3)))
        assertEquals("对方已不认识本机，需要重新配对", PeerTexts.stateLabel(peer("unpaired")))
        assertEquals("已解除配对", PeerTexts.stateLabel(peer("revoked")))
        assertTrue(PeerTexts.stateLabel(peer("zzz")).contains("zzz"))
    }

    @Test
    fun sendOutcomeMessages() {
        assertEquals("已发送给 2 台设备", PeerTexts.sendOutcome(SendResult(2)))
        assertEquals(Strings.SUPPRESSED_TOO_LARGE, PeerTexts.sendOutcome(SendResult(0, "too_large")))
        assertTrue(PeerTexts.sendOutcome(SendResult(0, "too_large")).contains("作为文件发送"))
        assertEquals(Strings.SUPPRESSED_NO_PEERS, PeerTexts.sendOutcome(SendResult(0, "no_peers")))
        assertEquals(Strings.SUPPRESSED_SENSITIVE, PeerTexts.sendOutcome(SendResult(0, "sensitive_blocked")))
        assertTrue(PeerTexts.sendOutcome(SendResult(0, "brand_new")).contains("brand_new"))
        assertEquals(Strings.SEND_NOTHING, PeerTexts.sendOutcome(SendResult(0)))
    }

    @Test
    fun transferLabels() {
        assertEquals(Strings.TR_OFFERED_IN, PeerTexts.transferStateLabel(TransferInfo("t", direction = "in", state = "offered")))
        assertEquals(Strings.TR_OFFERED, PeerTexts.transferStateLabel(TransferInfo("t", direction = "out", state = "offered")))
        val failed = PeerTexts.transferStateLabel(TransferInfo("t", state = "failed", error = "HASH_MISMATCH"))
        assertTrue(failed, failed.contains("文件校验失败"))
    }

    @Test
    fun applyModes() {
        assertEquals("只通知", PeerTexts.applyModeLabel("notify"))
        assertEquals("手动复制", PeerTexts.applyModeLabel("manual"))
        assertEquals("自动复制", PeerTexts.applyModeLabel("auto"))
        assertEquals("只通知", PeerTexts.applyModeLabel("???"))
    }
}

class UpdatePresenterTest {
    private fun u(state: String, extra: String = "") =
        UpdateState.from(org.json.JSONObject("""{"enabled":true,"state":"$state","current_code":5,"current_name":"0.1.5","latest":{"version_name":"0.1.9"}$extra}"""))

    @Test
    fun textsAreChinese() {
        assertEquals("发现新版本 0.1.9", UpdatePresenter.text(u("available")))
        assertTrue(UpdatePresenter.text(u("available", ""","mandatory":true""")).contains("必须更新"))
        assertEquals("已是最新版本（0.1.5）", UpdatePresenter.text(u("up_to_date")))
        assertTrue(UpdatePresenter.text(u("downloading", ""","received":50,"total":100""")).contains("50%"))
        assertTrue(UpdatePresenter.text(UpdateState(enabled = false)).contains("开发版本"))
        assertEquals("检查更新", UpdatePresenter.buttonText(u("idle")))
        assertEquals("下载", UpdatePresenter.buttonText(u("available")))
        assertEquals("安装更新", UpdatePresenter.buttonText(u("ready")))
        assertEquals("", UpdatePresenter.buttonText(u("downloading")))
    }
}

class PathGuardTest {
    @get:Rule val tmp = TemporaryFolder()

    @Test
    fun onlyRegularFilesInsideTheBaseDirAreAccepted() {
        val base = tmp.newFolder("clipbridge")
        val inside = File(base, "received").apply { mkdirs() }.let { File(it, "a.pdf").apply { writeText("x") } }
        val outside = tmp.newFile("secret.txt")
        assertTrue(PathGuard.isInside(base, inside))
        assertFalse(PathGuard.isInside(base, outside))
        assertFalse(PathGuard.isInside(base, File(base, "received/../../secret.txt")))
        assertFalse(PathGuard.isInside(base, File(base, "missing.bin")))
        assertFalse(PathGuard.isInside(base, File(base, "received"))) // a directory
        assertFalse(PathGuard.isInside(base, base))
    }
}

class TailscaleHintTest {
    @Test
    fun hintOnlyWithoutVpn() {
        assertNull(TailscaleHint.message(true))
        assertTrue(TailscaleHint.message(false)!!.contains("Tailscale"))
    }
}
