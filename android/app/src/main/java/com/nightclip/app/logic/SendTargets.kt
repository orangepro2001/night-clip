package com.nightclip.app.logic

import com.nightclip.app.core.PeerInfo

/** Who typed text goes to (same rule on all three shells). */
object SendTargets {
    /** The typed-text broadcast: every connected device that may be sent to and is not switched off for broadcasts
     * (like the core's "everyone"). Offline ones are left out, not queued. */
    fun online(peers: List<PeerInfo>): List<String> = peers.filter { it.isConnected && it.canSend && it.sendEnabled }.map { it.deviceId }
}
