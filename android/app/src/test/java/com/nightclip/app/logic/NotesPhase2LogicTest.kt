package com.nightclip.app.logic

import com.nightclip.app.core.AttachmentInfo
import com.nightclip.app.core.CleanupResult
import com.nightclip.app.core.ExportResult
import com.nightclip.app.core.Highlight
import com.nightclip.app.core.ImportResult
import com.nightclip.app.core.ImportSkip
import com.nightclip.app.core.SearchHit
import com.nightclip.app.core.SearchPage
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.ZoneId

private fun hit(id: String, title: String = id, snippet: String = "", at: Long = 1, h: List<Highlight> = emptyList(), th: List<Highlight> = emptyList(), copyOf: String = "") =
    SearchHit(id, title, at, snippet, h, th, copyOf)

private fun hl(vararg pairs: Int) = pairs.toList().chunked(2).map { Highlight(it[0], it[1]) }

class NotesSearchLogicTest {
    // ---- code points -> UTF-16 -----------------------------------------------------------------

    @Test
    fun plainTextOffsetsAreUnchanged() {
        assertEquals(listOf(Span16(6, 11)), NotesSearchLogic.toSpans("hello world", hl(6, 11)))
    }

    @Test
    fun anEmojiBeforeTheHighlightShiftsItByOneUnit() {
        val text = "😀 emoji"
        // "emoji" is code points 2..7 but UTF-16 units 3..8.
        assertEquals(listOf(Span16(3, 8)), NotesSearchLogic.toSpans(text, hl(2, 7)))
        assertEquals("emoji", text.substring(3, 8))
    }

    @Test
    fun anEmojiInsideTheHighlightIsCoveredWhole() {
        val text = "a😀b😀c"
        assertEquals(listOf(Span16(1, 6)), NotesSearchLogic.toSpans(text, hl(1, 4))) // 😀 b 😀
        assertEquals(listOf(Span16(1, 3)), NotesSearchLogic.toSpans(text, hl(1, 2))) // just the first emoji
    }

    @Test
    fun rareCjkCharactersOutsideTheBmpCountOnce() {
        val text = "𠮷野家 吉野家" // 𠮷 is one code point and two UTF-16 units
        assertEquals(listOf(Span16(0, 2)), NotesSearchLogic.toSpans(text, hl(0, 1)))
        assertEquals(listOf(Span16(2, 4)), NotesSearchLogic.toSpans(text, hl(1, 3)))
        assertEquals("野家", text.substring(2, 4))
    }

    @Test
    fun ordinaryCjkIsOneUnitPerCharacter() {
        assertEquals(listOf(Span16(3, 5)), NotesSearchLogic.toSpans("今天的笔记", hl(3, 5)))
    }

    @Test
    fun skinToneAndJoinedEmojiAreSeveralCodePoints() {
        val text = "👍🏽 ok" // thumbs up + skin tone: two code points, four units
        assertEquals(listOf(Span16(5, 7)), NotesSearchLogic.toSpans(text, hl(3, 5)))
        assertEquals("ok", text.substring(5, 7))
    }

    @Test
    fun aLoneSurrogateCountsAsOneCodePoint() {
        val text = "a\uD83Db"
        assertEquals(listOf(Span16(2, 3)), NotesSearchLogic.toSpans(text, hl(2, 3)))
    }

    @Test
    fun rangesOutsideTheTextAreClipped() {
        assertEquals(listOf(Span16(3, 5)), NotesSearchLogic.toSpans("abcde", hl(3, 99)))
        assertTrue(NotesSearchLogic.toSpans("abc", hl(10, 12)).isEmpty())
        assertEquals(listOf(Span16(0, 3)), NotesSearchLogic.toSpans("abc", hl(-4, 3)))
    }

    @Test
    fun emptyAndReversedRangesAreDropped() {
        assertTrue(NotesSearchLogic.toSpans("abc", hl(1, 1)).isEmpty())
        assertTrue(NotesSearchLogic.toSpans("abc", hl(2, 1)).isEmpty())
        assertTrue(NotesSearchLogic.toSpans("", hl(0, 3)).isEmpty())
        assertTrue(NotesSearchLogic.toSpans("abc", emptyList()).isEmpty())
    }

    @Test
    fun overlappingAndTouchingRangesAreMergedAndSorted() {
        assertEquals(listOf(Span16(0, 6), Span16(8, 9)), NotesSearchLogic.toSpans("0123456789", hl(8, 9, 2, 4, 0, 3, 3, 6)))
    }

    // ---- paging ----------------------------------------------------------------------------------

    @Test
    fun theFirstPageReplacesTheHitsAndRemembersTheCursor() {
        val s = NotesSearchLogic.firstPage("emoji", SearchPage(listOf(hit("a"), hit("b"), hit("a")), "2"))
        assertEquals(listOf("a", "b"), s.items.map { it.noteId })
        assertEquals("2", s.nextCursor)
        assertTrue(s.canLoadMore)
        assertTrue(s.loaded)
        assertEquals("emoji", s.query)
    }

    @Test
    fun noHitsIsAnAnswerNotALoadingScreen() {
        val s = NotesSearchLogic.firstPage("zzz", SearchPage(emptyList(), null))
        assertTrue(s.loaded)
        assertTrue(s.items.isEmpty())
        assertFalse(s.canLoadMore)
        assertFalse(SearchState().loaded)
    }

    @Test
    fun theNextPageIsAppendedOnce() {
        val s1 = NotesSearchLogic.firstPage("q", SearchPage(listOf(hit("a"), hit("b")), "2"))
        val s2 = NotesSearchLogic.nextPage(s1, "q", "2", SearchPage(listOf(hit("b"), hit("c")), null))
        assertEquals(listOf("a", "b", "c"), s2.items.map { it.noteId })
        assertNull(s2.nextCursor)
    }

    @Test
    fun aPageOfAnotherQueryOrAnOldCursorIsIgnored() {
        val s1 = NotesSearchLogic.firstPage("q", SearchPage(listOf(hit("a")), "1"))
        assertEquals(s1, NotesSearchLogic.nextPage(s1, "other", "1", SearchPage(listOf(hit("x")), null)))
        assertEquals(s1, NotesSearchLogic.nextPage(s1, "q", "0", SearchPage(listOf(hit("x")), null)))
    }

    @Test
    fun reloadAsksForAsManyHitsAsAreShownWithinTheCoresLimits() {
        assertEquals(30, NotesSearchLogic.reloadLimit(SearchState()))
        assertEquals(45, NotesSearchLogic.reloadLimit(SearchState(items = (1..45).map { hit("n$it") })))
        assertEquals(100, NotesSearchLogic.reloadLimit(SearchState(items = (1..400).map { hit("n$it") })))
    }

    // ---- the box -------------------------------------------------------------------------------

    @Test
    fun anEmptyOrBlankBoxIsNotASearch() {
        assertFalse(NotesSearchLogic.isActive(""))
        assertFalse(NotesSearchLogic.isActive("   \n"))
        assertTrue(NotesSearchLogic.isActive(" a "))
        assertEquals("a b", NotesSearchLogic.normalize("  a b \n"))
    }

    @Test
    fun anUntitledHitShowsUntitled() {
        assertEquals(NotesTexts.UNTITLED, NotesSearchLogic.title(hit("a", title = "  ")))
        assertEquals("Plan", NotesSearchLogic.title(hit("a", title = " Plan ")))
    }

    @Test
    fun aConflictCopyIsAHitToo() {
        assertTrue(hit("c", copyOf = "a").isConflictCopy)
        assertFalse(hit("a").isConflictCopy)
    }
}

class BlobAddressTest {
    private val id = "0123456789abcdef".repeat(4)

    @Test
    fun aWellFormedAddressGivesTheId() {
        assertEquals(id, BlobAddress.parse("blob:$id"))
        assertEquals(id, BlobAddress.parse("  blob:$id "))
        assertEquals(id, BlobAddress.parse("blob:" + id.uppercase())) // the core's ids are lower case
    }

    @Test
    fun anythingElseIsNotAnAddress() {
        for (bad in listOf(
            "", "blob:", "blob:abc", "blob:" + id.dropLast(1), "blob:$id" + "0", "blob:" + id.replaceRange(0, 1, "g"),
            "https://example.com/blob:$id", "xblob:$id", "blob:$id/../x", "blob:$id?x=1", "blob: $id", "file:///blob:$id",
        )) {
            assertNull("'$bad'", BlobAddress.parse(bad))
        }
    }

    @Test
    fun aLinkThatClaimsToBeABlobIsNeverAWebLink() {
        assertTrue(BlobAddress.claims("blob:abc"))
        assertTrue(BlobAddress.claims("BLOB:$id"))
        assertFalse(BlobAddress.claims("https://example.com"))
        assertNull(NoteLinks.openable("blob:$id"))
        assertNull(NoteLinks.openable("blob:abc"))
        assertNull(UrlGuard.webUrl("blob:$id"))
    }

    @Test
    fun blobIds() {
        assertTrue(BlobAddress.isBlobId(id))
        assertFalse(BlobAddress.isBlobId(id.uppercase()))
        assertFalse(BlobAddress.isBlobId("../" + id.drop(3)))
        assertFalse(BlobAddress.isBlobId(""))
    }
}

class AttachmentFilesTest {
    private val id = "ab".repeat(32)

    @Test
    fun pictureTypes() {
        for (m in listOf("image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "IMAGE/PNG", "image/png; charset=binary")) assertTrue(m, AttachmentFiles.isImage(m))
        for (m in listOf("image/svg+xml", "image/tiff", "application/pdf", "text/plain", "")) assertFalse(m, AttachmentFiles.isImage(m))
    }

    @Test
    fun kindFollowsThePresenceAndTheType() {
        fun info(present: Boolean, mime: String) = AttachmentInfo(id, "n", mime, 1, present)
        assertEquals(AttachmentFiles.Kind.ABSENT, AttachmentFiles.kindOf(info(false, "image/png")))
        assertEquals(AttachmentFiles.Kind.PICTURE, AttachmentFiles.kindOf(info(true, "image/png")))
        assertEquals(AttachmentFiles.Kind.DOCUMENT, AttachmentFiles.kindOf(info(true, "image/svg+xml")))
        assertEquals(AttachmentFiles.Kind.DOCUMENT, AttachmentFiles.kindOf(info(true, "application/pdf")))
    }

    @Test
    fun extensions() {
        assertEquals("png", AttachmentFiles.extensionOf("Photo.PNG"))
        assertEquals("", AttachmentFiles.extensionOf("README"))
        assertEquals("", AttachmentFiles.extensionOf("archive.tar.gz/../x"))
        assertEquals("", AttachmentFiles.extensionOf("a.b c"))
        assertEquals("", AttachmentFiles.extensionOf("a.123456789"))
    }

    @Test
    fun pictureCacheNamesAreIdPlusExtensionOnly() {
        assertEquals("$id.png", AttachmentFiles.pictureCacheName(id, "x", "image/png"))
        assertEquals("$id.jpg", AttachmentFiles.pictureCacheName(id, "x.jpeg", "image/jpeg"))
        assertEquals("$id.heic", AttachmentFiles.pictureCacheName(id, "IMG_1.HEIC", ""))
        assertEquals("$id.img", AttachmentFiles.pictureCacheName(id, "no extension", ""))
        // Nothing from the file name but a plain extension reaches the path.
        assertEquals("$id.png", AttachmentFiles.pictureCacheName(id, "../../evil/x.png", "image/png"))
        assertEquals("$id.img", AttachmentFiles.pictureCacheName(id, "x.p/ng", ""))
        assertNull(AttachmentFiles.pictureCacheName("../" + id.drop(3), "x", "image/png"))
        assertNull(AttachmentFiles.pictureCacheName("short", "x", "image/png"))
    }

    @Test
    fun namesCannotLeaveTheirFolder() {
        val hostile = listOf("../../etc/passwd", "..\\..\\windows\\x", "/abs/path/file.txt", "a/../../b.txt", "..", ".", "...", "   ", "", "C:\\x\\y.txt", "x\u0000y.txt", "a:b*c?.txt")
        for (raw in hostile) {
            for (name in listOf(AttachmentFiles.uploadName(raw), AttachmentFiles.openName(raw))) {
                assertFalse("'$raw' -> '$name'", name.contains('/') || name.contains('\\') || name.contains('\u0000') || name == ".." || name == "." || name.isEmpty())
            }
        }
        assertEquals("passwd", AttachmentFiles.openName("../../etc/passwd"))
        assertEquals("file", AttachmentFiles.openName(".."))
        assertEquals("file", AttachmentFiles.uploadName(null))
        assertEquals("报告 终稿.pdf", AttachmentFiles.openName("报告 终稿.pdf")) // a real name survives
    }

    @Test
    fun theOpenFolderIsDerivedFromTheIdOnly() {
        assertEquals("abababababababab", AttachmentFiles.openFolder(id))
        assertEquals("x", AttachmentFiles.openFolder("../../x"))
    }

    @Test
    fun executablesInstallersScriptsAndPagesAreNotOpened() {
        for (name in listOf("a.apk", "A.APK", "setup.exe", "run.sh", "x.bat", "m.msi", "lib.so", "page.html", "page.HTM", "pic.svg", "app.jar", "evil.js", "x.ps1", "pack.xapk")) {
            assertFalse(name, AttachmentFiles.isSafeToOpen(name, "application/octet-stream"))
        }
        assertFalse(AttachmentFiles.isSafeToOpen("noext", "application/vnd.android.package-archive"))
        assertFalse(AttachmentFiles.isSafeToOpen("noext", "text/html; charset=utf-8"))
        assertFalse(AttachmentFiles.isSafeToOpen("noext", "image/svg+xml"))
    }

    @Test
    fun ordinaryFilesAreOpened() {
        for ((name, mime) in listOf("a.pdf" to "application/pdf", "n.txt" to "text/plain", "p.png" to "image/png", "doc.docx" to "application/msword", "noext" to "", "a.apk.txt" to "text/plain")) {
            assertTrue(name, AttachmentFiles.isSafeToOpen(name, mime))
        }
    }

    @Test
    fun openMimeFallsBackToBinary() {
        assertEquals("application/pdf", AttachmentFiles.openMime("application/pdf; x=y"))
        assertEquals("application/octet-stream", AttachmentFiles.openMime(""))
    }

    @Test
    fun chipTexts() {
        assertEquals("📎 报告.pdf (1.5 MB)", AttachmentFiles.chipText("报告.pdf", 1_572_864))
        assertEquals("📎 a.txt", AttachmentFiles.chipText("a.txt", null))
        assertEquals("📎 file", AttachmentFiles.chipText("  ", -1))
    }
}

class ImageSamplingTest {
    @Test
    fun smallPicturesAreNotShrunk() {
        assertEquals(1, ImageSampling.sampleSize(300, 200, 1080))
        assertEquals(1, ImageSampling.sampleSize(1080, 1920, 1080))
    }

    @Test
    fun theWidthStaysAtLeastTheTargetWhileItCanBeHalved() {
        assertEquals(2, ImageSampling.sampleSize(2160, 1000, 1080))
        assertEquals(4, ImageSampling.sampleSize(4608, 3456, 1080)) // 12 MP camera photo: 4608 / 4 = 1152
    }

    @Test
    fun aHugePictureIsBroughtUnderThePixelBudget() {
        val n = ImageSampling.sampleSize(12000, 9000, 1080)
        assertTrue(n >= 8)
        assertTrue((12000L / n) * (9000L / n) <= ImageSampling.MAX_PIXELS)
        assertTrue("a power of two", n and (n - 1) == 0)
    }

    @Test
    fun aTallPanoramaIsLimitedByPixelsToo() {
        val n = ImageSampling.sampleSize(1080, 30000, 1080)
        assertTrue((1080L / n) * (30000L / n) <= ImageSampling.MAX_PIXELS)
    }

    @Test
    fun brokenSizesGiveOne() {
        assertEquals(1, ImageSampling.sampleSize(0, 100, 1080))
        assertEquals(1, ImageSampling.sampleSize(100, -1, 1080))
    }

    @Test
    fun exifOrientationsTurnPicturesUpright() {
        assertEquals(ImageTransform(0, false), ImageSampling.exifTransform(1))
        assertEquals(ImageTransform(90, false), ImageSampling.exifTransform(6))
        assertEquals(ImageTransform(180, false), ImageSampling.exifTransform(3))
        assertEquals(ImageTransform(270, false), ImageSampling.exifTransform(8))
        assertEquals(ImageTransform(0, true), ImageSampling.exifTransform(2))
        assertEquals(ImageTransform(180, true), ImageSampling.exifTransform(4))
        assertEquals(ImageTransform(90, true), ImageSampling.exifTransform(5))
        assertEquals(ImageTransform(270, true), ImageSampling.exifTransform(7))
        assertEquals(ImageTransform(0, false), ImageSampling.exifTransform(0))
        assertEquals(ImageTransform(0, false), ImageSampling.exifTransform(99))
    }
}

class NotesTransferTest {
    private val utc = ZoneId.of("UTC")

    @Test
    fun theProposedExportName() {
        assertEquals("clipbridge-notes-20260921-1413.zip", NotesTransfer.exportFileName(1_790_000_000_000L, utc))
    }

    @Test
    fun uniqueNamesKeepOnlyPlainCharacters() {
        assertEquals("notes-ab12.zip", NotesTransfer.uniqueName("notes", "ab12", "zip"))
        assertEquals("import-ab12.md", NotesTransfer.uniqueName("import", "ab12", ".md"))
        assertEquals("notes-0.zip", NotesTransfer.uniqueName("notes", "../../..", "zip"))
        assertEquals("notes-evil.zip", NotesTransfer.uniqueName("notes", "e/v\\i.l", "zip"))
        assertEquals("notes-ab.x", NotesTransfer.uniqueName("notes", "ab", "/../x/"))
        assertNotEquals(NotesTransfer.uniqueName("n", "a", "zip"), NotesTransfer.uniqueName("n", "b", "zip"))
    }

    @Test
    fun theImportExtensionIsDecidedByTheNameThenByTheType() {
        assertEquals(".zip", NotesTransfer.importExtension("backup.ZIP", null))
        assertEquals(".md", NotesTransfer.importExtension("note.md", "application/octet-stream"))
        assertEquals(".md", NotesTransfer.importExtension("note.markdown", null))
        assertEquals(".txt", NotesTransfer.importExtension("a.txt", "text/plain"))
        assertEquals(".zip", NotesTransfer.importExtension("download", "application/zip"))
        assertEquals(".zip", NotesTransfer.importExtension(null, "application/x-zip-compressed"))
        assertEquals(".md", NotesTransfer.importExtension("without extension", "text/markdown"))
        assertEquals(".md", NotesTransfer.importExtension(null, "text/x-markdown"))
        assertEquals(".txt", NotesTransfer.importExtension("", "text/plain; charset=utf-8"))
        assertNull(NotesTransfer.importExtension("photo.png", "image/png"))
        assertNull(NotesTransfer.importExtension("archive.rar", "application/octet-stream"))
        assertNull(NotesTransfer.importExtension(null, null))
        assertEquals(".zip", NotesTransfer.importExtension("evil.zip.exe.zip", null))
        assertNull(NotesTransfer.importExtension("evil.zip.exe", "application/octet-stream"))
    }

    @Test
    fun sizeLimits() {
        assertTrue(NotesTransfer.importSizeOk(-1)) // unknown: the copy itself stops at the limit
        assertTrue(NotesTransfer.importSizeOk(NotesTransfer.MAX_IMPORT_BYTES))
        assertFalse(NotesTransfer.importSizeOk(NotesTransfer.MAX_IMPORT_BYTES + 1))
        assertEquals(256L * 1024 * 1024, NotesTransfer.MAX_IMPORT_BYTES)
        assertTrue(NotesTransfer.attachmentSizeOk(32L * 1024 * 1024))
        assertFalse(NotesTransfer.attachmentSizeOk(32L * 1024 * 1024 + 1))
    }

    @Test
    fun exportText() {
        assertEquals("已导出 12 篇笔记（1.5 KB）", NotesTransfer.exportSummary(ExportResult("/x", 12, 1536)))
    }

    @Test
    fun importTexts() {
        assertEquals("导入了 5 篇", NotesTransfer.importSummary(ImportResult(5, emptyList(), emptyList())))
        assertEquals("导入了 3 篇，跳过 2 个", NotesTransfer.importSummary(ImportResult(3, listOf(ImportSkip("a", "x"), ImportSkip("b", "y")), emptyList())))
        assertEquals("导入了 0 篇，跳过 1 个", NotesTransfer.importSummary(ImportResult(0, listOf(ImportSkip("a", "x")), emptyList())))
        assertEquals(NotesTransfer.NOTHING_IMPORTED, NotesTransfer.importSummary(ImportResult(0, emptyList(), emptyList())))
    }

    @Test
    fun skipReasonsAreTranslatedAndUnknownOnesKept() {
        assertEquals("超过单篇笔记 1 MiB 的上限", NotesTransfer.skipReason("larger than the 1 MiB limit of a note"))
        assertEquals("无法读取", NotesTransfer.skipReason("cannot be read"))
        assertEquals("不是文本文件", NotesTransfer.skipReason("not a text file"))
        assertEquals("不是普通文件", NotesTransfer.skipReason("not a regular file"))
        assertEquals("something new", NotesTransfer.skipReason("something new"))
        assertEquals(listOf("a/b.md：不是文本文件", "c.md：something new"), NotesTransfer.skipLines(listOf(ImportSkip("a/b.md", "not a text file"), ImportSkip("c.md", "something new"))))
    }

    @Test
    fun cleanupText() {
        assertEquals("释放了 12.0 MB（删除 3 个附件）", NotesTransfer.cleanupSummary(CleanupResult(3, 12L * 1024 * 1024, 5)))
        assertEquals("释放了 0.5 MB（删除 1 个附件）", NotesTransfer.cleanupSummary(CleanupResult(1, 512L * 1024, 0)))
        assertEquals("没有可清理的附件", NotesTransfer.cleanupSummary(CleanupResult(0, 0, 4)))
        assertEquals("0.0 MB", NotesTransfer.megabytes(-5))
    }

    @Test
    fun cacheFolderNamesDoNotCollide() {
        val dirs = listOf(NotesTransfer.DIR_EXPORT, NotesTransfer.DIR_IMPORT, NotesTransfer.DIR_ADD, NotesTransfer.DIR_PICTURES, NotesTransfer.DIR_OPEN)
        assertEquals(dirs.size, dirs.toSet().size)
        assertEquals("notes-open", NotesTransfer.DIR_OPEN) // update_paths.xml shares exactly this folder
    }
}

class NoteShareFilesTest {
    private val send = "android.intent.action.SEND"

    @Test
    fun aSharedPictureBecomesAnAttachmentNote() {
        assertEquals(NoteSharePlan.Attach("", ""), NoteShareLogic.plan(send, "image/jpeg", null, null, hasStream = true))
    }

    @Test
    fun aCaptionAndATitleComeAlong() {
        assertEquals(NoteSharePlan.Attach("Holiday", "at the beach"), NoteShareLogic.plan(send, "image/png", " at the beach ", " Holiday ", hasStream = true))
    }

    @Test
    fun otherFilesAreAttachmentsToo() {
        assertEquals(NoteSharePlan.Attach("", ""), NoteShareLogic.plan(send, "application/pdf", null, null, hasStream = true))
        assertEquals(NoteSharePlan.Attach("", ""), NoteShareLogic.plan(send, null, null, null, hasStream = true))
    }

    @Test
    fun textStillWinsOverAFileAndTheTextPathIsUnchanged() {
        assertEquals(NoteSharePlan.Create("", "https://example.com"), NoteShareLogic.plan(send, "text/plain", "https://example.com", null, hasStream = true))
        assertEquals(NoteSharePlan.Create("", "https://example.com"), NoteShareLogic.plan(send, "text/plain", "https://example.com", null))
        assertEquals(NoteSharePlan.Nothing, NoteShareLogic.plan(send, "image/png", "x", null)) // no stream: nothing to attach
    }

    @Test
    fun aTextFileSharedAsAStreamIsAnAttachment() {
        assertEquals(NoteSharePlan.Attach("", ""), NoteShareLogic.plan(send, "text/plain", null, null, hasStream = true))
    }

    @Test
    fun otherActionsAreIgnored() {
        assertEquals(NoteSharePlan.Nothing, NoteShareLogic.plan("android.intent.action.SEND_MULTIPLE", "image/png", null, null, hasStream = true))
    }

    @Test
    fun aTooLargeCaptionIsRefused() {
        val big = "a".repeat(NoteShareLogic.MAX_BODY_BYTES + 1)
        assertEquals(NoteSharePlan.TooLarge, NoteShareLogic.plan(send, "image/png", big, null, hasStream = true))
    }

    @Test
    fun theNoteTextHasOneParagraphPerAttachmentThenTheCaption() {
        assertEquals("![a](blob:1)\n\n[b](blob:2)", NoteShareLogic.attachmentBody(listOf("![a](blob:1)", "[b](blob:2)"), ""))
        assertEquals("![a](blob:1)\n\nnice", NoteShareLogic.attachmentBody(listOf("![a](blob:1)"), "  nice \n"))
    }
}

class MarkdownInsertLineTest {
    private val md = "![p](blob:x)"

    private fun mark(r: EditResult) = r.text.substring(0, r.selStart) + "¦" + r.text.substring(r.selStart)

    @Test
    fun intoAnEmptyNote() {
        assertEquals("$md\n¦", mark(MarkdownEdit.insertLine("", 0, 0, md)))
    }

    @Test
    fun afterTextOnTheSameLineItGetsAParagraphOfItsOwn() {
        assertEquals("abc\n\n$md\n¦", mark(MarkdownEdit.insertLine("abc", 3, 3, md)))
    }

    @Test
    fun onAnEmptyLineBelowText() {
        assertEquals("abc\n\n$md\n¦", mark(MarkdownEdit.insertLine("abc\n", 4, 4, md)))
        assertEquals("abc\n\n$md\n¦", mark(MarkdownEdit.insertLine("abc\n\n", 5, 5, md)))
    }

    @Test
    fun inTheMiddleOfAParagraphItIsSplit() {
        val r = MarkdownEdit.insertLine("ab cd", 2, 2, md)
        assertEquals("ab\n\n$md\n\n cd", r.text)
    }

    @Test
    fun atTheStartOfALineBeforeAnother() {
        assertEquals("a\n\n$md\n\nb", MarkdownEdit.insertLine("a\nb", 2, 2, md).text)
    }

    @Test
    fun theSelectionIsKeptAndTheLineGoesAfterIt() {
        val r = MarkdownEdit.insertLine("hello world", 0, 5, md)
        assertEquals("hello\n\n$md\n\n world", r.text)
    }

    @Test
    fun existingBlankLinesAreNotDoubled() {
        assertEquals("a\n\n$md\n\nb", MarkdownEdit.insertLine("a\n\nb", 1, 1, md).text)
        assertEquals("a\n\n$md\n\nb", MarkdownEdit.insertLine("a\n\n\nb", 3, 3, md).text)
    }

    @Test
    fun outOfRangeOffsetsAreClamped() {
        assertEquals("abc\n\n$md\n", MarkdownEdit.insertLine("abc", 99, 99, md).text)
    }

    @Test
    fun cjkTextIsNeverSplitInsideACharacter() {
        assertEquals("你好\n\n$md\n", MarkdownEdit.insertLine("你好", 2, 2, md).text)
    }
}

class NotesErrorsPhase2Test {
    @Test
    fun attachmentErrors() {
        assertEquals("附件太大：每个附件最多 32 MiB", NotesErrors.message("TOO_LARGE", NotesErrors.Context.ATTACH))
        assertEquals("无法读取这个文件", NotesErrors.message("INVALID", NotesErrors.Context.ATTACH))
        assertTrue(NotesErrors.message("NOT_FOUND", NotesErrors.Context.ATTACH).contains("附件"))
        // The same code means something else elsewhere.
        assertTrue(NotesErrors.message("TOO_LARGE").contains("1 MiB"))
    }

    @Test
    fun transferAndSearchErrors() {
        assertTrue(NotesErrors.message("INVALID", NotesErrors.Context.TRANSFER).contains("已存在"))
        assertNotEquals(NotesErrors.message("INVALID"), NotesErrors.message("INVALID", NotesErrors.Context.SEARCH))
        assertEquals(NotesErrors.message("INTERNAL"), NotesErrors.message("INTERNAL", NotesErrors.Context.ATTACH))
    }

    @Test
    fun noSpaceFromTheCoreHasItsGeneralText() {
        assertFalse(NotesErrors.message("NO_SPACE", NotesErrors.Context.ATTACH).contains("未知错误"))
    }
}
