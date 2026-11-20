package cloud.moonl.password.logic

import java.util.Locale

/** Number and time formatting shared by notifications and screens. Pure Kotlin. */
object Formatters {
    private val UNITS = arrayOf("B", "KB", "MB", "GB", "TB")

    /** 0 -> "0 B", 1536 -> "1.5 KB". Binary multiples (1 KB = 1024 B). Negative sizes are shown as unknown. */
    fun fileSize(bytes: Long): String {
        if (bytes < 0) return "未知大小"
        if (bytes < 1024) return "$bytes B"
        var value = bytes.toDouble()
        var unit = 0
        while (value >= 1024 && unit < UNITS.lastIndex) {
            value /= 1024
            unit++
        }
        return String.format(Locale.ROOT, "%.1f %s", value, UNITS[unit])
    }

    /** Local wall-clock "HH:mm" of a unix time. */
    fun clockTime(epochSeconds: Long, zone: java.time.ZoneId = java.time.ZoneId.systemDefault()): String =
        java.time.format.DateTimeFormatter.ofPattern("HH:mm").withZone(zone).format(java.time.Instant.ofEpochSecond(epochSeconds))

    fun duration(totalSeconds: Long): String {
        val s = totalSeconds.coerceAtLeast(0)
        return when {
            s < 1 -> "不到 1 秒"
            s < 60 -> "$s 秒"
            s < 3600 -> String.format(Locale.ROOT, "%d 分 %02d 秒", s / 60, s % 60)
            else -> String.format(Locale.ROOT, "%d 时 %02d 分", s / 3600, (s % 3600) / 60)
        }
    }

    /** Relative time for "last seen"/"last synced"; times are Unix seconds, 0 means never. */
    fun relativeTime(thenSec: Long, nowSec: Long): String {
        if (thenSec <= 0) return Strings.NEVER
        val d = nowSec - thenSec
        return when {
            d < 60 -> Strings.JUST_NOW
            d < 3600 -> Strings.minutesAgo(d / 60)
            d < 86400 -> Strings.hoursAgo(d / 3600)
            else -> Strings.daysAgo(d / 86400)
        }
    }
}
