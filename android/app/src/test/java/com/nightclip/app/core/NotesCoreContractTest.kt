package com.nightclip.app.core

import com.nightclip.app.logic.EditorCommand
import com.nightclip.app.logic.FlatBlock
import com.nightclip.app.logic.NoteEditorLogic
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
            "search" to "NOT_IMPLEMENTED",
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
        assertEquals("Hello em strong del code link [图片：pic] 😀 soft line\nhard line", t) // soft break = space, hard break = newline
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
}
