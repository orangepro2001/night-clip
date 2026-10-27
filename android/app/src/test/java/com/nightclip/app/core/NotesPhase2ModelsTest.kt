package com.nightclip.app.core

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class NotesPhase2ModelsTest {
    private fun obj(s: String) = JSONObject(s)

    @Test
    fun updateResultCarriesChangedAndDefaultsToTrueWhenAbsent() {
        assertEquals(UpdateOutcome("r_1", false, null, true), NotesParsers.updated(obj("""{"rev_id":"r_1","changed":true,"merged":false}""")))
        assertEquals(UpdateOutcome("r_1", false, null, false), NotesParsers.updated(obj("""{"rev_id":"r_1","changed":false,"merged":false}""")))
        assertTrue("an older core without the field", NotesParsers.updated(obj("""{"rev_id":"r_1","merged":false}""")).changed)
        assertTrue(NotesParsers.updated(obj("""{"rev_id":"r_1","changed":null}""")).changed)
    }

    @Test
    fun searchPage() {
        val p = NotesParsers.search(
            obj(
                """{"items":[
                {"note_id":"nt_a","title":"😀 emoji","updated_at":1790000000123,"snippet":"…emoji…","highlights":[{"start":1,"end":6}],"title_highlights":[{"start":2,"end":7}]},
                {"note_id":"nt_b","title":"","updated_at":5,"snippet":"x","conflict_of":"nt_a"},{"title":"no id"}],"next_cursor":"2"}""",
            ),
        )
        assertEquals(listOf("nt_a", "nt_b"), p.items.map { it.noteId })
        assertEquals("2", p.nextCursor)
        assertEquals(listOf(Highlight(1, 6)), p.items[0].highlights)
        assertEquals(listOf(Highlight(2, 7)), p.items[0].titleHighlights)
        assertEquals(1790000000123L, p.items[0].updatedAt)
        assertTrue(p.items[1].highlights.isEmpty()) // omitted
        assertTrue(p.items[1].isConflictCopy)
    }

    @Test
    fun searchToleratesNullsAndNoise() {
        val p = NotesParsers.search(obj("""{"items":[{"note_id":"nt_a","title":null,"snippet":null,"highlights":null,"title_highlights":[{"start":3,"end":3},{"start":-1,"end":4},{"start":4,"end":2},{"start":1,"end":2},"junk"]}],"extra":1}"""))
        val hit = p.items.single()
        assertEquals("", hit.title)
        assertEquals("", hit.snippet)
        assertTrue(hit.highlights.isEmpty())
        assertEquals(listOf(Highlight(1, 2)), hit.titleHighlights) // empty, negative and reversed ranges are dropped
        assertNull(p.nextCursor)
        assertTrue(NotesParsers.search(obj("{}")).items.isEmpty())
    }

    @Test
    fun attachmentInfo() {
        val a = NotesParsers.attachment(obj("""{"blob_id":"${"ab".repeat(32)}","name":"报告.pdf","mime":"application/pdf","size":123456,"present":true,"markdown":"[报告.pdf](blob:x)"}"""))
        assertEquals("报告.pdf", a.name)
        assertEquals(123456L, a.size)
        assertTrue(a.present)
        assertEquals("[报告.pdf](blob:x)", a.markdown)
    }

    @Test
    fun anAttachmentThatHasNotArrivedHasNothingButItsId() {
        val a = NotesParsers.attachment(obj("""{"blob_id":"${"0".repeat(64)}","name":"","mime":"","size":0,"present":false}"""))
        assertFalse(a.present)
        assertEquals("", a.name)
        assertEquals("", a.markdown)
        assertFalse(NotesParsers.attachment(obj("""{"blob_id":"x"}""")).present) // a missing flag is "not there"
    }

    @Test
    fun cleanupExportAndImportResults() {
        assertEquals(CleanupResult(3, 1_234_567L, 9), NotesParsers.cleanup(obj("""{"removed":3,"freed_bytes":1234567,"kept":9}""")))
        assertEquals(CleanupResult(0, 0, 0), NotesParsers.cleanup(obj("{}")))
        assertEquals(ExportResult("/c/x.zip", 4, 2048), NotesParsers.exported(obj("""{"path":"/c/x.zip","count":4,"bytes":2048}""")))
        val i = NotesParsers.imported(obj("""{"imported":2,"skipped":[{"name":"a.md","reason":"not a text file"},{"name":"b"}],"note_ids":["nt_a","nt_b"]}"""))
        assertEquals(2, i.imported)
        assertEquals(listOf(ImportSkip("a.md", "not a text file"), ImportSkip("b", "")), i.skipped)
        assertEquals(listOf("nt_a", "nt_b"), i.noteIds)
        val none = NotesParsers.imported(obj("""{"imported":0,"skipped":null,"note_ids":null}"""))
        assertTrue(none.skipped.isEmpty() && none.noteIds.isEmpty())
    }

    @Test
    fun theLiveRenderHasNoNoteOrRevision() {
        val r = NotesParsers.rendered(obj("""{"blocks":[{"block_id":"b_1","kind":"paragraph","inline":[{"t":"text","text":"x"}]}],"toc":[]}"""))
        assertEquals("", r.noteId)
        assertEquals("", r.revId)
        assertEquals(1, r.blocks.size)
    }

    // ---- request bodies --------------------------------------------------------------------------

    @Test
    fun searchParams() {
        val p = NotesApi.searchParams("emoji 笔记", 30, null)
        assertEquals("emoji 笔记", p.getString("query"))
        assertEquals(30, p.getInt("limit"))
        assertFalse(p.has("cursor"))
        assertEquals("2", NotesApi.searchParams("q", 0, "2").getString("cursor"))
        assertFalse(NotesApi.searchParams("q", 0, "").has("limit"))
    }

    @Test
    fun exportParams() {
        assertFalse(NotesApi.exportParams("/c/x.zip", false).has("include_trash"))
        val p = NotesApi.exportParams("/c/x.zip", true)
        assertEquals("/c/x.zip", p.getString("path"))
        assertTrue(p.getBoolean("include_trash"))
    }

    @Test
    fun theTextOfASearchSurvivesTheJsonEncoding() {
        val q = "引号\" 反斜杠\\ 😀"
        assertEquals(q, JSONObject(NotesApi.searchParams(q, 5, null).toString()).getString("query"))
    }
}
