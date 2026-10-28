package com.nightclip.app.logic

import org.junit.Assert.assertEquals
import org.junit.Test

class PlatformLabelTest {
    @Test
    fun platformLabels() {
        assertEquals("Windows", PeerTexts.platformLabel("windows"))
        assertEquals("Android", PeerTexts.platformLabel("android"))
        assertEquals("macOS", PeerTexts.platformLabel("macos"))
        assertEquals("", PeerTexts.platformLabel(""))
        assertEquals("linux", PeerTexts.platformLabel("linux"))
    }
}
