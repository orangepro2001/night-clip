package com.nightclip.app.logic

import com.nightclip.app.core.ErrorMessages
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.ZoneId

class NotesErrorsTest {
    private val codes = listOf("NOT_FOUND", "CONFLICT", "TOO_LARGE", "UNSUPPORTED_FORMAT", "NOT_IMPLEMENTED", "NOT_PAIRED")

    @Test
    fun everyNotesCodeHasItsOwnMessage() {
        val seen = HashSet<String>()
        for (code in codes) {
            val m = NotesErrors.message(code)
            assertFalse("$code has no specific text", m.contains("未知错误"))
            assertFalse("$code leaks the code", m.contains(code))
            seen += m
        }
        assertEquals(codes.size, seen.size)
    }

    @Test
    fun conflictMeansTheNoteIsInTheTrash() {
        assertTrue(NotesErrors.message("CONFLICT").contains("回收站"))
        assertTrue(NotesErrors.message("CONFLICT", NotesErrors.Context.RESOLVE).contains("回收站"))
    }

    @Test
    fun contextChangesTheWording() {
        assertNotEquals(NotesErrors.message("NOT_FOUND"), NotesErrors.message("NOT_FOUND", NotesErrors.Context.RESOLVE))
        assertNotEquals(NotesErrors.message("NOT_PAIRED"), NotesErrors.message("NOT_PAIRED", NotesErrors.Context.SYNC))
    }

    @Test
    fun tooLargeNamesTheLimits() {
        val m = NotesErrors.message("TOO_LARGE")
        assertTrue(m.contains("1 MiB"))
        assertTrue(m.contains("1 KiB"))
    }

    @Test
    fun unsupportedFormatAsksForAnUpdate() {
        assertTrue(NotesErrors.message("UNSUPPORTED_FORMAT").contains("更新"))
    }

    @Test
    fun otherCodesFallBackToTheGeneralTexts() {
        assertEquals(ErrorMessages.forCode("INTERNAL"), NotesErrors.message("INTERNAL"))
        assertEquals(ErrorMessages.forCode("NOT_INITIALIZED"), NotesErrors.message("NOT_INITIALIZED"))
        assertEquals(ErrorMessages.forCode("WHATEVER"), NotesErrors.message("WHATEVER"))
        assertEquals(ErrorMessages.forCode(""), NotesErrors.message(""))
    }

    @Test
    fun permanentCodesAreTheOnesRetryingCannotFix() {
        assertTrue(NotesErrors.isPermanent("TOO_LARGE"))
        assertTrue(NotesErrors.isPermanent("UNSUPPORTED_FORMAT"))
        assertFalse(NotesErrors.isPermanent("INTERNAL"))
        assertFalse(NotesErrors.isPermanent("NOT_INITIALIZED"))
    }
}

class NotesTextsTest {
    private val utc = ZoneId.of("UTC")
    private val now = 1_790_000_000_000L // 2026-09-21 in UTC

    @Test
    fun recentTimesAreRelativeOldOnesAreDates() {
        assertEquals("刚刚", NotesTexts.updatedLabel(now - 10_000, now, utc))
        assertEquals("5 分钟前", NotesTexts.updatedLabel(now - 5 * 60_000, now, utc))
        assertEquals("6 天前", NotesTexts.updatedLabel(now - 6 * 86_400_000L, now, utc))
        assertEquals("2026-09-14", NotesTexts.updatedLabel(now - 7 * 86_400_000L, now, utc))
        assertEquals("从未", NotesTexts.updatedLabel(0, now, utc))
    }

    @Test
    fun aTimeInTheFutureIsJustNow() {
        assertEquals("刚刚", NotesTexts.updatedLabel(now + 30_000, now, utc))
    }

    @Test
    fun dateTimeUsesTheGivenZone() {
        assertEquals("2026-09-21 14:13", NotesTexts.dateTime(now, utc))
        assertEquals("2026-09-21 22:13", NotesTexts.dateTime(now, ZoneId.of("Asia/Shanghai")))
        assertEquals("从未", NotesTexts.dateTime(0, utc))
    }

    @Test
    fun deletedElsewhereNamesTheNote() {
        assertTrue(NotesTexts.deletedElsewhere("购物清单").contains("「购物清单」"))
        assertTrue(NotesTexts.deletedElsewhere("  ").contains(NotesTexts.UNTITLED))
        assertTrue(NotesTexts.deletedElsewhere("x").contains("保留"))
    }

    @Test
    fun conflictBannerCountsAndEveryActionHasAResultText() {
        assertEquals("有 3 处同步冲突需要你处理", NotesTexts.conflictBanner(3))
        for (a in listOf("keep_main", "keep_conflict", "merge")) assertFalse(NotesTexts.resolved(a) == NotesTexts.resolved("other"))
    }

    @Test
    fun originNamesThisDeviceOthersAndMerges() {
        val names = mapOf("dev_pc" to "Tony-PC")
        assertEquals("本机", NotesTexts.origin("dev_me", "dev_me", names))
        assertEquals("Tony-PC", NotesTexts.origin("dev_pc", "dev_me", names))
        assertEquals("其他设备", NotesTexts.origin("dev_gone", "dev_me", names))
        assertEquals("合并产生", NotesTexts.origin("merge", "dev_me", names))
        assertEquals("未知设备", NotesTexts.origin("", "dev_me", names))
    }

    @Test
    fun editorNoticesHaveTextsThatSayWhatHappened() {
        assertTrue(EditorNotice.Merged.text.contains("合并"))
        assertTrue(EditorNotice.ConflictCopy("nt_c").text.contains("冲突副本"))
        assertTrue(EditorNotice.SavedAsNew.text.contains("新笔记"))
        assertTrue(EditorNotice.Trashed.text.contains("回收站"))
    }
}

class NoteLinksTest {
    @Test
    fun webAndMailLinksOpen() {
        assertEquals("https://example.com/a?b=1#c", NoteLinks.openable("https://example.com/a?b=1#c"))
        assertEquals("http://example.com", NoteLinks.openable("  http://example.com "))
        assertEquals("HTTPS://EXAMPLE.COM", NoteLinks.openable("HTTPS://EXAMPLE.COM"))
        assertEquals("mailto:me@example.com", NoteLinks.openable("mailto:me@example.com"))
        assertEquals("mailto:me@example.com?subject=hi", NoteLinks.openable("mailto:me@example.com?subject=hi"))
    }

    @Test
    fun everythingElseIsRefused() {
        val refused = listOf(
            "javascript:alert(1)", "JaVaScRiPt:alert(1)", "intent://scan/#Intent;scheme=zxing;end", "file:///sdcard/x", "content://media/x",
            "tel:+8613800000000", "sms:1", "data:text/html;base64,AAAA", "ftp://example.com", "market://details?id=x", "clipbridge://pair?x=1",
            "example.com", "www.example.com", "/relative", "#anchor", "", "   ", "https://", "http:///nohost", "mailto:", "mailto:nobody", "mailto:@x.y",
            "https://exa mple.com", "https://example.com/\u0000", "https://exa\nmple.com",
        )
        for (href in refused) assertNull("must not open '$href'", NoteLinks.openable(href))
    }

    @Test
    fun anAbsurdlyLongLinkIsRefused() {
        assertNull(NoteLinks.openable("https://example.com/" + "a".repeat(3000)))
    }
}

class NoteShareLogicTest {
    @Test
    fun plainTextBecomesTheBody() {
        val p = NoteShareLogic.plan("android.intent.action.SEND", "text/plain", "https://example.com/post", null)
        assertEquals(NoteSharePlan.Create("", "https://example.com/post"), p)
    }

    @Test
    fun theSubjectBecomesTheTitleOnOneLine() {
        val p = NoteShareLogic.plan("android.intent.action.SEND", "text/plain", "https://example.com", "  A page\n title  ")
        assertEquals(NoteSharePlan.Create("A page title", "https://example.com"), p)
    }

    @Test
    fun aSubjectAloneIsSavedAsTheText() {
        assertEquals(NoteSharePlan.Create("", "Only subject"), NoteShareLogic.plan("android.intent.action.SEND", "text/plain", null, "Only subject"))
        assertEquals(NoteSharePlan.Create("", "Only subject"), NoteShareLogic.plan("android.intent.action.SEND", "text/plain", "  ", "Only subject"))
    }

    @Test
    fun nothingToSave() {
        assertEquals(NoteSharePlan.Nothing, NoteShareLogic.plan("android.intent.action.SEND", "text/plain", null, null))
        assertEquals(NoteSharePlan.Nothing, NoteShareLogic.plan("android.intent.action.SEND", "text/plain", " \n", " "))
        assertEquals(NoteSharePlan.Nothing, NoteShareLogic.plan("android.intent.action.SEND_MULTIPLE", "text/plain", "x", null))
        assertEquals(NoteSharePlan.Nothing, NoteShareLogic.plan(null, "text/plain", "x", null))
        assertEquals(NoteSharePlan.Nothing, NoteShareLogic.plan("android.intent.action.SEND", "image/png", "x", null))
    }

    @Test
    fun aMissingMimeTypeIsStillText() {
        assertEquals(NoteSharePlan.Create("", "x"), NoteShareLogic.plan("android.intent.action.SEND", null, "x", null))
    }

    @Test
    fun theBodyLimitIsInBytesNotCharacters() {
        val ok = "a".repeat(NoteShareLogic.MAX_BODY_BYTES)
        assertTrue(NoteShareLogic.plan("android.intent.action.SEND", "text/plain", ok, null) is NoteSharePlan.Create)
        assertEquals(NoteSharePlan.TooLarge, NoteShareLogic.plan("android.intent.action.SEND", "text/plain", ok + "a", null))
        // 400k Chinese characters are 1.2 MB in UTF-8.
        assertEquals(NoteSharePlan.TooLarge, NoteShareLogic.plan("android.intent.action.SEND", "text/plain", "字".repeat(400_000), null))
    }

    @Test
    fun aLongTitleIsCutWithoutSplittingAnEmoji() {
        val subject = "x".repeat(199) + "😀" + "tail"
        val p = NoteShareLogic.plan("android.intent.action.SEND", "text/plain", "body", subject) as NoteSharePlan.Create
        assertEquals("x".repeat(199), p.title) // the emoji would have been cut in half
        val p2 = NoteShareLogic.plan("android.intent.action.SEND", "text/plain", "body", "y".repeat(500)) as NoteSharePlan.Create
        assertEquals(NoteShareLogic.MAX_TITLE_CHARS, p2.title.length)
    }
}
