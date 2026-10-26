package com.nightclip.app.logic

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

    /** Local wall-clock "HH:mm" of a unix time (for "paused until 14:30"). */
    fun clockTime(epochSeconds: Long, zone: java.time.ZoneId = java.time.ZoneId.systemDefault()): String =
        java.time.format.DateTimeFormatter.ofPattern("HH:mm").withZone(zone).format(java.time.Instant.ofEpochSecond(epochSeconds))

    fun speed(bytesPerSecond: Long): String = fileSize(bytesPerSecond.coerceAtLeast(0)) + "/s"

    /** 0..100, or 0 when the total size is unknown. */
    fun percent(offset: Long, size: Long): Int {
        if (size <= 0) return 0
        return ((offset.coerceIn(0, size) * 100) / size).toInt()
    }

    /** Remaining time text such as "剩余 1 分 05 秒", or "" if the rate is unknown. */
    fun eta(offset: Long, size: Long, bytesPerSecond: Long): String {
        if (size <= 0 || bytesPerSecond <= 0) return ""
        val remaining = (size - offset).coerceAtLeast(0)
        if (remaining == 0L) return ""
        val seconds = (remaining + bytesPerSecond - 1) / bytesPerSecond
        return "剩余 " + duration(seconds)
    }

    fun duration(totalSeconds: Long): String {
        val s = totalSeconds.coerceAtLeast(0)
        return when {
            s < 1 -> "不到 1 秒"
            s < 60 -> "$s 秒"
            s < 3600 -> String.format(Locale.ROOT, "%d 分 %02d 秒", s / 60, s % 60)
            else -> String.format(Locale.ROOT, "%d 时 %02d 分", s / 3600, (s % 3600) / 60)
        }
    }

    /** Setting values such as clip_ttl_s: 60 -> "1 分钟", 3600 -> "1 小时", 86400 -> "1 天". */
    fun ttlLabel(seconds: Int): String = when {
        seconds < 3600 -> "${maxOf(seconds / 60, 1)} 分钟"
        seconds < 86400 -> "${seconds / 3600} 小时"
        else -> "${seconds / 86400} 天"
    }

    /** "45% · 12.3 MB / 27.0 MB · 5.2 MB/s · 剩余 3 秒". */
    fun progressLine(offset: Long, size: Long, bytesPerSecond: Long): String {
        val parts = mutableListOf("${percent(offset, size)}%")
        parts += "${fileSize(offset.coerceAtLeast(0))} / ${fileSize(size)}"
        if (bytesPerSecond > 0) parts += speed(bytesPerSecond)
        val eta = eta(offset, size, bytesPerSecond)
        if (eta.isNotEmpty()) parts += eta
        return parts.joinToString(" · ")
    }

    /** Relative time for "last seen"/"received"; times are Unix seconds, 0 means never. */
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

/** File-name handling for received files. Pure Kotlin. */
object FileNames {
    private const val MAX_LEN = 200
    private val FORBIDDEN = Regex("[\\u0000-\\u001f\\u007f<>:\"|?*]")

    /**
     * Makes a peer-supplied name safe to use as a MediaStore display name: keeps only the last
     * path component, drops control characters and characters Android file systems reject,
     * strips leading/trailing dots and spaces and caps the length (keeping the extension).
     */
    fun sanitize(raw: String): String {
        var name = raw.substringAfterLast('/').substringAfterLast('\\')
        name = FORBIDDEN.replace(name, "_").trim().trim('.').trim()
        if (name.isEmpty()) return "file"
        if (name.length > MAX_LEN) {
            val dot = name.lastIndexOf('.')
            val ext = if (dot > 0 && name.length - dot <= 16) name.substring(dot) else ""
            name = name.substring(0, MAX_LEN - ext.length) + ext
        }
        return name
    }

    /** "a.pdf" -> "a (1).pdf" -> "a (2).pdf" until [exists] says the candidate is free. */
    fun uniqueName(name: String, exists: (String) -> Boolean): String {
        if (!exists(name)) return name
        val dot = name.lastIndexOf('.')
        val stem = if (dot > 0) name.substring(0, dot) else name
        val ext = if (dot > 0) name.substring(dot) else ""
        for (i in 1..9999) {
            val candidate = "$stem ($i)$ext"
            if (!exists(candidate)) return candidate
        }
        return "$stem (${System.currentTimeMillis()})$ext"
    }

    /** Guesses a MIME type from the extension for MediaStore; unknown -> application/octet-stream. */
    fun mimeFromName(name: String, declared: String? = null): String {
        if (!declared.isNullOrBlank() && declared.contains('/')) return declared
        return when (name.substringAfterLast('.', "").lowercase(Locale.ROOT)) {
            "pdf" -> "application/pdf"
            "txt", "log", "md" -> "text/plain"
            "jpg", "jpeg" -> "image/jpeg"
            "png" -> "image/png"
            "gif" -> "image/gif"
            "webp" -> "image/webp"
            "mp4" -> "video/mp4"
            "mp3" -> "audio/mpeg"
            "zip" -> "application/zip"
            "apk" -> "application/vnd.android.package-archive"
            "json" -> "application/json"
            "doc" -> "application/msword"
            "docx" -> "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
            "xls" -> "application/vnd.ms-excel"
            "xlsx" -> "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
            else -> "application/octet-stream"
        }
    }
}
