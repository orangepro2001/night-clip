package com.nightclip.app.logic

/**
 * Decides which links of a note may be handed to the system when the user taps them: http, https and mailto only.
 * Anything else (javascript:, intent:, file:, content:, tel: …) and anything without a scheme is not opened. A note is
 * text from another device or from the web, so unlike the vault's URL field a bare "example.com" is not guessed.
 */
object NoteLinks {
    private val SCHEME = Regex("^([A-Za-z][A-Za-z0-9+.-]*):")
    private const val MAX_LEN = 2048

    /** The URL to open, or null when the link is not openable. */
    fun openable(href: String): String? {
        val t = href.trim()
        if (t.isEmpty() || t.length > MAX_LEN || t.any { it.isWhitespace() || it.isISOControl() }) return null
        val scheme = SCHEME.find(t)?.groupValues?.get(1)?.lowercase() ?: return null
        return when (scheme) {
            "http", "https" -> UrlGuard.webUrl(t)
            "mailto" -> mailto(t)
            else -> null
        }
    }

    private fun mailto(t: String): String? {
        val rest = t.substring("mailto:".length)
        val address = rest.substringBefore('?')
        // One "local@host" at least; the mail app does the real validation.
        return if (address.indexOf('@') in 1 until address.length - 1) t else null
    }
}
