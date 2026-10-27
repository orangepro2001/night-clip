package com.nightclip.app.logic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Cases are written as marked text: «selected», ¦ for a caret. The result is printed the same way, so one string
 * shows text and selection together.
 */
class MarkdownEditTest {
    private fun run(action: MdAction, marked: String): String {
        val a = marked.indexOf('«')
        val b = marked.indexOf('»')
        val c = marked.indexOf('¦')
        val (text, start, end) = when {
            a >= 0 -> Triple(marked.replace("«", "").replace("»", ""), a, b - 1)
            c >= 0 -> Triple(marked.replace("¦", ""), c, c)
            else -> error("no selection marks in: $marked")
        }
        return mark(MarkdownEdit.apply(action, text, start, end))
    }

    private fun mark(r: EditResult): String =
        if (r.selStart == r.selEnd) r.text.substring(0, r.selStart) + "¦" + r.text.substring(r.selStart)
        else r.text.substring(0, r.selStart) + "«" + r.text.substring(r.selStart, r.selEnd) + "»" + r.text.substring(r.selEnd)

    private fun h(level: Int) = MdAction.Heading(level)

    // ---- inline marks ------------------------------------------------------------------------

    @Test
    fun boldWrapsTheSelectionAndKeepsItSelected() {
        assertEquals("hello **«world»**", run(MdAction.Bold, "hello «world»"))
    }

    @Test
    fun boldTogglesOff() {
        assertEquals("hello «world»", run(MdAction.Bold, "hello **«world»**"))
        assertEquals("hello «world»", run(MdAction.Bold, "hello «**world**»")) // the marks may be inside the selection
    }

    @Test
    fun aCaretGetsAnEmptyPairThatTheNextPressRemoves() {
        assertEquals("a **¦** b", mark(MarkdownEdit.apply(MdAction.Bold, "a  b", 2, 2)))
        assertEquals("a ¦ b", mark(MarkdownEdit.apply(MdAction.Bold, "a **** b", 4, 4)))
        assertEquals("**¦**", mark(MarkdownEdit.apply(MdAction.Bold, "", 0, 0)))
    }

    @Test
    fun italicAndBoldShareTheStarWithoutBreakingEachOther() {
        assertEquals("*«x»*", run(MdAction.Italic, "«x»"))
        assertEquals("***«x»***", run(MdAction.Italic, "**«x»**")) // bold + italic
        assertEquals("**«x»**", run(MdAction.Italic, "***«x»***")) // italic off, bold stays
        assertEquals("*«x»*", run(MdAction.Bold, "***«x»***")) // bold off, italic stays
        assertEquals("«x»", run(MdAction.Italic, "*«x»*"))
        assertEquals("***«x»***", run(MdAction.Bold, "*«x»*"))
    }

    @Test
    fun strikethroughAndInlineCode() {
        assertEquals("~~«gone»~~", run(MdAction.Strike, "«gone»"))
        assertEquals("«gone»", run(MdAction.Strike, "~~«gone»~~"))
        assertEquals("run `«ls -la»`", run(MdAction.InlineCode, "run «ls -la»"))
        assertEquals("run «ls -la»", run(MdAction.InlineCode, "run `«ls -la»`"))
    }

    @Test
    fun marksHugTheTextNotTheWhiteSpace() {
        assertEquals(" **«hi»** ", run(MdAction.Bold, "« hi »"))
        assertEquals("a　**«b»**　c", run(MdAction.Bold, "a«　b　»c")) // the ideographic space is white space too
    }

    @Test
    fun cjkAndEmojiAreNeverSplit() {
        assertEquals("你好**«世界»**", run(MdAction.Bold, "你好«世界»"))
        assertEquals("**«😀»**", run(MdAction.Bold, "«😀»"))
        assertEquals("a *«한국어»* b", run(MdAction.Italic, "a «한국어» b"))
        assertEquals("你**¦**好", mark(MarkdownEdit.apply(MdAction.Bold, "你好", 1, 1)))
    }

    @Test
    fun multiLineSelectionMarksEachLine() {
        assertEquals("**«one**\n**two»**", run(MdAction.Bold, "«one\ntwo»"))
        assertEquals("**«a**\n\n**b»**", run(MdAction.Bold, "«a\n\nb»")) // the blank line stays blank
        assertEquals("- **«a**\n- **b»**", run(MdAction.Bold, "«- a\n- b»")) // not around the list marker
        assertEquals("«one\ntwo»", run(MdAction.Bold, "**«one**\n**two»**"))
    }

    @Test
    fun aMixedSelectionIsCompletedNotToggledOff() {
        assertEquals("**a**\n**«b»**", run(MdAction.Bold, "«**a**\nb»"))
    }

    @Test
    fun aSelectionEndingAfterALineBreakDoesNotTouchTheNextLine() {
        assertEquals("**«one»**\ntwo", run(MdAction.Bold, "«one»\ntwo"))
        assertEquals("**one**\ntwo", MarkdownEdit.apply(MdAction.Bold, "one\ntwo", 0, 4).text) // "one\n" selected
    }

    // ---- headings ----------------------------------------------------------------------------

    @Test
    fun headingSetsReplacesAndTogglesOff() {
        assertEquals("# «title»", run(h(1), "«title»"))
        assertEquals("## t¦", run(h(2), "# t¦"))
        assertEquals("## «t»", run(h(2), "# «t»"))
        assertEquals("«t»", run(h(2), "## «t»"))
        assertEquals("###### «t»", run(h(6), "«t»"))
    }

    @Test
    fun headingKeepsTheCaretInTheSameWord() {
        assertEquals("# ab¦cd", run(h(1), "ab¦cd"))
        assertEquals("ab¦cd", run(h(1), "# ab¦cd"))
    }

    @Test
    fun headingReplacesAListMarkerAndKeepsAQuote() {
        assertEquals("# item¦", run(h(1), "- item¦"))
        assertEquals("> # quoted¦", run(h(1), "> quoted¦"))
    }

    @Test
    fun headingOnSeveralLines() {
        assertEquals("# «a\n# b»", run(h(1), "«a\nb»"))
        assertEquals("«a\nb»", run(h(1), "# «a\n# b»"))
        assertEquals("# «a\n# b»", run(h(1), "# «a\nb»")) // not all are headings yet: complete them
    }

    // ---- lists -------------------------------------------------------------------------------

    @Test
    fun bulletListToggles() {
        assertEquals("- a¦", run(MdAction.Bullet, "a¦"))
        assertEquals("a¦", run(MdAction.Bullet, "- a¦"))
        assertEquals("- «a\n- b»", run(MdAction.Bullet, "«a\nb»"))
        assertEquals("«a\nb»", run(MdAction.Bullet, "- «a\n- b»"))
        assertEquals("- a¦", run(MdAction.Bullet, "1. a¦"))
        assertEquals("- a¦", run(MdAction.Bullet, "- [ ] a¦"))
    }

    @Test
    fun bulletsKeepTheNestingIndent() {
        assertEquals("  - a¦", run(MdAction.Bullet, "  a¦"))
        assertEquals("  a¦", run(MdAction.Bullet, "  - a¦"))
    }

    @Test
    fun otherBulletCharactersCountAsBullets() {
        assertEquals("a¦", run(MdAction.Bullet, "* a¦"))
        assertEquals("a¦", run(MdAction.Bullet, "+ a¦"))
    }

    @Test
    fun numberedListCountsUpAndToggles() {
        assertEquals("1. «a\n2. b\n3. c»", run(MdAction.Numbered, "«a\nb\nc»"))
        assertEquals("«a\nb\nc»", run(MdAction.Numbered, "1. «a\n2. b\n3. c»"))
        assertEquals("1. «a\n2. b»", run(MdAction.Numbered, "- «a\n- b»"))
        assertEquals("1. «a\n2. b»", run(MdAction.Numbered, "1. «a\nb»"))
    }

    @Test
    fun numberingSkipsBlankLines() {
        assertEquals("1. «a\n\n2. b»", run(MdAction.Numbered, "«a\n\nb»"))
    }

    @Test
    fun taskListToggles() {
        assertEquals("- [ ] a¦", run(MdAction.Task, "a¦"))
        assertEquals("a¦", run(MdAction.Task, "- [ ] a¦"))
        assertEquals("a¦", run(MdAction.Task, "- [x] a¦"))
        assertEquals("- [ ] a¦", run(MdAction.Task, "- a¦"))
        assertEquals("- [ ] «a\n- [ ] b»", run(MdAction.Task, "«a\nb»"))
        assertEquals("- [x] «done\n- [ ] b»", run(MdAction.Task, "- [x] «done\nb»")) // an existing task keeps its state
    }

    @Test
    fun indentAndOutdent() {
        assertEquals("  - a¦", run(MdAction.Indent, "- a¦"))
        assertEquals("- a¦", run(MdAction.Outdent, "  - a¦"))
        assertEquals("   1. a¦", run(MdAction.Indent, "1. a¦")) // an ordered item nests under its text
        assertEquals("1. a¦", run(MdAction.Outdent, "   1. a¦"))
        assertEquals("  «a\n  b»", run(MdAction.Indent, "«a\nb»"))
        assertEquals("- a¦", run(MdAction.Outdent, "- a¦")) // nothing to remove
        assertEquals("a", MarkdownEdit.apply(MdAction.Outdent, "\ta", 0, 0).text)
    }

    // ---- quote, code block, link, rule -------------------------------------------------------

    @Test
    fun quoteToggles() {
        assertEquals("> «a\n> b»", run(MdAction.Quote, "«a\nb»"))
        assertEquals("«a\nb»", run(MdAction.Quote, "> «a\n> b»"))
        assertEquals("> ¦", run(MdAction.Quote, "¦"))
        assertEquals("> - a¦", run(MdAction.Quote, "- a¦"))
    }

    @Test
    fun aBlankLineBetweenQuotedLinesKeepsOneQuote() {
        assertEquals("> «a\n>\n> b»", run(MdAction.Quote, "«a\n\nb»"))
        assertEquals("«a\n\nb»", run(MdAction.Quote, "> «a\n>\n> b»"))
    }

    @Test
    fun nestedQuoteLevelsComeOffOneAtATime() {
        assertEquals("> a¦", run(MdAction.Quote, ">> a¦"))
    }

    @Test
    fun codeBlockAtACaret() {
        assertEquals("```\n¦\n```", run(MdAction.CodeBlock, "¦"))
        assertEquals("abc\n```\n¦\n```", run(MdAction.CodeBlock, "abc¦"))
    }

    @Test
    fun codeBlockAroundSelectedLinesAndBackOut() {
        assertEquals("```\n«a\nb»\n```", run(MdAction.CodeBlock, "«a\nb»"))
        assertEquals("«a\nb»", run(MdAction.CodeBlock, "```\n«a\nb»\n```")) // selection inside the fences
        assertEquals("«a»", run(MdAction.CodeBlock, "«```\na\n```»")) // the fences themselves selected
        assertEquals("x\n```\n«mid»\n```\ny", run(MdAction.CodeBlock, "x\n«mid»\ny"))
    }

    @Test
    fun codeBlockExtendsToWholeLines() {
        assertEquals("```\nabcdef\n```", MarkdownEdit.apply(MdAction.CodeBlock, "abcdef", 2, 4).text)
    }

    @Test
    fun aLongerFenceWhenTheCodeContainsFences() {
        assertEquals("````\nuse ``` in text\n````", MarkdownEdit.apply(MdAction.CodeBlock, "use ``` in text", 0, 15).text)
        // A text that already is one fenced block is unwrapped, not wrapped again.
        assertEquals("x", MarkdownEdit.apply(MdAction.CodeBlock, "```\nx\n```", 0, 9).text)
    }

    @Test
    fun linkAroundTheSelection() {
        assertEquals("[site](«https://»)", run(MdAction.Link, "«site»"))
        assertEquals("[¦](https://)", run(MdAction.Link, "¦"))
        assertEquals("[¦](https://example.com/a?b=1)", run(MdAction.Link, "«https://example.com/a?b=1»"))
        assertEquals("你好 [世界](«https://»)", run(MdAction.Link, "你好 «世界»"))
    }

    @Test
    fun ruleKeepsABlankLineAboveSoTheTextIsNotTurnedIntoAHeading() {
        assertEquals("abc\n\n---\n¦", run(MdAction.Rule, "abc¦"))
        assertEquals("a\n\n---\n¦\nb", run(MdAction.Rule, "a¦\nb"))
        assertEquals("a\n\n---\n¦", run(MdAction.Rule, "a\n¦"))
        assertEquals("a\n\n---\n¦", run(MdAction.Rule, "a\n\n¦"))
        assertEquals("---\n¦", run(MdAction.Rule, "¦"))
    }

    // ---- robustness --------------------------------------------------------------------------

    @Test
    fun badOffsetsAreClampedAndReversedSelectionsWork() {
        val text = "hello"
        assertEquals("**hello**", MarkdownEdit.apply(MdAction.Bold, text, -5, 99).text)
        assertEquals("he**llo**", MarkdownEdit.apply(MdAction.Bold, text, 99, 2).text) // reversed and past the end
        assertEquals("# ", MarkdownEdit.apply(h(1), "", 0, 0).text)
    }

    @Test
    fun everyActionRunsOnManySelectionsOfAMixedTextWithoutFailing() {
        val text = "# 标题\n\n- 项目 *one* **two**\n  - [x] 子项 😀\n\n> 引用\n\n```\ncode\n```\n\n1. a\n2. b\n"
        val actions = listOf(
            MdAction.Bold, MdAction.Italic, MdAction.Strike, MdAction.InlineCode, h(1), h(3), MdAction.Bullet, MdAction.Numbered,
            MdAction.Task, MdAction.Quote, MdAction.CodeBlock, MdAction.Link, MdAction.Rule, MdAction.Indent, MdAction.Outdent,
        )
        for (action in actions) {
            for (a in 0..text.length step 3) {
                for (b in a..text.length step 7) {
                    val r = MarkdownEdit.apply(action, text, a, b)
                    assertTrue("$action $a..$b -> ${r.selStart}..${r.selEnd}", r.selStart in 0..r.text.length && r.selEnd in r.selStart..r.text.length)
                }
            }
        }
    }

    @Test
    fun applyingATogglingActionTwiceGivesBackTheText() {
        val samples = listOf(
            "hello world" to (6 to 11),
            "你好，世界" to (2 to 5),
            "a line" to (3 to 3),
            "one\ntwo\nthree" to (0 to 13),
            "para one\n\npara two" to (0 to 18),
            "plain" to (0 to 5),
        )
        val actions = listOf(
            MdAction.Bold, MdAction.Italic, MdAction.Strike, MdAction.InlineCode, h(2), MdAction.Bullet, MdAction.Numbered,
            MdAction.Task, MdAction.Quote,
        )
        for (action in actions) {
            for ((text, sel) in samples) {
                val once = MarkdownEdit.apply(action, text, sel.first, sel.second)
                val twice = MarkdownEdit.apply(action, once.text, once.selStart, once.selEnd)
                assertEquals("$action twice on '$text' ${sel.first}..${sel.second}", text, twice.text)
            }
        }
    }

    @Test
    fun listLinesGoThroughBulletQuoteAndBoldAndBack() {
        val text = "- x\n- y"
        for (action in listOf(MdAction.Bullet, MdAction.Quote, MdAction.Bold, MdAction.Italic)) {
            val once = MarkdownEdit.apply(action, text, 0, text.length)
            val twice = MarkdownEdit.apply(action, once.text, once.selStart, once.selEnd)
            assertEquals("$action", text, twice.text)
        }
    }

    @Test
    fun codeBlockOverSelectedLinesGoesAndComesBack() {
        for ((text, sel) in listOf("hello world" to (6 to 11), "one\ntwo" to (0 to 7), "a\nb\nc" to (2 to 3))) {
            val once = MarkdownEdit.apply(MdAction.CodeBlock, text, sel.first, sel.second)
            val twice = MarkdownEdit.apply(MdAction.CodeBlock, once.text, once.selStart, once.selEnd)
            assertEquals(text, twice.text)
        }
    }

    @Test
    fun indentThenOutdentIsTheIdentity() {
        for (text in listOf("a", "- a", "1. a", "one\ntwo")) {
            val indented = MarkdownEdit.apply(MdAction.Indent, text, 0, text.length)
            assertEquals(text, MarkdownEdit.apply(MdAction.Outdent, indented.text, indented.selStart, indented.selEnd).text)
        }
    }
}
