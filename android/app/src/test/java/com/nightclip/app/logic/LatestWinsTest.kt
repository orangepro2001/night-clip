package com.nightclip.app.logic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class LatestWinsTest {
    private var now = 1_000L
    private val preview = LatestWins<String>({ now }, 150)

    @Test
    fun aValueRunsAfterTheQuietPeriod() {
        preview.submit("a")
        assertEquals(now + 150, preview.nextDueAt())
        now += 149
        assertNull(preview.tick())
        now += 1
        assertEquals(Pending(1, "a"), preview.tick())
        assertNull("a request is out: nothing to schedule", preview.nextDueAt())
    }

    @Test
    fun everyChangeRestartsTheQuietPeriod() {
        var text = ""
        repeat(100) {
            text += "x"
            preview.submit(text)
            now += 10 // a fast typist: 100 changes in a second
            assertNull("no request while the text is still changing (step $it)", preview.tick())
        }
        now += 150
        val request = preview.tick()!!
        assertEquals(100, request.value.length)
        assertEquals(1L, request.seq)
    }

    @Test
    fun immediateSkipsTheWait() {
        preview.submit("a", immediate = true)
        assertEquals(Pending(1, "a"), preview.tick())
    }

    @Test
    fun onlyOneRequestIsOutAndTheNewestValueFollows() {
        preview.submit("a", immediate = true)
        val first = preview.tick()!!
        preview.submit("b")
        now += 1000
        assertNull("the answer to the first request comes first", preview.tick())
        assertNull(preview.nextDueAt())
        assertTrue(preview.finish(first.seq, ok = true))
        assertEquals(now - 850, preview.nextDueAt()) // the value typed meanwhile is due at its own time
        assertEquals(Pending(2, "b"), preview.tick())
    }

    @Test
    fun anAnswerForARequestDroppedByResetIsStale() {
        preview.submit("a", immediate = true)
        val old = preview.tick()!!
        preview.reset() // another note was opened
        preview.submit("b", immediate = true)
        val current = preview.tick()!!
        assertFalse("the old answer must not replace anything", preview.finish(old.seq, ok = true))
        assertTrue(preview.finish(current.seq, ok = true))
    }

    @Test
    fun anAnswerIsOnlyAcceptedOnce() {
        preview.submit("a", immediate = true)
        val r = preview.tick()!!
        assertTrue(preview.finish(r.seq, ok = true))
        assertFalse(preview.finish(r.seq, ok = true))
        assertFalse(preview.finish(r.seq + 5, ok = true))
    }

    @Test
    fun theSameValueIsNotRunTwice() {
        preview.submit("a", immediate = true)
        preview.finish(preview.tick()!!.seq, ok = true)
        preview.submit("a")
        assertNull(preview.nextDueAt())
        assertNull(preview.tick())
    }

    @Test
    fun typingBackToTheValueThatIsRunningCancelsTheWaitingOne() {
        preview.submit("a", immediate = true)
        val r = preview.tick()!!
        preview.submit("b")
        preview.submit("a")
        assertTrue(preview.finish(r.seq, ok = true))
        assertNull(preview.nextDueAt())
        assertNull(preview.tick())
    }

    @Test
    fun invalidateRunsTheSameValueAgain() {
        preview.submit("a", immediate = true)
        preview.finish(preview.tick()!!.seq, ok = true)
        preview.invalidate()
        assertEquals(Pending(2, "a"), preview.tick())
    }

    @Test
    fun invalidateCanWaitAMoment() {
        preview.submit("a", immediate = true)
        preview.finish(preview.tick()!!.seq, ok = true)
        preview.invalidate(afterMs = 400)
        now += 399
        assertNull(preview.tick())
        now += 1
        assertEquals(Pending(2, "a"), preview.tick())
    }

    @Test
    fun invalidateWithNothingWantedDoesNothing() {
        preview.invalidate()
        assertNull(preview.nextDueAt())
        assertNull(preview.tick())
    }

    @Test
    fun aFailureIsFlaggedAndTheNextEditOrInvalidateRetries() {
        preview.submit("a", immediate = true)
        assertTrue(preview.finish(preview.tick()!!.seq, ok = false))
        assertTrue(preview.failed)
        assertNull("no retry loop by itself", preview.nextDueAt())
        preview.submit("a") // the same text again is tried again after a failure
        assertEquals(now + 150, preview.nextDueAt())
        now += 150
        assertTrue(preview.finish(preview.tick()!!.seq, ok = true))
        assertFalse(preview.failed)
    }

    @Test
    fun resetClearsTheFailure() {
        preview.submit("a", immediate = true)
        preview.finish(preview.tick()!!.seq, ok = false)
        preview.reset()
        assertFalse(preview.failed)
        assertNull(preview.nextDueAt())
    }

    @Test
    fun theSearchBoxUsesTheSameRulesWithItsOwnDelay() {
        val search = LatestWins<String>({ now }, NotesSearchLogic.DEBOUNCE_MS)
        search.submit("em")
        now += 100
        search.submit("emo")
        now += 249
        assertNull(search.tick())
        now += 1
        assertEquals(Pending(1, "emo"), search.tick())
    }
}
