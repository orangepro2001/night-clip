package com.nightclip.app.logic

import com.nightclip.app.core.PeerInfo
import java.time.ZoneId
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class CaptureFilesTest {
    // 1793194212 = 2026-10-28T13:30:12Z
    private val takenAt = 1793194212L * 1000

    @Test
    fun photoNameCarriesTheLocalTimeOfTheGivenZone() {
        assertEquals("IMG_20261028_133012.jpg", CaptureFiles.photoName(takenAt, ZoneId.of("UTC")))
        assertEquals("IMG_20261028_223012.jpg", CaptureFiles.photoName(takenAt, ZoneId.of("Asia/Seoul")))
    }

    @Test
    fun photoNameIsAValidFileNameAndSurvivesTheSanitizer() {
        val name = CaptureFiles.photoName(takenAt, ZoneId.of("UTC"))
        assertEquals(name, FileNames.sanitize(name))
        assertTrue(name.endsWith(".jpg"))
    }

    @Test
    fun onlyPhotosOlderThanADayAreExpired() {
        val day = CaptureFiles.KEEP_MILLIS
        val files = listOf(
            "fresh.jpg" to takenAt - 1_000,
            "almost.jpg" to takenAt - day,          // exactly a day old: still kept
            "old.jpg" to takenAt - day - 1,
            "ancient.jpg" to 0L,
        )
        assertEquals(listOf("old.jpg", "ancient.jpg"), CaptureFiles.expired(files, takenAt))
        assertEquals(emptyList<String>(), CaptureFiles.expired(emptyList(), takenAt))
    }

    private fun peer(id: String, state: String) = PeerInfo(id, id, "windows", state)

    @Test
    fun everyoneMeansTheConnectedDevices() {
        val peers = listOf(peer("a", "connected"), peer("b", "offline"), peer("c", "connected"))
        assertEquals(listOf("a", "c"), CaptureFiles.everyoneTargets(peers))
    }

    @Test
    fun withNobodyConnectedEveryoneMeansEveryDeviceThatTrustsUs() {
        val peers = listOf(peer("a", "offline"), peer("b", "connecting"), peer("c", "revoked"), peer("d", "unpaired"))
        assertEquals(listOf("a", "b"), CaptureFiles.everyoneTargets(peers))
    }

    @Test
    fun noSendableDeviceMeansNoTargets() {
        assertEquals(emptyList<String>(), CaptureFiles.everyoneTargets(emptyList()))
        assertEquals(emptyList<String>(), CaptureFiles.everyoneTargets(listOf(peer("c", "revoked"), peer("d", "unpaired"))))
    }

    @Test
    fun targetsSurviveTheSavedStateRoundTrip() {
        val ids = listOf("dev_a", "dev_b")
        assertEquals(ids, CaptureFiles.decodeTargets(CaptureFiles.encodeTargets(ids)))
        assertEquals(emptyList<String>(), CaptureFiles.decodeTargets(CaptureFiles.encodeTargets(emptyList())))
        assertEquals(emptyList<String>(), CaptureFiles.decodeTargets(""))
    }
}
