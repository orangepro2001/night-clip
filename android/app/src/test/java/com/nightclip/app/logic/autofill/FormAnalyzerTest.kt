package com.nightclip.app.logic.autofill

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class FormAnalyzerTest {
    private fun user(ref: Int, focused: Boolean = false) = node(ref, hints = listOf("username"), focused = focused)
    private fun pw(ref: Int, focused: Boolean = false) = node(ref, hints = listOf("password"), focused = focused)

    @Test fun loginFormUsernameThenPassword() {
        val c = FormAnalyzer.analyze(listOf(user(0), pw(1)))!!
        assertEquals(FormKind.LOGIN, c.kind)
        assertEquals(0, c.username?.ref)
        assertEquals(listOf(1), c.currentPasswords.map { it.ref })
        assertTrue(c.wantsCredentials)
        assertTrue(c.offersSave)
    }

    @Test fun passwordOnlyIsALogin() {
        val c = FormAnalyzer.analyze(listOf(pw(0)))!!
        assertEquals(FormKind.LOGIN, c.kind)
        assertNull(c.username)
    }

    @Test fun usernameIsTheOneImmediatelyBeforeThePassword() {
        val nodes = listOf(node(0, id = "login_hint_unused"), user(1), node(2, id = "random"), pw(3))
        val c = FormAnalyzer.analyze(nodes)!!
        assertEquals(1, c.username?.ref)
    }

    @Test fun twoUsernameLikeFieldsPickTheLastOneBeforeThePassword() {
        val nodes = listOf(node(0, hints = listOf("emailAddress")), user(1), pw(2))
        assertEquals(1, FormAnalyzer.analyze(nodes)!!.username?.ref)
    }

    @Test fun signupFormTwoUnlabelledPasswordsMeansNewAndConfirm() {
        val nodes = listOf(user(0), node(1, inputType = IT.TEXT_PASSWORD), node(2, inputType = IT.TEXT_PASSWORD))
        val c = FormAnalyzer.analyze(nodes)!!
        assertEquals(FormKind.SIGNUP, c.kind)
        assertEquals(listOf(1, 2), c.newPasswords.map { it.ref })
        assertTrue(!c.wantsCredentials)
        assertEquals(1, c.passwordToSave?.ref)
    }

    @Test fun changePasswordThreeUnlabelledPasswords() {
        val nodes = (0..2).map { node(it, inputType = IT.TEXT_PASSWORD) }
        val c = FormAnalyzer.analyze(nodes)!!
        assertEquals(FormKind.CHANGE_PASSWORD, c.kind)
        assertEquals(listOf(0), c.currentPasswords.map { it.ref })
        assertEquals(listOf(1, 2), c.newPasswords.map { it.ref })
        assertEquals(1, c.passwordToSave?.ref)
        assertTrue(c.wantsCredentials)
    }

    @Test fun explicitNewPasswordHintsMakeASignup() {
        val nodes = listOf(user(0), node(1, hints = listOf("newPassword")))
        assertEquals(FormKind.SIGNUP, FormAnalyzer.analyze(nodes)!!.kind)
    }

    @Test fun currentPlusExplicitNewIsChangePassword() {
        val nodes = listOf(pw(0), node(1, hints = listOf("newPassword")))
        assertEquals(FormKind.CHANGE_PASSWORD, FormAnalyzer.analyze(nodes)!!.kind)
    }

    @Test fun strongUsernameAloneIsAMultiStepLogin() {
        val c = FormAnalyzer.analyze(listOf(user(0)))!!
        assertEquals(FormKind.USERNAME_STEP, c.kind)
        assertTrue(c.wantsCredentials)
        assertTrue(!c.offersSave)
    }

    @Test fun weakEmailBoxAloneIsNotALoginForm() {
        assertNull(FormAnalyzer.analyze(listOf(node(0, inputType = IT.TEXT_EMAIL))))
    }

    @Test fun weakEmailBoxBeforeAPasswordIsTheUsername() {
        val c = FormAnalyzer.analyze(listOf(node(0, inputType = IT.TEXT_EMAIL), pw(1)))!!
        assertEquals(0, c.username?.ref)
    }

    @Test fun otpOnlyPage() {
        val c = FormAnalyzer.analyze(listOf(node(0, hints = listOf("smsOTPCode"))))!!
        assertEquals(FormKind.OTP, c.kind)
        assertEquals(listOf(0), c.oneTimeCodes.map { it.ref })
    }

    @Test fun inlineOtpAfterPasswordStaysInTheLogin() {
        val c = FormAnalyzer.analyze(listOf(user(0), pw(1), node(2, hints = listOf("smsOTPCode"))))!!
        assertEquals(FormKind.LOGIN, c.kind)
        assertEquals(listOf(2), c.oneTimeCodes.map { it.ref })
    }

    @Test fun creditCardFieldsNeverFormAForm() {
        assertNull(FormAnalyzer.analyze(listOf(node(0, hints = listOf("creditCardNumber")), node(1, id = "cvv"))))
    }

    @Test fun searchPageIsNotAForm() {
        assertNull(FormAnalyzer.analyze(listOf(node(0, id = "search", hint = "Search"))))
    }

    @Test fun cardFieldBetweenUsernameAndPasswordDoesNotBreakTheForm() {
        val nodes = listOf(user(0), node(1, id = "card_number"), pw(2))
        assertEquals(0, FormAnalyzer.analyze(nodes)!!.username?.ref)
    }

    // ---- focus and clusters --------------------------------------------------------------------

    @Test fun loginAndSignupSideBySideFocusPicksTheRightOne() {
        val nodes = listOf(
            user(0), pw(1),
            user(2), node(3, hints = listOf("newPassword"), focused = true),
        )
        val c = FormAnalyzer.analyze(nodes)!!
        assertEquals(FormKind.SIGNUP, c.kind)
        assertEquals(2, c.username?.ref)
    }

    @Test fun focusOnTheLoginPartPicksTheLogin() {
        val nodes = listOf(user(0), pw(1, focused = true), user(2), node(3, hints = listOf("newPassword")))
        assertEquals(FormKind.LOGIN, FormAnalyzer.analyze(nodes)!!.kind)
    }

    @Test fun noFocusPrefersTheLogin() {
        val nodes = listOf(user(0), node(1, hints = listOf("newPassword")), user(2), pw(3))
        val c = FormAnalyzer.analyze(nodes)!!
        assertEquals(FormKind.LOGIN, c.kind)
        assertEquals(2, c.username?.ref)
    }

    @Test fun focusOnAnUnrelatedFieldOffersNothing() {
        val nodes = listOf(user(0), pw(1), node(2, id = "comment", focused = true))
        assertNull(FormAnalyzer.analyze(nodes))
    }

    @Test fun manualRequestIgnoresAnUnrelatedFocus() {
        val nodes = listOf(user(0), pw(1), node(2, id = "comment", focused = true))
        assertNotNull(FormAnalyzer.analyze(nodes, manual = true))
    }

    @Test fun fieldsOfAnotherContextAreNeverMixedIn() {
        val a = node(0, domain = "a.example", scheme = "https", hints = listOf("username"), pkg = CHROME)
        val b = node(1, domain = "b.example", scheme = "https", hints = listOf("password"), pkg = CHROME, focused = true)
        val c = FormAnalyzer.analyze(listOf(a, b))!!
        assertNull(c.username)
        assertEquals(listOf(1), c.fields.map { it.ref })
    }

    @Test fun differentSchemeIsADifferentContext() {
        val a = node(0, domain = "a.example", scheme = "http", hints = listOf("username"), pkg = CHROME)
        val b = node(1, domain = "a.example", scheme = "https", hints = listOf("password"), pkg = CHROME)
        assertEquals(2, FormAnalyzer.clusters(listOf(a, b)).size)
    }

    @Test fun differentPackageIsADifferentContext() {
        val a = node(0, pkg = "com.a", hints = listOf("username"))
        val b = node(1, pkg = "com.b", hints = listOf("password"))
        assertEquals(2, FormAnalyzer.clusters(listOf(a, b)).size)
    }

    @Test fun invisibleSecondPasswordDoesNotTurnALoginIntoASignup() {
        val nodes = listOf(user(0), node(1, inputType = IT.TEXT_PASSWORD), node(2, inputType = IT.TEXT_PASSWORD, visible = false))
        assertEquals(FormKind.LOGIN, FormAnalyzer.analyze(nodes)!!.kind)
    }

    @Test fun webLoginFormFromHtmlAttributes() {
        val nodes = listOf(web(0, type = "email", autocomplete = "username"), web(1, type = "password", autocomplete = "current-password", focused = true))
        val c = FormAnalyzer.analyze(nodes)!!
        assertEquals(FormKind.LOGIN, c.kind)
        assertEquals(0, c.username?.ref)
    }

    @Test fun duplicatedPasswordFieldsWithConfirmNameAreSignup() {
        val nodes = listOf(web(0, type = "text", name = "username"), web(1, type = "password", name = "password"), web(2, type = "password", name = "confirm_password"))
        assertEquals(FormKind.SIGNUP, FormAnalyzer.analyze(nodes)!!.kind)
    }

    @Test fun unlabelledPasswordAfterAnExplicitNewPasswordIsItsConfirmation() {
        val nodes = listOf(web(0, autocomplete = "username"), web(1, type = "password", autocomplete = "new-password"), web(2, type = "password"))
        val c = FormAnalyzer.analyze(nodes)!!
        assertEquals(FormKind.SIGNUP, c.kind)
        assertEquals(listOf(1, 2), c.newPasswords.map { it.ref })
    }

    @Test fun currentThenExplicitNewIsAChange() {
        val nodes = listOf(web(0, type = "password"), web(1, type = "password", autocomplete = "new-password"))
        val c = FormAnalyzer.analyze(nodes)!!
        assertEquals(FormKind.CHANGE_PASSWORD, c.kind)
        assertEquals(listOf(0), c.currentPasswords.map { it.ref })
    }

    @Test fun currentNewConfirmByNames() {
        val nodes = listOf(
            web(0, type = "password", name = "old_password"),
            web(1, type = "password", name = "new_password"),
            web(2, type = "password", name = "confirm_new_password"),
        )
        val c = FormAnalyzer.analyze(nodes)!!
        assertEquals(FormKind.CHANGE_PASSWORD, c.kind)
        assertEquals(listOf(0), c.currentPasswords.map { it.ref })
    }
}
