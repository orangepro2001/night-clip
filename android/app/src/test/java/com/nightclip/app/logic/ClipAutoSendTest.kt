package com.nightclip.app.logic

import com.nightclip.app.logic.ClipAutoSend.Verdict
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Test

class ClipAutoSendTest {
    private val now = 1_790_000_000_000L

    @Test
    fun emptyOrBlankClipboardIsSkipped() {
        assertEquals(Verdict.SKIP, ClipAutoSend.decide(null, 0, now, null))
        assertEquals(Verdict.SKIP, ClipAutoSend.decide("", 0, now, "x"))
        assertEquals(Verdict.SKIP, ClipAutoSend.decide("  \n\t", now, now, "x"))
    }

    @Test
    fun theFirstLookEverOnlyRecordsABaseline() {
        // whatever was on the clipboard before the app first ran must not be broadcast
        assertEquals(Verdict.BASELINE, ClipAutoSend.decide("old secret", now, now, null))
    }

    @Test
    fun theSameTextIsNotSentTwice() {
        val h = ClipAutoSend.hash("hello")
        assertEquals(Verdict.SKIP, ClipAutoSend.decide("hello", now, now, h))
    }

    @Test
    fun freshNewTextIsSent() {
        val h = ClipAutoSend.hash("hello")
        assertEquals(Verdict.SEND, ClipAutoSend.decide("world", now - 10_000, now, h))
        // the platform did not say when it was copied: trust it
        assertEquals(Verdict.SEND, ClipAutoSend.decide("world", 0, now, h))
    }

    @Test
    fun anOldClipIsRecordedButNeverSentOnItsOwn() {
        val h = ClipAutoSend.hash("hello")
        assertEquals(Verdict.BASELINE, ClipAutoSend.decide("world", now - ClipAutoSend.MAX_AGE_MS - 1, now, h))
        assertEquals(Verdict.SEND, ClipAutoSend.decide("world", now - ClipAutoSend.MAX_AGE_MS, now, h))
    }

    @Test
    fun hashIsStableSha256Hex() {
        assertEquals("2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", ClipAutoSend.hash("hello"))
        assertNotEquals(ClipAutoSend.hash("hello"), ClipAutoSend.hash("hello\n"))
    }
}
