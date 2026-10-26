package com.nightclip.app.logic.autofill

import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class SaveLogicTest {
    private val web = Target.Web("example.com", "https", false)
    private fun cand(user: String = "me", pw: String = "pw1") = SaveCandidate(user, pw, web, "https://example.com", "example.com")

    private fun decide(c: SaveCandidate, ms: List<MatchedEntry>, same: (MatchedEntry) -> Boolean = { false }) =
        runBlocking { SaveDecider.decide(c, ms) { same(it) } }

    // ---- decisions ------------------------------------------------------------------------------

    @Test fun noMatchMeansNewEntry() = assertEquals(SaveDecision.New, decide(cand(), emptyList()))

    @Test fun emptyPasswordIsIgnored() = assertEquals(SaveDecision.Ignore, decide(cand(pw = ""), emptyList()))

    @Test fun sameAccountWithTheSamePasswordIsIgnored() =
        assertEquals(SaveDecision.Ignore, decide(cand(), listOf(entry("a", username = "me")), same = { true }))

    @Test fun sameAccountWithAnotherPasswordOffersAnUpdate() {
        val e = entry("a", username = "me")
        assertEquals(SaveDecision.Update(e), decide(cand(), listOf(e)))
    }

    @Test fun usernameCompareIgnoresCaseAndSpaces() {
        val e = entry("a", username = "Me@Example.com")
        assertEquals(SaveDecision.Update(e), decide(cand(user = " me@example.com "), listOf(e)))
    }

    @Test fun anotherAccountOnTheSameSiteIsANewEntry() =
        assertEquals(SaveDecision.New, decide(cand(user = "other"), listOf(entry("a", username = "me"))))

    @Test fun apiKeysAndSecretsAreNeverUpdatedFromAForm() =
        assertEquals(SaveDecision.New, decide(cand(), listOf(entry("a", username = "me", kind = "apikey"))))

    @Test fun passwordIsOnlyComparedForTheSameAccount() {
        var asked = 0
        decide(cand(user = "other"), listOf(entry("a", username = "me"), entry("b", username = "other"))) { asked++; false }
        assertEquals(1, asked)
    }

    @Test fun anIdenticalEntryAmongSeveralWins() {
        val a = entry("a", username = "me")
        val b = entry("b", username = "me")
        assertEquals(SaveDecision.Ignore, decide(cand(), listOf(a, b)) { it.id == "b" })
    }

    @Test fun blankUsernameOnlyMatchesBlankUsername() {
        assertEquals(SaveDecision.New, decide(cand(user = ""), listOf(entry("a", username = "me"))))
        val e = entry("b", username = "")
        assertEquals(SaveDecision.Update(e), decide(cand(user = ""), listOf(e)))
    }

    @Test fun suggestions() {
        assertEquals("https://example.com", SaveDecider.suggestedUrl(Target.Web("example.com", "https", false)))
        assertEquals("http://localhost", SaveDecider.suggestedUrl(Target.Web("localhost", "http", false)))
        assertEquals("https://example.com", SaveDecider.suggestedUrl(Target.Web("example.com", null, false)))
        assertEquals("androidapp://com.example.shop", SaveDecider.suggestedUrl(Target.App("com.example.shop")))
        assertEquals("example.com", SaveDecider.suggestedTitle(Target.Web("www.example.com", "https", false), null))
        assertEquals("Shop", SaveDecider.suggestedTitle(Target.App("com.example.shop"), "Shop"))
        assertEquals("com.example.shop", SaveDecider.suggestedTitle(Target.App("com.example.shop"), " "))
    }

    @Test fun androidAppUrlRoundTripsThroughTheCoreConvention() {
        // the url we suggest is the one `vault.match` treats as an Android app (docs/vault-api.md §3.2b)
        val url = SaveDecider.suggestedUrl(Target.App("com.example.shop"))
        assertEquals("com.example.shop", url.removePrefix("androidapp://"))
    }

    @Test fun candidateNeverPrintsItsPassword() = assertFalse(cand(pw = "s3cret-value").toString().contains("s3cret-value"))

    // ---- pending values ------------------------------------------------------------------------

    @Test fun pendingValuesAreSingleUse() {
        val store = PendingSaveStore { 0L }
        val token = store.put(cand())
        assertNotNull(store.take(token))
        assertNull(store.take(token))
    }

    @Test fun pendingValuesExpireAfterTwoMinutes() {
        var now = 1_000L
        val store = PendingSaveStore { now }
        val token = store.put(cand())
        now += PendingSaveStore.TTL_MS - 1
        assertEquals(1, store.size())
        now += 1
        assertNull(store.take(token))
        assertEquals(0, store.size())
    }

    @Test fun ttlIsTwoMinutes() = assertEquals(120_000L, PendingSaveStore.TTL_MS)

    @Test fun unknownOrNullTokenGivesNothing() {
        val store = PendingSaveStore { 0L }
        store.put(cand())
        assertNull(store.take("nope"))
        assertNull(store.take(null))
    }

    @Test fun tokensAreUniqueAndLong() {
        val store = PendingSaveStore { 0L }
        val a = store.put(cand())
        val b = store.put(cand())
        assertNotEquals(a, b)
        assertEquals(24, a.length)
    }

    @Test fun expiredEntriesAreDroppedWhenSomethingNewArrives() {
        var now = 0L
        val store = PendingSaveStore { now }
        store.put(cand())
        now += PendingSaveStore.TTL_MS + 1
        store.put(cand())
        assertEquals(1, store.size())
    }

    @Test fun clearForgetsEverything() {
        val store = PendingSaveStore { 0L }
        val t = store.put(cand())
        store.clear()
        assertNull(store.take(t))
    }

    // ---- extracting a submitted login from the request --------------------------------------------

    private fun typedLogin(user: String, pw: String) = listOf(
        node(0, pkg = "com.example.shop", hints = listOf("username"), text = user),
        node(1, pkg = "com.example.shop", hints = listOf("password"), text = pw),
    )

    @Test fun extractsAnAppLogin() {
        val c = SaveExtractor.extract(listOf(typedLogin("me", "pw")), "com.nightclip.app") { "Shop" }!!
        assertEquals("me", c.username)
        assertEquals("pw", c.password)
        assertEquals(Target.App("com.example.shop"), c.target)
        assertEquals("androidapp://com.example.shop", c.suggestedUrl)
        assertEquals("Shop", c.suggestedTitle)
    }

    @Test fun extractsAWebLoginFromTheBrowserDomain() {
        val nodes = listOf(
            web(0, autocomplete = "username", domain = "login.example.com", text = "me"),
            web(1, type = "password", domain = "login.example.com", text = "pw"),
        )
        val c = SaveExtractor.extract(listOf(nodes), "com.nightclip.app")!!
        assertEquals(Target.Web("login.example.com", "https", false), c.target)
        assertEquals("https://login.example.com", c.suggestedUrl)
    }

    @Test fun nothingIsExtractedWithoutAPassword() {
        assertNull(SaveExtractor.extract(listOf(typedLogin("me", "")), "com.nightclip.app"))
    }

    @Test fun nothingIsExtractedFromOurOwnPackage() {
        val nodes = listOf(node(0, pkg = "com.nightclip.app", hints = listOf("password"), text = "pw"))
        assertNull(SaveExtractor.extract(listOf(nodes), "com.nightclip.app"))
    }

    @Test fun nothingIsExtractedFromABrowserWithoutADomain() {
        val nodes = listOf(node(0, pkg = CHROME, hints = listOf("password"), text = "pw"))
        assertNull(SaveExtractor.extract(listOf(nodes), "com.nightclip.app"))
    }

    @Test fun signupExtractsTheNewPasswordWhenConfirmationMatches() {
        val nodes = listOf(
            node(0, hints = listOf("username"), text = "me"),
            node(1, hints = listOf("newPassword"), text = "pw"),
            node(2, hints = listOf("newPassword"), text = "pw"),
        )
        assertEquals("pw", SaveExtractor.extract(listOf(nodes), "own")!!.password)
    }

    @Test fun signupWithMismatchedConfirmationIsNotSaved() {
        val nodes = listOf(
            node(0, hints = listOf("username"), text = "me"),
            node(1, hints = listOf("newPassword"), text = "pw1"),
            node(2, hints = listOf("newPassword"), text = "pw2"),
        )
        assertNull(SaveExtractor.extract(listOf(nodes), "own"))
    }

    @Test fun changePasswordSavesTheNewOneNotTheOldOne() {
        val nodes = listOf(
            node(0, hints = listOf("password"), text = "old"),
            node(1, hints = listOf("newPassword"), text = "new"),
        )
        assertEquals("new", SaveExtractor.extract(listOf(nodes), "own")!!.password)
    }

    @Test fun multiStepLoginTakesTheUsernameFromAnEarlierScreen() {
        val step1 = listOf(node(0, pkg = "com.example.shop", hints = listOf("username"), text = "me@x.com"))
        val step2 = listOf(node(0, pkg = "com.example.shop", hints = listOf("password"), text = "pw"))
        assertEquals("me@x.com", SaveExtractor.extract(listOf(step1, step2), "own")!!.username)
    }

    @Test fun anEarlierScreenOfAnotherAppIsNotUsedForTheUsername() {
        val step1 = listOf(node(0, pkg = "com.other", hints = listOf("username"), text = "someone"))
        val step2 = listOf(node(0, pkg = "com.example.shop", hints = listOf("password"), text = "pw"))
        assertEquals("", SaveExtractor.extract(listOf(step1, step2), "own")!!.username)
    }

    @Test fun aFormThatIsNotLoginLikeIsNotSaved() {
        val nodes = listOf(node(0, id = "comment", text = "hello"))
        assertNull(SaveExtractor.extract(listOf(nodes), "own"))
    }

    @Test fun usernameIsTrimmedButPasswordIsKeptExactly() {
        val c = SaveExtractor.extract(listOf(typedLogin("  me ", " pw ")), "own")!!
        assertEquals("me", c.username)
        assertEquals(" pw ", c.password)
        assertTrue(c.password.length == 4)
    }
}
