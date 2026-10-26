package com.nightclip.app.logic.autofill

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class DatasetPlannerTest {
    private val web = Target.Web("example.com", "https", embedded = false)
    private fun login(): FormCluster = FormAnalyzer.analyze(listOf(node(0, hints = listOf("username")), node(1, hints = listOf("password"))))!!
    private fun otp(): FormCluster = FormAnalyzer.analyze(listOf(node(0, hints = listOf("smsOTPCode"))))!!

    @Test fun atMostFiveDatasetsBestScoreFirst() {
        val ms = (1..8).map { entry("e$it", score = if (it == 7) 3 else 2) }
        val plans = DatasetPlanner.plan(login(), web, ms, requireAuth = false)
        assertEquals(5, plans.datasets.size)
        assertEquals("e7", plans.datasets.first().entry.id)
        assertFalse(plans.offerOpenVault)
    }

    @Test fun onlyTheFieldsThePageAsksFor() {
        val form = FormAnalyzer.analyze(listOf(node(0, hints = listOf("password"))))!! // no username field
        val p = DatasetPlanner.plan(form, web, listOf(entry("a", username = "me")), false).datasets.single()
        assertEquals(listOf(FieldFill(0, ValueSource.PASSWORD)), p.fills)
    }

    @Test fun usernameAndPasswordFill() {
        val p = DatasetPlanner.plan(login(), web, listOf(entry("a")), false).datasets.single()
        assertEquals(listOf(FieldFill(0, ValueSource.USERNAME), FieldFill(1, ValueSource.PASSWORD)), p.fills)
        assertEquals(FillMode.INLINE, p.mode)
    }

    @Test fun entryWithoutUsernameOnlyFillsThePassword() {
        val p = DatasetPlanner.plan(login(), web, listOf(entry("a", username = "")), false).datasets.single()
        assertEquals(listOf(FieldFill(1, ValueSource.PASSWORD)), p.fills)
    }

    @Test fun newPasswordFieldsAreNeverFilled() {
        val form = FormAnalyzer.analyze(listOf(node(0, hints = listOf("password")), node(1, hints = listOf("newPassword")), node(2, hints = listOf("newPassword"))))!!
        val p = DatasetPlanner.plan(form, web, listOf(entry("a")), false).datasets.single()
        assertEquals(listOf(FieldFill(0, ValueSource.PASSWORD)), p.fills)
    }

    @Test fun signupGetsNoCredentialDatasets() {
        val form = FormAnalyzer.analyze(listOf(node(0, hints = listOf("username")), node(1, hints = listOf("newPassword"))))!!
        val plans = DatasetPlanner.plan(form, web, listOf(entry("a")), false)
        assertTrue(plans.datasets.isEmpty())
    }

    @Test fun noMatchesOffersOpenVault() {
        val plans = DatasetPlanner.plan(login(), web, emptyList(), false)
        assertTrue(plans.datasets.isEmpty())
        assertTrue(plans.offerOpenVault)
    }

    @Test fun openVaultIsNeverOfferedAlongsideMatches() {
        assertFalse(DatasetPlanner.plan(login(), web, listOf(entry("a")), false).offerOpenVault)
    }

    @Test fun noOpenVaultRowOnACodePage() {
        assertFalse(DatasetPlanner.plan(otp(), web, emptyList(), false).offerOpenVault)
    }

    @Test fun totpOnlyForEntriesThatHaveIt() {
        val plans = DatasetPlanner.plan(otp(), web, listOf(entry("a", totp = false), entry("b", totp = true)), false)
        assertEquals(listOf("b"), plans.datasets.map { it.entry.id })
        assertEquals(listOf(FieldFill(0, ValueSource.TOTP)), plans.datasets.single().fills)
        assertEquals(FillMode.DEFERRED_SILENT, plans.datasets.single().mode)
        assertTrue(plans.datasets.single().usesTotp)
    }

    @Test fun duplicateIdsAreOfferedOnce() {
        assertEquals(1, DatasetPlanner.plan(login(), web, listOf(entry("a"), entry("a")), false).datasets.size)
    }

    @Test fun notesNeverGivePasswords() {
        val form = FormAnalyzer.analyze(listOf(node(0, hints = listOf("password"))))!!
        assertTrue(DatasetPlanner.plan(form, web, listOf(entry("n", kind = "note")), false).datasets.isEmpty())
    }

    // ---- require-auth policy -------------------------------------------------------------------

    @Test fun requireAuthMakesEveryDatasetVerifyFirst() {
        val p = DatasetPlanner.plan(login(), web, listOf(entry("a")), requireAuth = true).datasets.single()
        assertEquals(FillMode.DEFERRED_VERIFY, p.mode)
    }

    @Test fun embeddedWebViewAlwaysVerifies() {
        val t = Target.Web("example.com", "https", embedded = true)
        assertEquals(FillMode.DEFERRED_VERIFY, DatasetPlanner.plan(login(), t, listOf(entry("a")), false).datasets.single().mode)
    }

    @Test fun verifyBeatsSilentTotp() {
        assertEquals(FillMode.DEFERRED_VERIFY, AuthPolicy.mode(requireAuth = true, embeddedWeb = false, hasTotpFill = true))
    }

    @Test fun afterAFreshUnlockVerifyDatasetsCarryValues() {
        assertEquals(FillMode.INLINE, AuthPolicy.afterFreshUnlock(FillMode.DEFERRED_VERIFY))
        assertEquals(FillMode.DEFERRED_SILENT, AuthPolicy.afterFreshUnlock(FillMode.DEFERRED_SILENT))
        assertEquals(FillMode.INLINE, AuthPolicy.afterFreshUnlock(FillMode.INLINE))
    }

    @Test fun totpWaitsOnlyNearTheRollover() {
        assertEquals(0L, TotpWait.delayMs(30))
        assertEquals(0L, TotpWait.delayMs(3))
        assertEquals(1400L, TotpWait.delayMs(1))
        assertEquals(400L, TotpWait.delayMs(0))
    }
}
