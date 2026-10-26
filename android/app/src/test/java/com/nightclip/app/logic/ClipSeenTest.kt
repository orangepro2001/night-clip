package com.nightclip.app.logic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ClipSeenTest {
    @Test
    fun theFirstLookClaimsTheBaseline() {
        val seen = ClipSeen(null)
        assertTrue(seen.claim(null, "hash-a"))
        assertEquals("hash-a", seen.current)
        // A second observer that captured the same "previous" loses the race.
        assertFalse(seen.claim(null, "hash-a"))
    }

    @Test
    fun onlyTheObserverThatSawTheCurrentValueCanReplaceIt() {
        val seen = ClipSeen("hash-a")
        assertFalse(seen.claim(null, "hash-x"))
        assertTrue(seen.claim("hash-a", "hash-b"))
        assertEquals("hash-b", seen.current)
        assertFalse(seen.claim("hash-a", "hash-c"))
    }

    @Test
    fun rollbackDoesNotUndoANewerClaim() {
        val seen = ClipSeen("hash-a")
        assertTrue(seen.claim("hash-a", "hash-b"))
        assertFalse(seen.rollback("hash-a", "hash-a")) // someone else already claimed
        assertTrue(seen.rollback("hash-b", "hash-a"))
        assertEquals("hash-a", seen.current)
    }
}
