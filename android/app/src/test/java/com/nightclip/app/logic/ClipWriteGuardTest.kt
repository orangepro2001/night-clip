package com.nightclip.app.logic

import com.nightclip.app.platform.ClipWriteGuard
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ClipWriteGuardTest {
    @Test
    fun writesAreIgnoredForTwoSeconds() {
        val t = 1_790_000_000_000L
        assertTrue(ClipWriteGuard.shouldIgnore(t, t))
        assertTrue(ClipWriteGuard.shouldIgnore(t + 1_999, t))
        assertFalse(ClipWriteGuard.shouldIgnore(t + 2_000, t))
        assertFalse(ClipWriteGuard.shouldIgnore(t, 0))
        // A clock jump backwards must not create an unbounded suppression window.
        assertFalse(ClipWriteGuard.shouldIgnore(t - 1, t))
    }

    @Test
    fun customWindowIsHonored() {
        val t = 1_790_000_000_000L
        assertTrue(ClipWriteGuard.shouldIgnore(t + 499, t, windowMs = 500))
        assertFalse(ClipWriteGuard.shouldIgnore(t + 500, t, windowMs = 500))
    }
}
