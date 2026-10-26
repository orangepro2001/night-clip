package com.nightclip.app.logic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class BackgroundReadSetupTest {
    private fun facts(
        enabled: Boolean = true,
        realtime: Boolean = true,
        overlay: Boolean = true,
        logs: Boolean = true,
        service: Boolean = true,
        watcher: Boolean = true,
        error: Boolean = false,
        supported: Boolean = true,
    ) = BackgroundReadFacts(enabled, realtime, overlay, logs, service, watcher, error, supported)

    @Test
    fun everythingGrantedAndRunningIsTheOnlyRunnableCombination() {
        assertTrue(BackgroundReadSetup.canRun(facts()))
        assertFalse(BackgroundReadSetup.canRun(facts(enabled = false)))
        assertFalse(BackgroundReadSetup.canRun(facts(realtime = false)))
        assertFalse(BackgroundReadSetup.canRun(facts(overlay = false)))
        assertFalse(BackgroundReadSetup.canRun(facts(logs = false)))
        assertFalse(BackgroundReadSetup.canRun(facts(service = false)))
        // watcherRunning is an observation, not a requirement: STARTING is still runnable.
        assertTrue(BackgroundReadSetup.canRun(facts(watcher = false)))
    }

    @Test
    fun statusReportsTheFirstMissingThingInSetupOrder() {
        assertEquals(BackgroundReadStatus.OFF, BackgroundReadSetup.status(facts(enabled = false)))
        assertEquals(BackgroundReadStatus.REALTIME_OFF, BackgroundReadSetup.status(facts(realtime = false, overlay = false, logs = false)))
        assertEquals(BackgroundReadStatus.NEEDS_OVERLAY, BackgroundReadSetup.status(facts(overlay = false, logs = false)))
        assertEquals(BackgroundReadStatus.NEEDS_LOGS, BackgroundReadSetup.status(facts(logs = false)))
        assertEquals(BackgroundReadStatus.WAITING_SERVICE, BackgroundReadSetup.status(facts(service = false)))
        assertEquals(BackgroundReadStatus.STARTING, BackgroundReadSetup.status(facts(watcher = false)))
        assertEquals(BackgroundReadStatus.RUNNING, BackgroundReadSetup.status(facts()))
    }

    @Test
    fun androidNineAndOlderAreNotOffered() {
        val old = facts(supported = false)
        assertEquals(BackgroundReadStatus.UNSUPPORTED, BackgroundReadSetup.status(old))
        assertFalse(BackgroundReadSetup.canRun(old))
        assertEquals(emptyList<BackgroundReadStep>(), BackgroundReadSetup.missing(old))
        // Turning the feature off still reads as OFF, not UNSUPPORTED.
        assertEquals(BackgroundReadStatus.OFF, BackgroundReadSetup.status(facts(enabled = false, supported = false)))
    }

    @Test
    fun errorWinsOnceTheFeatureIsOn() {
        assertEquals(BackgroundReadStatus.ERROR, BackgroundReadSetup.status(facts(error = true)))
        assertEquals(BackgroundReadStatus.OFF, BackgroundReadSetup.status(facts(enabled = false, error = true)))
    }

    @Test
    fun missingStepsFollowTheSetupWizardOrder() {
        assertEquals(
            listOf(BackgroundReadStep.REALTIME, BackgroundReadStep.OVERLAY, BackgroundReadStep.LOGS),
            BackgroundReadSetup.missing(facts(realtime = false, overlay = false, logs = false)),
        )
        assertEquals(listOf(BackgroundReadStep.OVERLAY), BackgroundReadSetup.missing(facts(overlay = false)))
        assertEquals(emptyList<BackgroundReadStep>(), BackgroundReadSetup.missing(facts()))
        // An off switch has no setup work to show.
        assertEquals(emptyList<BackgroundReadStep>(), BackgroundReadSetup.missing(facts(enabled = false)))
    }

    @Test
    fun adbCommandsCarryTheRealPackageNameAndARestart() {
        val text = BackgroundReadSetup.adbCommands("com.nightclip.app")
        assertTrue(text.contains("adb shell pm grant com.nightclip.app android.permission.READ_LOGS"))
        assertTrue(text.contains("adb shell am force-stop com.nightclip.app"))
        assertTrue(BackgroundReadSetup.overlayAdbCommand("com.nightclip.app").contains("SYSTEM_ALERT_WINDOW"))
    }
}
