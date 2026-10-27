package com.nightclip.app.logic

import com.nightclip.app.core.ColumnAlign
import com.nightclip.app.core.ListItem
import com.nightclip.app.core.NoteBlock
import com.nightclip.app.core.NoteInline
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class NotesRenderSpansTest {
    private fun t(s: String) = NoteInline.Text(s)
    private fun para(vararg i: NoteInline) = NoteBlock.Paragraph("b", "", i.toList())
    private fun item(checked: Boolean?, vararg blocks: NoteBlock) = ListItem(checked, blocks.toList())
    private fun styled(vararg i: NoteInline) = NotesRenderSpans.styled(i.toList())
    private fun list(ordered: Boolean, items: List<ListItem>, start: Int = 1, tight: Boolean = true) =
        NoteBlock.ListBlock("b", "", ordered, start, tight, items)

    // ---- inline ------------------------------------------------------------------------------

    @Test
    fun plainTextHasNoSpans() {
        val s = styled(t("hello "), t("world"))
        assertEquals("hello world", s.text)
        assertTrue(s.spans.isEmpty())
    }

    @Test
    fun emphasisStrongAndStrikeCoverTheirText() {
        val s = styled(t("a "), NoteInline.Em(listOf(t("b"))), t(" "), NoteInline.Strong(listOf(t("cd"))), t(" "), NoteInline.Del(listOf(t("e"))))
        assertEquals("a b cd e", s.text)
        assertEquals(
            listOf(StyledSpan(2, 3, SpanStyle.EM), StyledSpan(4, 6, SpanStyle.STRONG), StyledSpan(7, 8, SpanStyle.DEL)),
            s.spans,
        )
    }

    @Test
    fun nestedStylesOverlapAndKeepTheOuterRangeFirst() {
        val s = styled(t("x"), NoteInline.Strong(listOf(t("bold "), NoteInline.Em(listOf(t("both"))), t(" end"))))
        assertEquals("xbold both end", s.text)
        assertEquals(listOf(StyledSpan(1, 14, SpanStyle.STRONG), StyledSpan(6, 10, SpanStyle.EM)), s.spans)
    }

    @Test
    fun inlineCodeIsASpanOverItsText() {
        val s = styled(t("run "), NoteInline.Code("ls -la"), t("!"))
        assertEquals("run ls -la!", s.text)
        assertEquals(listOf(StyledSpan(4, 10, SpanStyle.CODE)), s.spans)
        assertTrue(styled(NoteInline.Code("")).spans.isEmpty()) // nothing to style
    }

    @Test
    fun codeInsideBoldHasBothSpans() {
        val s = styled(NoteInline.Strong(listOf(NoteInline.Code("x"))))
        assertEquals(setOf(StyledSpan(0, 1, SpanStyle.STRONG), StyledSpan(0, 1, SpanStyle.CODE)), s.spans.toSet())
    }

    @Test
    fun anOpenableLinkCarriesItsTarget() {
        val s = styled(t("see "), NoteInline.Link("https://example.com/a?b=1", "title", listOf(t("this "), NoteInline.Em(listOf(t("page"))))))
        assertEquals("see this page", s.text)
        assertEquals(
            listOf(StyledSpan(4, 13, SpanStyle.LINK, "https://example.com/a?b=1"), StyledSpan(9, 13, SpanStyle.EM)),
            s.spans,
        )
    }

    @Test
    fun mailtoLinksOpen() {
        val s = styled(NoteInline.Link("mailto:me@example.com", "", listOf(t("mail"))))
        assertEquals("mailto:me@example.com", s.spans.single().target)
    }

    @Test
    fun linksTheAppDoesNotOpenAreJustText() {
        for (href in listOf("javascript:alert(1)", "intent://x#Intent;end", "file:///etc/passwd", "content://x/y", "tel:123", "ftp://x.y", "relative/path", "example.com", "")) {
            val s = styled(NoteInline.Link(href, "", listOf(t("click"))))
            assertEquals("click", s.text)
            assertTrue("no link span for '$href'", s.spans.isEmpty())
        }
    }

    @Test
    fun aLinkWithoutTextShowsItsAddressAsText() {
        val s = styled(NoteInline.Link("javascript:x", "", emptyList()))
        assertEquals("javascript:x", s.text)
        assertTrue(s.spans.isEmpty())
    }

    @Test
    fun imagesBecomeTextAndLoadNothing() {
        assertEquals("[图片：猫]", styled(NoteInline.Image("https://x.y/c.png", "猫", "")).text)
        assertEquals("[图片：t]", styled(NoteInline.Image("https://x.y/c.png", "", "t")).text)
        assertEquals("[图片]", styled(NoteInline.Image("https://x.y/c.png", "", "")).text)
        val s = styled(NoteInline.Image("https://x.y/c.png", "cat", ""))
        assertEquals(listOf(StyledSpan(0, 8, SpanStyle.LINK, "https://x.y/c.png")), s.spans)
        assertTrue(styled(NoteInline.Image("data:image/png;base64,AAAA", "x", "")).spans.isEmpty())
    }

    @Test
    fun hardBreakIsANewlineAndSoftBreakASpace() {
        assertEquals("a\nb c", styled(t("a"), NoteInline.HardBreak, t("b"), NoteInline.SoftBreak, t("c")).text)
    }

    @Test
    fun unknownInlineKindsShowTheirTextOrChildren() {
        assertEquals("x", styled(NoteInline.Other("sparkle", "x", emptyList())).text)
        assertEquals("y", styled(NoteInline.Other("sparkle", "", listOf(t("y")))).text)
    }

    @Test
    fun htmlLookingTextIsPlainTextNotMarkup() {
        val s = styled(t("<script>alert(1)</script> <b>x</b>"))
        assertEquals("<script>alert(1)</script> <b>x</b>", s.text)
        assertTrue(s.spans.isEmpty())
    }

    @Test
    fun cjkTextKeepsSpanOffsetsInUtf16Units() {
        val s = styled(t("你好 😀 "), NoteInline.Strong(listOf(t("世界"))))
        assertEquals("你好 😀 世界", s.text)
        assertEquals(StyledSpan(6, 8, SpanStyle.STRONG), s.spans.single()) // the emoji is two UTF-16 units
    }

    // ---- blocks ------------------------------------------------------------------------------

    @Test
    fun headingAndParagraph() {
        val flat = NotesRenderSpans.flatten(listOf(NoteBlock.Heading("b", "", 2, listOf(t("Head"))), para(t("Body"))))
        assertEquals(
            listOf(
                FlatBlock.Heading(2, StyledText("Head"), 0, 0),
                FlatBlock.Paragraph(StyledText("Body"), 0, 0),
            ),
            flat,
        )
    }

    @Test
    fun codeBlockKeepsLanguageAndTextVerbatim() {
        val flat = NotesRenderSpans.flatten(listOf(NoteBlock.CodeBlock("b", "", "go", "go title=x", "  if x {\n\ty()\n  }")))
        assertEquals(listOf<FlatBlock>(FlatBlock.Code("go", "  if x {\n\ty()\n  }", 0, 0)), flat)
    }

    @Test
    fun quotesAddDepthToTheirChildren() {
        val inner = NoteBlock.Quote("b", "", listOf(para(t("deep"))))
        val flat = NotesRenderSpans.flatten(listOf(NoteBlock.Quote("b", "", listOf(para(t("one")), inner, NoteBlock.CodeBlock("b", "", "", "", "c")))))
        assertEquals(
            listOf(
                FlatBlock.Paragraph(StyledText("one"), 1, 0),
                FlatBlock.Paragraph(StyledText("deep"), 2, 0),
                FlatBlock.Code("", "c", 1, 0),
            ),
            flat,
        )
    }

    @Test
    fun bulletListUsesAMarkerPerLevel() {
        val nested = list(false, items = listOf(item(null, para(t("c")))))
        val mid = list(false, items = listOf(item(null, para(t("b")), nested)))
        val top = list(false, items = listOf(item(null, para(t("a")), mid), item(null, para(t("a2")))))
        val flat = NotesRenderSpans.flatten(listOf(top)).filterIsInstance<FlatBlock.Item>()
        assertEquals(listOf("•" to 0, "◦" to 1, "▪" to 2, "•" to 0), flat.map { it.marker to it.indent })
        assertEquals(listOf("a", "b", "c", "a2"), flat.map { it.text.text })
    }

    @Test
    fun orderedListCountsFromItsStart() {
        val flat = NotesRenderSpans.flatten(listOf(list(true, start = 3, items = listOf(item(null, para(t("x"))), item(null, para(t("y"))), item(null, para(t("z")))))))
        assertEquals(listOf("3.", "4.", "5."), flat.filterIsInstance<FlatBlock.Item>().map { it.marker })
    }

    @Test
    fun orderedListNestedInABulletKeepsItsOwnNumbers() {
        val ordered = list(true, start = 1, items = listOf(item(null, para(t("one"))), item(null, para(t("two")))))
        val flat = NotesRenderSpans.flatten(listOf(list(false, items = listOf(item(null, para(t("top")), ordered)))))
        val items = flat.filterIsInstance<FlatBlock.Item>()
        assertEquals(listOf("•" to 0, "1." to 1, "2." to 1), items.map { it.marker to it.indent })
    }

    @Test
    fun taskItemsGetCheckboxMarkersAndKeepTheirState() {
        val flat = NotesRenderSpans.flatten(listOf(list(false, items = listOf(item(true, para(t("done"))), item(false, para(t("todo")))))))
        val items = flat.filterIsInstance<FlatBlock.Item>()
        assertEquals(listOf("☑", "☐"), items.map { it.marker })
        assertEquals(listOf<Boolean?>(true, false), items.map { it.checked })
    }

    @Test
    fun anItemsLaterBlocksSitOneLevelDeeper() {
        val flat = NotesRenderSpans.flatten(
            listOf(list(false, items = listOf(item(null, para(t("first")), para(t("more text")), NoteBlock.CodeBlock("b", "", "sh", "sh", "ls"))))),
        )
        assertEquals(
            listOf(
                FlatBlock.Item("•", null, StyledText("first"), true, 0, 0),
                FlatBlock.Paragraph(StyledText("more text"), 0, 1),
                FlatBlock.Code("sh", "ls", 0, 1),
            ),
            flat,
        )
    }

    @Test
    fun anEmptyItemAndAnItemThatStartsWithABlock() {
        val flat = NotesRenderSpans.flatten(
            listOf(list(false, items = listOf(item(null), item(null, NoteBlock.CodeBlock("b", "", "", "", "x"))))),
        )
        assertEquals(
            listOf(
                FlatBlock.Item("•", null, StyledText.EMPTY, true, 0, 0),
                FlatBlock.Item("•", null, StyledText.EMPTY, true, 0, 0),
                FlatBlock.Code("", "x", 0, 1),
            ),
            flat,
        )
    }

    @Test
    fun listInsideAQuoteKeepsBothDepths() {
        val q = NoteBlock.Quote("b", "", listOf(list(false, items = listOf(item(null, para(t("q item")))))))
        val item = NotesRenderSpans.flatten(listOf(q)).single() as FlatBlock.Item
        assertEquals(1, item.quoteDepth)
        assertEquals(0, item.indent)
    }

    @Test
    fun tableCellsAreStyledAndEveryRowHasTheSameColumns() {
        val table = NoteBlock.Table(
            "b", "",
            align = listOf(ColumnAlign.LEFT, ColumnAlign.RIGHT),
            header = listOf(listOf(t("名称")), listOf(t("数量")), listOf(t("extra"))),
            rows = listOf(listOf(listOf(NoteInline.Strong(listOf(t("苹果")))), listOf(t("3"))), listOf(listOf(t("only one")))),
        )
        val flat = NotesRenderSpans.flatten(listOf(table)).single() as FlatBlock.Table
        assertEquals(listOf(ColumnAlign.LEFT, ColumnAlign.RIGHT, ColumnAlign.NONE), flat.align)
        assertEquals(listOf("名称", "数量", "extra"), flat.header.map { it.text })
        assertEquals(2, flat.rows.size)
        assertEquals(listOf("苹果", "3", ""), flat.rows[0].map { it.text })
        assertEquals(listOf(StyledSpan(0, 2, SpanStyle.STRONG)), flat.rows[0][0].spans)
        assertEquals(listOf("only one", "", ""), flat.rows[1].map { it.text })
    }

    @Test
    fun aTableWithoutHeaderStillHasRectangularRows() {
        val flat = NotesRenderSpans.flatten(listOf(NoteBlock.Table("b", "", emptyList(), emptyList(), listOf(listOf(listOf(t("a")), listOf(t("b"))))))).single() as FlatBlock.Table
        assertEquals(2, flat.align.size)
        assertEquals(2, flat.header.size)
        assertEquals(2, flat.rows[0].size)
    }

    @Test
    fun ruleHtmlAndUnknownBlocks() {
        val flat = NotesRenderSpans.flatten(
            listOf(
                NoteBlock.Rule("b", "---"),
                NoteBlock.Html("b", "<b>x</b>", "<b>x</b>"),
                NoteBlock.Unknown("b", "[^1]: n", "footnote", "[^1]: n"),
            ),
        )
        assertEquals(listOf(FlatBlock.Rule(0, 0), FlatBlock.Literal("<b>x</b>", 0, 0), FlatBlock.Literal("[^1]: n", 0, 0)), flat)
    }

    @Test
    fun htmlStaysLiteralText() {
        val html = "<img src=x onerror=alert(1)><script>steal()</script>"
        val lit = NotesRenderSpans.flatten(listOf(NoteBlock.Html("b", html, html))).single() as FlatBlock.Literal
        assertEquals(html, lit.text)
    }

    @Test
    fun displayWidthCountsCjkAsTwoUnits() {
        assertEquals(5, NotesRenderSpans.displayWidth("hello"))
        assertEquals(4, NotesRenderSpans.displayWidth("苹果"))
        assertEquals(3, NotesRenderSpans.displayWidth("a苹"))
        assertEquals(2, NotesRenderSpans.displayWidth("😀")) // one code point (two UTF-16 units), drawn wide
        assertEquals(0, NotesRenderSpans.displayWidth(""))
        assertEquals(6, NotesRenderSpans.displayWidth("ab\nabcdef\nx")) // the longest line counts
        assertEquals(4, NotesRenderSpans.displayWidth("한국")) // Hangul
        assertEquals(2, NotesRenderSpans.displayWidth("Ａ")) // full-width letter
    }

    @Test
    fun columnUnitsFollowTheWidestCellWithinLimits() {
        val table = NoteBlock.Table(
            "b", "",
            align = listOf(ColumnAlign.NONE, ColumnAlign.NONE, ColumnAlign.NONE),
            header = listOf(listOf(t("n")), listOf(t("名称名称名称")), listOf(t("x"))),
            rows = listOf(
                listOf(listOf(t("a much longer cell than anyone needs, really, quite a lot longer")), listOf(t("苹")), listOf(t("0123456789"))),
            ),
        )
        val units = NotesRenderSpans.columnUnits(NotesRenderSpans.flatten(listOf(table)).single() as FlatBlock.Table)
        assertEquals(listOf(NotesRenderSpans.MAX_COLUMN_UNITS, 12, 10), units)
        val narrow = NoteBlock.Table("b", "", emptyList(), listOf(listOf(t("a"))), emptyList())
        assertEquals(listOf(NotesRenderSpans.MIN_COLUMN_UNITS), NotesRenderSpans.columnUnits(NotesRenderSpans.flatten(listOf(narrow)).single() as FlatBlock.Table))
    }

    @Test
    fun emptyTreeIsEmpty() {
        assertTrue(NotesRenderSpans.flatten(emptyList()).isEmpty())
    }
}
