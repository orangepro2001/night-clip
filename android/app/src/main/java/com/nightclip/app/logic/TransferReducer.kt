package com.nightclip.app.logic

import com.nightclip.app.core.CoreEvent
import com.nightclip.app.core.TransferInfo

/** Keeps a transfer list current from file.* events; pure so it can be tested. */
object TransferReducer {
    fun knows(list: List<TransferInfo>, id: String) = list.any { it.transferId == id }

    fun apply(list: List<TransferInfo>, event: CoreEvent): List<TransferInfo> = when (event) {
        is CoreEvent.FileOffered ->
            if (knows(list, event.transferId)) {
                list
            } else {
                listOf(
                    TransferInfo(
                        transferId = event.transferId,
                        peerId = event.from,
                        peerName = event.fromName,
                        direction = "in",
                        name = event.name,
                        size = event.size,
                        sha256 = event.sha256,
                        state = "offered",
                    ),
                ) + list
            }
        is CoreEvent.FileState -> update(list, event.transferId) { it.copy(state = event.state) }
        is CoreEvent.FileProgress -> update(list, event.transferId) {
            it.copy(
                offset = event.offset,
                size = if (event.size > 0) event.size else it.size,
                rateBps = event.rateBps,
                state = if (it.state == "accepted" || it.state == "offered") "transferring" else it.state,
            )
        }
        is CoreEvent.FileDone -> update(list, event.transferId) {
            it.copy(state = "done", offset = it.size.coerceAtLeast(event.size), path = event.path.ifEmpty { it.path }, rateBps = 0)
        }
        is CoreEvent.FileFailed -> update(list, event.transferId) { it.copy(state = "failed", error = event.code, rateBps = 0) }
        else -> list
    }

    private inline fun update(list: List<TransferInfo>, id: String, f: (TransferInfo) -> TransferInfo) =
        list.map { if (it.transferId == id) f(it) else it }

    /** Event types that mean the list should be re-read from the core. */
    fun needsRefresh(list: List<TransferInfo>, event: CoreEvent): Boolean = when (event) {
        is CoreEvent.FileState -> !knows(list, event.transferId)
        is CoreEvent.FileProgress -> !knows(list, event.transferId)
        is CoreEvent.FileDone -> !knows(list, event.transferId)
        is CoreEvent.FileFailed -> !knows(list, event.transferId)
        else -> false
    }
}
