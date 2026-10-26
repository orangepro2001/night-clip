package com.nightclip.app.logic.autofill

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class TargetResolverTest {
    private val own = "com.nightclip.app"

    @Test fun nativeAppUsesItsPackage() {
        assertEquals(Target.App("com.example.shop"), TargetResolver.resolve(node(pkg = "com.example.shop"), own))
    }

    @Test fun ownPackageIsIgnored() {
        assertNull(TargetResolver.resolve(node(pkg = own), own))
        assertNull(TargetResolver.resolve(node(pkg = own, domain = "example.com", scheme = "https"), own))
    }

    @Test fun missingPackageIsNotGuessed() {
        assertNull(TargetResolver.resolve(node(pkg = null), own))
        assertNull(TargetResolver.resolve(node(pkg = " "), own))
    }

    @Test fun browserWithDomainUsesTheDomainExactlyAsReported() {
        val t = TargetResolver.resolve(node(pkg = CHROME, domain = "login.Example.com", scheme = "HTTPS"), own)
        assertEquals(Target.Web("login.example.com", "https", embedded = false), t)
    }

    @Test fun lookAlikeDomainsArePassedThroughUnchanged() {
        assertEquals("example.com.evil.net", (TargetResolver.resolve(node(pkg = CHROME, domain = "example.com.evil.net", scheme = "https"), own) as Target.Web).host)
        assertEquals("evil-example.com", (TargetResolver.resolve(node(pkg = CHROME, domain = "evil-example.com", scheme = "https"), own) as Target.Web).host)
        assertEquals("exarnple.com", (TargetResolver.resolve(node(pkg = CHROME, domain = "exarnple.com", scheme = "https"), own) as Target.Web).host)
    }

    @Test fun everyListedBrowserWithoutADomainGetsNothing() {
        for (p in listOf(
            "com.android.chrome", "org.mozilla.firefox", "org.mozilla.fenix", "com.microsoft.emmx", "com.brave.browser", "com.sec.android.app.sbrowser",
            "com.opera.browser", "com.vivaldi.browser", "com.duckduckgo.mobile.android", "com.kiwibrowser.browser", "org.mozilla.focus",
        )) {
            assertNull(p, TargetResolver.resolve(node(pkg = p), own))
        }
    }

    @Test fun unknownBrowserNamedPackageGetsNothing() {
        assertNull(TargetResolver.resolve(node(pkg = "com.vendor.superbrowser"), own))
    }

    @Test fun blankDomainOnABrowserCountsAsNoDomain() {
        assertNull(TargetResolver.resolve(node(pkg = CHROME, domain = " "), own))
    }

    @Test fun browserWithAnUnreadableDomainGetsNothingAndNoFallbackToThePackage() {
        assertNull(TargetResolver.resolve(node(pkg = CHROME, domain = "exa mple.com/evil", scheme = "https"), own))
        assertNull(TargetResolver.resolve(node(pkg = "com.example.shop", domain = "https://x.com/login", scheme = "https"), own))
    }

    @Test fun webViewInAnAppIsMarkedEmbedded() {
        val t = TargetResolver.resolve(node(pkg = "com.example.shop", domain = "example.com", scheme = "https"), own) as Target.Web
        assertTrue(t.embedded)
    }

    @Test fun unknownSchemeIsNotInvented() {
        val t = TargetResolver.resolve(node(pkg = CHROME, domain = "example.com", scheme = null), own) as Target.Web
        assertNull(t.scheme)
        val ftp = TargetResolver.resolve(node(pkg = CHROME, domain = "example.com", scheme = "ftp"), own) as Target.Web
        assertNull(ftp.scheme)
    }

    @Test fun matchParams() {
        assertEquals("https://example.com" to null, TargetResolver.matchParams(Target.Web("example.com", "https", false)))
        assertEquals("example.com" to null, TargetResolver.matchParams(Target.Web("example.com", null, false)))
        assertEquals(null to "com.example.shop", TargetResolver.matchParams(Target.App("com.example.shop")))
    }

    @Test fun localAndIpHosts() {
        assertTrue(TargetResolver.isLocalOrIpLiteral("localhost"))
        assertTrue(TargetResolver.isLocalOrIpLiteral("app.localhost"))
        assertTrue(TargetResolver.isLocalOrIpLiteral("192.168.1.10"))
        assertTrue(TargetResolver.isLocalOrIpLiteral("[::1]"))
        assertFalse(TargetResolver.isLocalOrIpLiteral("localhost.evil.com"))
        assertFalse(TargetResolver.isLocalOrIpLiteral("1.2.3.4.evil.com"))
        assertFalse(TargetResolver.isLocalOrIpLiteral("example.com"))
        assertFalse(TargetResolver.isLocalOrIpLiteral("999.1.1.1"))
    }

    // ---- scheme policy -------------------------------------------------------------------------

    private val httpsEntry = entry("a", url = "https://example.com/login", score = 3)
    private val httpEntry = entry("b", url = "http://example.com/login", score = 3)

    @Test fun httpsPageGetsHttpsAndHttpEntries() {
        val t = Target.Web("example.com", "https", false)
        assertEquals(listOf("a", "b"), SchemePolicy.allowed(t, listOf(httpsEntry, httpEntry)).map { it.id })
    }

    @Test fun httpPageNeverGetsAnHttpsRegisteredEntry() {
        val t = Target.Web("example.com", "http", false)
        assertEquals(listOf("b"), SchemePolicy.allowed(t, listOf(httpsEntry, httpEntry)).map { it.id })
    }

    @Test fun pageWithoutASchemeIsTreatedLikeHttp() {
        val t = Target.Web("example.com", null, false)
        assertEquals(listOf("b"), SchemePolicy.allowed(t, listOf(httpsEntry, httpEntry)).map { it.id })
    }

    @Test fun httpLocalhostAndIpLiteralsAreFine() {
        assertEquals(1, SchemePolicy.allowed(Target.Web("localhost", "http", false), listOf(entry("l", url = "https://localhost"))).size)
        assertEquals(1, SchemePolicy.allowed(Target.Web("192.168.0.2", "http", false), listOf(entry("l", url = "https://192.168.0.2"))).size)
    }

    @Test fun webTargetsNeverTakeAppScoredMatches() {
        assertEquals(emptyList<MatchedEntry>(), SchemePolicy.allowed(Target.Web("example.com", "https", false), listOf(entry("x", score = 4))))
    }

    @Test fun appTargetsOnlyTakeAppScoredMatches() {
        val t = Target.App("com.example.shop")
        assertEquals(listOf("x"), SchemePolicy.allowed(t, listOf(entry("x", score = 4), entry("y", score = 2))).map { it.id })
    }

    @Test fun cleanHostAcceptsHostsAndRejectsEverythingElse() {
        assertEquals("example.com", TargetResolver.cleanHost(" Example.COM. "))
        assertEquals("[::1]", TargetResolver.cleanHost("[::1]"))
        assertEquals("localhost", TargetResolver.cleanHost("localhost"))
        assertNull(TargetResolver.cleanHost(""))
        assertNull(TargetResolver.cleanHost(null))
        assertNull(TargetResolver.cleanHost("a b.com"))
        assertNull(TargetResolver.cleanHost("exam@ple.com"))
        assertNull(TargetResolver.cleanHost("example.com/path"))
        assertNull(TargetResolver.cleanHost("-example.com"))
        assertNull(TargetResolver.cleanHost("a".repeat(300)))
    }
}
