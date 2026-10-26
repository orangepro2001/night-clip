package com.nightclip.app.logic

/** Names for attachments and exported files. SAF display names are user-controlled text; they are never used as paths. */
object AttachmentNames {
    const val FALLBACK = "附件"
    private const val MAX_BYTES = 200

    /** Strips path separators, control characters and leading dots; shortens to a safe length; never empty. */
    fun sanitize(raw: String?): String {
        var s = (raw ?: "").map { c -> if (c == '/' || c == '\\' || c == ':' || c.isISOControl()) '_' else c }.joinToString("").trim()
        s = s.trimStart('.').trim()
        while (s.toByteArray(Charsets.UTF_8).size > MAX_BYTES) s = s.dropLast(1)
        return s.ifEmpty { FALLBACK }
    }

    /** "密码库备份-20261026.kdbx" for the SAF "create document" suggestion. */
    fun exportName(prefix: String, dateYyyyMmDd: String, extension: String): String = "$prefix-$dateYyyyMmDd.$extension"

    /** Guess for the "save attachment" dialog. */
    fun mimeFor(name: String): String = when (name.substringAfterLast('.', "").lowercase()) {
        "txt" -> "text/plain"
        "pdf" -> "application/pdf"
        "png" -> "image/png"
        "jpg", "jpeg" -> "image/jpeg"
        "csv" -> "text/csv"
        "json" -> "application/json"
        "zip" -> "application/zip"
        else -> "application/octet-stream"
    }
}
