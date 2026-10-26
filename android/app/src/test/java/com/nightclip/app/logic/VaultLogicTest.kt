package com.nightclip.app.logic

import com.nightclip.app.core.ErrorMessages
import com.nightclip.app.core.VaultEntrySummary
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private fun entry(
    id: String,
    title: String,
    kind: String = "login",
    username: String = "",
    url: String = "",
    expiresAt: Long = 0,
    inTrash: Boolean = false,
) = VaultEntrySummary(id, "", kind, title, username, url, emptyList(), false, false, 0, expiresAt, inTrash)

class VaultListLogicTest {
    private val now = 1_000_000L
    private val all = listOf(
        entry("1", "zeta", "login"),
        entry("2", "Alpha", "apikey"),
        entry("3", "beta", "secret"),
        entry("4", "Note A", "note"),
        entry("5", "Soon", "login", expiresAt = now + 5 * 86400),
        entry("6", "Late", "login", expiresAt = now + 90 * 86400),
        entry("7", "Gone", "login", expiresAt = now - 10),
        entry("8", "Binned", "login", inTrash = true, expiresAt = now + 86400),
    )

    @Test
    fun allHidesTheTrashAndSortsByTitleIgnoringCase() {
        val ids = VaultListLogic.apply(all, VaultFilter.ALL, now).map { it.title }
        assertEquals(listOf("Alpha", "beta", "Gone", "Late", "Note A", "Soon", "zeta"), ids)
    }

    @Test
    fun kindFiltersMatchTheirKind() {
        assertEquals(listOf("Alpha"), VaultListLogic.apply(all, VaultFilter.APIKEY, now).map { it.title })
        assertEquals(listOf("beta"), VaultListLogic.apply(all, VaultFilter.SECRET, now).map { it.title })
        assertEquals(listOf("Note A"), VaultListLogic.apply(all, VaultFilter.NOTE, now).map { it.title })
        assertEquals(4, VaultListLogic.apply(all, VaultFilter.LOGIN, now).size)
    }

    @Test
    fun expiringSoonIncludesExpiredAndNearButNotFarOrTrash() {
        assertEquals(listOf("Gone", "Soon"), VaultListLogic.apply(all, VaultFilter.EXPIRING, now).map { it.title })
    }

    @Test
    fun trashFilterShowsOnlyTrash() {
        assertEquals(listOf("Binned"), VaultListLogic.apply(all, VaultFilter.TRASH, now).map { it.title })
        assertTrue(VaultListLogic.coreTrashOnly(VaultFilter.TRASH))
        assertFalse(VaultListLogic.coreTrashOnly(VaultFilter.ALL))
    }

    @Test
    fun coreKindMapping() {
        assertEquals("login", VaultListLogic.coreKind(VaultFilter.LOGIN))
        assertEquals("apikey", VaultListLogic.coreKind(VaultFilter.APIKEY))
        assertNull(VaultListLogic.coreKind(VaultFilter.EXPIRING))
        assertNull(VaultListLogic.coreKind(VaultFilter.ALL))
    }

    @Test
    fun subtitleFallsBackToTheHost() {
        assertEquals("tony", VaultListLogic.subtitle(entry("a", "t", username = "tony", url = "https://x.com")))
        assertEquals("www.example.com", VaultListLogic.subtitle(entry("a", "t", url = "https://www.example.com/login?x=1")))
        assertEquals("example.com", VaultListLogic.subtitle(entry("a", "t", url = "example.com/path")))
        assertEquals("", VaultListLogic.subtitle(entry("a", "t")))
        assertEquals("（无标题）", VaultListLogic.title(entry("a", " ")))
    }
}

class TotpClockTest {
    @Test
    fun countsDownInWholeSecondsRoundedUp() {
        val c = TotpClock(fetchedAtMs = 10_000, secondsLeftAtFetch = 20)
        assertEquals(20, c.remainingSeconds(10_000))
        assertEquals(20, c.remainingSeconds(10_001) + 0) // 19.999 s left rounds up
        assertEquals(19, c.remainingSeconds(11_000))
        assertEquals(1, c.remainingSeconds(29_999))
        assertEquals(0, c.remainingSeconds(30_000))
        assertEquals(0, c.remainingSeconds(99_000))
    }

    @Test
    fun expiryAndBackwardsClock() {
        val c = TotpClock(5_000, 3)
        assertFalse(c.isExpired(5_000))
        assertTrue(c.isExpired(8_000))
        assertEquals(3, c.remainingSeconds(1_000)) // clock went backwards: treated as just fetched
    }

    @Test
    fun fractionAndPeriodGuess() {
        val c = TotpClock(0, 15)
        assertEquals(0.5f, c.fraction(0, 30), 0.001f)
        assertEquals(0f, c.fraction(15_000, 30), 0.001f)
        assertEquals(1f, TotpClock(0, 30).fraction(0, 30), 0.001f)
        assertEquals(0f, c.fraction(0, 0), 0.001f)
        assertEquals(30, TotpClock.guessPeriod(12))
        assertEquals(30, TotpClock.guessPeriod(30))
        assertEquals(60, TotpClock.guessPeriod(45))
        assertEquals(120, TotpClock.guessPeriod(100))
    }

    @Test
    fun formatsCodesInGroups() {
        assertEquals("123 456", TotpClock.format("123456"))
        assertEquals("1234 5678", TotpClock.format("12345678"))
        assertEquals("1234567", TotpClock.format("1234567"))
        assertTrue(TotpClock.isUrgent(5))
        assertFalse(TotpClock.isUrgent(6))
    }
}

class PasswordStrengthUiTest {
    @Test
    fun labelsMapToLevelsTextAndFractions() {
        assertEquals(0, PasswordStrengthUi.level("very_weak"))
        assertEquals(4, PasswordStrengthUi.level("strong"))
        assertEquals(0, PasswordStrengthUi.level("???"))
        assertEquals("很弱", PasswordStrengthUi.text("very_weak"))
        assertEquals("弱", PasswordStrengthUi.text("weak"))
        assertEquals("一般", PasswordStrengthUi.text("fair"))
        assertEquals("较强", PasswordStrengthUi.text("good"))
        assertEquals("强", PasswordStrengthUi.text("strong"))
        assertEquals("未知", PasswordStrengthUi.text("x"))
        assertEquals(0.2f, PasswordStrengthUi.fraction("very_weak"), 0.001f)
        assertEquals(1f, PasswordStrengthUi.fraction("strong"), 0.001f)
    }

    @Test
    fun weakPasswordsWarnButAreAllowed() {
        assertTrue(PasswordStrengthUi.isWeak("weak"))
        assertFalse(PasswordStrengthUi.isWeak("fair"))
        assertNull(PasswordStrengthUi.masterWarning("strong", 0))
        assertTrue(PasswordStrengthUi.masterWarning("weak", 6)!!.contains("仍然可以使用"))
        assertTrue(PasswordStrengthUi.masterWarning("good", 8)!!.contains("12"))
        assertNull(PasswordStrengthUi.masterWarning("good", 16))
    }
}

class UrlGuardTest {
    @Test
    fun onlyWebLinksPass() {
        assertEquals("https://example.com/a", UrlGuard.webUrl(" https://example.com/a "))
        assertEquals("http://10.0.0.1:8080/x", UrlGuard.webUrl("http://10.0.0.1:8080/x"))
        assertEquals("https://example.com", UrlGuard.webUrl("example.com"))
        assertEquals("HTTPS://Example.com", UrlGuard.webUrl("HTTPS://Example.com"))
    }

    @Test
    fun otherSchemesAndJunkAreRefused() {
        for (bad in listOf("javascript:alert(1)", "intent://x#Intent;end", "file:///etc/passwd", "content://x/y", "tel:123", "ftp://h/x", "", "  ", "http://", "https://exa mple.com", "market://details?id=x")) {
            assertNull(bad, UrlGuard.webUrl(bad))
        }
    }
}

class RateLimitCountdownTest {
    @Test
    fun countsDownToZero() {
        val c = RateLimitCountdown.from(5, 1_000)
        assertTrue(c.isActive(1_000))
        assertEquals(5, c.remainingSeconds(1_000))
        assertEquals(3, c.remainingSeconds(3_000))
        assertEquals(1, c.remainingSeconds(5_999))
        assertEquals(0, c.remainingSeconds(6_000))
        assertFalse(c.isActive(7_000))
    }

    @Test
    fun noneAndCap() {
        assertFalse(RateLimitCountdown.NONE.isActive(0))
        assertFalse(RateLimitCountdown.from(0, 0).isActive(0))
        assertFalse(RateLimitCountdown.from(-4, 0).isActive(0))
        assertEquals(360, RateLimitCountdown.from(100000, 0).remainingSeconds(0))
        assertEquals("9 秒", RateLimitCountdown.label(9))
        assertEquals("1 分 05 秒", RateLimitCountdown.label(65))
        assertEquals("", RateLimitCountdown.label(0))
    }
}

class VaultErrorsTest {
    @Test
    fun everyDocumentedCodeHasItsOwnText() {
        for (code in listOf("VAULT_LOCKED", "VAULT_WRONG_CREDENTIALS", "VAULT_NOT_FOUND", "VAULT_EXISTS", "VAULT_UNSUPPORTED", "VAULT_BROKEN", "RATE_LIMITED", "TOO_LARGE", "INVALID")) {
            val m = VaultErrors.message(code, 5)
            assertFalse("$code -> $m", m.contains("未知错误"))
            assertTrue(m.isNotBlank())
        }
    }

    @Test
    fun wrongCredentialsTextMatchesTheSpec() {
        assertEquals("主密码或密钥文件不对，或文件已损坏。可以从备份恢复。", VaultErrors.message("VAULT_WRONG_CREDENTIALS"))
        assertEquals("这个库的加密参数不受支持或不安全。", VaultErrors.message("VAULT_UNSUPPORTED"))
        assertTrue(VaultErrors.message("VAULT_WRONG_CREDENTIALS", context = VaultErrors.Context.BACKUP).contains("旧密码"))
        assertTrue(VaultErrors.message("VAULT_EXISTS", context = VaultErrors.Context.EXPORT).contains("文件"))
    }

    @Test
    fun rateLimitedShowsTheCountdown() {
        assertEquals("尝试次数过多，请 30 秒后再试", VaultErrors.message("RATE_LIMITED", 30))
        assertEquals("尝试次数过多，请 2 分 05 秒后再试", VaultErrors.message("RATE_LIMITED", 125))
        assertEquals("尝试次数过多，请稍后再试", VaultErrors.message("RATE_LIMITED", 0))
    }

    @Test
    fun otherCodesFallBackToTheGenericTable() {
        assertEquals(ErrorMessages.forCode("NO_TAILNET"), VaultErrors.message("NO_TAILNET"))
        assertTrue(VaultErrors.message("WHATEVER").contains("WHATEVER"))
        assertTrue(VaultErrors.meansLocked("VAULT_LOCKED"))
        assertFalse(VaultErrors.meansLocked("VAULT_BROKEN"))
        assertTrue(VaultErrors.isWrongCredentials("VAULT_WRONG_CREDENTIALS"))
    }

    @Test
    fun genericTableKnowsVaultCodesToo() {
        assertEquals("密码库已锁定，请先解锁", ErrorMessages.forCode("VAULT_LOCKED"))
        assertTrue(ErrorMessages.forCode("VAULT_BROKEN").contains("重新解锁"))
    }
}
