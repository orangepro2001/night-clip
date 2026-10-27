package com.nightclip.app.core

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class NotesModelsTest {
    private fun obj(s: String) = JSONObject(s)

    // ---- lists -------------------------------------------------------------------------------

    @Test
    fun listPageParsesRowsAndCursor() {
        val p = NotesParsers.page(
            obj(
                """{"items":[
                {"note_id":"nt_a","title":"Plan","updated_at":1790000000123,"deleted":false,"preview":"first line"},
                {"note_id":"nt_b","title":"Copy","updated_at":5,"deleted":true,"preview":"","conflict_of":"nt_a"}],
                "next_cursor":"1790000000123:nt_a"}""",
            ),
        )
        assertEquals(2, p.items.size)
        assertEquals("Plan", p.items[0].title)
        assertEquals(1790000000123L, p.items[0].updatedAt) // milliseconds survive as a Long
        assertFalse(p.items[0].isConflictCopy)
        assertTrue(p.items[1].deleted)
        assertTrue(p.items[1].isConflictCopy)
        assertEquals("nt_a", p.items[1].conflictOf)
        assertEquals("1790000000123:nt_a", p.nextCursor)
    }

    @Test
    fun listPageToleratesMissingAndExtraFields() {
        val p = NotesParsers.page(obj("""{"items":[{"note_id":"nt_a","surprise":{"x":1}},{"title":"no id"},{"note_id":"nt_c"}],"extra":true}"""))
        assertEquals(listOf("nt_a", "nt_c"), p.items.map { it.noteId }) // a row without an id is dropped
        assertEquals("", p.items[0].title)
        assertEquals(0L, p.items[0].updatedAt)
        assertNull(p.nextCursor)
        assertTrue(NotesParsers.page(obj("{}")).items.isEmpty())
        assertTrue(NotesParsers.page(obj("""{"items":null}""")).items.isEmpty())
    }

    @Test
    fun jsonNullIsNeverTheTextNull() {
        val s = NotesParsers.summary(obj("""{"note_id":"nt_a","title":null,"preview":null,"conflict_of":null,"updated_at":null,"deleted":null}"""))
        assertEquals("", s.title)
        assertEquals("", s.preview)
        assertEquals("", s.conflictOf)
        assertEquals(0L, s.updatedAt)
        assertFalse(s.deleted)
        assertNull(NotesParsers.page(obj("""{"items":[],"next_cursor":null}""")).nextCursor)
    }

    // ---- one note ----------------------------------------------------------------------------

    @Test
    fun docParsesEveryField() {
        val d = NotesParsers.doc(
            obj(
                """{"note_id":"nt_a","title":"T","body":"line\n","format_version":1,"rev_id":"r_1","parents":["r_0"],
                "created_at":1000,"updated_at":2000,"deleted":false,"origin_device":"dev_x","rev_count":3,"is_head":false,"conflict_of":"nt_o"}""",
            ),
        )
        assertEquals("line\n", d.body)
        assertEquals("r_1", d.revId)
        assertEquals(listOf("r_0"), d.parents)
        assertEquals(1000L, d.createdAt)
        assertEquals(2000L, d.updatedAt)
        assertEquals(3, d.revCount)
        assertFalse(d.isHead)
        assertEquals("nt_o", d.conflictOf)
    }

    @Test
    fun docDefaultsWhenFieldsAreMissing() {
        val d = NotesParsers.doc(obj("""{"note_id":"nt_a","rev_id":"r_1","parents":null}"""))
        assertEquals("", d.title)
        assertEquals("", d.body)
        assertTrue(d.parents.isEmpty())
        assertTrue(d.isHead) // an absent flag must not make the editor think the revision is old
        assertFalse(d.deleted)
    }

    @Test
    fun docNeverPrintsItsText() {
        val d = NotesParsers.doc(obj("""{"note_id":"nt_a","title":"secret title","body":"secret body","rev_id":"r_1"}"""))
        assertFalse(d.toString().contains("secret"))
    }

    @Test
    fun createdAndUpdatedResults() {
        assertEquals(NoteCreated("nt_a", "r_1"), NotesParsers.created(obj("""{"note_id":"nt_a","rev_id":"r_1"}""")))
        assertEquals(UpdateOutcome("r_2", false, null), NotesParsers.updated(obj("""{"rev_id":"r_2","merged":false}""")))
        assertEquals(UpdateOutcome("r_3", true, null), NotesParsers.updated(obj("""{"rev_id":"r_3","merged":true}""")))
        assertEquals(UpdateOutcome("r_4", true, "nt_c"), NotesParsers.updated(obj("""{"rev_id":"r_4","merged":true,"conflict_note_id":"nt_c"}""")))
        assertEquals(UpdateOutcome("r_5", false, null), NotesParsers.updated(obj("""{"rev_id":"r_5"}""")))
    }

    @Test
    fun historyPage() {
        val h = NotesParsers.history(
            obj(
                """{"items":[{"rev_id":"r_2","title":"B","body_hash":"h2","origin_device":"merge","created_at":2000,"parents":["r_1","r_x"],"deleted":false},
                {"rev_id":"r_1","title":"A","body_hash":"h1","origin_device":"dev_a","created_at":1000,"parents":[],"deleted":true},{"title":"orphan"}],"next_cursor":"7"}""",
            ),
        )
        assertEquals(listOf("r_2", "r_1"), h.items.map { it.revId })
        assertEquals(listOf("r_1", "r_x"), h.items[0].parents)
        assertTrue(h.items[1].deleted)
        assertEquals("7", h.nextCursor)
    }

    // ---- sync --------------------------------------------------------------------------------

    @Test
    fun syncStatus() {
        val s = NotesParsers.syncStatus(
            obj(
                """{"peers":[{"peer_id":"dev_pc","name":"PC","connected":true,"state":"syncing","last_sync_at":1790000000000,"behind":3,"conflict_count":2},
                {"peer_id":"dev_old","name":"Old","connected":false,"state":"peer_old","detail":"format 9","last_sync_at":0,"behind":0,"conflict_count":2}],"pending_push":4}""",
            ),
        )
        assertEquals(2, s.peers.size)
        assertEquals("syncing", s.peers[0].state)
        assertEquals(3, s.peers[0].behind)
        assertEquals(1790000000000L, s.peers[0].lastSyncAt)
        assertEquals("format 9", s.peers[1].detail)
        assertEquals(4, s.pendingPush)
    }

    @Test
    fun syncStatusMissingStateIsIdle() {
        val s = NotesParsers.syncStatus(obj("""{"peers":[{"peer_id":"dev_pc"}]}"""))
        assertEquals("idle", s.peers[0].state)
        assertEquals(0, s.pendingPush)
        assertTrue(NotesParsers.syncStatus(obj("""{"peers":null}""")).peers.isEmpty())
    }

    @Test
    fun conflictsKeepOnlyCompletePairs() {
        val c = NotesParsers.conflicts(
            obj("""{"items":[{"note_id":"nt_a","conflict_note_id":"nt_c","title":"X","created_at":9},{"note_id":"nt_a"},{"conflict_note_id":"nt_z"}]}"""),
        )
        assertEquals(listOf(NoteConflict("nt_a", "nt_c", "X", 9)), c.items)
    }

    @Test
    fun normalizeResult() {
        assertEquals(Normalized("a\n", "abc"), NotesParsers.normalized(obj("""{"body_norm":"a\n","body_hash":"abc"}""")))
    }

    // ---- render tree -------------------------------------------------------------------------

    private val tree = """
    {"note_id":"nt_a","rev_id":"r_1","toc":[{"block_id":"b_1","level":2,"text":"Head"}],"blocks":[
      {"block_id":"b_1","kind":"heading","raw":"## Head","level":2,"inline":[{"t":"text","text":"Head"}]},
      {"block_id":"b_2","kind":"paragraph","raw":"x","inline":[
         {"t":"text","text":"a "},{"t":"em","children":[{"t":"text","text":"b"}]},{"t":"strong","children":[{"t":"text","text":"c"}]},
         {"t":"del","children":[{"t":"text","text":"d"}]},{"t":"code","text":"e"},
         {"t":"link","href":"https://x.y","title":"tt","children":[{"t":"text","text":"f"}]},
         {"t":"image","href":"https://x.y/i.png","alt":"pic","title":"ti"},{"t":"br"},{"t":"softbr"}]},
      {"block_id":"b_3","kind":"code","raw":"```go\nx\n```","lang":"go","info":"go title=x","text":"x := 1"},
      {"block_id":"b_4","kind":"quote","raw":"> q","children":[{"block_id":"b_5","kind":"paragraph","inline":[{"t":"text","text":"q"}]}]},
      {"block_id":"b_6","kind":"list","raw":"- a","ordered":true,"start":3,"tight":false,"items":[
         {"checked":null,"blocks":[{"block_id":"b_7","kind":"paragraph","inline":[{"t":"text","text":"one"}]}]},
         {"checked":true,"blocks":[{"block_id":"b_8","kind":"paragraph","inline":[{"t":"text","text":"two"}]}]},
         {"checked":false,"blocks":[]}]},
      {"block_id":"b_9","kind":"table","raw":"|a|","align":["none","left","center","right"],
         "header":[[{"t":"text","text":"H1"}],[{"t":"text","text":"H2"}]],
         "rows":[[[{"t":"text","text":"c1"}],[{"t":"strong","children":[{"t":"text","text":"c2"}]}]]]},
      {"block_id":"b_10","kind":"hr","raw":"---"},
      {"block_id":"b_11","kind":"html","raw":"<b>x</b>","text":"<b>x</b>"},
      {"block_id":"b_12","kind":"unknown","raw":"[^1]: n","text":"[^1]: n"}]}
    """

    @Test
    fun renderTreeEveryBlockKind() {
        val r = NotesParsers.rendered(obj(tree))
        assertEquals("r_1", r.revId)
        assertEquals(listOf(TocEntry("b_1", 2, "Head")), r.toc)
        val b = r.blocks
        assertEquals(9, b.size)

        val h = b[0] as NoteBlock.Heading
        assertEquals(2, h.level)
        assertEquals("b_1", h.blockId)

        val p = (b[1] as NoteBlock.Paragraph).inline
        assertEquals(
            listOf(
                NoteInline.Text("a "), NoteInline.Em(listOf(NoteInline.Text("b"))), NoteInline.Strong(listOf(NoteInline.Text("c"))),
                NoteInline.Del(listOf(NoteInline.Text("d"))), NoteInline.Code("e"),
                NoteInline.Link("https://x.y", "tt", listOf(NoteInline.Text("f"))),
                NoteInline.Image("https://x.y/i.png", "pic", "ti"), NoteInline.HardBreak, NoteInline.SoftBreak,
            ),
            p,
        )

        val code = b[2] as NoteBlock.CodeBlock
        assertEquals("go", code.lang)
        assertEquals("go title=x", code.info)
        assertEquals("x := 1", code.text)

        val q = b[3] as NoteBlock.Quote
        assertEquals(1, q.children.size)
        assertTrue(q.children[0] is NoteBlock.Paragraph)

        val l = b[4] as NoteBlock.ListBlock
        assertTrue(l.ordered)
        assertEquals(3, l.start)
        assertFalse(l.tight)
        assertEquals(listOf(null, true, false), l.items.map { it.checked })
        assertTrue(l.items[2].blocks.isEmpty())

        val t = b[5] as NoteBlock.Table
        assertEquals(listOf(ColumnAlign.NONE, ColumnAlign.LEFT, ColumnAlign.CENTER, ColumnAlign.RIGHT), t.align)
        assertEquals(2, t.header.size)
        assertEquals(1, t.rows.size)
        assertEquals(NoteInline.Strong(listOf(NoteInline.Text("c2"))), t.rows[0][1][0])

        assertTrue(b[6] is NoteBlock.Rule)
        assertEquals("<b>x</b>", (b[7] as NoteBlock.Html).text)
        assertEquals("[^1]: n", (b[8] as NoteBlock.Unknown).text)
    }

    @Test
    fun renderTreeToleratesOmittedFields() {
        val r = NotesParsers.rendered(
            obj(
                """{"blocks":[{"kind":"paragraph"},{"kind":"heading"},{"kind":"list"},{"kind":"table"},{"kind":"code"},{"kind":"quote"},
                {"kind":"html","raw":"<i>"},{"kind":"list","items":[{"blocks":null}]}]}""",
            ),
        )
        assertEquals("", r.noteId)
        assertTrue(r.toc.isEmpty())
        assertTrue((r.blocks[0] as NoteBlock.Paragraph).inline.isEmpty())
        assertEquals(1, (r.blocks[1] as NoteBlock.Heading).level)
        val l = r.blocks[2] as NoteBlock.ListBlock
        assertTrue(l.items.isEmpty())
        assertEquals(1, l.start)
        assertTrue(l.tight)
        val t = r.blocks[3] as NoteBlock.Table
        assertTrue(t.header.isEmpty() && t.rows.isEmpty() && t.align.isEmpty())
        assertEquals("", (r.blocks[4] as NoteBlock.CodeBlock).text)
        assertTrue((r.blocks[5] as NoteBlock.Quote).children.isEmpty())
        assertEquals("<i>", (r.blocks[6] as NoteBlock.Html).text) // text falls back to the raw source
        assertEquals(1, (r.blocks[7] as NoteBlock.ListBlock).items.size)
    }

    @Test
    fun headingLevelIsClamped() {
        assertEquals(6, (NotesParsers.block(obj("""{"kind":"heading","level":9}""")) as NoteBlock.Heading).level)
        assertEquals(1, (NotesParsers.block(obj("""{"kind":"heading","level":0}""")) as NoteBlock.Heading).level)
    }

    @Test
    fun unknownKindsAreKeptAsLiteralText() {
        val b = NotesParsers.block(obj("""{"block_id":"b_1","kind":"mermaid","raw":"graph TD","text":"graph TD"}"""))
        assertEquals(NoteBlock.Unknown("b_1", "graph TD", "mermaid", "graph TD"), b)
        val i = NotesParsers.inlines(JSONObject("""{"a":[{"t":"sparkle","text":"x"},{"t":"sparkle","children":[{"t":"text","text":"y"}]}]}""").getJSONArray("a"), 0)
        assertEquals(NoteInline.Other("sparkle", "x", emptyList()), i[0])
        assertEquals(NoteInline.Other("sparkle", "", listOf(NoteInline.Text("y"))), i[1])
    }

    @Test
    fun absurdNestingDoesNotOverflowTheStack() {
        val depth = 200 // org.json itself refuses documents deeper than 512 levels; 200 quotes are 400
        val sb = StringBuilder()
        repeat(depth) { sb.append("""{"kind":"quote","raw":"> x","children":[""") }
        sb.append("""{"kind":"paragraph","inline":[{"t":"text","text":"deep"}]}""")
        repeat(depth) { sb.append("]}") }
        var b: NoteBlock = NotesParsers.block(JSONObject(sb.toString()))
        var levels = 0
        while (b is NoteBlock.Quote) {
            levels++
            b = b.children.single()
        }
        assertEquals(NotesParsers.MAX_DEPTH, levels)
        assertTrue(b is NoteBlock.Unknown) // the rest is kept as literal text instead of being nested further
    }

    // ---- events ------------------------------------------------------------------------------

    @Test
    fun notesEvents() {
        assertEquals(CoreEvent.NotesChanged("nt_a", "r_1"), CoreEvent.parse("""{"type":"notes.changed","note_id":"nt_a","rev_id":"r_1"}"""))
        assertEquals(CoreEvent.NotesSyncUpdated("dev_a", "syncing", 3), CoreEvent.parse("""{"type":"notes.sync.updated","peer_id":"dev_a","state":"syncing","behind":3}"""))
        assertEquals(
            CoreEvent.NotesSyncConflict("nt_a", "nt_c", "diverged"),
            CoreEvent.parse("""{"type":"notes.sync.conflict","note_id":"nt_a","conflict_note_id":"nt_c","reason":"diverged"}"""),
        )
        assertEquals(
            CoreEvent.NotesSyncConflict("nt_a", "", "deleted_elsewhere"),
            CoreEvent.parse("""{"type":"notes.sync.conflict","note_id":"nt_a","reason":"deleted_elsewhere"}"""),
        )
        assertEquals("notes.changed", CoreEvent.NotesChanged("nt_a", "r").type)
    }
}
