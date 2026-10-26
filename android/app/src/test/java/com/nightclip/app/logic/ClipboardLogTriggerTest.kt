package com.nightclip.app.logic

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ClipboardLogTriggerTest {
    private val pkg = "com.nightclip.app"

    // Wording from AOSP 11-15 (android-11.0.0_r1 .. android-15.0.0_r1 ClipboardService.java).
    private val android12 =
        "E ClipboardService: Denying clipboard access to com.nightclip.app, application is not in focus nor is it a system service for user 0"

    // AOSP 10 used "neither" instead of "nor".
    private val android10 =
        "E ClipboardService: Denying clipboard access to com.nightclip.app, application is not in focus neither is a system service for user 0"

    @Test
    fun acopyDeniedForOurPackageIsATrigger() {
        assertTrue(ClipboardLogTrigger.matches(android12, pkg))
        assertTrue(ClipboardLogTrigger.matches(android10, pkg))
        // A slightly different OEM wording still contains the same facts.
        assertTrue(
            ClipboardLogTrigger.matches(
                "W ClipboardService: clipboard access DENIED for com.nightclip.app (not focused)",
                pkg,
            ),
        )
    }

    @Test
    fun otherAppsAndUnrelatedLinesDoNotTrigger() {
        assertFalse(ClipboardLogTrigger.matches(android12.replace(pkg, "com.example.other"), pkg))
        assertFalse(ClipboardLogTrigger.matches("ClipboardService: com.nightclip.app listener added", pkg))
        assertFalse(ClipboardLogTrigger.matches("", pkg))
        assertFalse(ClipboardLogTrigger.matches(android12, ""))
        assertFalse(ClipboardLogTrigger.matches("Denying clipboard access to somebody else", pkg))
    }

    @Test
    fun aBurstIsDebounced() {
        val last = 1_790_000_000_000L
        assertTrue(ClipboardLogTrigger.isDebounced(last + 1, last))
        assertTrue(ClipboardLogTrigger.isDebounced(last + 799, last))
        assertFalse(ClipboardLogTrigger.isDebounced(last + 800, last))
        assertFalse(ClipboardLogTrigger.isDebounced(last, 0))
    }
}
