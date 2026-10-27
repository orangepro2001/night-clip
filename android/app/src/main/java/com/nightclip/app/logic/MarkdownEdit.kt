package com.nightclip.app.logic

/** Text and selection after a toolbar action. [selStart] <= [selEnd]; equal = a caret. Offsets are UTF-16 indexes like TextRange. */
data class EditResult(val text: String, val selStart: Int, val selEnd: Int)

/** The buttons of the formatting toolbar. */
sealed interface MdAction {
    data object Bold : MdAction
    data object Italic : MdAction
    data object Strike : MdAction
    data object InlineCode : MdAction
    data class Heading(val level: Int) : MdAction
    data object Bullet : MdAction
    data object Numbered : MdAction
    data object Task : MdAction
    data object Quote : MdAction
    data object CodeBlock : MdAction
    data object Link : MdAction
    data object Rule : MdAction
    data object Indent : MdAction
    data object Outdent : MdAction
}

/**
 * Toolbar actions as pure functions on (text, selection). They write the canonical forms the core would produce
 * anyway (`**bold**`, `*italic*`, `~~strike~~`, `- ` bullets, `1. ` numbers, `- [ ] ` tasks, ``` fences), so the
 * core's normalisation after a save leaves the text as it is. Nothing here parses Markdown: it only looks at the
 * characters around the selection. Every action toggles: applying it twice returns the original text.
 *
 * Offsets are UTF-16 indexes (what Compose's TextRange uses); only ASCII markers are inserted, always at positions the
 * caller supplied or at line boundaries, so CJK text and surrogate pairs are never split.
 */
object MarkdownEdit {
    fun apply(action: MdAction, text: String, selStart: Int, selEnd: Int): EditResult {
        val a = minOf(selStart, selEnd).coerceIn(0, text.length)
        val b = maxOf(selStart, selEnd).coerceIn(0, text.length)
        return when (action) {
            MdAction.Bold -> wrap(text, a, b, BOLD)
            MdAction.Italic -> wrap(text, a, b, ITALIC)
            MdAction.Strike -> wrap(text, a, b, STRIKE)
            MdAction.InlineCode -> wrap(text, a, b, CODE)
            is MdAction.Heading -> heading(text, a, b, action.level.coerceIn(1, 6))
            MdAction.Bullet -> bullet(text, a, b)
            MdAction.Numbered -> numbered(text, a, b)
            MdAction.Task -> task(text, a, b)
            MdAction.Quote -> quote(text, a, b)
            MdAction.CodeBlock -> codeBlock(text, a, b)
            MdAction.Link -> link(text, a, b)
            MdAction.Rule -> rule(text, a, b)
            MdAction.Indent -> indent(text, a, b, outdent = false)
            MdAction.Outdent -> indent(text, a, b, outdent = true)
        }
    }

    // ---- inline marks --------------------------------------------------------------------------

    /**
     * [isOn] tells from the number of marker characters that touch the text on BOTH sides whether the mark is already
     * there: for `*` an odd count is italic, two is bold, three is both.
     */
    private class InlineSpec(val marker: String, val ch: Char, val strip: Int, val isOn: (Int) -> Boolean)

    private val BOLD = InlineSpec("**", '*', 2) { it >= 2 }
    private val ITALIC = InlineSpec("*", '*', 1) { it % 2 == 1 }
    private val STRIKE = InlineSpec("~~", '~', 2) { it >= 2 }
    private val CODE = InlineSpec("`", '`', 1) { it >= 1 }

    private class Seg(val s: Int, val e: Int, val on: Boolean)

    private fun wrap(text: String, a: Int, b: Int, spec: InlineSpec): EditResult {
        val segs = inlineSegments(text, a, b).map { (s0, e0) ->
            var s = s0
            var e = e0
            // Marker characters inside the selection count as part of the mark: selecting "**a**" is selecting the bold.
            while (s < e && text[s] == spec.ch) s++
            while (e > s && text[e - 1] == spec.ch) e--
            var left = 0
            while (s - 1 - left >= 0 && text[s - 1 - left] == spec.ch) left++
            var right = 0
            while (e + right < text.length && text[e + right] == spec.ch) right++
            Seg(s, e, spec.isOn(minOf(left, right)))
        }
        if (segs.isEmpty()) return EditResult(text, a, b)
        val remove = segs.all { it.on }
        val sb = StringBuilder(text)
        for (seg in segs.asReversed()) {
            if (remove) {
                sb.delete(seg.e, seg.e + spec.strip)
                sb.delete(seg.s - spec.strip, seg.s)
            } else if (!seg.on) {
                sb.insert(seg.e, spec.marker)
                sb.insert(seg.s, spec.marker)
            }
        }
        var shift = 0
        var first = -1
        var last = -1
        for (seg in segs) {
            if (!remove && seg.on) continue
            val d = if (remove) -spec.strip else spec.marker.length
            if (first < 0) first = seg.s + shift + d
            last = seg.e + shift + d
            shift += 2 * d
        }
        return EditResult(sb.toString(), first, last)
    }

    /**
     * The pieces of the selection a mark is put around: one per non-blank line, without the line's own `- `, `> `,
     * `# ` prefix and without leading/trailing white space (a mark must hug the text). A caret is one empty piece.
     */
    private fun inlineSegments(text: String, a: Int, b: Int): List<Pair<Int, Int>> {
        if (a == b) return listOf(a to a)
        val out = mutableListOf<Pair<Int, Int>>()
        var ls = lineStart(text, a)
        while (true) {
            val nl = text.indexOf('\n', ls)
            val le = if (nl < 0) text.length else nl
            var s = maxOf(ls, a)
            var e = minOf(le, b)
            if (s < e) {
                if (s == ls) s = minOf(e, ls + parse(text.substring(ls, le)).prefixLen)
                while (s < e && text[s].isWhitespace()) s++
                while (e > s && text[e - 1].isWhitespace()) e--
                if (s < e) out += s to e
            }
            if (nl < 0 || nl + 1 >= b) break
            ls = nl + 1
        }
        return out
    }

    // ---- line prefixes -------------------------------------------------------------------------

    private enum class Kind { NONE, HEADING, TASK, BULLET, ORDERED }

    private class Parsed(val indent: String, val quote: String, val marker: String, val kind: Kind, val level: Int) {
        val prefixLen: Int get() = indent.length + quote.length + marker.length
    }

    private val PREFIX = Regex("^([ \\t]*)((?:>[ \\t]?)*)(#{1,6}[ \\t]+|[-*+][ \\t]+\\[[ xX]\\][ \\t]+|[-*+][ \\t]+|\\d{1,9}[.)][ \\t]+)?")

    private fun parse(line: String): Parsed {
        val m = PREFIX.find(line) ?: return Parsed("", "", "", Kind.NONE, 0)
        val marker = m.groupValues[3]
        val kind = when {
            marker.isEmpty() -> Kind.NONE
            marker[0] == '#' -> Kind.HEADING
            marker[0].isDigit() -> Kind.ORDERED
            marker.contains('[') -> Kind.TASK
            else -> Kind.BULLET
        }
        return Parsed(m.groupValues[1], m.groupValues[2], marker, kind, marker.takeWhile { it == '#' }.length)
    }

    /** One line after an action; [oldPrefix] / [newPrefix] are the lengths of the part at the start that was replaced. */
    private class LineChange(val line: String, val oldPrefix: Int, val newPrefix: Int)

    private fun unchanged(line: String) = LineChange(line, 0, 0)

    private fun replaceMarker(line: String, p: Parsed, marker: String, keepIndent: Boolean): LineChange {
        val prefix = (if (keepIndent) p.indent else "") + p.quote + marker
        return LineChange(prefix + line.substring(p.prefixLen), p.prefixLen, prefix.length)
    }

    // ---- line based actions --------------------------------------------------------------------

    private fun lineStart(text: String, pos: Int): Int = if (pos <= 0) 0 else text.lastIndexOf('\n', pos - 1) + 1

    private fun lineEnd(text: String, pos: Int): Int = text.indexOf('\n', pos).let { if (it < 0) text.length else it }

    /**
     * Runs [transform] on the lines the selection touches (a selection that ends right after a line break does not
     * touch the next line) and maps the selection onto the new text.
     */
    private fun onLines(text: String, a: Int, b: Int, transform: (List<String>) -> List<LineChange>): EditResult {
        val first = lineStart(text, a)
        val endPos = if (b > a && text[b - 1] == '\n') b - 1 else b
        val last = lineEnd(text, endPos)
        val old = text.substring(first, last).split('\n')
        val changes = transform(old)
        check(changes.size == old.size)
        val newBlock = changes.joinToString("\n") { it.line }
        if (newBlock == text.substring(first, last)) return EditResult(text, a, b)
        val oldStarts = IntArray(old.size)
        val newStarts = IntArray(old.size)
        var o = first
        var n = first
        for (i in old.indices) {
            oldStarts[i] = o
            newStarts[i] = n
            o += old[i].length + 1
            n += changes[i].line.length + 1
        }
        val delta = newBlock.length - (last - first)
        fun map(pos: Int): Int {
            if (pos > last) return pos + delta
            var i = old.indices.last
            while (i > 0 && oldStarts[i] > pos) i--
            val col = pos - oldStarts[i]
            val c = changes[i]
            val newCol = if (col <= c.oldPrefix) c.newPrefix else col - c.oldPrefix + c.newPrefix
            return newStarts[i] + newCol.coerceAtMost(c.line.length)
        }
        val newText = text.substring(0, first) + newBlock + text.substring(last)
        return EditResult(newText, map(a), map(b))
    }

    /** The lines an action applies to: the non-blank ones, or the only line when it is the one with the caret. */
    private fun targets(lines: List<String>): Set<Int> {
        val nonBlank = lines.indices.filter { lines[it].isNotBlank() }
        return if (nonBlank.isEmpty() && lines.size == 1) setOf(0) else nonBlank.toSet()
    }

    private fun heading(text: String, a: Int, b: Int, level: Int) = onLines(text, a, b) { lines ->
        val t = targets(lines)
        val parsed = lines.map(::parse)
        val remove = t.isNotEmpty() && t.all { parsed[it].kind == Kind.HEADING && parsed[it].level == level }
        lines.indices.map { i ->
            when {
                i !in t -> unchanged(lines[i])
                remove -> replaceMarker(lines[i], parsed[i], "", keepIndent = false)
                else -> replaceMarker(lines[i], parsed[i], "#".repeat(level) + " ", keepIndent = false)
            }
        }
    }

    private fun bullet(text: String, a: Int, b: Int) = onLines(text, a, b) { lines ->
        val t = targets(lines)
        val parsed = lines.map(::parse)
        val remove = t.isNotEmpty() && t.all { parsed[it].kind == Kind.BULLET }
        lines.indices.map { i ->
            when {
                i !in t -> unchanged(lines[i])
                remove -> replaceMarker(lines[i], parsed[i], "", keepIndent = true)
                parsed[i].kind == Kind.BULLET -> unchanged(lines[i])
                else -> replaceMarker(lines[i], parsed[i], "- ", keepIndent = true)
            }
        }
    }

    private fun numbered(text: String, a: Int, b: Int) = onLines(text, a, b) { lines ->
        val t = targets(lines)
        val parsed = lines.map(::parse)
        val remove = t.isNotEmpty() && t.all { parsed[it].kind == Kind.ORDERED }
        var number = 0
        lines.indices.map { i ->
            when {
                i !in t -> unchanged(lines[i])
                remove -> replaceMarker(lines[i], parsed[i], "", keepIndent = true)
                else -> {
                    number++
                    replaceMarker(lines[i], parsed[i], "$number. ", keepIndent = true)
                }
            }
        }
    }

    private fun task(text: String, a: Int, b: Int) = onLines(text, a, b) { lines ->
        val t = targets(lines)
        val parsed = lines.map(::parse)
        val remove = t.isNotEmpty() && t.all { parsed[it].kind == Kind.TASK }
        lines.indices.map { i ->
            when {
                i !in t -> unchanged(lines[i])
                remove -> replaceMarker(lines[i], parsed[i], "", keepIndent = true)
                parsed[i].kind == Kind.TASK -> unchanged(lines[i])
                else -> replaceMarker(lines[i], parsed[i], "- [ ] ", keepIndent = true)
            }
        }
    }

    private val QUOTE_LEVEL = Regex("^>[ \\t]?")

    private fun quote(text: String, a: Int, b: Int) = onLines(text, a, b) { lines ->
        val t = targets(lines)
        val parsed = lines.map(::parse)
        val remove = t.isNotEmpty() && t.all { parsed[it].quote.isNotEmpty() }
        val firstText = lines.indices.firstOrNull { lines[it].isNotBlank() } ?: -1
        val lastText = lines.indices.lastOrNull { lines[it].isNotBlank() } ?: -1
        lines.indices.map { i ->
            val p = parsed[i]
            when {
                remove && i in t -> {
                    val q = p.quote.replaceFirst(QUOTE_LEVEL, "")
                    val prefix = p.indent + q + p.marker
                    LineChange(prefix + lines[i].substring(p.prefixLen), p.prefixLen, prefix.length)
                }
                remove -> unchanged(lines[i])
                i in t -> LineChange("> " + lines[i], 0, 2)
                // A blank line between two quoted lines keeps them one quote.
                lines[i].isBlank() && i in (firstText + 1) until lastText -> LineChange(">", 0, 1)
                else -> unchanged(lines[i])
            }
        }
    }

    private fun indent(text: String, a: Int, b: Int, outdent: Boolean) = onLines(text, a, b) { lines ->
        val t = targets(lines)
        lines.indices.map { i ->
            val line = lines[i]
            if (i !in t) return@map unchanged(line)
            val width = if (parse(line).kind == Kind.ORDERED) 3 else 2
            if (!outdent) {
                LineChange(" ".repeat(width) + line, 0, width)
            } else {
                val spaces = line.takeWhile { it == ' ' }.length
                val cut = when {
                    line.startsWith("\t") -> 1
                    else -> minOf(spaces, width)
                }
                LineChange(line.substring(cut), cut, 0)
            }
        }
    }

    // ---- code block, link, rule ----------------------------------------------------------------

    private fun isFence(line: String): Boolean {
        val t = line.trimStart()
        return t.startsWith("```") || t.startsWith("~~~")
    }

    private fun codeBlock(text: String, a: Int, b: Int): EditResult {
        if (a == b) {
            val ls = lineStart(text, a)
            val le = lineEnd(text, a)
            return if (text.substring(ls, le).isBlank()) {
                EditResult(text.substring(0, ls) + "```\n\n```" + text.substring(le), ls + 4, ls + 4)
            } else {
                // After the current line; a fenced block may interrupt a paragraph.
                val caret = le + "\n```\n".length
                EditResult(text.substring(0, le) + "\n```\n\n```" + text.substring(le), caret, caret)
            }
        }
        val ls = lineStart(text, a)
        val le = lineEnd(text, if (text[b - 1] == '\n') b - 1 else b)
        val block = text.substring(ls, le)
        val lines = block.split('\n')
        // The selection is a whole fenced block: take the fences away.
        if (lines.size >= 2 && isFence(lines.first()) && isFence(lines.last())) {
            val inner = lines.subList(1, lines.size - 1).joinToString("\n")
            return EditResult(text.substring(0, ls) + inner + text.substring(le), ls, ls + inner.length)
        }
        // The selection sits right inside a fenced block.
        if (ls > 0 && le < text.length) {
            val prevStart = lineStart(text, ls - 1)
            val nextEnd = lineEnd(text, le + 1)
            if (isFence(text.substring(prevStart, ls - 1)) && isFence(text.substring(le + 1, nextEnd))) {
                return EditResult(text.substring(0, prevStart) + block + text.substring(nextEnd), prevStart, prevStart + block.length)
            }
        }
        val longest = Regex("`+").findAll(block).maxOfOrNull { it.value.length } ?: 0
        val fence = "`".repeat(maxOf(3, longest + 1))
        val open = fence.length + 1
        return EditResult(text.substring(0, ls) + fence + "\n" + block + "\n" + fence + text.substring(le), ls + open, ls + open + block.length)
    }

    private val URL_LIKE = Regex("^https?://\\S+$", RegexOption.IGNORE_CASE)

    private fun link(text: String, a: Int, b: Int): EditResult {
        val sel = text.substring(a, b)
        val (insert, caretStart, caretEnd) = when {
            sel.isEmpty() -> Triple("[](https://)", 1, 1)
            URL_LIKE.matches(sel.trim()) -> Triple("[](${sel.trim()})", 1, 1)
            else -> Triple("[$sel](https://)", sel.length + 3, sel.length + 3 + "https://".length)
        }
        return EditResult(text.substring(0, a) + insert + text.substring(b), a + caretStart, a + caretEnd)
    }

    private fun rule(text: String, a: Int, b: Int): EditResult {
        val ls = lineStart(text, b)
        val le = lineEnd(text, b)
        // On a blank line the rule goes in front of it; otherwise after the line.
        val at = if (text.substring(ls, le).isBlank()) ls else le
        val prefix = text.substring(0, at)
        val suffix = text.substring(at)
        // A rule right under a text line would make that line a heading: keep a blank line above it.
        val lead = if (prefix.isEmpty()) 0 else maxOf(0, 2 - prefix.takeLastWhile { it == '\n' }.length.coerceAtMost(2))
        val following = suffix.takeWhile { it == '\n' }.length
        val trail = if (suffix.isEmpty()) 1 else maxOf(0, 2 - following)
        val ruleAt = at + lead
        val newText = prefix + "\n".repeat(lead) + "---" + "\n".repeat(trail) + suffix
        return EditResult(newText, ruleAt + 4, ruleAt + 4)
    }
}
