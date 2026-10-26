package com.nightclip.app.core

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class CoreEventTest {
    private inline fun <reified T : CoreEvent> parse(json: String): T {
        val ev = CoreEvent.parse(json)
        assertTrue("parsed: $ev", ev is T)
        return ev as T
    }

    @Test
    fun clipReceivedKeepsTextButNeverPrintsIt() {
        val e = parse<CoreEvent.ClipReceived>(
            """{"type":"clip.received","event_id":"e1","from":"dev_a","from_name":"PC","text":"top secret","sensitive":true,"decision":"auto","received_at":1790000000}""",
        )
        assertEquals("top secret", e.text)
        assertTrue(e.sensitive)
        assertEquals("auto", e.decision)
        assertEquals("e1", e.logId)
        assertFalse(e.toString().contains("top secret"))
    }

    @Test
    fun pairEvents() {
        val sas = parse<CoreEvent.PairSas>("""{"type":"pair.sas","join_id":"j1","sas":"482913"}""")
        assertEquals("j1", sas.joinId)
        assertEquals("482913", sas.sas)
        assertFalse(sas.toString().contains("482913"))
        val ok = parse<CoreEvent.PairResult>("""{"type":"pair.result","join_id":"j1","state":"accepted","role":"joiner","device_id":"dev_pc","name":"Tony-PC"}""")
        assertEquals("accepted", ok.state)
        assertEquals("Tony-PC", ok.name)
        val bad = parse<CoreEvent.PairResult>("""{"type":"pair.result","state":"failed","role":"joiner","code":"PAIRING_FAILED","message":"x"}""")
        assertEquals("PAIRING_FAILED", bad.code)
    }

    @Test
    fun peerEvents() {
        assertEquals(CoreEvent.PeerState("dev_a", "connected"), parse<CoreEvent.PeerState>("""{"type":"peer.state","device_id":"dev_a","state":"connected"}"""))
        assertEquals(CoreEvent.PeerRevoked("dev_a", "PC"), parse<CoreEvent.PeerRevoked>("""{"type":"peer.revoked","device_id":"dev_a","name":"PC"}"""))
    }

    @Test
    fun fileEvents() {
        val o = parse<CoreEvent.FileOffered>("""{"type":"file.offered","transfer_id":"tr_1","from":"dev_a","from_name":"PC","name":"a.pdf","size":123456,"sha256":"ab","mime_type":"application/pdf"}""")
        assertEquals("a.pdf", o.name)
        assertEquals(123456L, o.size)
        assertEquals("application/pdf", o.mimeType)
        val p = parse<CoreEvent.FileProgress>("""{"type":"file.progress","transfer_id":"tr_1","offset":1048576,"size":123456789,"rate_bps":52428800}""")
        assertEquals(1048576L, p.offset)
        assertEquals(52428800L, p.rateBps)
        val d = parse<CoreEvent.FileDone>("""{"type":"file.done","transfer_id":"tr_1","direction":"in","path":"/data/x/a.pdf","name":"a.pdf","size":1,"sha256":"ab"}""")
        assertEquals("in", d.direction)
        assertEquals("/data/x/a.pdf", d.path)
        assertEquals("HASH_MISMATCH", parse<CoreEvent.FileFailed>("""{"type":"file.failed","transfer_id":"tr_1","code":"HASH_MISMATCH","message":"m"}""").code)
        assertEquals("transferring", parse<CoreEvent.FileState>("""{"type":"file.state","transfer_id":"tr_1","state":"transferring"}""").state)
    }

    @Test
    fun updateEvents() {
        val a = parse<CoreEvent.UpdateAvailable>("""{"type":"update.available","version_code":12,"version_name":"0.1.12","mandatory":true,"notes":"n"}""")
        assertEquals(12L, a.versionCode)
        assertTrue(a.mandatory)
        assertEquals(CoreEvent.UpdateProgress(10, 100), parse<CoreEvent.UpdateProgress>("""{"type":"update.progress","received":10,"total":100}"""))
        assertEquals("/p.apk", parse<CoreEvent.UpdateReady>("""{"type":"update.ready","version_code":1,"version_name":"v","path":"/p.apk","mandatory":false}""").path)
        assertEquals("HASH_MISMATCH", parse<CoreEvent.UpdateError>("""{"type":"update.error","code":"HASH_MISMATCH","message":"m"}""").code)
    }

    @Test
    fun coreLifecycleEvents() {
        assertEquals("dev_me", parse<CoreEvent.CoreStarted>("""{"type":"core.started","device_id":"dev_me"}""").deviceId)
        assertEquals("NO_TAILNET", parse<CoreEvent.CoreWarning>("""{"type":"core.warning","code":"NO_TAILNET"}""").code)
        val l = parse<CoreEvent.CoreListening>("""{"type":"core.listening","addrs":["100.1.1.1"],"port":47821}""")
        assertEquals(listOf("100.1.1.1"), l.addrs)
    }

    @Test
    fun unknownAndMalformedEvents() {
        assertEquals("future.thing", parse<CoreEvent.Unknown>("""{"type":"future.thing","x":1}""").type)
        assertNull(CoreEvent.parse("not json"))
        assertNull(CoreEvent.parse("""{"no":"type"}"""))
        assertNull(CoreEvent.parse(""))
    }
}
