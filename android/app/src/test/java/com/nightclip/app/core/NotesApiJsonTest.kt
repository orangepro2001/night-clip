package com.nightclip.app.core

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class NotesApiJsonTest {
    @Test
    fun listParamsOnlyCarryWhatIsSet() {
        val none = NotesApi.listParams(0, null, false)
        assertFalse(none.has("limit") || none.has("cursor") || none.has("deleted"))
        val all = NotesApi.listParams(50, "123:nt_a", true)
        assertEquals(50, all.getInt("limit"))
        assertEquals("123:nt_a", all.getString("cursor"))
        assertTrue(all.getBoolean("deleted"))
        assertFalse(NotesApi.listParams(10, "", false).has("cursor"))
    }

    @Test
    fun idParamsLeaveOutAnEmptyRevision() {
        assertFalse(NotesApi.idParams("nt_a", null).has("rev_id"))
        assertFalse(NotesApi.idParams("nt_a", "").has("rev_id"))
        assertEquals("r_1", NotesApi.idParams("nt_a", "r_1").getString("rev_id"))
        assertEquals("nt_a", NotesApi.idParams("nt_a", null).getString("note_id"))
    }

    @Test
    fun updateSendsOnlyChangedFields() {
        val titleOnly = NotesApi.updateParams("nt_a", "r_1", "New", null)
        assertEquals("New", titleOnly.getString("title"))
        assertFalse(titleOnly.has("body")) // absent keeps the base revision's text
        assertEquals("r_1", titleOnly.getString("base_rev_id"))

        val bodyOnly = NotesApi.updateParams("nt_a", "r_1", null, "text\n")
        assertFalse(bodyOnly.has("title"))
        assertEquals("text\n", bodyOnly.getString("body"))
    }

    @Test
    fun anEmptyTitleOrBodyIsSentNotLeftOut() {
        // "" means "make it empty"; only null means "unchanged".
        val p = NotesApi.updateParams("nt_a", "r_1", "", "")
        assertTrue(p.has("title"))
        assertTrue(p.has("body"))
        assertEquals("", p.getString("body"))
    }

    @Test
    fun updateWithoutBaseOmitsIt() {
        assertFalse(NotesApi.updateParams("nt_a", "", "t", null).has("base_rev_id"))
    }

    @Test
    fun createAndResolveParams() {
        val c = NotesApi.createParams("T", "B")
        assertEquals("T", c.getString("title"))
        assertEquals("B", c.getString("body"))
        val r = NotesApi.resolveParams("nt_a", "nt_c", NotesApi.MERGE)
        assertEquals("nt_a", r.getString("note_id"))
        assertEquals("nt_c", r.getString("conflict_note_id"))
        assertEquals("merge", r.getString("action"))
        assertEquals("keep_main", NotesApi.KEEP_MAIN)
        assertEquals("keep_conflict", NotesApi.KEEP_CONFLICT)
    }

    @Test
    fun cjkAndEmojiSurviveTheJsonEncoding() {
        val text = "标题 😀 \"引号\"\n第二行\t制表"
        val p = org.json.JSONObject(NotesApi.updateParams("nt_a", "r_1", null, text).toString())
        assertEquals(text, p.getString("body"))
    }
}
