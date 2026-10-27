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

    /** An attachment shown as a picture (`![alt](blob:<id>)`). [alt] is the placeholder text while it is not there. */
    data class Image(val blobId: String, val alt: String, override val quoteDepth: Int, override val indent: Int) : FlatBlock

    /** An attachment shown as a chip (`[name](blob:<id>)`). */
    data class File(val blobId: String, val name: String, override val quoteDepth: Int, override val indent: Int) : FlatBlock
}

/** An attachment found in inline content: `blob:<id>` is an address in the notes' file store, never a web link. */
data class AttachmentRef(val blobId: String, val name: String, val image: Boolean)

/** Inline content split into the text that is drawn as text and the attachments that are drawn as blocks of their own. */
data class InlineParts(val text: StyledText, val attachments: List<AttachmentRef>)

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
            is NoteBlock.Heading -> {
                val parts = inlineParts(b.inline)
                if (parts.text.text.isNotBlank() || parts.attachments.isEmpty()) out += FlatBlock.Heading(b.level, parts.text, quoteDepth, indent)
                attachmentBlocks(parts, quoteDepth, indent, out)
            }
            is NoteBlock.Paragraph -> {
                val parts = inlineParts(b.inline)
                if (parts.text.text.isNotBlank() || parts.attachments.isEmpty()) out += FlatBlock.Paragraph(parts.text, quoteDepth, indent)
                attachmentBlocks(parts, quoteDepth, indent, out)
            }
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
        val parts = when (first) {
            is NoteBlock.Paragraph -> inlineParts(first.inline)
            is NoteBlock.Heading -> inlineParts(first.inline)
            else -> InlineParts(StyledText.EMPTY, emptyList())
        }
        out += FlatBlock.Item(marker, item.checked, parts.text, tight, quoteDepth, indent)
        attachmentBlocks(parts, quoteDepth, indent + 1, out)
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

    /** Soft line breaks are line breaks, as in most note apps (Obsidian's default); the Windows client does the same. */
    private const val SOFT_BREAK = "\n"

    /** Text for a line of inline content. Attachments stay in the text as plain words (table cells, tests). */
    fun styled(inlines: List<NoteInline>): StyledText = Collector(extract = false).collect(inlines).text

    /** Like [styled], but attachments are taken out of the text, in order, to be drawn as pictures and chips. */
    fun inlineParts(inlines: List<NoteInline>): InlineParts = Collector(extract = true).collect(inlines)

    private fun attachmentBlocks(parts: InlineParts, quoteDepth: Int, indent: Int, out: MutableList<FlatBlock>) {
        for (a in parts.attachments) {
            out += if (a.image) FlatBlock.Image(a.blobId, a.name, quoteDepth, indent) else FlatBlock.File(a.blobId, a.name, quoteDepth, indent)
        }
    }

    private fun trimmed(t: StyledText): StyledText {
        var s = 0
        var e = t.text.length
        while (s < e && t.text[s].isWhitespace()) s++
        while (e > s && t.text[e - 1].isWhitespace()) e--
        if (s == 0 && e == t.text.length) return t
        val spans = t.spans.mapNotNull { sp ->
            val a = maxOf(sp.start, s) - s
            val b = minOf(sp.end, e) - s
            if (b > a) sp.copy(start = a, end = b) else null
        }
        return StyledText(t.text.substring(s, e), spans)
    }

    private class Collector(private val extract: Boolean) {
        private val sb = StringBuilder()
        private val spans = mutableListOf<StyledSpan>()
        private val refs = mutableListOf<AttachmentRef>()

        fun collect(inlines: List<NoteInline>): InlineParts {
            append(inlines)
            // Outer ranges first; an outer span is added after its children, so the list is not in start order yet.
            val text = StyledText(sb.toString(), spans.sortedWith(compareBy<StyledSpan> { it.start }.thenByDescending { it.end }))
            // What is left around a picture (a line break, a space) is not text.
            return InlineParts(if (refs.isEmpty()) text else trimmed(text), refs.toList())
        }

        private fun append(inlines: List<NoteInline>) {
            for (n in inlines) {
                when (n) {
                    is NoteInline.Text -> sb.append(n.text)
                    is NoteInline.Em -> styledRun(SpanStyle.EM, null, n.children)
                    is NoteInline.Strong -> styledRun(SpanStyle.STRONG, null, n.children)
                    is NoteInline.Del -> styledRun(SpanStyle.DEL, null, n.children)
                    is NoteInline.Code -> {
                        val start = sb.length
                        sb.append(n.text)
                        if (sb.length > start) spans += StyledSpan(start, sb.length, SpanStyle.CODE)
                    }
                    is NoteInline.Link -> link(n)
                    is NoteInline.Image -> image(n)
                    NoteInline.HardBreak -> sb.append('\n')
                    NoteInline.SoftBreak -> sb.append(SOFT_BREAK)
                    is NoteInline.Other -> if (n.children.isNotEmpty()) append(n.children) else sb.append(n.text)
                }
            }
        }

        private fun link(n: NoteInline.Link) {
            val blob = BlobAddress.parse(n.href)
            if (blob != null && extract) {
                refs += AttachmentRef(blob, Collector(extract = false).collect(n.children).text.text.trim().ifEmpty { "file" }, image = false)
                return
            }
            // `blob:` is never a web address: without extraction it is just its text, and NoteLinks refuses it as a link.
            val target = NoteLinks.openable(n.href)
            if (target != null) styledRun(SpanStyle.LINK, target, n.children) else append(n.children.ifEmpty { listOf(NoteInline.Text(n.href)) })
        }

        private fun image(n: NoteInline.Image) {
            val blob = BlobAddress.parse(n.href)
            if (blob != null && extract) {
                refs += AttachmentRef(blob, n.alt.ifBlank { n.title }.trim(), image = true)
                return
            }
            val start = sb.length
            sb.append(imageText(n.alt, n.title))
            NoteLinks.openable(n.href)?.let { spans += StyledSpan(start, sb.length, SpanStyle.LINK, it) }
        }

        private fun styledRun(style: SpanStyle, target: String?, children: List<NoteInline>) {
            val start = sb.length
            append(children)
            if (sb.length > start) spans += StyledSpan(start, sb.length, style, target)
        }
    }
}
