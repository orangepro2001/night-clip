package com.nightclip.app.logic

import com.nightclip.app.core.PeerInfo
import org.junit.Assert.assertEquals
import org.junit.Test

class SendTargetsTest {
    private fun peer(id: String, state: String, send: Boolean = true) =
        PeerInfo(deviceId = id, name = id, platform = "windows", state = state, sendEnabled = send)

    @Test
    fun onlineMeansConnectedAndSendable() {
        val peers = listOf(peer("a", "connected"), peer("b", "offline"), peer("c", "connected", send = false), peer("d", "connected"))
        assertEquals(listOf("a", "d"), SendTargets.online(peers))
    }

    @Test
    fun nobodyOnline() {
        assertEquals(emptyList<String>(), SendTargets.online(listOf(peer("b", "offline"))))
    }
}
