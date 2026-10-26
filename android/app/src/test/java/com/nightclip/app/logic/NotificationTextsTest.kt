package com.nightclip.app.logic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class NotificationTextsTest {
    @Test
    fun shortTextIsKeptButWhitespaceIsFlattened() {
        assertEquals("a b c", NotificationTexts.preview("  a \n\n b\t c  "))
        assertEquals("", NotificationTexts.preview("   "))
    }

    @Test
    fun longTextIsCutAtEightyCodePointsWithAnEllipsis() {
        val long = "x".repeat(200)
        val p = NotificationTexts.preview(long)
        assertEquals(81, p.length)
        assertTrue(p.endsWith("…"))
        assertEquals("y".repeat(80), NotificationTexts.preview("y".repeat(80)))
    }

    @Test
    fun cuttingNeverSplitsASurrogatePair() {
        val emoji = "\uD83D\uDE00" // one code point, two chars
        val p = NotificationTexts.preview(emoji.repeat(100))
        assertEquals(80, p.codePointCount(0, p.length) - 1) // 80 emoji + the ellipsis
        assertFalse(Character.isHighSurrogate(p[p.length - 2]) && !Character.isLowSurrogate(p[p.length - 1]))
    }

    @Test
    fun sensitiveBodyIsAlwaysMasked() {
        assertEquals(Strings.SENSITIVE_HIDDEN, NotificationTexts.clipBody("hunter2", true))
        assertEquals("hello", NotificationTexts.clipBody("hello", false))
        assertFalse(NotificationTexts.clipBody("hunter2", true).contains("hunter2"))
    }

    @Test
    fun inboxRevealsSensitiveTextOnlyAfterATap() {
        assertEquals(Strings.SENSITIVE_HIDDEN, NotificationTexts.inboxBody("pw123", true, revealed = false))
        assertEquals("pw123", NotificationTexts.inboxBody("pw123", true, revealed = true))
        assertEquals("plain", NotificationTexts.inboxBody("plain", false, revealed = false))
    }

    @Test
    fun titlesAndCounts() {
        assertEquals("来自 Tony-PC 的剪贴板", Strings.clipTitle("Tony-PC"))
        assertEquals("来自 ${Strings.UNKNOWN_DEVICE} 的剪贴板", Strings.clipTitle(" "))
        assertEquals("已连接 3 台设备", Strings.connectedDevices(3))
        assertEquals("未连接设备", Strings.connectedDevices(0))
        assertEquals("a.pdf（1.5 KB）", Strings.fileOfferBody("a.pdf", 1536))
        assertEquals("PC 想发送文件", Strings.fileOfferTitle("PC"))
    }
}
