package com.nightclip.app.logic

import com.nightclip.app.core.CoreEvent
import org.junit.Assert.assertEquals
import org.junit.Test

class ReceiveDeciderTest {
    private fun ev(decision: String, sensitive: Boolean = false) =
        CoreEvent.ClipReceived("e1", "dev_a", "PC", "hello", sensitive, decision, 1790000000)

    @Test
    fun notifyAndManualAlwaysNotify() {
        for (fg in listOf(true, false)) {
            for (decision in listOf("notify", "manual")) {
                val o = ReceiveDecider.decide(ev(decision), fg)
                assertEquals("$decision fg=$fg", ReceiveDecider.Action.NOTIFY, o.action)
                assertEquals(false, o.degraded)
            }
        }
    }

    @Test
    fun autoInForegroundWritesNow() {
        val o = ReceiveDecider.decide(ev("auto"), appInForeground = true)
        assertEquals(ReceiveDecider.Action.WRITE_NOW, o.action)
        assertEquals(false, o.degraded)
    }

    @Test
    fun autoInBackgroundDegradesToNotification() {
        val o = ReceiveDecider.decide(ev("auto"), appInForeground = false)
        assertEquals(ReceiveDecider.Action.NOTIFY, o.action)
        assertEquals(true, o.degraded)
    }

    @Test
    fun sensitiveIsNeverWrittenEvenIfCoreSaidAuto() {
        for (fg in listOf(true, false)) {
            val o = ReceiveDecider.decide(ev("auto", sensitive = true), fg)
            assertEquals(ReceiveDecider.Action.NOTIFY, o.action)
            assertEquals(true, o.degraded)
        }
    }

    @Test
    fun unknownDecisionIsTreatedAsNotify() {
        assertEquals(ReceiveDecider.Action.NOTIFY, ReceiveDecider.decide(ev("something-new"), true).action)
        assertEquals(ReceiveDecider.Action.NOTIFY, ReceiveDecider.decide(ev(""), true).action)
    }
}
