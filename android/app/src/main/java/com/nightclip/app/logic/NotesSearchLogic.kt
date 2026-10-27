package com.nightclip.app.logic

import com.nightclip.app.core.Highlight
import com.nightclip.app.core.SearchHit
import com.nightclip.app.core.SearchPage

/** A range of a string in UTF-16 units: what `AnnotatedString` and `String` use. */
data class Span16(val start: Int, val end: Int)

/** What the search box shows: the hits of [query] loaded so far. */
data class SearchState(
    val query: String = "",
    val items: List<SearchHit> = emptyList(),
    val nextCursor: String? = null,
    /** The first page of [query] has arrived (an empty list before that is not "no results"). */
    val loaded: Boolean = false,
    val error: String? = null,
) {
    val canLoadMore: Boolean get() = nextCursor != null
}

/** Query rules, paging and highlight conversion of the note search (notes/api-spec.md `notes.search`). Pure. */
object NotesSearchLogic {
    const val DEBOUNCE_MS = 250L
    const val PAGE_SIZE = 30

    /** The core's largest page. */
    const val MAX_LIMIT = 100

    /** Only text that is not blank is searched; an empty box is the normal list. */
    fun normalize(raw: String): String = raw.trim()

    fun isActive(raw: String): Boolean = normalize(raw).isNotEmpty()

    /** The first page of [query] replaces the hits; an answer for another query than the one asked last is not passed in. */
    fun firstPage(query: String, page: SearchPage): SearchState = SearchState(query, dedupe(page.items), page.nextCursor, loaded = true)

    /** The page after [cursor] is appended, if the list still is the one [cursor] came from (otherwise it is stale and ignored). */
    fun nextPage(state: SearchState, query: String, cursor: String, page: SearchPage): SearchState {
        if (state.query != query || state.nextCursor != cursor) return state
        val seen = state.items.mapTo(HashSet()) { it.noteId }
        return state.copy(items = state.items + page.items.filter { seen.add(it.noteId) }, nextCursor = page.nextCursor)
    }

    /** How many hits to ask for when the same search is repeated after a change: as many as are shown, within the core's limits. */
    fun reloadLimit(state: SearchState): Int = state.items.size.coerceIn(PAGE_SIZE, MAX_LIMIT)

    private fun dedupe(items: List<SearchHit>): List<SearchHit> {
        val seen = HashSet<String>()
        return items.filter { seen.add(it.noteId) }
    }

    fun title(hit: SearchHit): String = hit.title.trim().ifEmpty { NotesTexts.UNTITLED }

    // ---- highlights ----------------------------------------------------------------------------

    /**
     * Converts highlight ranges counted in code points (what the core sends) to UTF-16 ranges of [text]. A character
     * outside the BMP (an emoji, a rare CJK character) is one code point but two UTF-16 units, so the same numbers
     * would otherwise land in the wrong place. Out-of-range or empty ranges are dropped or clipped, overlapping ones
     * are merged, and the result is sorted. A lone surrogate counts as one code point.
     */
    fun toSpans(text: String, highlights: List<Highlight>): List<Span16> {
        if (highlights.isEmpty() || text.isEmpty()) return emptyList()
        // unitAt[i] = the UTF-16 index where code point i starts; the last entry is text.length.
        val unitAt = IntArray(text.codePointCount(0, text.length) + 1)
        var unit = 0
        var cp = 0
        while (unit < text.length) {
            unitAt[cp++] = unit
            unit += Character.charCount(text.codePointAt(unit))
        }
        unitAt[cp] = text.length
        val last = cp
        val spans = highlights.mapNotNull { h ->
            val start = h.start.coerceIn(0, last)
            val end = h.end.coerceIn(0, last)
            if (end > start) Span16(unitAt[start], unitAt[end]) else null
        }.sortedBy { it.start }
        val merged = mutableListOf<Span16>()
        for (s in spans) {
            val prev = merged.lastOrNull()
            if (prev != null && s.start <= prev.end) merged[merged.lastIndex] = Span16(prev.start, maxOf(prev.end, s.end)) else merged += s
        }
        return merged
    }
}
