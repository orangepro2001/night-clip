package com.nightclip.app.logic

import com.nightclip.app.core.AttachmentInfo
import java.util.Locale

/**
 * The address of an attachment inside a note: `blob:` plus the 64 hex digits of the file's SHA-256
 * (notes/api-spec.md §2.2). It names a file in the notes' own store. It is NOT a URL: it never goes through
 * [NoteLinks] or [UrlGuard], and nothing hands it to the system.
 */
object BlobAddress {
    private val ADDRESS = Regex("^blob:([0-9a-fA-F]{64})$")

    /** The blob id (lower case) of a well-formed address, otherwise null. */
    fun parse(href: String): String? = ADDRESS.matchEntire(href.trim())?.groupValues?.get(1)?.lowercase(Locale.ROOT)

    /** Whether a link claims to be a blob address, valid or not. Such a link is never a web link. */
    fun claims(href: String): Boolean = href.trim().startsWith("blob:", ignoreCase = true)

    fun isBlobId(id: String): Boolean = id.length == 64 && id.all { it in '0'..'9' || it in 'a'..'f' }
}

/** What the preview knows about one attachment. */
sealed interface AttachmentState {
    /** Not asked yet, or the core is being asked. */
    data object Loading : AttachmentState

    /** The note refers to it but the file has not synced to this device yet; the `notes.attachment` event ends this state. */
    data object Waiting : AttachmentState

    /** A picture, exported to [path] in the cache; [name] is for the description. */
    data class Picture(val path: String, val name: String, val size: Long) : AttachmentState

    /** A file that can be opened, saved or shared. */
    data class Document(val name: String, val mime: String, val size: Long) : AttachmentState

    /** The file is there but cannot be shown (damaged image, no space in the cache …). */
    data class Failed(val name: String) : AttachmentState
}

/** File names, kinds and safety rules for attachments. Pure. */
object AttachmentFiles {
    /** The picture types the core calls images (SVG is deliberately not one: it can carry scripts). */
    private val IMAGE_MIMES = setOf("image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp")

    private val EXTENSIONS = mapOf(
        "image/png" to "png", "image/jpeg" to "jpg", "image/gif" to "gif", "image/webp" to "webp", "image/bmp" to "bmp",
    )

    /** Types and extensions the app does not hand to another app to run or render with its own privileges. */
    private val NEVER_OPEN_EXTENSIONS = setOf(
        "apk", "apks", "xapk", "aab", "dex", "jar", "exe", "msi", "bat", "cmd", "com", "scr", "pif", "vbs", "vbe", "js", "jse", "wsf", "wsh",
        "ps1", "psm1", "sh", "bash", "run", "bin", "elf", "so", "dll", "lnk", "reg", "hta", "cpl", "jnlp", "appimage", "deb", "rpm", "dmg",
        "pkg", "app", "command", "html", "htm", "xhtml", "mht", "mhtml", "svg", "svgz",
    )

    private val NEVER_OPEN_MIMES = setOf(
        "application/vnd.android.package-archive", "application/x-msdownload", "application/x-msdos-program", "application/x-dosexec",
        "application/x-sh", "application/x-shellscript", "application/java-archive", "application/x-executable", "application/x-elf",
        "text/html", "application/xhtml+xml", "image/svg+xml", "application/javascript", "text/javascript",
    )

    fun baseMime(mime: String): String = mime.substringBefore(';').trim().lowercase(Locale.ROOT)

    fun isImage(mime: String): Boolean = baseMime(mime) in IMAGE_MIMES

    /** The extension of a file name, lower case, only letters and digits (at most 8); "" when there is none. */
    fun extensionOf(name: String): String {
        val ext = name.substringAfterLast('.', "").lowercase(Locale.ROOT)
        return if (ext.length in 1..8 && ext.all { it in 'a'..'z' || it in '0'..'9' }) ext else ""
    }

    /** `<blob id>.<ext>` for the picture cache: the id is validated, the extension comes from the type or the name. */
    fun pictureCacheName(blobId: String, name: String, mime: String): String? {
        if (!BlobAddress.isBlobId(blobId)) return null
        val ext = EXTENSIONS[baseMime(mime)] ?: extensionOf(name).ifEmpty { "img" }
        return "$blobId.$ext"
    }

    /** The name of the temporary copy handed to `notes.attachment_add`; the core takes the attachment's name from it. */
    fun uploadName(displayName: String?): String = FileNames.sanitize(displayName.orEmpty())

    /**
     * The real name of a file in the open folder: a path from a note cannot reach outside it, and an empty or odd
     * name becomes "file".
     */
    fun openName(name: String): String = FileNames.sanitize(name)

    /** Folder (inside the open cache) for one attachment, so two different files with the same name never replace each other. */
    fun openFolder(blobId: String): String = if (BlobAddress.isBlobId(blobId)) blobId.take(16) else "x"

    /** Executables, installers, scripts and web pages are not opened from a note; the user can save or share them. */
    fun isSafeToOpen(name: String, mime: String): Boolean =
        extensionOf(name) !in NEVER_OPEN_EXTENSIONS && baseMime(mime) !in NEVER_OPEN_MIMES

    /** The MIME type to open a file with: the core's, or a generic one. */
    fun openMime(mime: String): String = baseMime(mime).ifEmpty { "application/octet-stream" }

    /** Text of a chip: "📎 name (1.5 MB)". */
    fun chipText(name: String, size: Long?): String =
        "📎 " + name.ifBlank { "file" } + if (size != null && size >= 0) " (${Formatters.fileSize(size)})" else ""

    /**
     * What to show for the core's answer about one attachment. A picture needs an export first, so it is [Picture] only
     * once the caller has a file at [pictureFile]; here: which kind it is.
     */
    fun kindOf(info: AttachmentInfo): Kind = when {
        !info.present -> Kind.ABSENT
        isImage(info.mime) -> Kind.PICTURE
        else -> Kind.DOCUMENT
    }

    enum class Kind { ABSENT, PICTURE, DOCUMENT }
}

/** How to turn a picture upright after decoding: rotate clockwise by [rotation] degrees, then mirror left-right if [flipHorizontal]. */
data class ImageTransform(val rotation: Int, val flipHorizontal: Boolean)

/** How big a picture to decode and how to turn it upright. Pure. */
object ImageSampling {
    /** The EXIF orientation values 1..8 (1 = as stored); anything else is treated as 1. */
    fun exifTransform(orientation: Int): ImageTransform = when (orientation) {
        2 -> ImageTransform(0, true)
        3 -> ImageTransform(180, false)
        4 -> ImageTransform(180, true)
        5 -> ImageTransform(90, true)
        6 -> ImageTransform(90, false)
        7 -> ImageTransform(270, true)
        8 -> ImageTransform(270, false)
        else -> ImageTransform(0, false)
    }

    /** A decoded picture may use at most this many pixels (4 bytes each): 4 MP are 16 MB, however large the file is. */
    const val MAX_PIXELS = 4_000_000

    /**
     * The power of two for `BitmapFactory.Options.inSampleSize`: as large as it can be while the width stays at least
     * [targetWidth] (so the picture is not blurrier than needed), and large enough that the result has at most
     * [maxPixels] pixels.
     */
    fun sampleSize(width: Int, height: Int, targetWidth: Int, maxPixels: Int = MAX_PIXELS): Int {
        if (width <= 0 || height <= 0) return 1
        var n = 1
        while (n < 1 shl 12 && width / (n * 2) >= targetWidth && height / (n * 2) >= 1) n *= 2
        while (n < 1 shl 12 && (width.toLong() / n) * (height.toLong() / n) > maxPixels) n *= 2
        return n
    }
}
