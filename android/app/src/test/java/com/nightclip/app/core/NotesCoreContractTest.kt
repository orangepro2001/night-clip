package com.nightclip.app.core

import com.nightclip.app.logic.EditorCommand
import com.nightclip.app.logic.FlatBlock
import com.nightclip.app.logic.AttachmentFiles
import com.nightclip.app.logic.BlobAddress
import com.nightclip.app.logic.NoteEditorLogic
import com.nightclip.app.logic.NotesSearchLogic
import com.nightclip.app.logic.NotesTransfer
import com.nightclip.app.logic.NotesErrors
import com.nightclip.app.logic.NotesRenderSpans
import com.nightclip.app.logic.SpanStyle
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Runs REAL replies of the Go core through the Kotlin parsers. `notes/core-replies.json` was captured from
 * `mobile.Core` (the object the AAR wraps) while it created, edited, merged, conflicted, deleted and restored a note
 * with every kind of block. If the core's JSON changes shape, this is where the shell finds out.
 */
class NotesCoreContractTest {
    private val fixture = JSONObject(javaClass.getResourceAsStream("/notes/core-replies.json")!!.readBytes().toString(Charsets.UTF_8))

    private fun raw(name: String): String {
        val steps = fixture.getJSONArray("steps")
        for (i in 0 until steps.length()) {
            val s = steps.getJSONObject(i)
            if (s.getString("name") == name) return s.getJSONObject("reply").toString()
        }
        error("no step $name")
    }

    private fun reply(name: String) = Reply.parse(raw(name))
    private fun result(name: String): JSONObject = reply(name).also { assertTrue("$name failed: ${it.errorCode}", it.ok) }.result

    // ---- results -----------------------------------------------------------------------------

    @Test
    fun createGetAndListAgree() {
        val created = NotesParsers.created(result("create"))
        assertTrue(created.noteId.startsWith("nt_"))
        assertTrue(created.revId.startsWith("r_"))

        val doc = NotesParsers.doc(result("get"))
        assertEquals(created.noteId, doc.noteId)
        assertEquals(created.revId, doc.revId)
        assertEquals("Plan", doc.title)
        assertTrue(doc.body.startsWith("# 计划 Plan\n"))
        assertTrue(doc.body.endsWith("last paragraph\n"))
        assertEquals(1, doc.formatVersion)
        assertEquals(1, doc.revCount)
        assertTrue(doc.isHead)
        assertFalse(doc.deleted)
        assertTrue(doc.parents.isEmpty())
        assertTrue("milliseconds", doc.createdAt > 1_700_000_000_000L)

        val list = NotesParsers.page(result("list"))
        assertEquals(created.noteId, list.items.single().noteId)
        assertEquals("Plan", list.items.single().title)
        assertTrue(list.items.single().updatedAt > 1_700_000_000_000L)
        assertNull(list.nextCursor)
    }

    @Test
    fun pagedListHasACursor() {
        val page = NotesParsers.page(result("list_final"))
        assertEquals(2, page.items.size)
        assertNotNull(page.nextCursor)
        assertTrue(page.nextCursor!!.contains(":nt_")) // "<updated_at ms>:<note id>", opaque to the shell
    }

    @Test
    fun anUntitledNoteComesBackWithAnEmptyTitleAndPreview() {
        val item = NotesParsers.page(result("list_final")).items.single { it.title.isEmpty() }
        assertEquals("", item.preview)
    }

    @Test
    fun updateOutcomes() {
        assertEquals(false, NotesParsers.updated(result("update_plain")).merged)
        val merged = NotesParsers.updated(result("update_merged"))
        assertTrue(merged.merged)
        assertNull(merged.conflictNoteId)
        val conflict = NotesParsers.updated(result("update_conflict"))
        assertTrue(conflict.merged)
        assertNotNull(conflict.conflictNoteId)
        assertEquals(NotesParsers.updated(result("update_title_only")).revId, NotesParsers.updated(result("update_nochange")).revId)
    }

    @Test
    fun historyAndItsPages() {
        val all = NotesParsers.history(result("history"))
        assertTrue(all.items.size >= 4)
        assertTrue(all.items.all { it.revId.startsWith("r_") && it.createdAt > 0 })
        assertTrue(all.items.any { it.parents.size == 2 }) // a merge revision
        assertNull(all.nextCursor)
        val first = NotesParsers.history(result("history_page"))
        assertEquals(1, first.items.size)
        assertEquals(all.items.first().revId, first.items.first().revId) // newest first
        assertNotNull(first.nextCursor)
    }

    @Test
    fun conflictCopyAndItsResolution() {
        val list = NotesParsers.conflicts(result("conflicts"))
        val c = list.items.single()
        assertTrue(c.title.contains("冲突"))
        val copy = NotesParsers.doc(result("get_copy"))
        assertEquals(c.conflictNoteId, copy.noteId)
        assertEquals(c.noteId, copy.conflictOf)
        assertTrue(copy.body.startsWith("> 冲突副本")) // the explanation block the core puts on top
        assertTrue(NotesParsers.rendered(result("render_copy")).blocks.first() is NoteBlock.Quote)
        assertTrue(result("resolve_merge").getString("rev_id").startsWith("r_"))
        assertTrue(NotesParsers.conflicts(result("conflicts_after")).items.isEmpty())
    }

    @Test
    fun trashRoundTrip() {
        // The note itself, and the conflict copy that "merge" put in the trash.
        val deleted = NotesParsers.page(result("list_deleted")).items
        assertEquals(2, deleted.size)
        assertTrue(deleted.all { it.deleted })
        assertEquals(1, deleted.count { it.isConflictCopy })
        assertTrue(NotesParsers.doc(result("get_deleted")).deleted)
        assertTrue(result("restore").getString("rev_id").startsWith("r_"))
    }

    @Test
    fun syncReplies() {
        val status = NotesParsers.syncStatus(result("sync_status"))
        assertTrue(status.peers.isEmpty())
        assertEquals(0, status.pendingPush)
        assertTrue(result("sync_now").getBoolean("started"))
    }

    @Test
    fun normalize() {
        val n = NotesParsers.normalized(result("normalize"))
        assertEquals("a\n\n- b\n", n.bodyNorm)
        assertTrue(n.bodyHash.isNotEmpty())
    }

    // ---- errors ------------------------------------------------------------------------------

    @Test
    fun errorCodesAndTheirTexts() {
        val expected = mapOf(
            "sync_now_unpaired" to "NOT_PAIRED",
            "update_deleted" to "CONFLICT",
            "get_missing" to "NOT_FOUND",
            "update_bad_base" to "NOT_FOUND",
            "get_bad_id" to "INVALID",
            "create_too_large" to "TOO_LARGE",
            "search_empty" to "INVALID",
            "attachment_add_too_large" to "TOO_LARGE",
            "attachment_add_relative" to "INVALID",
            "attachment_get_malformed" to "INVALID",
            "attachment_export_missing" to "NOT_FOUND",
            "export_exists" to "INVALID",
            "render_body_and_id" to "INVALID",
        )
        for ((step, code) in expected) {
            val r = reply(step)
            assertFalse(step, r.ok)
            assertEquals(step, code, r.errorCode)
            assertFalse("$step has no specific text", NotesErrors.message(code).contains("未知错误"))
        }
        assertNotEquals(NotesErrors.message("CONFLICT"), NotesErrors.message("NOT_FOUND"))
    }

    // ---- the render tree, all the way to drawable blocks -------------------------------------

    @Test
    fun theRealRenderTreeParsesIntoEveryBlockKind() {
        val tree = NotesParsers.rendered(result("render"))
        assertEquals(
            listOf(
                "Heading", "Paragraph", "Quote", "ListBlock", "ListBlock", "CodeBlock", "Table", "Rule", "Html", "Unknown", "Unknown", "Paragraph",
            ),
            tree.blocks.map { it::class.simpleName },
        )
        assertEquals(1, tree.toc.single().level)
    }

    @Test
    fun theRealRenderTreeFlattensToStyledText() {
        val flat = NotesRenderSpans.flatten(NotesParsers.rendered(result("render")).blocks)

        val head = flat[0] as FlatBlock.Heading
        assertEquals("计划 Plan", head.text.text)

        val para = flat[1] as FlatBlock.Paragraph
        val t = para.text.text
        assertEquals("Hello em strong del code link [图片：pic] 😀\nsoft line\nhard line", t) // soft and hard breaks are both line breaks
        val styles = para.text.spans.map { it.style }.toSet()
        assertEquals(setOf(SpanStyle.EM, SpanStyle.STRONG, SpanStyle.DEL, SpanStyle.CODE, SpanStyle.LINK), styles)
        val links = para.text.spans.filter { it.style == SpanStyle.LINK }
        assertEquals(setOf("https://example.com/a?b=1", "https://x.y/i.png"), links.map { it.target }.toSet())
        val link = links.first { it.target == "https://example.com/a?b=1" }
        assertEquals("link", t.substring(link.start, link.end))

        // The quote holds a paragraph and a list.
        val quoted = flat.filter { it.quoteDepth == 1 }
        assertEquals(listOf("Paragraph", "Item"), quoted.map { it::class.simpleName })

        // Bullets nest: bullet one (0) > nested bullet (1) > deep ordered (2); then the two tasks; then the numbered list from 3.
        val items = flat.filterIsInstance<FlatBlock.Item>().filter { it.quoteDepth == 0 }
        assertEquals(
            listOf("•" to 0, "◦" to 1, "1." to 2, "☐" to 0, "☑" to 0, "3." to 0, "4." to 0),
            items.map { it.marker to it.indent },
        )
        assertEquals(listOf<Boolean?>(null, null, null, false, true, null, null), items.map { it.checked })

        val code = flat.filterIsInstance<FlatBlock.Code>().single()
        assertEquals("go", code.lang)
        assertEquals("func main() {}", code.text)

        val table = flat.filterIsInstance<FlatBlock.Table>().single()
        assertEquals(listOf("名称", "数量", "价格"), table.header.map { it.text })
        assertEquals(listOf("苹果", "3", "1.5"), table.rows[0].map { it.text })
        assertEquals(SpanStyle.STRONG, table.rows[1][1].spans.single().style)
        assertEquals(3, NotesRenderSpans.columnUnits(table).size)

        assertTrue(flat.any { it is FlatBlock.Rule })
        // HTML and unsupported syntax are literal text.
        val literals = flat.filterIsInstance<FlatBlock.Literal>().map { it.text }
        assertEquals(listOf("<div onclick=\"x()\">raw html</div>", "[^1]: footnote text", "$$ x^2 $$"), literals)
        assertEquals("last paragraph", (flat.last() as FlatBlock.Paragraph).text.text)
    }

    // ---- events ------------------------------------------------------------------------------

    @Test
    fun theRealEventsParse() {
        val events = fixture.getJSONArray("events")
        val parsed = (0 until events.length()).map { CoreEvent.parse(events.getJSONObject(it).toString()) }
        assertTrue(parsed.none { it == null })
        assertTrue(parsed.first() is CoreEvent.CoreStarted)
        val changed = parsed.filterIsInstance<CoreEvent.NotesChanged>()
        assertTrue(changed.size >= 8)
        assertTrue(changed.all { it.noteId.startsWith("nt_") && it.revId.startsWith("r_") })
        val conflict = parsed.filterIsInstance<CoreEvent.NotesSyncConflict>().single()
        assertEquals("diverged", conflict.reason)
        assertTrue(conflict.conflictNoteId.startsWith("nt_"))
    }

    // ---- the editor against the real answers -------------------------------------------------

    @Test
    fun theEditorHandlesTheRealMergedAndConflictAnswers() {
        var clock = 0L
        val logic = NoteEditorLogic({ clock })
        val doc = NotesParsers.doc(result("get"))
        val load = logic.open(doc.noteId).single() as EditorCommand.Load
        logic.onLoaded(load.ticket, doc)

        var expectedBase = doc.revId
        for (step in listOf("update_merged", "update_conflict")) {
            logic.edit(doc.title, "typed for $step")
            val save = logic.flush().single() as EditorCommand.Save
            assertEquals(expectedBase, save.baseRevId)
            val out = NotesParsers.updated(result(step))
            val cmds = logic.onSaved(save.ticket, doc.noteId, out.revId, out.merged, out.conflictNoteId)
            val reload = cmds.filterIsInstance<EditorCommand.Load>().single() // the newest text is loaded
            logic.onLoaded(reload.ticket, doc.copy(revId = out.revId, body = "newest after $step"))
            assertEquals(out.revId, logic.state.baseRevId)
            assertEquals("newest after $step", logic.state.body)
            assertFalse(logic.state.dirty)
            expectedBase = out.revId
        }
    }

    // ---- phase 2: live preview, search, attachments, export and import -----------------------

    @Test
    fun updateTellsWhetherARevisionWasMade() {
        assertTrue(NotesParsers.updated(result("update_plain")).changed)
        val nothing = NotesParsers.updated(result("update_nochange"))
        assertFalse(nothing.changed)
        assertFalse(nothing.merged)
        assertEquals(NotesParsers.updated(result("update_title_only")).revId, nothing.revId)
    }

    @Test
    fun renderOfUnsavedTextHasNoNoteAndKeepsLineBreaks() {
        val tree = NotesParsers.rendered(result("render_body"))
        assertEquals("", tree.noteId)
        assertEquals("", tree.revId)
        val flat = NotesRenderSpans.flatten(tree.blocks)
        assertEquals("Live", (flat[0] as FlatBlock.Heading).text.text)
        assertEquals("unsaved text\nsecond line", (flat[1] as FlatBlock.Paragraph).text.text) // the soft break is a line break
    }

    @Test
    fun searchHitsAndTheirHighlightsInCodePoints() {
        val title = NotesParsers.search(result("search_title")).items.single()
        assertEquals("Renamed", title.title)
        assertEquals(listOf(Span(0, 7)), title.titleHighlights.map { Span(it.start, it.end) })
        assertTrue(title.highlights.isEmpty())
        assertFalse(title.snippet.contains('\n'))

        val body = NotesParsers.search(result("search_body")).items.single()
        assertTrue(body.highlights.isNotEmpty())
        val words = NotesSearchLogic.toSpans(body.snippet, body.highlights).map { body.snippet.substring(it.start, it.end) }
        assertEquals(listOf("last", "paragraph"), words)

        assertTrue(NotesParsers.search(result("search_none")).items.isEmpty())
    }

    @Test
    fun emojiBeforeAHighlightMoveItInUtf16() {
        val hit = NotesParsers.search(result("search_emoji")).items.single()
        assertEquals("😀 emoji 笔记", hit.title)
        val titleSpan = NotesSearchLogic.toSpans(hit.title, hit.titleHighlights).single()
        assertEquals("emoji", hit.title.substring(titleSpan.start, titleSpan.end))
        val snippetSpan = NotesSearchLogic.toSpans(hit.snippet, hit.highlights).single()
        assertEquals("emoji", hit.snippet.substring(snippetSpan.start, snippetSpan.end))
        // Used as they are, the code point numbers would point at the wrong characters.
        val raw = hit.highlights.single()
        assertNotEquals("emoji", hit.snippet.substring(raw.start, raw.end))

        val cjk = NotesParsers.search(result("search_cjk")).items.single()
        val cjkSnippet = NotesSearchLogic.toSpans(cjk.snippet, cjk.highlights).single()
        assertEquals("笔记", cjk.snippet.substring(cjkSnippet.start, cjkSnippet.end))
        val cjkTitle = NotesSearchLogic.toSpans(cjk.title, cjk.titleHighlights).single()
        assertEquals("笔记", cjk.title.substring(cjkTitle.start, cjkTitle.end))
    }

    @Test
    fun searchPagesFollowTheCursor() {
        val first = NotesParsers.search(result("search_page1"))
        val second = NotesParsers.search(result("search_page2"))
        assertEquals(1, first.items.size)
        assertNotNull(first.nextCursor)
        assertEquals(1, second.items.size)
        assertNotEquals(first.items.single().noteId, second.items.single().noteId)
        // The pages are put together the way the list does it.
        val state = NotesSearchLogic.nextPage(NotesSearchLogic.firstPage("e", first), "e", first.nextCursor!!, second)
        assertEquals(2, state.items.size)
    }

    @Test
    fun attachmentReplies() {
        val image = NotesParsers.attachment(result("attachment_add_image"))
        assertTrue(BlobAddress.isBlobId(image.blobId))
        assertEquals("image/png", image.mime)
        assertTrue(image.present)
        assertEquals(70L, image.size)
        assertEquals("photo [1].png", image.name)
        assertEquals("![photo \\[1\\].png](blob:${image.blobId})", image.markdown)
        assertEquals(AttachmentFiles.Kind.PICTURE, AttachmentFiles.kindOf(image))

        val file = NotesParsers.attachment(result("attachment_add_file"))
        assertTrue(file.markdown.startsWith("[报告 notes.txt](blob:"))
        assertEquals(AttachmentFiles.Kind.DOCUMENT, AttachmentFiles.kindOf(file))
        assertTrue(AttachmentFiles.isSafeToOpen(file.name, file.mime))

        assertEquals(image, NotesParsers.attachment(result("attachment_get_image")).copy(markdown = image.markdown))
        val missing = NotesParsers.attachment(result("attachment_get_missing"))
        assertFalse(missing.present)
        assertEquals("", missing.name)
        assertEquals(AttachmentFiles.Kind.ABSENT, AttachmentFiles.kindOf(missing))
        assertTrue(NotesParsers.attachment(result("attachment_export_image")).present)

        val cleanup = NotesParsers.cleanup(result("attachment_cleanup"))
        assertEquals(0, cleanup.removed)
        assertEquals(2, cleanup.kept)
        assertEquals("没有可清理的附件", NotesTransfer.cleanupSummary(cleanup))
    }

    @Test
    fun theRenderTreeOfANoteWithAttachmentsBecomesPicturesAndChips() {
        val image = NotesParsers.attachment(result("attachment_add_image"))
        val file = NotesParsers.attachment(result("attachment_add_file"))
        val flat = NotesRenderSpans.flatten(NotesParsers.rendered(result("render_attachments")).blocks)
        assertEquals(
            listOf("Heading", "Image", "Paragraph", "File", "Image"),
            flat.map { it::class.simpleName },
        )
        assertEquals(FlatBlock.Image(image.blobId, "photo [1].png", 0, 0), flat[1])
        assertEquals("text with  inline", (flat[2] as FlatBlock.Paragraph).text.text)
        assertEquals(FlatBlock.File(file.blobId, "报告 notes.txt", 0, 0), flat[3])
        assertEquals("0".repeat(64), (flat[4] as FlatBlock.Image).blobId) // not on this device: the preview waits for it
        assertEquals("missing", (flat[4] as FlatBlock.Image).alt)
        // None of it is a web link.
        assertTrue(flat.filterIsInstance<FlatBlock.Paragraph>().all { p -> p.text.spans.none { it.target != null } })
    }

    @Test
    fun exportAndImportReplies() {
        val export = NotesParsers.exported(result("export"))
        assertEquals(6, export.count)
        assertTrue(export.bytes > 0)
        assertTrue(NotesTransfer.exportSummary(export).startsWith("已导出 6 篇笔记"))

        val zip = NotesParsers.imported(result("import_zip"))
        assertEquals(6, zip.imported)
        assertTrue(zip.skipped.isEmpty())
        assertTrue(zip.noteIds.isNotEmpty())
        assertEquals("导入了 6 篇", NotesTransfer.importSummary(zip))
        assertEquals(1, NotesParsers.imported(result("import_md")).imported)

        val binary = NotesParsers.imported(result("import_binary"))
        assertEquals(0, binary.imported)
        assertEquals("binary.txt", binary.skipped.single().name)
        assertEquals(listOf("binary.txt：不是文本文件"), NotesTransfer.skipLines(binary.skipped))

        for (step in listOf("import_missing", "import_relative")) assertEquals("INVALID", reply(step).errorCode)
    }

    @Test
    fun attachmentEventParses() {
        assertEquals(CoreEvent.NotesAttachment("a".repeat(64)), CoreEvent.parse("""{"type":"notes.attachment","blob_id":"${"a".repeat(64)}"}"""))
    }

    private data class Span(val start: Int, val end: Int)
}
