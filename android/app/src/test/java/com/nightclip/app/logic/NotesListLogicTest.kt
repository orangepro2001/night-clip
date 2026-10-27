package com.nightclip.app.logic

import com.nightclip.app.core.CoreEvent
import com.nightclip.app.core.NoteSummary
import com.nightclip.app.core.NotesPage
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class NotesListLogicTest {
    private fun n(id: String, at: Long, title: String = id, deleted: Boolean = false, preview: String = "", copyOf: String = "") =
        NoteSummary(id, title, at, deleted, preview, copyOf)

    private val empty = NotesListState()

    // ---- paging ------------------------------------------------------------------------------

    @Test
    fun theFirstPageReplacesTheListAndRemembersTheCursor() {
        val s = NotesListLogic.firstPage(empty, NotesPage(listOf(n("a", 3), n("b", 2)), "2:b"))
        assertEquals(listOf("a", "b"), s.items.map { it.noteId })
        assertEquals("2:b", s.nextCursor)
        assertTrue(s.canLoadMore)
        assertTrue(s.loaded)
        val again = NotesListLogic.firstPage(s, NotesPage(listOf(n("c", 9)), null))
        assertEquals(listOf("c"), again.items.map { it.noteId })
        assertFalse(again.canLoadMore)
    }

    @Test
    fun nothingIsLoadedUntilTheFirstPageArrives() {
        assertFalse(empty.loaded)
        assertTrue(NotesListLogic.firstPage(empty, NotesPage(emptyList(), null)).loaded) // an empty list is then "no notes"
    }

    @Test
    fun followingPagesAreAppendedWithoutDuplicates() {
        val s1 = NotesListLogic.firstPage(empty, NotesPage(listOf(n("a", 5), n("b", 4)), "4:b"))
        // The list moved while the user scrolled: "b" came back in the second page.
        val s2 = NotesListLogic.nextPage(s1, NotesPage(listOf(n("b", 4), n("c", 3)), null))
        assertEquals(listOf("a", "b", "c"), s2.items.map { it.noteId })
        assertNull(s2.nextCursor)
        assertFalse(s2.canLoadMore)
    }

    @Test
    fun aDuplicateInsideOnePageIsShownOnce() {
        val s = NotesListLogic.firstPage(empty, NotesPage(listOf(n("a", 5), n("a", 5)), null))
        assertEquals(1, s.items.size)
    }

    @Test
    fun reloadAsksForAsManyRowsAsAreLoaded() {
        assertEquals(50, NotesListLogic.reloadLimit(empty))
        val big = empty.copy(items = (1..120).map { n("n$it", it.toLong()) })
        assertEquals(120, NotesListLogic.reloadLimit(big))
        val huge = empty.copy(items = (1..900).map { n("n$it", it.toLong()) })
        assertEquals(500, NotesListLogic.reloadLimit(huge)) // the core's maximum page
    }

    // ---- live notes and trash ----------------------------------------------------------------

    @Test
    fun switchingToTheTrashStartsWithAnEmptyList() {
        val live = NotesListLogic.firstPage(empty, NotesPage(listOf(n("a", 1)), "1:a"))
        val trash = NotesListLogic.switchMode(live, true)
        assertTrue(trash.trash)
        assertTrue(trash.items.isEmpty())
        assertFalse(trash.loaded)
        assertNull(trash.nextCursor)
        assertEquals(live, NotesListLogic.switchMode(live, false)) // same mode: nothing changes
    }

    @Test
    fun aListOnlyShowsRowsOfItsOwnMode() {
        val live = NotesListLogic.firstPage(empty, NotesPage(listOf(n("a", 2), n("t", 1, deleted = true)), null))
        assertEquals(listOf("a"), live.items.map { it.noteId })
        val trash = NotesListLogic.firstPage(NotesListState(trash = true), NotesPage(listOf(n("a", 2), n("t", 1, deleted = true)), null))
        assertEquals(listOf("t"), trash.items.map { it.noteId })
        val more = NotesListLogic.nextPage(live, NotesPage(listOf(n("x", 0, deleted = true), n("y", 0)), null))
        assertEquals(listOf("a", "y"), more.items.map { it.noteId })
    }

    @Test
    fun removeAndUndoPutTheRowBackInOrder() {
        val s = NotesListLogic.firstPage(empty, NotesPage(listOf(n("a", 30), n("b", 20), n("c", 10)), null))
        val gone = NotesListLogic.remove(s, "b")
        assertEquals(listOf("a", "c"), gone.items.map { it.noteId })
        val back = NotesListLogic.insert(gone, n("b", 20))
        assertEquals(listOf("a", "b", "c"), back.items.map { it.noteId })
        assertEquals(back, NotesListLogic.insert(back, n("b", 20))) // not twice
        assertEquals(listOf("z", "a", "b", "c"), NotesListLogic.insert(back, n("z", 99)).items.map { it.noteId })
        assertEquals(listOf("a", "b", "c", "e"), NotesListLogic.insert(back, n("e", 1)).items.map { it.noteId })
    }

    // ---- events ------------------------------------------------------------------------------

    @Test
    fun onlyNotesChangedAffectsTheList() {
        assertTrue(NotesListLogic.affectsList(CoreEvent.NotesChanged("nt_a", "r_1")))
        assertFalse(NotesListLogic.affectsList(CoreEvent.NotesSyncUpdated("p", "idle", 0)))
        assertFalse(NotesListLogic.affectsList(CoreEvent.NotesSyncConflict("nt_a", "nt_c", "diverged")))
        assertFalse(NotesListLogic.affectsList(CoreEvent.PeerState("p", "connected")))
    }

    // ---- rows --------------------------------------------------------------------------------

    @Test
    fun titleFallsBackToUntitled() {
        assertEquals("Plan", NotesListLogic.title(n("a", 1, title = "  Plan ")))
        assertEquals(NotesTexts.UNTITLED, NotesListLogic.title(n("a", 1, title = "   ")))
    }

    @Test
    fun previewIsOneLineAndDoesNotRepeatTheTitle() {
        assertEquals("first second third", NotesListLogic.previewLine(n("a", 1, title = "T", preview = "first\n  second\n\nthird")))
        assertEquals("", NotesListLogic.previewLine(n("a", 1, title = "Same line", preview = "Same  line")))
        assertEquals("", NotesListLogic.previewLine(n("a", 1, title = "T", preview = " \n ")))
        assertEquals("Same line more", NotesListLogic.previewLine(n("a", 1, title = "Same line", preview = "Same line more")))
    }

    @Test
    fun conflictBadgeOnlyForLiveCopies() {
        assertTrue(NotesListLogic.showsConflictBadge(n("c", 1, copyOf = "a")))
        assertFalse(NotesListLogic.showsConflictBadge(n("c", 1, copyOf = "a", deleted = true)))
        assertFalse(NotesListLogic.showsConflictBadge(n("a", 1)))
    }

    @Test
    fun updatedLabelUsesMillisecondsAndSwitchesToADate() {
        val now = 1_790_000_000_000L
        assertEquals("刚刚", NotesListLogic.updatedLabel(n("a", now - 5_000), now))
        assertEquals("3 分钟前", NotesListLogic.updatedLabel(n("a", now - 3 * 60_000), now))
        assertEquals("2 小时前", NotesListLogic.updatedLabel(n("a", now - 2 * 3_600_000), now))
        assertEquals("3 天前", NotesListLogic.updatedLabel(n("a", now - 3 * 86_400_000L), now))
        assertEquals("从未", NotesListLogic.updatedLabel(n("a", 0), now))
    }
}
