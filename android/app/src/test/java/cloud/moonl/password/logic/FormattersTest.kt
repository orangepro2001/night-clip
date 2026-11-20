package cloud.moonl.password.logic

import org.junit.Assert.assertEquals
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
    fun durations() {
        assertEquals("不到 1 秒", Formatters.duration(0))
        assertEquals("59 秒", Formatters.duration(59))
        assertEquals("1 分 00 秒", Formatters.duration(60))
        assertEquals("1 时 00 分", Formatters.duration(3600))
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
}
