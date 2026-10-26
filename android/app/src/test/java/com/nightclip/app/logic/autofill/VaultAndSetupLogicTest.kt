package com.nightclip.app.logic.autofill

import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class VaultAndSetupLogicTest {
    // ---- quick unlock policy --------------------------------------------------------------------

    private val hour = 3_600_000L
    private fun provider(available: Boolean) = object : QuickUnlockProvider {
        override val available = available
        override suspend fun obtainMasterPassword(host: Any): String? = null
    }
    private val good = AutofillQuickUnlockState(lastMasterAtMs = 1_000 * hour, lastMasterElapsedMs = 100, consecutiveFailures = 0)

    @Test fun defaultProviderNeverQuickUnlocks() {
        assertFalse(QuickUnlockRegistry.provider.available)
        assertEquals(AutofillQuickUnlockPolicy.Decision.ASK_PASSWORD, AutofillQuickUnlockPolicy.decide(QuickUnlockRegistry.provider, good, 1_001 * hour, 200))
    }

    @Test fun availableProviderWithinPolicyIsUsed() =
        assertEquals(AutofillQuickUnlockPolicy.Decision.USE_QUICK, AutofillQuickUnlockPolicy.decide(provider(true), good, 1_001 * hour, 200))

    @Test fun after72HoursThePasswordIsNeeded() {
        assertEquals(AutofillQuickUnlockPolicy.Decision.USE_QUICK, AutofillQuickUnlockPolicy.decide(provider(true), good, 1_072 * hour, 200))
        assertEquals(AutofillQuickUnlockPolicy.Decision.ASK_PASSWORD, AutofillQuickUnlockPolicy.decide(provider(true), good, 1_072 * hour + 1, 200))
    }

    @Test fun afterARestartThePasswordIsNeeded() =
        assertEquals(AutofillQuickUnlockPolicy.Decision.ASK_PASSWORD, AutofillQuickUnlockPolicy.decide(provider(true), good, 1_001 * hour, 50))

    @Test fun afterFiveFailuresThePasswordIsNeeded() {
        assertEquals(AutofillQuickUnlockPolicy.Decision.USE_QUICK, AutofillQuickUnlockPolicy.decide(provider(true), good.copy(consecutiveFailures = 4), 1_001 * hour, 200))
        assertEquals(AutofillQuickUnlockPolicy.Decision.ASK_PASSWORD, AutofillQuickUnlockPolicy.decide(provider(true), good.copy(consecutiveFailures = 5), 1_001 * hour, 200))
    }

    @Test fun neverTypedMasterOrClockGoingBackwardsNeedsThePassword() {
        assertEquals(AutofillQuickUnlockPolicy.Decision.ASK_PASSWORD, AutofillQuickUnlockPolicy.decide(provider(true), good.copy(lastMasterAtMs = 0), 1_001 * hour, 200))
        assertEquals(AutofillQuickUnlockPolicy.Decision.ASK_PASSWORD, AutofillQuickUnlockPolicy.decide(provider(true), good, 999 * hour, 200))
    }

    // ---- setup ----------------------------------------------------------------------------------

    @Test fun setupStates() {
        assertEquals(AutofillSetupLogic.State.UNSUPPORTED, AutofillSetupLogic.state(false, true))
        assertEquals(AutofillSetupLogic.State.NOT_ENABLED, AutofillSetupLogic.state(true, false))
        assertEquals(AutofillSetupLogic.State.ENABLED, AutofillSetupLogic.state(true, true))
    }

    @Test fun packageUriForTheSystemDialog() = assertEquals("package:com.nightclip.app", AutofillSetupLogic.packageUri("com.nightclip.app"))

    @Test fun serviceIdParsing() {
        val pkg = "com.nightclip.app"
        assertTrue(AutofillSetupLogic.isOurServiceId("com.nightclip.app/com.nightclip.app.autofill.VaultAutofillService", pkg))
        assertTrue(AutofillSetupLogic.isOurServiceId("com.nightclip.app/.autofill.VaultAutofillService", pkg))
        assertFalse(AutofillSetupLogic.isOurServiceId("com.other/com.other.Service", pkg))
        assertFalse(AutofillSetupLogic.isOurServiceId("com.nightclip.app/com.nightclip.app.Other", pkg))
        assertFalse(AutofillSetupLogic.isOurServiceId(null, pkg))
        assertFalse(AutofillSetupLogic.isOurServiceId("", pkg))
        assertFalse(AutofillSetupLogic.isOurServiceId("nonsense", pkg))
    }

    // ---- parsers and errors ---------------------------------------------------------------------

    @Test fun statusParser() {
        val s = VaultParsers.status(JSONObject("""{"exists":true,"unlocked":false,"retry_after_s":7,"entries":3}"""))
        assertEquals(VaultStatus(true, false, 7, 3), s)
        assertEquals(VaultStatus(false, false, 0, 0), VaultParsers.status(JSONObject()))
    }

    @Test fun matchParserKeepsOrderAndSkipsBrokenItems() {
        val r = JSONObject(
            """{"matches":[{"id":"a","title":"A","username":"u","url":"https://x.com","kind":"login","has_totp":true,"score":3},{"title":"no id"},{"id":"b","score":2}]}""",
        )
        val m = VaultParsers.matches(r)
        assertEquals(listOf("a", "b"), m.map { it.id })
        assertTrue(m[0].hasTotp)
        assertEquals("login", m[1].kind)
        assertEquals(emptyList<MatchedEntry>(), VaultParsers.matches(JSONObject()))
    }

    @Test fun errorMessages() {
        assertTrue(VaultErrors.message("RATE_LIMITED", 12).contains("12"))
        assertTrue(VaultErrors.message("VAULT_WRONG_CREDENTIALS").contains("主密码"))
        assertTrue(VaultErrors.message("WHATEVER").contains("WHATEVER"))
    }

    @Test fun rateLimitCountdown() {
        assertEquals(5, RateLimit.remainingS(5, 0))
        assertEquals(3, RateLimit.remainingS(5, 2_000))
        assertEquals(0, RateLimit.remainingS(5, 9_000))
    }

    // ---- entry JSON ------------------------------------------------------------------------------

    @Test fun newLoginEntry() {
        val e = EntryJson.newLogin("  Example ", "me", "pw", " https://example.com ")
        assertEquals("login", e.getString("kind"))
        assertEquals("Example", e.getString("title"))
        assertEquals("https://example.com", e.getString("url"))
        assertEquals("pw", e.getString("password"))
    }

    private fun detail() = JSONObject(
        """{"id":"a","kind":"login","title":"T","username":"me","url":"https://x.com","notes":"n","tags":["t1"],"group_id":"g",
        "fields":[{"name":"Plain","value":"v","protected":false,"has_value":true},{"name":"Key","value":"","protected":true,"has_value":true},{"name":"Empty","value":"","protected":true,"has_value":false}],
        "has_totp":true,"expires_at":99}""",
    )

    @Test fun protectedFieldsToRevealAreOnlyTheOnesWithValues() =
        assertEquals(listOf("Key"), EntryJson.protectedFieldsToReveal(detail()))

    @Test fun updateKeepsEverythingButThePassword() {
        val e = EntryJson.withNewPassword(detail(), "new-pw", mapOf("Key" to "secret-key"), "JBSWY3DP")!!
        assertEquals("new-pw", e.getString("password"))
        assertEquals("me", e.getString("username"))
        assertEquals("n", e.getString("notes"))
        assertEquals("JBSWY3DP", e.getString("totp"))
        assertEquals(99, e.getLong("expires_at"))
        assertEquals("g", e.getString("group_id"))
        val fields: JSONArray = e.getJSONArray("fields")
        assertEquals(3, fields.length())
        assertEquals("v", fields.getJSONObject(0).getString("value"))
        assertEquals("secret-key", fields.getJSONObject(1).getString("value"))
        assertTrue(fields.getJSONObject(1).getBoolean("protected"))
        assertEquals("t1", e.getJSONArray("tags").getString(0))
    }

    @Test fun updateIsRefusedWhenAProtectedValueIsMissingSoNothingIsWiped() {
        assertNull(EntryJson.withNewPassword(detail(), "x", emptyMap(), "JBSWY3DP"))
    }

    @Test fun updateIsRefusedWhenTheTotpSecretIsMissing() {
        assertNull(EntryJson.withNewPassword(detail(), "x", mapOf("Key" to "k"), null))
    }

    @Test fun updateWithoutTotpSendsAnEmptyTotp() {
        val d = JSONObject("""{"kind":"login","title":"T","username":"u","url":"","fields":[],"has_totp":false}""")
        val e = EntryJson.withNewPassword(d, "p", emptyMap(), null)
        assertNotNull(e)
        assertEquals("", e!!.getString("totp"))
    }
}
