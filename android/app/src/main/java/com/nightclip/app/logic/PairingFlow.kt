package com.nightclip.app.logic

import com.nightclip.app.core.CoreEvent
import com.nightclip.app.core.ErrorMessages

/** Joiner-side pairing screen state (shell-guide §1 "配对流程" steps 2 and 4). */
sealed interface PairUi {
    data object Idle : PairUi

    /** `pair.join` sent; [joinId] stays null until the call returns (events may arrive first). */
    data class Joining(val joinId: String?) : PairUi

    data class ShowSas(val joinId: String?, val sas: String) : PairUi {
        override fun toString() = "ShowSas(joinId=$joinId)"
    }

    data class Accepted(val name: String) : PairUi
    data class Failed(val code: String, val message: String) : PairUi
}

object PairingFlow {
    /** Only one join runs at a time, so a null id on our side matches any event. */
    private fun matches(ours: String?, theirs: String) = ours == null || theirs.isEmpty() || ours == theirs

    private fun idOf(s: PairUi): String? = when (s) {
        is PairUi.Joining -> s.joinId
        is PairUi.ShowSas -> s.joinId
        else -> null
    }

    fun reduce(state: PairUi, event: CoreEvent): PairUi {
        if (state !is PairUi.Joining && state !is PairUi.ShowSas) return state
        val id = idOf(state)
        return when (event) {
            is CoreEvent.PairSas -> if (matches(id, event.joinId)) PairUi.ShowSas(id ?: event.joinId.ifEmpty { null }, event.sas) else state
            is CoreEvent.PairResult -> when {
                event.role == "listener" || !matches(id, event.joinId) -> state
                event.state == "accepted" -> PairUi.Accepted(event.name)
                event.state == "rejected" -> PairUi.Failed("REJECTED", "对方拒绝了配对")
                event.state == "expired" -> PairUi.Failed("PAIRING_EXPIRED", ErrorMessages.forCode("PAIRING_EXPIRED"))
                else -> PairUi.Failed(event.code, ErrorMessages.forCode(event.code))
            }
            else -> state
        }
    }

    /** Applies the id returned by `pair.join` to a state that may already have progressed. */
    fun withJoinId(state: PairUi, joinId: String): PairUi = when (state) {
        is PairUi.Joining -> if (state.joinId == null) PairUi.Joining(joinId) else state
        is PairUi.ShowSas -> if (state.joinId == null) state.copy(joinId = joinId) else state
        else -> state
    }

    /** Groups "482913" as "482 913" for large display. */
    fun formatSas(sas: String): String = if (sas.length == 6) sas.substring(0, 3) + " " + sas.substring(3) else sas
}
