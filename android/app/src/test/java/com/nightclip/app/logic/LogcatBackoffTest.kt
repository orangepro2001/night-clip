package com.nightclip.app.logic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class LogcatBackoffTest {
    @Test
    fun backoffGrowsAndThenStops() {
        assertEquals(0L, LogcatBackoff.delayMs(0))
        assertEquals(1_000L, LogcatBackoff.delayMs(1))
        assertEquals(2_000L, LogcatBackoff.delayMs(2))
        assertEquals(5_000L, LogcatBackoff.delayMs(3))
        assertEquals(10_000L, LogcatBackoff.delayMs(4))
        assertEquals(30_000L, LogcatBackoff.delayMs(5))
        assertEquals(30_000L, LogcatBackoff.delayMs(9))
    }

    @Test
    fun fiveConsecutiveFailuresAreEnough() {
        assertFalse(LogcatBackoff.shouldStop(4))
        assertTrue(LogcatBackoff.shouldStop(5))
        assertTrue(LogcatBackoff.shouldStop(6))
    }
}
