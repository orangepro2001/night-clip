package com.nightclip.app.logic

import com.nightclip.app.core.ListItem
import com.nightclip.app.core.NoteBlock
import com.nightclip.app.core.NoteInline
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class NotesRenderSpansAttachmentsTest {
    private val id1 = "11".repeat(32)
    private val id2 = "22".repeat(32)

    private fun t(s: String) = NoteInline.Text(s)
    private fun para(vararg i: NoteInline) = NoteBlock.Paragraph("b", "", i.toList())
    private fun image(id: String, alt: String = "pic") = NoteInline.Image("blob:$id", alt, "")
    private fun file(id: String, name: String) = NoteInline.Link("blob:$id", "", listOf(t(name)))
    private fun flat(vararg b: NoteBlock) = NotesRenderSpans.flatten(b.toList())

    @Test
    fun aParagraphThatIsOnlyAPictureBecomesAPictureBlock() {
        assertEquals(listOf<FlatBlock>(FlatBlock.Image(id1, "pic", 0, 0)), flat(para(image(id1))))
    }

    @Test
    fun aParagraphThatIsOnlyAFileBecomesAChip() {
        assertEquals(listOf<FlatBlock>(FlatBlock.File(id1, "报告.pdf", 0, 0)), flat(para(file(id1, "报告.pdf"))))
    }

    @Test
    fun textAroundAPictureStaysAndThePictureFollows() {
        val blocks = flat(para(t("see "), image(id1), t(" here")))
        assertEquals(listOf("Paragraph", "Image"), blocks.map { it::class.simpleName })
        assertEquals("see  here", (blocks[0] as FlatBlock.Paragraph).text.text)
    }

    @Test
    fun severalAttachmentsKeepTheirOrder() {
        val blocks = flat(para(image(id1, "a"), NoteInline.SoftBreak, file(id2, "b.txt"), NoteInline.SoftBreak, image(id2, "c")))
        assertEquals(
            listOf<FlatBlock>(FlatBlock.Image(id1, "a", 0, 0), FlatBlock.File(id2, "b.txt", 0, 0), FlatBlock.Image(id2, "c", 0, 0)),
            blocks,
        )
    }

    @Test
    fun theLineBreaksLeftBehindAreNotText() {
        val blocks = flat(para(t("first"), NoteInline.SoftBreak, image(id1), NoteInline.SoftBreak, t("last")))
        val text = (blocks[0] as FlatBlock.Paragraph).text
        assertEquals("first\n\nlast", text.text) // the picture sat between two breaks
    }

    @Test
    fun spansAroundAnAttachmentAreKeptInRange() {
        val blocks = flat(para(image(id1), t(" "), NoteInline.Strong(listOf(t("bold"))), t(" "), file(id2, "n")))
        val text = (blocks[0] as FlatBlock.Paragraph).text
        assertEquals("bold", text.text)
        assertEquals(listOf(StyledSpan(0, 4, SpanStyle.STRONG)), text.spans)
    }

    @Test
    fun theBlobAddressIsNeverALinkTarget() {
        val all = flat(para(t("x "), image(id1), file(id2, "n"), t(" y")))
        for (b in all.filterIsInstance<FlatBlock.Paragraph>()) assertTrue(b.text.spans.none { it.target != null })
    }

    @Test
    fun aMalformedBlobAddressIsJustText() {
        val bad = NoteInline.Image("blob:nothex", "broken", "")
        val blocks = flat(para(bad))
        assertEquals(listOf("Paragraph"), blocks.map { it::class.simpleName })
        assertEquals("[图片：broken]", (blocks[0] as FlatBlock.Paragraph).text.text)
        assertTrue((blocks[0] as FlatBlock.Paragraph).text.spans.isEmpty()) // and not a tappable link
        val link = flat(para(NoteInline.Link("blob:nothex", "", listOf(t("name")))))
        assertEquals("name", (link[0] as FlatBlock.Paragraph).text.text)
        assertTrue((link[0] as FlatBlock.Paragraph).text.spans.isEmpty())
    }

    @Test
    fun aWebPictureStaysTextWithALinkAndIsNotLoaded() {
        val blocks = flat(para(NoteInline.Image("https://x.y/c.png", "cat", "")))
        assertEquals(listOf("Paragraph"), blocks.map { it::class.simpleName })
        assertEquals(StyledSpan(0, 8, SpanStyle.LINK, "https://x.y/c.png"), (blocks[0] as FlatBlock.Paragraph).text.spans.single())
    }

    @Test
    fun anEmptyAltTextGivesAnEmptyName() {
        assertEquals(listOf<FlatBlock>(FlatBlock.Image(id1, "", 0, 0)), flat(para(NoteInline.Image("blob:$id1", "", ""))))
        assertEquals(listOf<FlatBlock>(FlatBlock.Image(id1, "title", 0, 0)), flat(para(NoteInline.Image("blob:$id1", "", "title"))))
    }

    @Test
    fun aLinkWithoutTextIsNamedFile() {
        assertEquals(listOf<FlatBlock>(FlatBlock.File(id1, "file", 0, 0)), flat(para(NoteInline.Link("blob:$id1", "", emptyList()))))
    }

    @Test
    fun attachmentsInAQuoteKeepTheirDepth() {
        val blocks = flat(NoteBlock.Quote("b", "", listOf(para(image(id1)))))
        assertEquals(listOf<FlatBlock>(FlatBlock.Image(id1, "pic", 1, 0)), blocks)
    }

    @Test
    fun anAttachmentInAListItemHangsUnderTheItem() {
        val list = NoteBlock.ListBlock("b", "", false, 1, true, listOf(ListItem(null, listOf(para(t("photo: "), image(id1))))))
        val blocks = flat(list)
        assertEquals(2, blocks.size)
        assertEquals("photo:", (blocks[0] as FlatBlock.Item).text.text)
        assertEquals(FlatBlock.Image(id1, "pic", 0, 1), blocks[1])
    }

    @Test
    fun aHeadingKeepsItsTextAndItsPictureFollows() {
        val blocks = flat(NoteBlock.Heading("b", "", 2, listOf(t("Trip "), image(id1))))
        assertEquals(listOf("Heading", "Image"), blocks.map { it::class.simpleName })
    }

    @Test
    fun tableCellsCannotHoldPicturesSoTheyShowTheirNames() {
        val table = NoteBlock.Table(
            "b", "", emptyList(),
            header = listOf(listOf(t("file"))),
            rows = listOf(listOf(listOf(image(id1, "pic"), t(" "), file(id2, "doc.pdf")))),
        )
        val cell = (flat(table).single() as FlatBlock.Table).rows[0][0]
        assertEquals("[图片：pic] doc.pdf", cell.text)
        assertTrue(cell.spans.isEmpty())
    }

    @Test
    fun textWithoutAttachmentsIsUnchanged() {
        val text = (flat(para(t("plain "), NoteInline.Em(listOf(t("em"))), t("  "))).single() as FlatBlock.Paragraph).text
        assertEquals("plain em  ", text.text) // trailing white space is only trimmed when an attachment was taken out
        assertFalse(text.spans.isEmpty())
    }
}
