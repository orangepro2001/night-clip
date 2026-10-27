package com.nightclip.app.logic

import com.nightclip.app.core.ColumnAlign
import com.nightclip.app.core.ListItem
import com.nightclip.app.core.NoteBlock
import com.nightclip.app.core.NoteInline

/** Character styles of a [StyledText]. Several can cover the same range (bold inside a link, code inside bold …). */
enum class SpanStyle { STRONG, EM, DEL, CODE, LINK }

/**
 * A style over `text[start, end)`. A LINK span carries [target], the URL that may be opened (see [NoteLinks]); a link
 * the app does not open has no LINK span at all, its text is plain.
 */
data class StyledSpan(val start: Int, val end: Int, val style: SpanStyle, val target: String? = null)

/** Plain text plus the style ranges over it. Hard breaks are "\n" inside [text]. */
data class StyledText(val text: String, val spans: List<StyledSpan> = emptyList()) {
    val isEmpty: Boolean get() = text.isEmpty()

    companion object {
        val EMPTY = StyledText("")
    }
}

/**
 * The render tree flattened for drawing. Quote and list nesting do not become nested objects: every block carries
 * how deep it is, so the Compose layer is one loop over a list.
 *  - [quoteDepth] = how many quotes it sits in (a bar per level),
 *  - [indent] = how many list levels deep (an indent per level).
 */
sealed interface FlatBlock {
    val quoteDepth: Int
    val indent: Int

    data class Heading(val level: Int, val text: StyledText, override val quoteDepth: Int, override val indent: Int) : FlatBlock
    data class Paragraph(val text: StyledText, override val quoteDepth: Int, override val indent: Int) : FlatBlock

    /** A list item's first line. [marker] is "•", "1." or a checkbox glyph; [checked] is non-null for task items. */
    data class Item(
        val marker: String,
        val checked: Boolean?,
        val text: StyledText,
        val tight: Boolean,
        override val quoteDepth: Int,
        override val indent: Int,
    ) : FlatBlock

    data class Code(val lang: String, val text: String, override val quoteDepth: Int, override val indent: Int) : FlatBlock

    /** [align], [header] and every row have the same number of columns. */
    data class Table(
        val align: List<ColumnAlign>,
        val header: List<StyledText>,
        val rows: List<List<StyledText>>,
        override val quoteDepth: Int,
        override val indent: Int,
    ) : FlatBlock

    data class Rule(override val quoteDepth: Int, override val indent: Int) : FlatBlock

    /** HTML and syntax the core does not support: shown literally in monospace, never interpreted. */
    data class Literal(val text: String, override val quoteDepth: Int, override val indent: Int) : FlatBlock
}

/** Turns the render tree of `notes.render` into [FlatBlock]s. Pure Kotlin; the only place that maps tree to text. */
object NotesRenderSpans {
    /** Marker per list nesting level (CommonMark has no marker for it; this is the usual visual convention). */
    private val BULLETS = listOf("•", "◦", "▪")

    const val CHECKED_MARKER = "☑"
    const val UNCHECKED_MARKER = "☐"

    /** Text shown for an image; the app loads no images from the network. */
    fun imageText(alt: String, title: String): String {
        val name = alt.ifBlank { title }.trim()
        return if (name.isEmpty()) "[图片]" else "[图片：$name]"
    }

    fun flatten(blocks: List<NoteBlock>): List<FlatBlock> {
        val out = mutableListOf<FlatBlock>()
        for (b in blocks) block(b, quoteDepth = 0, indent = 0, listLevel = 0, out = out)
        return out
    }

    private fun block(b: NoteBlock, quoteDepth: Int, indent: Int, listLevel: Int, out: MutableList<FlatBlock>) {
        when (b) {
            is NoteBlock.Heading -> out += FlatBlock.Heading(b.level, styled(b.inline), quoteDepth, indent)
            is NoteBlock.Paragraph -> out += FlatBlock.Paragraph(styled(b.inline), quoteDepth, indent)
            is NoteBlock.CodeBlock -> out += FlatBlock.Code(b.lang, b.text, quoteDepth, indent)
            is NoteBlock.Quote -> for (c in b.children) block(c, quoteDepth + 1, indent, listLevel, out)
            is NoteBlock.ListBlock -> list(b, quoteDepth, indent, listLevel, out)
            is NoteBlock.Table -> out += table(b, quoteDepth, indent)
            is NoteBlock.Rule -> out += FlatBlock.Rule(quoteDepth, indent)
            is NoteBlock.Html -> out += FlatBlock.Literal(b.text, quoteDepth, indent)
            is NoteBlock.Unknown -> out += FlatBlock.Literal(b.text, quoteDepth, indent)
        }
    }

    private fun list(b: NoteBlock.ListBlock, quoteDepth: Int, indent: Int, listLevel: Int, out: MutableList<FlatBlock>) {
        var number = b.start
        for (item in b.items) {
            val marker = when {
                item.checked != null -> if (item.checked) CHECKED_MARKER else UNCHECKED_MARKER
                b.ordered -> "$number."
                else -> BULLETS[listLevel % BULLETS.size]
            }
            if (b.ordered) number++
            listItem(item, marker, b.tight, quoteDepth, indent, listLevel, out)
        }
    }

    /**
     * The first paragraph (or heading) of the item becomes the marker's own line; whatever follows (more paragraphs,
     * nested lists, code) is flattened one level deeper. An item that starts with something else gets an empty line.
     */
    private fun listItem(item: ListItem, marker: String, tight: Boolean, quoteDepth: Int, indent: Int, listLevel: Int, out: MutableList<FlatBlock>) {
        val first = item.blocks.firstOrNull()
        val text = when (first) {
            is NoteBlock.Paragraph -> styled(first.inline)
            is NoteBlock.Heading -> styled(first.inline)
            else -> StyledText.EMPTY
        }
        out += FlatBlock.Item(marker, item.checked, text, tight, quoteDepth, indent)
        val rest = if (first is NoteBlock.Paragraph || first is NoteBlock.Heading) item.blocks.drop(1) else item.blocks
        for (c in rest) block(c, quoteDepth, indent + 1, listLevel + 1, out)
    }

    private fun table(b: NoteBlock.Table, quoteDepth: Int, indent: Int): FlatBlock.Table {
        val columns = maxOf(b.header.size, b.rows.maxOfOrNull { it.size } ?: 0, b.align.size)
        fun row(cells: List<List<NoteInline>>) = List(columns) { i -> cells.getOrNull(i)?.let(::styled) ?: StyledText.EMPTY }
        return FlatBlock.Table(
            align = List(columns) { b.align.getOrElse(it) { ColumnAlign.NONE } },
            header = row(b.header),
            rows = b.rows.map(::row),
            quoteDepth = quoteDepth,
            indent = indent,
        )
    }

    // ---- tables ------------------------------------------------------------------------------

    /** Narrowest and widest a table column is drawn, in display units (see [displayWidth]). */
    const val MIN_COLUMN_UNITS = 6
    const val MAX_COLUMN_UNITS = 36

    /** Width of a cell's longest line: a CJK or full-width character takes two units, anything else one. */
    fun displayWidth(text: String): Int = text.split('\n').maxOf { line ->
        var units = 0
        var i = 0
        while (i < line.length) {
            val cp = line.codePointAt(i)
            units += if (isWide(cp)) 2 else 1
            i += Character.charCount(cp)
        }
        units
    }

    private fun isWide(cp: Int): Boolean =
        cp in 0x1100..0x115F || cp in 0x2E80..0xA4CF || cp in 0xAC00..0xD7A3 || cp in 0xF900..0xFAFF ||
            cp in 0xFE30..0xFE6F || cp in 0xFF00..0xFF60 || cp in 0xFFE0..0xFFE6 || cp in 0x1F300..0x1FAFF || cp in 0x20000..0x3FFFD

    /** The width in display units of every column of [table]: its widest cell, kept between the minimum and the maximum. */
    fun columnUnits(table: FlatBlock.Table): List<Int> = table.align.indices.map { c ->
        val widest = (listOf(table.header) + table.rows).maxOf { row -> displayWidth(row[c].text) }
        widest.coerceIn(MIN_COLUMN_UNITS, MAX_COLUMN_UNITS)
    }

    // ---- inline ------------------------------------------------------------------------------

    /** Soft line breaks are spaces, as CommonMark renders them; hard breaks are line breaks. */
    private const val SOFT_BREAK = " "

    fun styled(inlines: List<NoteInline>): StyledText {
        val sb = StringBuilder()
        val spans = mutableListOf<StyledSpan>()
        append(inlines, sb, spans)
        // Outer ranges first; an outer span is added after its children, so the list is not in start order yet.
        return StyledText(sb.toString(), spans.sortedWith(compareBy<StyledSpan> { it.start }.thenByDescending { it.end }))
    }

    private fun append(inlines: List<NoteInline>, sb: StringBuilder, spans: MutableList<StyledSpan>) {
        for (n in inlines) {
            when (n) {
                is NoteInline.Text -> sb.append(n.text)
                is NoteInline.Em -> styledRun(SpanStyle.EM, null, n.children, sb, spans)
                is NoteInline.Strong -> styledRun(SpanStyle.STRONG, null, n.children, sb, spans)
                is NoteInline.Del -> styledRun(SpanStyle.DEL, null, n.children, sb, spans)
                is NoteInline.Code -> {
                    val start = sb.length
                    sb.append(n.text)
                    if (sb.length > start) spans += StyledSpan(start, sb.length, SpanStyle.CODE)
                }
                is NoteInline.Link -> {
                    val target = NoteLinks.openable(n.href)
                    if (target != null) styledRun(SpanStyle.LINK, target, n.children, sb, spans) else append(n.children.ifEmpty { listOf(NoteInline.Text(n.href)) }, sb, spans)
                }
                is NoteInline.Image -> {
                    val start = sb.length
                    sb.append(imageText(n.alt, n.title))
                    NoteLinks.openable(n.href)?.let { spans += StyledSpan(start, sb.length, SpanStyle.LINK, it) }
                }
                NoteInline.HardBreak -> sb.append('\n')
                NoteInline.SoftBreak -> sb.append(SOFT_BREAK)
                is NoteInline.Other -> if (n.children.isNotEmpty()) append(n.children, sb, spans) else sb.append(n.text)
            }
        }
    }

    private fun styledRun(style: SpanStyle, target: String?, children: List<NoteInline>, sb: StringBuilder, spans: MutableList<StyledSpan>) {
        val start = sb.length
        append(children, sb, spans)
        if (sb.length > start) spans += StyledSpan(start, sb.length, style, target)
    }
}
