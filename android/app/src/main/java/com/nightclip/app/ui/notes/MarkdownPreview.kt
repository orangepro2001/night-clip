package com.nightclip.app.ui.notes

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.LinkAnnotation
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLinkStyles
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.unit.sp
import com.nightclip.app.core.ColumnAlign
import com.nightclip.app.logic.FlatBlock
import com.nightclip.app.logic.NotesRenderSpans
import com.nightclip.app.logic.SpanStyle as NoteSpan
import com.nightclip.app.logic.StyledText

private const val QUOTE_BAR_STEP_DP = 11
private const val INDENT_STEP_DP = 18
private const val TABLE_UNIT_DP = 7
private const val TABLE_CELL_PADDING_DP = 20

/**
 * Draws the render tree of a note ([FlatBlock]s from `NotesRenderSpans`) with plain Compose text. Nothing here parses
 * Markdown or interprets HTML: the structure comes from the core, HTML and unknown syntax are shown as literal text,
 * and only the links the logic layer marked as openable (http, https, mailto) can be tapped.
 */
@Composable
fun MarkdownPreview(blocks: List<FlatBlock>, onLink: (String) -> Unit, modifier: Modifier = Modifier) {
    LazyColumn(modifier, contentPadding = PaddingValues(horizontal = 16.dp, vertical = 12.dp)) {
        itemsIndexed(blocks) { _, block -> PreviewBlock(block, onLink) }
    }
}

@Composable
private fun PreviewBlock(block: FlatBlock, onLink: (String) -> Unit) {
    val gap = when (block) {
        is FlatBlock.Heading -> 8.dp
        is FlatBlock.Item -> if (block.tight) 3.dp else 8.dp
        else -> 10.dp
    }
    Framed(block, gap) {
        when (block) {
            is FlatBlock.Heading -> Text(rememberAnnotated(block.text, onLink), style = headingStyle(block.level))
            is FlatBlock.Paragraph -> Text(rememberAnnotated(block.text, onLink), style = bodyStyle())
            is FlatBlock.Item -> ItemRow(block, onLink)
            is FlatBlock.Code -> CodeBlock(block.lang, block.text, wrap = false)
            is FlatBlock.Table -> TableBlock(block, onLink)
            is FlatBlock.Rule -> HorizontalDivider(Modifier.padding(vertical = 6.dp), color = MaterialTheme.colorScheme.outlineVariant)
            is FlatBlock.Literal -> CodeBlock("", block.text, wrap = true)
        }
    }
}

/** Quote bars on the left (one per level, running through the gap below so a quote reads as one piece) and the indent of nested list content. */
@Composable
private fun Framed(block: FlatBlock, gap: Dp, content: @Composable () -> Unit) {
    val bar = MaterialTheme.colorScheme.outline
    val depth = block.quoteDepth
    Box(
        Modifier
            .fillMaxWidth()
            .padding(start = (block.indent * INDENT_STEP_DP).dp)
            .drawBehind {
                repeat(depth) { level ->
                    drawRect(bar, Offset(level * QUOTE_BAR_STEP_DP.dp.toPx(), 0f), Size(3.dp.toPx(), size.height))
                }
            }
            .padding(start = (depth * QUOTE_BAR_STEP_DP).dp, bottom = gap),
    ) { content() }
}

@Composable
private fun ItemRow(item: FlatBlock.Item, onLink: (String) -> Unit) {
    val scheme = MaterialTheme.colorScheme
    Row {
        Text(
            item.marker,
            Modifier.widthIn(min = 24.dp),
            style = bodyStyle(),
            color = if (item.checked == true) scheme.primary else scheme.onSurfaceVariant,
        )
        Text(
            rememberAnnotated(item.text, onLink),
            Modifier.weight(1f),
            style = bodyStyle(),
            color = if (item.checked == true) scheme.onSurfaceVariant else scheme.onSurface,
        )
    }
}

/** Code, and HTML or unsupported syntax kept as text: monospace, never interpreted. Code scrolls sideways, literal text wraps. */
@Composable
private fun CodeBlock(lang: String, text: String, wrap: Boolean) {
    Surface(Modifier.fillMaxWidth(), shape = MaterialTheme.shapes.small, color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.7f)) {
        Column(Modifier.padding(horizontal = 12.dp, vertical = 8.dp)) {
            if (lang.isNotBlank()) {
                Text(lang, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            val code = TextStyle(fontFamily = FontFamily.Monospace, fontSize = 13.sp, lineHeight = 19.sp, color = MaterialTheme.colorScheme.onSurface)
            if (wrap) {
                Text(text, style = code)
            } else {
                Box(Modifier.horizontalScroll(rememberScrollState())) { Text(text, style = code, softWrap = false) }
            }
        }
    }
}

/** A table is as wide as its content and scrolls sideways when that is wider than the screen. */
@Composable
private fun TableBlock(table: FlatBlock.Table, onLink: (String) -> Unit) {
    val scheme = MaterialTheme.colorScheme
    val units = remember(table) { NotesRenderSpans.columnUnits(table) }
    val line = scheme.outlineVariant
    Box(
        Modifier
            .fillMaxWidth()
            .border(1.dp, line, MaterialTheme.shapes.extraSmall)
            .horizontalScroll(rememberScrollState()),
    ) {
        Column {
            TableRow(table, table.header, units, header = true, onLink = onLink)
            table.rows.forEach { TableRow(table, it, units, header = false, onLink = onLink) }
        }
    }
}

@Composable
private fun TableRow(table: FlatBlock.Table, cells: List<StyledText>, units: List<Int>, header: Boolean, onLink: (String) -> Unit) {
    val scheme = MaterialTheme.colorScheme
    Row(Modifier.height(IntrinsicSize.Min)) {
        cells.forEachIndexed { i, cell ->
            val align = when (table.align.getOrElse(i) { ColumnAlign.NONE }) {
                ColumnAlign.CENTER -> TextAlign.Center
                ColumnAlign.RIGHT -> TextAlign.End
                else -> TextAlign.Start
            }
            Box(
                Modifier
                    .width((units[i] * TABLE_UNIT_DP + TABLE_CELL_PADDING_DP).dp)
                    .fillMaxHeight()
                    .background(if (header) scheme.surfaceVariant else Color.Transparent)
                    .border(0.5.dp, scheme.outlineVariant)
                    .padding(horizontal = 8.dp, vertical = 6.dp),
            ) {
                Text(
                    rememberAnnotated(cell, onLink),
                    Modifier.fillMaxWidth(),
                    style = bodyStyle().copy(fontWeight = if (header) FontWeight.SemiBold else FontWeight.Normal, textAlign = align),
                )
            }
        }
    }
}

// ---- text styles -----------------------------------------------------------------------------

@Composable
private fun bodyStyle(): TextStyle = TextStyle(fontSize = 15.sp, lineHeight = 23.sp, color = MaterialTheme.colorScheme.onSurface)

@Composable
private fun headingStyle(level: Int): TextStyle {
    val color = if (level >= 6) MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.onSurface
    return when (level) {
        1 -> TextStyle(fontSize = 24.sp, lineHeight = 30.sp, fontWeight = FontWeight.Bold, color = color)
        2 -> TextStyle(fontSize = 20.sp, lineHeight = 26.sp, fontWeight = FontWeight.Bold, color = color)
        3 -> TextStyle(fontSize = 18.sp, lineHeight = 24.sp, fontWeight = FontWeight.SemiBold, color = color)
        4 -> TextStyle(fontSize = 16.sp, lineHeight = 22.sp, fontWeight = FontWeight.SemiBold, color = color)
        else -> TextStyle(fontSize = 15.sp, lineHeight = 21.sp, fontWeight = FontWeight.SemiBold, color = color)
    }
}

/** The styled spans of [text] as an [AnnotatedString]; a LINK span becomes a tap target that calls [onLink] with its URL. */
@Composable
private fun rememberAnnotated(text: StyledText, onLink: (String) -> Unit): AnnotatedString {
    val scheme = MaterialTheme.colorScheme
    val codeBackground = scheme.surfaceVariant
    val linkColor = scheme.primary
    val latest = rememberUpdatedState(onLink)
    return remember(text, codeBackground, linkColor) {
        buildAnnotatedString {
            append(text.text)
            for (span in text.spans) {
                when (span.style) {
                    NoteSpan.STRONG -> addStyle(SpanStyle(fontWeight = FontWeight.Bold), span.start, span.end)
                    NoteSpan.EM -> addStyle(SpanStyle(fontStyle = FontStyle.Italic), span.start, span.end)
                    NoteSpan.DEL -> addStyle(SpanStyle(textDecoration = TextDecoration.LineThrough), span.start, span.end)
                    NoteSpan.CODE -> addStyle(SpanStyle(fontFamily = FontFamily.Monospace, fontSize = 0.92.em, background = codeBackground), span.start, span.end)
                    NoteSpan.LINK -> span.target?.let { url ->
                        addLink(
                            LinkAnnotation.Clickable(
                                tag = url,
                                styles = TextLinkStyles(SpanStyle(color = linkColor, textDecoration = TextDecoration.Underline)),
                                linkInteractionListener = { latest.value(url) },
                            ),
                            span.start,
                            span.end,
                        )
                    }
                }
            }
        }
    }
}
