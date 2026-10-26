package com.nightclip.app.logic

import com.nightclip.app.core.VaultEntrySummary

/** The chips above the entry list. */
enum class VaultFilter { ALL, LOGIN, APIKEY, SECRET, NOTE, EXPIRING, TRASH }

/** Filtering, sorting and one-line summaries of the vault list. Pure. */
object VaultListLogic {
    /** "Expiring soon" covers entries that expire within this many seconds, and the ones already expired. */
    const val EXPIRING_WINDOW_S = 30L * 86400

    /** The `kind` parameter of vault.list for a filter (null = every kind). */
    fun coreKind(filter: VaultFilter): String? = when (filter) {
        VaultFilter.LOGIN -> VaultTexts.KIND_LOGIN
        VaultFilter.APIKEY -> VaultTexts.KIND_APIKEY
        VaultFilter.SECRET -> VaultTexts.KIND_SECRET
        VaultFilter.NOTE -> VaultTexts.KIND_NOTE
        else -> null
    }

    /** The `trash` parameter of vault.list: only the recycle bin is shown for that chip. */
    fun coreTrashOnly(filter: VaultFilter): Boolean = filter == VaultFilter.TRASH

    fun isExpiring(e: VaultEntrySummary, nowS: Long): Boolean =
        e.expiresAt > 0 && e.expiresAt <= nowS + EXPIRING_WINDOW_S && !e.inTrash

    /**
     * Applies the client-side part of a filter to what the core returned for the matching vault.list call
     * and puts the result in display order: by title (ignoring case), "expiring soon" by expiry date.
     */
    fun apply(entries: List<VaultEntrySummary>, filter: VaultFilter, nowS: Long): List<VaultEntrySummary> {
        val kept = when (filter) {
            VaultFilter.ALL -> entries.filter { !it.inTrash }
            VaultFilter.LOGIN, VaultFilter.APIKEY, VaultFilter.SECRET, VaultFilter.NOTE ->
                entries.filter { !it.inTrash && it.kind == coreKind(filter) }
            VaultFilter.EXPIRING -> entries.filter { isExpiring(it, nowS) }
            VaultFilter.TRASH -> entries.filter { it.inTrash }
        }
        return if (filter == VaultFilter.EXPIRING) {
            kept.sortedWith(compareBy<VaultEntrySummary> { it.expiresAt }.thenBy(String.CASE_INSENSITIVE_ORDER) { it.title })
        } else {
            kept.sortedWith(compareBy(String.CASE_INSENSITIVE_ORDER) { it.title })
        }
    }

    /** Second line of a list row: the user name, or the site when there is none. */
    fun subtitle(e: VaultEntrySummary): String = when {
        e.username.isNotBlank() -> e.username
        e.url.isNotBlank() -> hostOf(e.url)
        else -> ""
    }

    /** "https://www.example.com/a?b" -> "www.example.com"; text that is not a URL is returned trimmed. */
    fun hostOf(url: String): String {
        val t = url.trim()
        val noScheme = t.substringAfter("://", t)
        return noScheme.substringBefore('/').substringBefore('?').substringBefore('#').ifEmpty { t }
    }

    fun title(e: VaultEntrySummary): String = e.title.ifBlank { "（无标题）" }
}
