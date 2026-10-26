package com.nightclip.app.logic

import java.net.URLDecoder
import java.util.Base64

/** A validated `clipbridge://pair?...` link (architecture §3.2). Mirrors core pairing.ParseLink. */
data class ParsedLink(
    val host: String,
    val port: Int,
    val psk: String,
    val fp: String?,
    val expiresAt: Long,
) {
    override fun toString() = "ParsedLink(host=$host, port=$port)" // psk stays out of logs
}

sealed interface LinkResult {
    data class Ok(val link: ParsedLink) : LinkResult
    data class Bad(val message: String) : LinkResult
}

object PairingLink {
    private const val SCHEME_PREFIX = "clipbridge://pair"
    private val B64URL = Regex("^[A-Za-z0-9_-]+$")
    const val PSK_CHARS = 22 // 16 bytes, unpadded base64url
    const val FP_CHARS = 43 // 32 bytes, unpadded base64url

    /**
     * Finds a pairing link inside scanned or pasted text (the user may paste a whole line);
     * returns null when there is none.
     */
    fun extract(text: String): String? {
        val start = text.indexOf(SCHEME_PREFIX, ignoreCase = true)
        if (start < 0) return null
        val end = text.indexOfFirst(start) { it.isWhitespace() }
        return text.substring(start, if (end < 0) text.length else end)
    }

    private inline fun String.indexOfFirst(from: Int, pred: (Char) -> Boolean): Int {
        for (i in from until length) if (pred(this[i])) return i
        return -1
    }

    fun parse(raw: String, nowSec: Long): LinkResult {
        val s = raw.trim()
        if (!s.startsWith(SCHEME_PREFIX, ignoreCase = true)) return LinkResult.Bad(Strings.PAIR_NOT_A_LINK)
        val rest = s.substring(SCHEME_PREFIX.length)
        val queryStart = rest.indexOf('?')
        val pathPart = if (queryStart < 0) rest else rest.substring(0, queryStart)
        if (pathPart.isNotEmpty() && pathPart != "/") return LinkResult.Bad(Strings.PAIR_NOT_A_LINK)
        val query = if (queryStart < 0) "" else rest.substring(queryStart + 1).substringBefore('#')

        val params = HashMap<String, MutableList<String>>()
        for (pair in query.split('&')) {
            if (pair.isEmpty()) continue
            val k = decode(pair.substringBefore('='))
            val v = decode(if (pair.contains('=')) pair.substringAfter('=') else "")
            params.getOrPut(k) { mutableListOf() }.add(v)
        }
        fun one(k: String): String? = params[k]?.takeIf { it.size == 1 }?.first()

        if (one("v") != "1") return LinkResult.Bad(Strings.PAIR_BAD_VERSION)
        val host = one("host")?.trim() ?: return LinkResult.Bad(Strings.PAIR_BAD_HOST)
        if (!isIpAddress(host)) return LinkResult.Bad(Strings.PAIR_BAD_HOST)
        val port = one("port")?.toIntOrNull()
        if (port == null || port !in 1..65535) return LinkResult.Bad(Strings.PAIR_BAD_PORT)
        val fp: String? = if (params.containsKey("fp")) {
            val v = one("fp")
            if (v == null || !validFp(v)) return LinkResult.Bad(Strings.PAIR_BAD_FP)
            v
        } else {
            null
        }
        val psk = one("psk")
        if (psk == null || !validPsk(psk)) return LinkResult.Bad(Strings.PAIR_BAD_PSK)
        val exp = one("exp")?.toLongOrNull() ?: return LinkResult.Bad(Strings.PAIR_BAD_EXP)
        if (nowSec >= exp) return LinkResult.Bad(Strings.PAIR_EXPIRED)
        return LinkResult.Ok(ParsedLink(host, port, psk, fp, exp))
    }

    private fun decode(s: String): String = try {
        URLDecoder.decode(s, "UTF-8")
    } catch (e: IllegalArgumentException) {
        s
    }

    fun validPsk(s: String): Boolean = s.length == PSK_CHARS && B64URL.matches(s) && decodesTo(s, 16)

    fun validFp(s: String): Boolean = s.length == FP_CHARS && B64URL.matches(s) && decodesTo(s, 32)

    private fun decodesTo(s: String, bytes: Int): Boolean = try {
        Base64.getUrlDecoder().decode(s).size == bytes
    } catch (e: IllegalArgumentException) {
        false
    }

    /** Dotted-quad IPv4 or IPv6 literal (no DNS names: the core refuses them too). */
    fun isIpAddress(s: String): Boolean = isIpv4(s) || isIpv6(s)

    private fun isIpv4(s: String): Boolean {
        val parts = s.split('.')
        return parts.size == 4 && parts.all { p ->
            p.isNotEmpty() && p.length <= 3 && p.all { it in '0'..'9' } && p.toInt() in 0..255
        }
    }

    private fun isHex(c: Char) = c in '0'..'9' || c in 'a'..'f' || c in 'A'..'F'

    /** Number of 16-bit groups in a colon-separated piece, or null if the piece is malformed. */
    private fun groups(part: String, allowV4Tail: Boolean): Int? {
        if (part.isEmpty()) return 0
        val items = part.split(':')
        var n = 0
        for ((i, p) in items.withIndex()) {
            if (p.contains('.')) {
                if (!allowV4Tail || i != items.lastIndex || !isIpv4(p)) return null
                n += 2
            } else {
                if (p.isEmpty() || p.length > 4 || !p.all(::isHex)) return null
                n += 1
            }
        }
        return n
    }

    private fun isIpv6(v: String): Boolean {
        if (!v.contains(':')) return false
        val dbl = v.indexOf("::")
        if (dbl >= 0 && v.indexOf("::", dbl + 1) >= 0) return false // a second "::" (or ":::")
        if (dbl < 0) return groups(v, allowV4Tail = true) == 8
        val head = groups(v.substring(0, dbl), allowV4Tail = false) ?: return false
        val tail = groups(v.substring(dbl + 2), allowV4Tail = true) ?: return false
        return head + tail < 8 // "::" stands for at least one group
    }
}

/** Validation for the manual host / port / psk (+ optional fingerprint) form. */
sealed interface ManualResult {
    data class Ok(val host: String, val port: Int, val psk: String, val fp: String?) : ManualResult {
        override fun toString() = "Ok(host=$host, port=$port)"
    }

    data class Bad(val hostError: String? = null, val portError: String? = null, val pskError: String? = null, val fpError: String? = null) : ManualResult
}

object ManualPairInput {
    fun validate(hostText: String, portText: String, pskText: String, fpText: String): ManualResult {
        val host = hostText.trim().removePrefix("[").removeSuffix("]")
        val port = portText.trim().toIntOrNull()
        // The core decodes unpadded base64url; people paste padded text from time to time.
        val psk = pskText.trim().trimEnd('=')
        val fp = fpText.trim().trimEnd('=')

        val hostErr = when {
            host.isEmpty() -> Strings.PAIR_MISSING_HOST
            !PairingLink.isIpAddress(host) -> Strings.PAIR_BAD_HOST
            else -> null
        }
        val portErr = when {
            portText.isBlank() -> Strings.PAIR_MISSING_PORT
            port == null || port !in 1..65535 -> Strings.PAIR_BAD_PORT
            else -> null
        }
        val pskErr = when {
            psk.isEmpty() -> Strings.PAIR_MISSING_PSK
            !PairingLink.validPsk(psk) -> Strings.PAIR_BAD_PSK
            else -> null
        }
        val fpErr = if (fp.isNotEmpty() && !PairingLink.validFp(fp)) Strings.PAIR_BAD_FP else null
        if (hostErr != null || portErr != null || pskErr != null || fpErr != null) {
            return ManualResult.Bad(hostErr, portErr, pskErr, fpErr)
        }
        return ManualResult.Ok(host, port!!, psk, fp.ifEmpty { null })
    }
}
