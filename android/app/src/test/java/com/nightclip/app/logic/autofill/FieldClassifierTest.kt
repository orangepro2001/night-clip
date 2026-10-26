package com.nightclip.app.logic.autofill

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class FieldClassifierTest {
    // ---- explicit autofill hints ----------------------------------------------------------------

    @Test fun hintUsername() = assertEquals(FieldRole.USERNAME, role(node(hints = listOf("username"))))
    @Test fun hintEmailAddressIsAWeakUsername() {
        val c = FieldClassifier.classify(node(hints = listOf("emailAddress")))
        assertEquals(FieldRole.USERNAME, c.role)
        assertTrue(c.weak)
    }
    @Test fun hintUsernameIsStrong() = assertFalse(FieldClassifier.classify(node(hints = listOf("username"))).weak)
    @Test fun hintPassword() = assertEquals(FieldRole.PASSWORD, role(node(hints = listOf("password"))))
    @Test fun hintNewPassword() = assertEquals(FieldRole.NEW_PASSWORD, role(node(hints = listOf("newPassword"))))
    @Test fun hintNewUsernameIsAUsername() = assertEquals(FieldRole.USERNAME, role(node(hints = listOf("newUsername"))))
    @Test fun hintSmsOtpIsOneTimeCode() = assertEquals(FieldRole.ONE_TIME_CODE, role(node(hints = listOf("smsOTPCode"))))
    @Test fun hintCreditCardIsIgnoredForFilling() = assertEquals(FieldRole.CREDIT_CARD, role(node(hints = listOf("creditCardNumber"))))
    @Test fun hintPostalCodeIsIgnored() = assertEquals(FieldRole.IGNORED, role(node(hints = listOf("postalCode"))))
    @Test fun explicitHintBeatsAMisleadingName() = assertEquals(FieldRole.PASSWORD, role(node(hints = listOf("password"), id = "search_box")))
    @Test fun genericPasswordHintIsRefinedByAConfirmName() =
        assertEquals(FieldRole.NEW_PASSWORD, role(node(hints = listOf("password"), id = "confirm_password")))

    // ---- HTML ----------------------------------------------------------------------------------

    @Test fun htmlTypePassword() = assertEquals(FieldRole.PASSWORD, role(web(0, type = "password")))
    @Test fun htmlAutocompleteCurrentPassword() = assertEquals(FieldRole.PASSWORD, role(web(0, autocomplete = "current-password")))
    @Test fun htmlAutocompleteNewPassword() = assertEquals(FieldRole.NEW_PASSWORD, role(web(0, type = "password", autocomplete = "new-password")))
    @Test fun htmlAutocompleteUsernameWithSectionTokens() =
        assertEquals(FieldRole.USERNAME, role(web(0, type = "text", autocomplete = "section-login shipping username webauthn")))
    @Test fun htmlAutocompleteOneTimeCode() = assertEquals(FieldRole.ONE_TIME_CODE, role(web(0, type = "text", autocomplete = "one-time-code")))
    @Test fun htmlAutocompleteCcNumberIsCard() = assertEquals(FieldRole.CREDIT_CARD, role(web(0, autocomplete = "cc-number")))
    @Test fun htmlAutocompleteOffDoesNotHideAPasswordField() = assertEquals(FieldRole.PASSWORD, role(web(0, type = "password", autocomplete = "off")))
    @Test fun htmlTypeSearchIsIgnored() = assertEquals(FieldRole.IGNORED, role(web(0, type = "search", name = "q")))
    @Test fun htmlRoleSearchboxIsIgnored() =
        assertEquals(FieldRole.IGNORED, role(node(tag = "input", html = mapOf("role" to "searchbox", "type" to "text"), domain = "a.com", pkg = CHROME)))
    @Test fun htmlTypeEmailIsAWeakUsername() {
        val c = FieldClassifier.classify(web(0, type = "email"))
        assertEquals(FieldRole.USERNAME, c.role)
        assertTrue(c.weak)
    }
    @Test fun htmlTypeEmailNamedLoginIsStrong() = assertFalse(FieldClassifier.classify(web(0, type = "email", name = "login")).weak)
    @Test fun htmlTypeCheckboxIsIgnored() = assertEquals(FieldRole.IGNORED, role(web(0, type = "checkbox", name = "remember_password")))
    @Test fun htmlTypeHiddenIsIgnored() = assertEquals(FieldRole.IGNORED, role(web(0, type = "hidden", name = "password")))
    @Test fun htmlPlainTextWithNameUser() = assertEquals(FieldRole.USERNAME, role(web(0, type = "text", name = "user_name")))
    @Test fun htmlNumberWithOtpNameIsOneTimeCode() = assertEquals(FieldRole.ONE_TIME_CODE, role(web(0, type = "number", name = "otp")))
    @Test fun htmlTelWithOtpNameIsOneTimeCode() = assertEquals(FieldRole.ONE_TIME_CODE, role(web(0, type = "tel", id = "totp-code")))

    // ---- inputType flags -----------------------------------------------------------------------

    @Test fun inputTypePassword() = assertEquals(FieldRole.PASSWORD, role(node(inputType = IT.TEXT_PASSWORD)))
    @Test fun inputTypeVisiblePassword() = assertEquals(FieldRole.PASSWORD, role(node(inputType = IT.TEXT_VISIBLE_PASSWORD)))
    @Test fun inputTypeWebPassword() = assertEquals(FieldRole.PASSWORD, role(node(inputType = IT.TEXT_WEB_PASSWORD)))
    @Test fun inputTypeNumberPasswordIsAPin() = assertEquals(FieldRole.PASSWORD, role(node(inputType = IT.NUMBER_PASSWORD)))
    @Test fun inputTypeNumberPasswordNamedCodeIsOtp() = assertEquals(FieldRole.ONE_TIME_CODE, role(node(inputType = IT.NUMBER_PASSWORD, id = "otp_input")))
    @Test fun inputTypeEmailIsWeakUsername() {
        val c = FieldClassifier.classify(node(inputType = IT.TEXT_EMAIL))
        assertEquals(FieldRole.USERNAME, c.role)
        assertTrue(c.weak)
        assertEquals(Evidence.INPUT_TYPE, c.evidence)
    }
    @Test fun inputTypeWebEmail() = assertEquals(FieldRole.USERNAME, role(node(inputType = IT.TEXT_WEB_EMAIL)))
    @Test fun inputTypePhoneIsWeakUsername() = assertTrue(FieldClassifier.classify(node(inputType = IT.PHONE)).weak)
    @Test fun inputTypePasswordWithNewNameIsNewPassword() = assertEquals(FieldRole.NEW_PASSWORD, role(node(inputType = IT.TEXT_PASSWORD, id = "new_password")))
    @Test fun inputTypePasswordWithOldNameIsCurrent() {
        val c = FieldClassifier.classify(node(inputType = IT.TEXT_PASSWORD, id = "old_password"))
        assertEquals(FieldRole.PASSWORD, c.role)
        assertFalse(c.ambiguousPassword)
    }
    @Test fun plainPasswordWithoutMarkersIsAmbiguous() = assertTrue(FieldClassifier.classify(node(inputType = IT.TEXT_PASSWORD)).ambiguousPassword)
    @Test fun plainTextWithNoWordsIsIgnored() = assertEquals(FieldRole.IGNORED, role(node(inputType = IT.TEXT, id = "field_17")))

    // ---- names in several languages ------------------------------------------------------------

    @Test fun nameEnglishUsername() = assertEquals(FieldRole.USERNAME, role(node(id = "etUsername")))
    @Test fun nameEnglishLoginId() = assertEquals(FieldRole.USERNAME, role(node(id = "login_id")))
    @Test fun nameEnglishEmail() = assertEquals(FieldRole.USERNAME, role(node(hint = "Email address")))
    @Test fun nameChineseUsername() = assertEquals(FieldRole.USERNAME, role(node(hint = "请输入用户名")))
    @Test fun nameChineseAccount() = assertEquals(FieldRole.USERNAME, role(node(hint = "手机号/邮箱/账号")))
    @Test fun nameChineseEmail() = assertEquals(FieldRole.USERNAME, role(node(hint = "邮箱")))
    @Test fun nameChinesePassword() = assertEquals(FieldRole.PASSWORD, role(node(hint = "请输入密码")))
    @Test fun nameChineseConfirmPassword() = assertEquals(FieldRole.NEW_PASSWORD, role(node(hint = "确认密码")))
    @Test fun nameChineseNewPassword() = assertEquals(FieldRole.NEW_PASSWORD, role(node(hint = "新密码")))
    @Test fun nameChineseCurrentPassword() = assertFalse(FieldClassifier.classify(node(hint = "当前密码")).ambiguousPassword)
    @Test fun nameChineseDynamicPasswordIsOtp() = assertEquals(FieldRole.ONE_TIME_CODE, role(node(hint = "动态口令")))
    @Test fun nameJapanesePassword() = assertEquals(FieldRole.PASSWORD, role(node(hint = "パスワード")))
    @Test fun nameJapaneseUser() = assertEquals(FieldRole.USERNAME, role(node(hint = "ユーザー名")))
    @Test fun nameKoreanPassword() = assertEquals(FieldRole.PASSWORD, role(node(hint = "비밀번호")))
    @Test fun nameGermanPassword() = assertEquals(FieldRole.PASSWORD, role(node(id = "edit_passwort")))
    @Test fun nameGermanUser() = assertEquals(FieldRole.USERNAME, role(node(id = "benutzername")))
    @Test fun nameSpanishPassword() = assertEquals(FieldRole.PASSWORD, role(node(hint = "Contraseña")))
    @Test fun nameFrenchPassword() = assertEquals(FieldRole.PASSWORD, role(node(hint = "Mot de passe")))
    @Test fun nameRussianPassword() = assertEquals(FieldRole.PASSWORD, role(node(hint = "Пароль")))
    @Test fun nameRussianLogin() = assertEquals(FieldRole.USERNAME, role(node(hint = "Логин")))
    @Test fun namePortuguesePassword() = assertEquals(FieldRole.PASSWORD, role(node(hint = "Senha")))
    @Test fun nameShortPwdToken() = assertEquals(FieldRole.PASSWORD, role(node(id = "pwd")))
    @Test fun nameCamelCasePassword() = assertEquals(FieldRole.PASSWORD, role(node(id = "loginPassword")))
    @Test fun namePassportIsNotAPassword() = assertEquals(FieldRole.IGNORED, role(node(id = "passport_number")))
    @Test fun namePassengerIsNotAPassword() = assertEquals(FieldRole.IGNORED, role(node(hint = "Passenger name")))
    @Test fun nameRepeatPasswordIsNew() = assertEquals(FieldRole.NEW_PASSWORD, role(node(id = "repeat_password")))
    @Test fun nameNumberedPasswordSplits() = assertEquals(FieldRole.PASSWORD, role(node(id = "password2")))
    @Test fun nameVerificationCodeIsOtp() = assertEquals(FieldRole.ONE_TIME_CODE, role(node(hint = "Verification code")))
    @Test fun nameAuthenticatorIsOtp() = assertEquals(FieldRole.ONE_TIME_CODE, role(node(hint = "Authenticator app code")))

    // ---- things that must not be filled ---------------------------------------------------------

    @Test fun searchBoxIsIgnored() = assertEquals(FieldRole.IGNORED, role(node(id = "search_input", hint = "Search")))
    @Test fun chineseSearchBoxIsIgnored() = assertEquals(FieldRole.IGNORED, role(node(hint = "搜索")))
    @Test fun searchByEmailTypeStaysIgnored() = assertEquals(FieldRole.IGNORED, role(node(inputType = IT.TEXT_EMAIL, id = "search_email_box")))
    @Test fun cardNumberIsCard() = assertEquals(FieldRole.CREDIT_CARD, role(node(id = "card_number")))
    @Test fun cvvIsCard() = assertEquals(FieldRole.CREDIT_CARD, role(node(hint = "CVV")))
    @Test fun chineseBankCardIsCard() = assertEquals(FieldRole.CREDIT_CARD, role(node(hint = "银行卡号")))
    @Test fun cardPinPasswordKeepsPassword() = assertEquals(FieldRole.PASSWORD, role(node(inputType = IT.TEXT_PASSWORD, id = "card_pin")))
    @Test fun captchaIsIgnored() = assertEquals(FieldRole.IGNORED, role(node(id = "captcha")))
    @Test fun invisibleFieldIsIgnored() = assertEquals(FieldRole.IGNORED, role(node(hints = listOf("password"), visible = false)))
    @Test fun nonTextNodeIsIgnored() = assertEquals(FieldRole.IGNORED, role(node(hints = listOf("password"), isText = false)))
    @Test fun firstNameIsIgnored() = assertEquals(FieldRole.IGNORED, role(node(id = "first_name")))
    @Test fun zipCodeIsIgnored() = assertEquals(FieldRole.IGNORED, role(node(id = "zip")))
}
