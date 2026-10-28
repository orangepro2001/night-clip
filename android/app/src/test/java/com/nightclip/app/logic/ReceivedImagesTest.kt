package com.nightclip.app.logic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ReceivedImagesTest {
    @Test
    fun previewableByTypeAndSize() {
        assertTrue(ReceivedImages.isPreviewable("IMG_1.JPG", 10))
        assertTrue(ReceivedImages.isPreviewable("a.webp", ReceivedImages.MAX_BYTES))
        assertFalse(ReceivedImages.isPreviewable("a.webp", ReceivedImages.MAX_BYTES + 1))
        assertFalse(ReceivedImages.isPreviewable("a.png", 0))
        assertFalse(ReceivedImages.isPreviewable("a.mp4", 10))
        assertFalse(ReceivedImages.isPreviewable("png", 10))
    }

    @Test
    fun mimeAndFolder() {
        assertEquals("image/jpeg", ReceivedImages.mimeOf("x.jpeg"))
        assertEquals("image/png", ReceivedImages.mimeOf("x.PNG"))
        assertEquals("tr_1-a", ReceivedImages.holdFolder("tr_1-a"))
        assertEquals("x", ReceivedImages.holdFolder("../x"))
        assertEquals("transfer", ReceivedImages.holdFolder("/.."))
    }

    @Test
    fun displayLocation() {
        assertEquals("Download/a.jpg", ReceivedImages.displayLocation("primary:Download/a.jpg"))
        assertNull(ReceivedImages.displayLocation("msf:1234"))
        assertNull(ReceivedImages.displayLocation(null))
        assertNull(ReceivedImages.displayLocation("1234"))
    }
}
