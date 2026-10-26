package com.nightclip.app.logic

import java.net.URI

/** Decides which entry URLs the "open" button may hand to the system: web links only. */
object UrlGuard {
    private val SCHEME = Regex("^[A-Za-z][A-Za-z0-9+.-]*:")

    /**
     * Returns the URL to open (a bare "example.com" becomes https://example.com) or null if it is not a web link.
     * Anything with another scheme (javascript:, intent:, file:, content:, tel: …) is refused.
     */
    fun webUrl(raw: String): String? {
        val t = raw.trim()
        if (t.isEmpty() || t.any { it.isWhitespace() || it.isISOControl() }) return null
        val candidate = if (SCHEME.containsMatchIn(t)) t else "https://$t"
        val uri = try {
            URI(candidate)
        } catch (e: Exception) {
            return null
        }
        val scheme = uri.scheme?.lowercase() ?: return null
        if (scheme != "http" && scheme != "https") return null
        if (uri.host.isNullOrEmpty()) return null
        return candidate
    }
}
