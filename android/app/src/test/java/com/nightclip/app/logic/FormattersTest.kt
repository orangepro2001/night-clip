package com.nightclip.app.logic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class FormattersTest {
    @Test
    fun fileSizes() {
        assertEquals("0 B", Formatters.fileSize(0))
        assertEquals("1023 B", Formatters.fileSize(1023))
        assertEquals("1.0 KB", Formatters.fileSize(1024))
        assertEquals("1.5 KB", Formatters.fileSize(1536))
        assertEquals("117.7 MB", Formatters.fileSize(123_456_789))
        assertEquals("4.0 GB", Formatters.fileSize(4L * 1024 * 1024 * 1024))
        assertEquals("1.0 TB", Formatters.fileSize(1L shl 40))
        assertEquals("2048.0 TB", Formatters.fileSize(2048L shl 40))
        assertEquals("未知大小", Formatters.fileSize(-1))
    }

    @Test
    fun clockTimeUsesTheGivenZone() {
        // 1790003600 = 2026-09-21T15:13:20Z
        assertEquals("15:13", Formatters.clockTime(1790003600, java.time.ZoneOffset.UTC))
        assertEquals("00:13", Formatters.clockTime(1790003600, java.time.ZoneOffset.ofHours(9)))
    }

    @Test
    fun speeds() {
        assertEquals("50.0 MB/s", Formatters.speed(52_428_800))
        assertEquals("0 B/s", Formatters.speed(-5))
    }

    @Test
    fun percentIsClamped() {
        assertEquals(0, Formatters.percent(0, 0))
        assertEquals(50, Formatters.percent(50, 100))
        assertEquals(100, Formatters.percent(150, 100))
        assertEquals(0, Formatters.percent(-5, 100))
        assertEquals(99, Formatters.percent(999, 1000))
    }

    @Test
    fun etaFormats() {
        assertEquals("剩余 10 秒", Formatters.eta(0, 1000, 100))
        assertEquals("剩余 1 分 05 秒", Formatters.eta(0, 6500, 100))
        assertEquals("剩余 2 时 03 分", Formatters.eta(0, 7380 * 100L, 100))
        assertEquals("剩余 1 秒", Formatters.eta(999, 1000, 100)) // rounds up
        assertEquals("", Formatters.eta(0, 1000, 0)) // unknown speed
        assertEquals("", Formatters.eta(1000, 1000, 100)) // finished
        assertEquals("", Formatters.eta(0, 0, 100))
    }

    @Test
    fun durations() {
        assertEquals("不到 1 秒", Formatters.duration(0))
        assertEquals("59 秒", Formatters.duration(59))
        assertEquals("1 分 00 秒", Formatters.duration(60))
        assertEquals("1 时 00 分", Formatters.duration(3600))
    }

    @Test
    fun progressLine() {
        assertEquals("50% · 500 B / 1000 B · 100 B/s · 剩余 5 秒", Formatters.progressLine(500, 1000, 100))
        assertEquals("0% · 0 B / 1000 B", Formatters.progressLine(0, 1000, 0))
        val line = Formatters.progressLine(12_900_000, 28_300_000, 5_452_595)
        assertTrue(line, line.startsWith("45% · "))
        assertTrue(line, line.contains("MB/s"))
    }

    @Test
    fun relativeTimes() {
        val now = 1_000_000L
        assertEquals(Strings.NEVER, Formatters.relativeTime(0, now))
        assertEquals("刚刚", Formatters.relativeTime(now - 30, now))
        assertEquals("5 分钟前", Formatters.relativeTime(now - 300, now))
        assertEquals("2 小时前", Formatters.relativeTime(now - 7200, now))
        assertEquals("3 天前", Formatters.relativeTime(now - 3 * 86400, now))
        assertEquals("刚刚", Formatters.relativeTime(now + 100, now)) // clock skew
    }

    @Test
    fun ttlLabels() {
        assertEquals("1 分钟", Formatters.ttlLabel(60))
        assertEquals("15 分钟", Formatters.ttlLabel(900))
        assertEquals("1 小时", Formatters.ttlLabel(3600))
        assertEquals("1 天", Formatters.ttlLabel(86400))
        assertEquals("1 分钟", Formatters.ttlLabel(30))
    }
}

class FileNamesTest {
    @Test
    fun sanitizeKeepsOrdinaryNames() {
        assertEquals("report.pdf", FileNames.sanitize("report.pdf"))
        assertEquals("报告 最终版.docx", FileNames.sanitize("报告 最终版.docx"))
    }

    @Test
    fun sanitizeStripsPathsAndForbiddenCharacters() {
        assertEquals("passwd", FileNames.sanitize("../../etc/passwd"))
        assertEquals("evil.exe", FileNames.sanitize("C:\\Windows\\evil.exe"))
        assertEquals("a_b_c.txt", FileNames.sanitize("a:b*c.txt"))
        assertEquals("ab.txt", FileNames.sanitize("a\u0000b.txt".replace("\u0000", "")))
        assertEquals("a_b.txt", FileNames.sanitize("a\u0001b.txt"))
    }

    @Test
    fun sanitizeHandlesEmptyAndDotNames() {
        assertEquals("file", FileNames.sanitize(""))
        assertEquals("file", FileNames.sanitize("   "))
        assertEquals("file", FileNames.sanitize(".."))
        assertEquals("file", FileNames.sanitize("a/"))
        assertEquals("hidden", FileNames.sanitize(".hidden"))
    }

    @Test
    fun sanitizeCapsLengthButKeepsTheExtension() {
        val n = FileNames.sanitize("a".repeat(500) + ".pdf")
        assertEquals(200, n.length)
        assertTrue(n.endsWith(".pdf"))
    }

    @Test
    fun uniqueNameAddsCounters() {
        assertEquals("a.pdf", FileNames.uniqueName("a.pdf") { false })
        assertEquals("a (1).pdf", FileNames.uniqueName("a.pdf") { it == "a.pdf" })
        assertEquals("a (2).pdf", FileNames.uniqueName("a.pdf") { it == "a.pdf" || it == "a (1).pdf" })
        assertEquals("noext (1)", FileNames.uniqueName("noext") { it == "noext" })
        assertEquals("a.tar (1).gz", FileNames.uniqueName("a.tar.gz") { it == "a.tar.gz" })
        assertEquals(".bashrc (1)", FileNames.uniqueName(".bashrc") { it == ".bashrc" })
    }

    @Test
    fun mimeGuessing() {
        assertEquals("application/pdf", FileNames.mimeFromName("a.PDF"))
        assertEquals("image/png", FileNames.mimeFromName("x.png", null))
        assertEquals("application/octet-stream", FileNames.mimeFromName("x.bin"))
        assertEquals("image/jpeg", FileNames.mimeFromName("x.bin", "image/jpeg"))
        assertEquals("application/pdf", FileNames.mimeFromName("x.pdf", "garbage"))
        assertFalse(FileNames.mimeFromName("noext").isEmpty())
    }
}
