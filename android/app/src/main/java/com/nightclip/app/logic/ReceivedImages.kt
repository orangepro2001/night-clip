package com.nightclip.app.logic

/**
 * Received images are not saved straight away (same rule as the desktop shells' ReceivedImages.cs): the file is
 * held in `<cacheDir>/received-images/<transfer>/`, previewed, and written only where the user picks with
 * "保存到…". Leaving the preview discards it. Other files still go to Downloads as before.
 */
object ReceivedImages {
    /** Sub-folder of the cache directory; must match the `received_images` entry in res/xml/update_paths.xml. */
    const val DIR = "received-images"

    /** Larger images are saved to Downloads like any file (decoding them for a preview costs too much memory). */
    const val MAX_BYTES = 64L shl 20

    private val MIME = mapOf(
        "png" to "image/png", "jpg" to "image/jpeg", "jpeg" to "image/jpeg",
        "gif" to "image/gif", "bmp" to "image/bmp", "webp" to "image/webp",
    )

    fun extension(name: String): String = name.substringAfterLast('.', "").lowercase()

    fun isPreviewable(name: String, size: Long): Boolean = extension(name) in MIME && size in 1..MAX_BYTES

    fun mimeOf(name: String): String = MIME[extension(name)] ?: "image/*"

    /** A folder-safe name for the per-transfer hold folder. */
    fun holdFolder(transferId: String): String =
        transferId.filter { it.isLetterOrDigit() && it.code < 128 || it == '-' || it == '_' }.ifEmpty { "transfer" }

    /**
     * A readable location for a saved document, from its SAF document id ("primary:Download/a.jpg" →
     * "Download/a.jpg"); null when the provider uses opaque ids.
     */
    fun displayLocation(documentId: String?): String? {
        val id = documentId ?: return null
        val path = id.substringAfter(':', "")
        // "msf:1234" (Downloads provider) and bare numbers are database ids, not paths.
        return path.takeIf { it.isNotBlank() && id.substringBefore(':') != "msf" && !it.all(Char::isDigit) }
    }
}
