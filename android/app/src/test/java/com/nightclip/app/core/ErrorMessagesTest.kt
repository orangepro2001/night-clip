package com.nightclip.app.core

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ErrorMessagesTest {
    private val documented = listOf(
        "EXPIRED", "HASH_MISMATCH", "TOO_LARGE", "NO_SPACE", "RATE_LIMITED", "INVALID", "DB_UNAVAILABLE",
        "KEY_INVALID", "NOT_INITIALIZED", "METHOD_NOT_FOUND", "NOT_FOUND", "UPDATE_DISABLED", "UPDATE_FAILED", "UPDATE_REJECTED", "INTERNAL",
        "VAULT_LOCKED", "VAULT_WRONG_CREDENTIALS", "VAULT_NOT_FOUND", "VAULT_EXISTS", "VAULT_UNSUPPORTED", "VAULT_NOT_EMPTY",
        "VAULT_REVIEW", "VAULT_BROKEN",
    )

    @Test
    fun everyDocumentedCodeHasItsOwnMessage() {
        val seen = HashSet<String>()
        for (code in documented) {
            val m = ErrorMessages.forCode(code)
            assertFalse("$code has no specific text", m.contains("未知错误"))
            assertFalse("$code leaks the code", m.contains(code))
            seen += m
        }
        assertTrue(seen.size >= documented.size - 2) // a couple of codes may share wording, but not most
    }

    @Test
    fun retiredNetworkCodesAreUnknownNow() {
        for (code in listOf("NO_TAILNET", "NOT_PAIRED", "PEER_OFFLINE")) {
            assertTrue(ErrorMessages.forCode(code).contains(code))
        }
    }

    @Test
    fun unknownCodeGetsGenericTextPlusCode() {
        val m = ErrorMessages.forCode("SOMETHING_NEW")
        assertTrue(m.contains("SOMETHING_NEW"))
        assertTrue(m.contains("未知错误"))
    }

    @Test
    fun emptyCodeGetsGenericText() {
        assertEquals("操作失败", ErrorMessages.forCode(""))
    }

    @Test
    fun initFailureCodesAreExtractedFromTheGoErrorText() {
        assertEquals("KEY_INVALID", ErrorMessages.codeFromInitFailure("KEY_INVALID: the database key check does not match"))
        assertEquals("DB_UNAVAILABLE", ErrorMessages.codeFromInitFailure("DB_UNAVAILABLE: open: disk I/O error"))
        assertEquals("INTERNAL", ErrorMessages.codeFromInitFailure("something odd"))
        assertEquals("INTERNAL", ErrorMessages.codeFromInitFailure(null))
    }

    @Test
    fun onlyDataProblemsOfferTheReset() {
        assertTrue(ErrorMessages.isResettable("KEY_INVALID"))
        assertTrue(ErrorMessages.isResettable("DB_UNAVAILABLE"))
        assertTrue(ErrorMessages.isResettable("KEY_CORRUPT"))
        assertFalse(ErrorMessages.isResettable("INTERNAL"))
        assertFalse(ErrorMessages.isResettable("KEYSTORE_UNAVAILABLE"))
    }

    @Test
    fun replyParsingCoversBothEnvelopes() {
        val ok = Reply.parse("""{"ok":true,"result":{"a":1}}""")
        assertTrue(ok.ok)
        assertEquals(1, ok.result.getInt("a"))
        val bad = Reply.parse("""{"ok":false,"error":{"code":"VAULT_LOCKED","message":"x"}}""")
        assertFalse(bad.ok)
        assertEquals("VAULT_LOCKED", bad.errorCode)
        assertEquals(ErrorMessages.forCode("VAULT_LOCKED"), bad.userMessage)
        val garbage = Reply.parse("<<")
        assertFalse(garbage.ok)
        assertEquals("INTERNAL", garbage.errorCode)
        assertEquals("NOT_INITIALIZED", Reply.notReady().errorCode)
    }
}
