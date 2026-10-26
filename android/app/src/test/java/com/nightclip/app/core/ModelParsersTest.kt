package com.nightclip.app.core

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ModelParsersTest {
    @Test
    fun parsesPeersList() {
        val r = JSONObject(
            """{"peers":[
              {"device_id":"dev_a","name":"Tony-PC","platform":"windows","state":"connected","tailnet_addrs":["100.101.146.117"],
               "outbox_pending":0,"recv_cursor":4,"next_seq_out":9,"last_seen_at":1790000000,"apply_mode":"manual"},
              {"device_id":"dev_b","name":"Old","platform":"android","state":"revoked","tailnet_addrs":[],"outbox_pending":2}
            ]}""",
        )
        val peers = ModelParsers.peers(r)
        assertEquals(2, peers.size)
        val a = peers[0]
        assertEquals("dev_a", a.deviceId)
        assertEquals("windows", a.platform)
        assertEquals(listOf("100.101.146.117"), a.addrs)
        assertEquals("manual", a.applyMode)
        assertEquals(1790000000L, a.lastSeenAt)
        assertTrue(a.canSend)
        assertTrue(a.isConnected)
        assertFalse(peers[1].canSend)
        assertEquals(2, peers[1].outboxPending)
        assertEquals("notify", peers[1].applyMode) // default when missing
    }

    @Test
    fun emptyOrMissingPeersAreHandled() {
        assertTrue(ModelParsers.peers(JSONObject("{}")).isEmpty())
        assertTrue(ModelParsers.peers(JSONObject("""{"peers":[]}""")).isEmpty())
    }

    @Test
    fun unpairedPeersCannotBeSentTo() {
        assertFalse(ModelParsers.peer(JSONObject("""{"device_id":"x","state":"unpaired"}""")).canSend)
        assertTrue(ModelParsers.peer(JSONObject("""{"device_id":"x","state":"offline"}""")).canSend)
    }

    @Test
    fun parsesInbox() {
        val r = JSONObject(
            """{"items":[
              {"event_id":"e2","from":"dev_a","from_name":"PC","text":"secret","sensitive":true,"state":"received","received_at":20,"expires_at":30},
              {"event_id":"e1","from":"dev_a","from_name":"PC","text":"hi","sensitive":false,"state":"applied","received_at":10,"expires_at":30,"applied_at":12}
            ]}""",
        )
        val items = ModelParsers.inbox(r)
        assertEquals(listOf("e2", "e1"), items.map { it.eventId })
        assertTrue(items[0].sensitive)
        assertEquals("applied", items[1].state)
        assertEquals(12L, items[1].appliedAt)
        assertEquals(0L, items[0].appliedAt)
        assertFalse(items[0].toString().contains("secret")) // never print clipboard text
    }

    @Test
    fun parsesTransfers() {
        val r = JSONObject(
            """{"transfers":[
              {"transfer_id":"tr_1","peer_id":"dev_a","peer_name":"PC","direction":"in","name":"a.pdf","size":123456,"sha256":"ab",
               "state":"transferring","offset":1000,"rate_bps":5000,"created_at":1,"expires_at":2},
              {"transfer_id":"tr_2","direction":"out","name":"b.zip","size":10,"state":"failed","error":"PEER_OFFLINE"}
            ]}""",
        )
        val t = ModelParsers.transfers(r)
        assertEquals(2, t.size)
        assertTrue(t[0].incoming)
        assertTrue(t[0].active)
        assertEquals(123456L, t[0].size)
        assertEquals(5000L, t[0].rateBps)
        assertFalse(t[1].incoming)
        assertTrue(t[1].terminal)
        assertEquals("PEER_OFFLINE", t[1].error)
    }

    @Test
    fun transferStatesClassifyCorrectly() {
        val offeredIn = TransferInfo("t", direction = "in", state = "offered")
        assertTrue(offeredIn.awaitingMyDecision)
        assertFalse(TransferInfo("t", direction = "out", state = "offered").awaitingMyDecision)
        for (s in listOf("hashing", "offered", "accepted", "transferring")) assertTrue(s, TransferInfo("t", state = s).active)
        for (s in listOf("done", "rejected", "cancelled", "failed", "expired")) assertTrue(s, TransferInfo("t", state = s).terminal)
    }

    @Test
    fun parsesStatus() {
        val r = JSONObject(
            """{"device_id":"dev_me","device_name":"Pixel","platform":"android","listen":false,"port":47821,
                "peers":[{"device_id":"p1","name":"PC","platform":"windows","state":"connected"},
                         {"device_id":"p2","name":"Tab","platform":"android","state":"offline"}],
                "capture":"on","listening_addrs":[],"listening_port":0,"events_dropped":3}""",
        )
        val s = ModelParsers.status(r)
        assertEquals("dev_me", s.deviceId)
        assertEquals("Pixel", s.deviceName)
        assertTrue(s.capture)
        assertEquals(3L, s.eventsDropped)
        assertEquals(1, s.connectedCount)
        assertEquals(2, s.peers.size)
    }

    @Test
    fun parsesSettingsFromStringValues() {
        val r = JSONObject(
            """{"settings":{"capture":"off","clip_ttl_s":"300","history_limit":"1000","send_sensitive":"off","sensitive_detection":"off","default_apply_mode":"manual"}}""",
        )
        val s = ModelParsers.settings(r)
        assertFalse(s.capture)
        assertEquals(300, s.clipTtlS)
        assertEquals(1000, s.historyLimit)
        assertFalse(s.sendSensitive)
        assertFalse(s.sensitiveDetection)
        assertEquals("manual", s.defaultApplyMode)
        assertEquals(CoreSettings(), ModelParsers.settings(JSONObject("{}")))
        // an explicit "off" is honoured, anything else (including unset) means on
        assertTrue(ModelParsers.settings(JSONObject("{}")).capture)
        assertEquals("auto", ModelParsers.settings(JSONObject("{}")).defaultApplyMode)
    }

    @Test
    fun parsesSendResult() {
        assertEquals(SendResult(2, ""), ModelParsers.sendResult(JSONObject("""{"sent":[{"device_id":"a"},{"device_id":"b"}]}""")))
        assertEquals(SendResult(0, "too_large"), ModelParsers.sendResult(JSONObject("""{"sent":[],"suppressed":"too_large"}""")))
        assertEquals(SendResult(0, ""), ModelParsers.sendResult(JSONObject("{}")))
    }

    @Test
    fun parsesSendEnabledAndPause() {
        val peers = ModelParsers.peers(JSONObject("""{"peers":[{"device_id":"a"},{"device_id":"b","send_enabled":false}]}"""))
        assertTrue(peers[0].sendEnabled)
        assertFalse(peers[1].sendEnabled)
        val s = ModelParsers.settings(JSONObject("""{"settings":{"paused_until":"1790003600"}}"""))
        assertEquals(1790003600L, s.pausedUntil)
        assertTrue(s.isPaused(1790000000))
        assertFalse(s.isPaused(1790003600))
        assertEquals(0L, ModelParsers.settings(JSONObject("""{"settings":{"paused_until":"soon"}}""")).pausedUntil)
    }
}
