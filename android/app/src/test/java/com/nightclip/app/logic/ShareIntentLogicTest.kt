package com.nightclip.app.logic

import org.junit.Assert.assertEquals
import org.junit.Test

class ShareIntentLogicTest {
    private val send = ShareIntentLogic.ACTION_SEND
    private val multi = ShareIntentLogic.ACTION_SEND_MULTIPLE

    @Test
    fun plainTextShareIsText() {
        assertEquals(SharePlan.Text("hello"), ShareIntentLogic.plan(send, "text/plain", "hello", null, 0))
    }

    @Test
    fun browserShareKeepsTheBodyNotTheSubject() {
        assertEquals(SharePlan.Text("https://example.com/a"), ShareIntentLogic.plan(send, "text/plain", "https://example.com/a", "Page title", 0))
    }

    @Test
    fun subjectIsUsedOnlyWithoutABody() {
        assertEquals(SharePlan.Text("Just a subject"), ShareIntentLogic.plan(send, "text/plain", "  ", "Just a subject", 0))
        assertEquals(SharePlan.Nothing, ShareIntentLogic.plan(send, "text/plain", null, null, 0))
        assertEquals(SharePlan.Nothing, ShareIntentLogic.plan(send, "text/plain", "", "", 0))
    }

    @Test
    fun streamsMeanFiles() {
        assertEquals(SharePlan.Files(1), ShareIntentLogic.plan(send, "application/pdf", null, null, 1))
        assertEquals(SharePlan.Files(1), ShareIntentLogic.plan(send, "image/png", "caption", null, 1)) // an image with a caption is still a file
        assertEquals(SharePlan.Files(3), ShareIntentLogic.plan(multi, "*/*", null, null, 3))
    }

    @Test
    fun plainTextWithBodyBeatsAStream() {
        assertEquals(SharePlan.Text("body"), ShareIntentLogic.plan(send, "text/plain", "body", null, 1))
    }

    @Test
    fun textFileWithoutBodyIsAFile() {
        assertEquals(SharePlan.Files(1), ShareIntentLogic.plan(send, "text/plain", null, null, 1))
    }

    @Test
    fun multipleWithoutStreamsIsNothing() {
        assertEquals(SharePlan.Nothing, ShareIntentLogic.plan(multi, "*/*", "x", null, 0))
    }

    @Test
    fun unknownActionsAreIgnored() {
        assertEquals(SharePlan.Nothing, ShareIntentLogic.plan("android.intent.action.VIEW", "text/plain", "x", null, 0))
        assertEquals(SharePlan.Nothing, ShareIntentLogic.plan(null, null, "x", null, 0))
    }

    @Test
    fun oversizedTextIsRefusedByUtf8Bytes() {
        val ok = "a".repeat(64 * 1024)
        assertEquals(SharePlan.Text(ok), ShareIntentLogic.plan(send, "text/plain", ok, null, 0))
        assertEquals(SharePlan.TextTooLarge, ShareIntentLogic.plan(send, "text/plain", ok + "a", null, 0))
        // 22000 three-byte characters are only 22000 chars but 66000 bytes
        assertEquals(SharePlan.TextTooLarge, ShareIntentLogic.plan(send, "text/plain", "中".repeat(22_000), null, 0))
    }
}
