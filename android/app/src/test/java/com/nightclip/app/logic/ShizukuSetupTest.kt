package com.nightclip.app.logic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ShizukuSetupTest {
    private fun facts(
        enabled: Boolean = true,
        realtime: Boolean = true,
        installed: Boolean = true,
        running: Boolean = true,
        permitted: Boolean = true,
        service: Boolean = true,
        connected: Boolean = true,
        error: Boolean = false,
    ) = ShizukuFacts(enabled, realtime, installed, running, permitted, service, connected, error)

    @Test
    fun theHelperMayOnlyStartWhenEverythingIsInPlace() {
        assertTrue(ShizukuSetup.canRun(facts(connected = false)))
        assertFalse(ShizukuSetup.canRun(facts(enabled = false)))
        assertFalse(ShizukuSetup.canRun(facts(realtime = false)))
        assertFalse(ShizukuSetup.canRun(facts(installed = false)))
        assertFalse(ShizukuSetup.canRun(facts(running = false)))
        assertFalse(ShizukuSetup.canRun(facts(permitted = false)))
        assertFalse(ShizukuSetup.canRun(facts(service = false)))
    }

    @Test
    fun statusNamesTheFirstMissingStep() {
        assertEquals(ShizukuStatus.OFF, ShizukuSetup.status(facts(enabled = false)))
        assertEquals(ShizukuStatus.REALTIME_OFF, ShizukuSetup.status(facts(realtime = false)))
        assertEquals(ShizukuStatus.NOT_INSTALLED, ShizukuSetup.status(facts(installed = false, running = false)))
        assertEquals(ShizukuStatus.NOT_RUNNING, ShizukuSetup.status(facts(running = false, permitted = false)))
        assertEquals(ShizukuStatus.NEEDS_PERMISSION, ShizukuSetup.status(facts(permitted = false)))
        assertEquals(ShizukuStatus.WAITING_SERVICE, ShizukuSetup.status(facts(service = false)))
        assertEquals(ShizukuStatus.CONNECTING, ShizukuSetup.status(facts(connected = false)))
        assertEquals(ShizukuStatus.RUNNING, ShizukuSetup.status(facts()))
    }

    @Test
    fun aRecordedErrorShowsOnlyOnceEverythingElseIsInPlace() {
        assertEquals(ShizukuStatus.ERROR, ShizukuSetup.status(facts(error = true, connected = false)))
        // a missing step is more useful than a stale error
        assertEquals(ShizukuStatus.NOT_RUNNING, ShizukuSetup.status(facts(error = true, running = false, permitted = false)))
    }

    @Test
    fun anUnconnectedHelperIsNeverReportedAsRunning() {
        assertFalse(ShizukuSetup.status(facts(connected = false)) == ShizukuStatus.RUNNING)
    }
}
