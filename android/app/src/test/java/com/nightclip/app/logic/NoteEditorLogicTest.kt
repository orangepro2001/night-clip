package com.nightclip.app.logic

import com.nightclip.app.core.NoteDoc
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class NoteEditorLogicTest {
    private var clock = 1_000_000L
    private val logic = NoteEditorLogic({ clock })

    private fun doc(
        rev: String = "r_1",
        title: String = "Title",
        body: String = "Body\n",
        id: String = "nt_a",
        deleted: Boolean = false,
    ) = NoteDoc(
        noteId = id, title = title, body = body, formatVersion = 1, revId = rev, parents = emptyList(), createdAt = 1, updatedAt = 2,
        deleted = deleted, originDevice = "dev", revCount = 1, isHead = true,
    )

    /** Opens note nt_a and answers the load. */
    private fun opened(d: NoteDoc = doc()): NoteEditorLogic {
        val cmds = logic.open(d.noteId)
        val load = cmds.single() as EditorCommand.Load
        assertEquals(d.noteId, load.noteId)
        logic.onLoaded(load.ticket, d)
        return logic
    }

    private fun only(cmds: List<EditorCommand>) = cmds.filterIsInstance<EditorCommand.Save>().single()
    private fun saves(cmds: List<EditorCommand>) = cmds.filterIsInstance<EditorCommand.Save>()
    private fun loads(cmds: List<EditorCommand>) = cmds.filterIsInstance<EditorCommand.Load>()
    private fun notices(cmds: List<EditorCommand>) = cmds.filterIsInstance<EditorCommand.Notify>().map { it.notice }

    /** Types [body] and lets the quiet period pass: returns the save that starts. */
    private fun typeAndWait(body: String, title: String = logic.state.title): EditorCommand.Save {
        logic.edit(title, body)
        clock += NoteEditorLogic.DEBOUNCE_MS
        return only(logic.tick())
    }

    // ---- opening -----------------------------------------------------------------------------

    @Test
    fun openLoadsTheNoteAndTheLoadedRevisionIsTheBase() {
        val cmds = logic.open("nt_a")
        assertEquals(EditorPhase.LOADING, logic.state.phase)
        assertTrue(logic.state.readOnly)
        logic.onLoaded((cmds.single() as EditorCommand.Load).ticket, doc(rev = "r_7", title = "T", body = "B\n"))
        val s = logic.state
        assertEquals(EditorPhase.READY, s.phase)
        assertEquals("r_7", s.baseRevId)
        assertEquals("T", s.title)
        assertEquals("B\n", s.body)
        assertFalse(s.dirty)
        assertEquals(SaveStatus.CLEAN, s.status)
        assertNull(logic.nextDueAt())
    }

    @Test
    fun aFailedOpenIsReportedAndKeepsTheEditorLocked() {
        val t = (logic.open("nt_a").single() as EditorCommand.Load).ticket
        logic.onLoadFailed(t, "NOT_FOUND")
        assertEquals(EditorPhase.LOAD_FAILED, logic.state.phase)
        assertEquals("NOT_FOUND", logic.state.loadError)
        logic.edit("x", "y") // ignored: nothing was loaded
        assertFalse(logic.state.dirty)
    }

    @Test
    fun anAnswerForAnEarlierOpenIsIgnored() {
        val first = (logic.open("nt_a").single() as EditorCommand.Load).ticket
        val second = (logic.open("nt_b").single() as EditorCommand.Load).ticket
        logic.onLoaded(first, doc(id = "nt_a", body = "old note"))
        assertEquals(EditorPhase.LOADING, logic.state.phase)
        logic.onLoaded(second, doc(id = "nt_b", body = "new note"))
        assertEquals("new note", logic.state.body)
        assertEquals("nt_b", logic.state.noteId)
    }

    @Test
    fun aNoteInTheTrashOpensReadOnly() {
        opened(doc(deleted = true))
        assertTrue(logic.state.deleted)
        assertTrue(logic.state.readOnly)
        assertEquals(SaveStatus.READ_ONLY, logic.state.status)
        logic.edit("T", "typed")
        assertEquals("Body\n", logic.state.body)
        assertTrue(logic.flush().isEmpty())
    }

    // ---- debounce ----------------------------------------------------------------------------

    @Test
    fun theSaveWaitsForTheQuietPeriodAfterTheLastEdit() {
        opened()
        logic.edit("Title", "a")
        assertEquals(clock + 500, logic.nextDueAt())
        clock += 300
        logic.edit("Title", "ab")
        assertEquals(clock + 500, logic.nextDueAt()) // restarted by the second edit
        clock += 499
        assertTrue(logic.tick().isEmpty())
        clock += 1
        val s = only(logic.tick())
        assertEquals("ab", s.body)
        assertEquals("r_1", s.baseRevId)
        assertEquals("nt_a", s.noteId)
        assertNull(logic.nextDueAt())
        assertTrue(logic.state.saving)
        assertEquals(SaveStatus.SAVING, logic.state.status)
    }

    @Test
    fun anImeCompositionStormMakesOneSave() {
        opened()
        var typed = ""
        repeat(120) { i ->
            typed += "字"
            logic.edit("Title", typed)
            clock += 4 // 120 changes inside half a second
            assertTrue("no save while the composition is still going (step $i)", logic.tick().isEmpty())
        }
        clock += 500
        val s = only(logic.tick())
        assertEquals(120, s.body!!.length)
        assertTrue(logic.tick().isEmpty())
    }

    @Test
    fun onlyChangedFieldsAreSent() {
        opened()
        val titleOnly = typeAndWait(body = "Body\n", title = "New title")
        assertEquals("New title", titleOnly.title)
        assertNull(titleOnly.body)
        logic.onSaved(titleOnly.ticket, "nt_a", "r_2", false, null)

        val bodyOnly = typeAndWait("changed\n")
        assertNull(bodyOnly.title)
        assertEquals("changed\n", bodyOnly.body)
    }

    @Test
    fun typingTheTextBackCancelsThePendingSave() {
        opened()
        logic.edit("Title", "Body\nx")
        assertEquals(SaveStatus.PENDING, logic.state.status)
        logic.edit("Title", "Body\n")
        assertFalse(logic.state.dirty)
        assertNull(logic.nextDueAt())
        clock += 1000
        assertTrue(logic.tick().isEmpty())
    }

    @Test
    fun afterASaveTheReturnedRevisionIsTheNewBase() {
        opened()
        val first = typeAndWait("one\n")
        assertEquals("r_1", first.baseRevId)
        val out = logic.onSaved(first.ticket, "nt_a", "r_2", false, null)
        assertTrue(out.isEmpty())
        assertEquals("r_2", logic.state.baseRevId)
        assertFalse(logic.state.dirty)
        assertEquals(SaveStatus.CLEAN, logic.state.status)

        val second = typeAndWait("two\n")
        assertEquals("r_2", second.baseRevId)
    }

    @Test
    fun textTypedWhileSavingIsSentByTheNextSave() {
        opened()
        val first = typeAndWait("one\n")
        logic.edit("Title", "one\ntwo\n") // typed while the request is out
        clock += 600
        assertTrue("one save at a time", logic.tick().isEmpty())
        assertNull("nothing to schedule while the request is out", logic.nextDueAt())
        logic.onSaved(first.ticket, "nt_a", "r_2", false, null)
        assertTrue(logic.state.dirty) // the second text is not saved yet
        assertEquals(clock - 100, logic.nextDueAt()) // overdue: the host calls tick at once
        val second = only(logic.tick())
        assertEquals("r_2", second.baseRevId)
        assertEquals("one\ntwo\n", second.body)
    }

    @Test
    fun coreNormalisationDoesNotMakeTheNoteDirtyForever() {
        opened()
        val s = typeAndWait("no trailing newline") // the core stores "no trailing newline\n"
        logic.onSaved(s.ticket, "nt_a", "r_2", false, null)
        assertFalse(logic.state.dirty)
        assertEquals("no trailing newline", logic.state.body) // the text under the cursor is not rewritten
    }

    // ---- merge, conflict ---------------------------------------------------------------------

    @Test
    fun aMergedSaveReloadsTheNewestTextAndTellsTheUser() {
        opened()
        val s = typeAndWait("mine\n")
        val out = logic.onSaved(s.ticket, "nt_a", "r_3", true, null)
        val load = loads(out).single()
        assertEquals("r_1", logic.state.baseRevId) // the base moves only when the merged text is loaded
        assertTrue(notices(out).isEmpty())

        val out2 = logic.onLoaded(load.ticket, doc(rev = "r_3", body = "mine\n+ theirs\n"))
        assertEquals("mine\n+ theirs\n", logic.state.body)
        assertEquals("r_3", logic.state.baseRevId)
        assertEquals(listOf<EditorNotice>(EditorNotice.Merged), notices(out2))
        assertFalse(logic.state.dirty)
    }

    @Test
    fun aConflictCopyReloadsAndNamesTheCopy() {
        opened()
        val s = typeAndWait("mine\n")
        val out = logic.onSaved(s.ticket, "nt_a", "r_4", true, "nt_copy")
        assertEquals(listOf<EditorNotice>(EditorNotice.ConflictCopy("nt_copy")), notices(out))
        assertEquals(1, loads(out).size)
        logic.onLoaded(loads(out).single().ticket, doc(rev = "r_4", body = "theirs\n"))
        assertEquals("theirs\n", logic.state.body) // the newest text; "mine" is in the copy
    }

    @Test
    fun mergedWhileTypingKeepsTheTypedTextAndOldBase() {
        opened()
        val s = typeAndWait("mine\n")
        logic.edit("Title", "mine\nmore") // typed while the request was out
        val out = logic.onSaved(s.ticket, "nt_a", "r_3", true, null)
        assertTrue("no reload while there is unsaved text", loads(out).isEmpty())
        assertEquals("mine\nmore", logic.state.body)
        assertEquals("r_1", logic.state.baseRevId)
        assertTrue(logic.state.dirty)

        // The next save is based on the old revision, so the core merges it with the newest one.
        clock += 500
        val next = only(logic.tick())
        assertEquals("r_1", next.baseRevId)
        assertEquals("mine\nmore", next.body)

        // Once that save is done and nothing more was typed, the merged text is loaded and the user is told.
        val after = logic.onSaved(next.ticket, "nt_a", "r_5", true, null)
        val load = loads(after).single()
        val told = logic.onLoaded(load.ticket, doc(rev = "r_5", body = "everything\n"))
        assertEquals("everything\n", logic.state.body)
        assertEquals(listOf<EditorNotice>(EditorNotice.Merged), notices(told))
    }

    @Test
    fun aReloadNeverReplacesTextTypedSinceTheRequest() {
        opened()
        val s = typeAndWait("mine\n")
        val load = loads(logic.onSaved(s.ticket, "nt_a", "r_3", true, null)).single()
        logic.edit("Title", "mine\nand then I kept typing") // before the reload answer arrives
        logic.onLoaded(load.ticket, doc(rev = "r_3", body = "merged\n"))
        assertEquals("mine\nand then I kept typing", logic.state.body)
        assertTrue(logic.state.dirty)
        assertEquals("r_1", logic.state.baseRevId)
    }

    // ---- changes made elsewhere --------------------------------------------------------------

    @Test
    fun anEventForANewRevisionWithCleanTextReloadsSilently() {
        opened()
        val out = logic.onExternalChange("nt_a", "r_9")
        val load = loads(out).single()
        assertTrue(notices(out).isEmpty())
        val told = logic.onLoaded(load.ticket, doc(rev = "r_9", body = "from the PC\n"))
        assertTrue(told.isEmpty()) // silent
        assertEquals("from the PC\n", logic.state.body)
        assertEquals("r_9", logic.state.baseRevId)
    }

    @Test
    fun theEventOfOurOwnSaveIsIgnored() {
        opened()
        assertTrue(logic.onExternalChange("nt_a", "r_1").isEmpty()) // the loaded base: nothing new
        val s = typeAndWait("x\n")
        logic.onSaved(s.ticket, "nt_a", "r_2", false, null)
        assertTrue(logic.onExternalChange("nt_a", "r_2").isEmpty())
    }

    @Test
    fun anEventForAnotherNoteIsIgnored() {
        opened()
        assertTrue(logic.onExternalChange("nt_other", "r_9").isEmpty())
    }

    @Test
    fun anEventWhileTypingKeepsTypingAndLeavesTheMergeToTheNextSave() {
        opened()
        logic.edit("Title", "typing…")
        assertTrue(logic.onExternalChange("nt_a", "r_9").isEmpty())
        assertEquals("typing…", logic.state.body)
        clock += 500
        val s = only(logic.tick())
        assertEquals("r_1", s.baseRevId) // merged by the core through the old base
    }

    @Test
    fun aSaveThatChangedNothingMustNotAdoptTheOtherDevicesRevisionAsItsBase() {
        opened()
        // Only white space changes: the core normalises it away, makes no revision and answers with the newest one.
        logic.edit("Title", "Body\n   ")
        clock += 500
        val s = only(logic.tick())
        val out = logic.onSaved(s.ticket, "nt_a", "r_9", merged = false, conflictNoteId = null, changed = false) // r_9 is not ours
        assertEquals("r_1", logic.state.baseRevId) // the editor still shows r_1's content
        val load = loads(out).single()
        assertTrue("a silent reload: nothing was lost", notices(out).isEmpty())
        logic.onLoaded(load.ticket, doc(rev = "r_9", body = "from the PC\n"))
        assertEquals("r_9", logic.state.baseRevId)
        assertEquals("from the PC\n", logic.state.body)
    }

    @Test
    fun aSaveThatChangedNothingOnTheSameBaseChangesNothing() {
        opened()
        logic.edit("Title", "Body\n   ")
        val s = only(logic.flush())
        val out = logic.onSaved(s.ticket, "nt_a", "r_1", merged = false, conflictNoteId = null, changed = false)
        assertTrue(out.isEmpty())
        assertEquals("r_1", logic.state.baseRevId)
        assertFalse(logic.state.dirty)
    }

    @Test
    fun anEventWithUnsavedTextAfterAFailedSaveDoesNotDiscardIt() {
        opened()
        val s = typeAndWait("precious\n")
        logic.onSaveFailed(s.ticket, "INTERNAL")
        assertTrue(logic.onExternalChange("nt_a", "r_9").isEmpty())
        assertEquals("precious\n", logic.state.body)
    }

    @Test
    fun anEventDuringASaveIsDecidedWhenTheAnswerArrives() {
        opened()
        val s = typeAndWait("mine\n")
        assertTrue(logic.onExternalChange("nt_a", "r_2").isEmpty()) // our own revision, event faster than the answer
        val out = logic.onSaved(s.ticket, "nt_a", "r_2", false, null)
        assertTrue("the event was our own save", out.isEmpty())
        assertEquals("r_2", logic.state.baseRevId)
    }

    @Test
    fun aForeignEventDuringASaveReloadsAfterIt() {
        opened()
        val s = typeAndWait("mine\n")
        assertTrue(logic.onExternalChange("nt_a", "r_8").isEmpty())
        val out = logic.onSaved(s.ticket, "nt_a", "r_2", false, null)
        assertEquals(1, loads(out).size)
    }

    @Test
    fun aRemoteDeletionMakesTheNoteReadOnly() {
        opened()
        val load = loads(logic.onExternalChange("nt_a", "r_tomb")).single()
        logic.onLoaded(load.ticket, doc(rev = "r_tomb", deleted = true))
        assertTrue(logic.state.deleted)
        assertTrue(logic.state.readOnly)
    }

    @Test
    fun aFailedReloadKeepsTheTextOnScreen() {
        opened()
        val load = loads(logic.onExternalChange("nt_a", "r_9")).single()
        logic.onLoadFailed(load.ticket, "INTERNAL")
        assertEquals(EditorPhase.READY, logic.state.phase)
        assertEquals("Body\n", logic.state.body)
    }

    // ---- leaving -----------------------------------------------------------------------------

    @Test
    fun flushSavesAtOnceWithoutWaiting() {
        opened()
        logic.edit("Title", "typed\n")
        val s = only(logic.flush())
        assertEquals("typed\n", s.body)
        assertNull(logic.nextDueAt())
    }

    @Test
    fun flushOfACleanNoteDoesNothing() {
        opened()
        assertTrue(logic.flush().isEmpty())
    }

    @Test
    fun flushDuringASaveSavesTheRestRightAfter() {
        opened()
        val first = typeAndWait("one\n")
        logic.edit("Title", "one\ntwo\n")
        assertTrue(logic.flush().isEmpty()) // one at a time
        val out = logic.onSaved(first.ticket, "nt_a", "r_2", false, null)
        val second = only(out) // the leave does not have to wait for another quiet period
        assertEquals("one\ntwo\n", second.body)
        assertEquals("r_2", second.baseRevId)
    }

    @Test
    fun noTextIsLostIfTheSaveFails() {
        opened()
        val s = typeAndWait("important\n")
        val out = logic.onSaveFailed(s.ticket, "INTERNAL")
        assertTrue(out.isEmpty())
        val st = logic.state
        assertEquals("important\n", st.body)
        assertTrue(st.dirty)
        assertTrue(st.hasUnsavedText)
        assertEquals("INTERNAL", st.saveError)
        assertEquals(SaveStatus.FAILED, st.status)
        assertNull("no automatic retry loop", logic.nextDueAt())
        assertEquals("r_1", st.baseRevId)
        // Leaving tries once more.
        assertEquals("important\n", only(logic.flush()).body)
    }

    @Test
    fun aFailedSaveIsRetriedWhenTheUserTypesAgain() {
        opened()
        val s = typeAndWait("a\n")
        logic.onSaveFailed(s.ticket, "TOO_LARGE")
        logic.edit("Title", "ab\n")
        assertEquals(clock + 500, logic.nextDueAt())
        clock += 500
        val retry = only(logic.tick())
        assertEquals("ab\n", retry.body)
        assertEquals(SaveStatus.SAVING, logic.state.status) // not "failed" while the retry is out
        assertTrue(logic.onSaved(retry.ticket + 99, "nt_a", "r_2", false, null).isEmpty()) // an answer for another request
        assertTrue(logic.state.saving)
        logic.onSaved(retry.ticket, "nt_a", "r_2", false, null)
        assertEquals(SaveStatus.CLEAN, logic.state.status)
    }

    @Test
    fun aSuccessfulSaveClearsTheError() {
        opened()
        val s = typeAndWait("a\n")
        logic.onSaveFailed(s.ticket, "INTERNAL")
        val retry = only(logic.flush())
        logic.onSaved(retry.ticket, "nt_a", "r_2", false, null)
        assertNull(logic.state.saveError)
        assertEquals(SaveStatus.CLEAN, logic.state.status)
    }

    @Test
    fun aNoteThatVanishedIsSavedAsANewNote() {
        opened()
        logic.edit("Title", "my words")
        val s = only(logic.flush())
        val out = logic.onSaveFailed(s.ticket, "NOT_FOUND")
        assertEquals(listOf<EditorNotice>(EditorNotice.SavedAsNew), notices(out))
        val create = only(out)
        assertNull(create.noteId)
        assertEquals("Title", create.title)
        assertEquals("my words", create.body)
        logic.onSaved(create.ticket, "nt_new", "r_n", false, null)
        assertEquals("nt_new", logic.state.noteId)
        assertEquals("r_n", logic.state.baseRevId)
        assertFalse(logic.state.dirty)
    }

    @Test
    fun aNoteTrashedElsewhereTurnsReadOnlyButKeepsTheText() {
        opened()
        val s = typeAndWait("unsaved\n")
        val out = logic.onSaveFailed(s.ticket, "CONFLICT")
        assertEquals(listOf<EditorNotice>(EditorNotice.Trashed), notices(out))
        assertTrue(logic.state.readOnly)
        assertEquals("unsaved\n", logic.state.body)
        assertTrue(logic.state.dirty)

        // After the user restores it the unsaved text is saved.
        assertTrue(logic.onRestored().isEmpty())
        assertFalse(logic.state.deleted)
        clock += 500
        assertEquals("unsaved\n", only(logic.tick()).body)
    }

    @Test
    fun restoringACleanNoteLoadsItAgain() {
        opened(doc(deleted = true))
        val load = loads(logic.onRestored()).single()
        logic.onLoaded(load.ticket, doc(rev = "r_back", deleted = false))
        assertFalse(logic.state.readOnly)
        assertEquals("r_back", logic.state.baseRevId)
    }

    // ---- new notes ---------------------------------------------------------------------------

    @Test
    fun anEmptyDraftIsNeverCreated() {
        logic.openDraft()
        assertEquals(EditorPhase.READY, logic.state.phase)
        assertFalse(logic.state.dirty)
        assertTrue(logic.flush().isEmpty())
        logic.edit("  ", "\n\n")
        assertFalse(logic.state.dirty)
        assertTrue(logic.flush().isEmpty())
    }

    @Test
    fun aDraftIsCreatedByTheFirstSaveAndThenUpdated() {
        logic.openDraft()
        logic.edit("", "hello")
        clock += 500
        val create = only(logic.tick())
        assertNull(create.noteId)
        assertEquals("", create.baseRevId)
        assertEquals("hello", create.body)
        assertEquals("", create.title)

        logic.edit("", "hello world") // typed while the create is out
        logic.onSaved(create.ticket, "nt_new", "r_1", false, null)
        assertEquals("nt_new", logic.state.noteId)
        assertEquals("r_1", logic.state.baseRevId)
        assertTrue(logic.state.dirty)
        clock += 500
        val update = only(logic.tick())
        assertEquals("nt_new", update.noteId)
        assertEquals("r_1", update.baseRevId)
        assertEquals("hello world", update.body)
        assertNull(update.title)
    }

    @Test
    fun aDraftWithInitialTextIsSavedAfterTheQuietPeriod() {
        logic.openDraft("Shared title", "https://example.com")
        assertTrue(logic.state.dirty)
        clock += 500
        val create = only(logic.tick())
        assertEquals("Shared title", create.title)
        assertEquals("https://example.com", create.body)
    }

    @Test
    fun aFailedCreateKeepsTheDraft() {
        logic.openDraft()
        logic.edit("T", "words")
        val create = only(logic.flush())
        logic.onSaveFailed(create.ticket, "INTERNAL")
        assertNull(logic.state.noteId)
        assertTrue(logic.state.hasUnsavedText)
        assertEquals("words", logic.state.body)
        assertEquals("words", only(logic.flush()).body)
    }

    // ---- settled -----------------------------------------------------------------------------

    @Test
    fun settledMeansNothingIsOnItsWay() {
        opened()
        assertTrue(logic.state.settled)
        logic.edit("Title", "typing")
        assertFalse("waiting for the quiet period", logic.state.settled)
        val s = only(logic.flush())
        assertFalse("request out", logic.state.settled)
        logic.onSaveFailed(s.ticket, "INTERNAL")
        assertTrue("failed for now: nothing more will happen by itself", logic.state.settled)
        val retry = only(logic.flush())
        logic.onSaved(retry.ticket, "nt_a", "r_2", true, null) // merged: the reload is on its way
        assertTrue(logic.state.loading)
        assertFalse(logic.state.settled)
    }

    // ---- misc --------------------------------------------------------------------------------

    @Test
    fun closeForgetsEverythingAndLateAnswersAreIgnored() {
        opened()
        val s = typeAndWait("x\n")
        logic.close()
        assertEquals(EditorPhase.IDLE, logic.state.phase)
        assertTrue(logic.onSaved(s.ticket, "nt_a", "r_2", false, null).isEmpty())
        assertEquals("", logic.state.baseRevId)
        assertTrue(logic.onExternalChange("nt_a", "r_9").isEmpty())
    }

    @Test
    fun cjkAndEmojiTextIsKeptByteForByte() {
        opened()
        val text = "标题测试 😀 한국어\n- 项目\n"
        val s = typeAndWait(text)
        assertEquals(text, s.body)
        logic.onSaved(s.ticket, "nt_a", "r_2", false, null)
        assertEquals(text, logic.state.body)
    }
}
