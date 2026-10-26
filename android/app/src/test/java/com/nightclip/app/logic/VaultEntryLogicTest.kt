package com.nightclip.app.logic

import com.nightclip.app.core.PasswordStrength
import com.nightclip.app.core.VaultAttachment
import com.nightclip.app.core.VaultEntryDetail
import com.nightclip.app.core.VaultField
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.LocalDate
import java.time.ZoneId

class EntryValidatorTest {
    private val ok = EntryDraft(title = "GitHub", username = "tony", password = "pw")

    @Test
    fun aMinimalEntryIsValid() {
        assertTrue(EntryValidator.validate(ok).isEmpty())
        assertTrue(EntryValidator.validate(EntryDraft(kind = "note", title = "n", notes = "text")).isEmpty())
    }

    @Test
    fun titleIsRequired() {
        val issues = EntryValidator.validate(ok.copy(title = "   "))
        assertEquals(listOf(DraftIssueField.TITLE), issues.map { it.field })
    }

    @Test
    fun textLimitsAreInUtf8Bytes() {
        val atLimit = "a".repeat(EntryValidator.MAX_TEXT_BYTES)
        assertTrue(EntryValidator.validate(ok.copy(notes = atLimit)).isEmpty())
        assertEquals(DraftIssueField.NOTES, EntryValidator.validate(ok.copy(notes = atLimit + "a")).single().field)
        // 3 bytes per character: 90 000 characters are 270 000 bytes, over the 262 144 limit
        assertEquals(DraftIssueField.PASSWORD, EntryValidator.validate(ok.copy(password = "密".repeat(90_000))).single().field)
        assertTrue(EntryValidator.validate(ok.copy(password = "密".repeat(87_381))).isEmpty())
    }

    @Test
    fun customFieldNames() {
        assertNull(EntryValidator.fieldNameProblem("Key ID"))
        assertNotNullProblem("")
        assertNotNullProblem("   ")
        assertNotNullProblem("password")
        assertNotNullProblem("UserName")
        assertNotNullProblem("OTP")
        assertNotNullProblem("kph:x")
        assertNotNullProblem("n".repeat(129))
        assertNull(EntryValidator.fieldNameProblem("n".repeat(128)))
        assertNotNullProblem("region", others = listOf("Region"))
    }

    private fun assertNotNullProblem(name: String, others: List<String> = emptyList()) =
        assertTrue("'$name'", EntryValidator.fieldNameProblem(name, others) != null)

    @Test
    fun fieldIssuesPointToTheirRow() {
        val d = ok.withField("Region", "eu").withField("region", "us").withField("URL", "x")
        val issues = EntryValidator.validate(d)
        assertEquals(2, issues.size)
        assertEquals(setOf(d.fields[1].key, d.fields[2].key), issues.map { it.fieldKey }.toSet())
        assertTrue(issues.all { it.field == DraftIssueField.FIELD_NAME })
    }

    @Test
    fun tooManyFieldsAndTags() {
        var d = ok
        repeat(101) { d = d.withField("f$it") }
        assertTrue(EntryValidator.validate(d).any { it.field == DraftIssueField.TOO_MANY })
        val tags = (1..101).joinToString(",") { "t$it" }
        assertTrue(EntryValidator.validate(ok.copy(tags = tags)).any { it.field == DraftIssueField.TOO_MANY })
        assertTrue(EntryValidator.validate(ok.copy(tags = (1..100).joinToString(",") { "t$it" })).isEmpty())
    }

    @Test
    fun expiryMustBeARealDate() {
        assertTrue(EntryValidator.validate(ok.copy(expires = "2027-01-31")).isEmpty())
        assertTrue(EntryValidator.validate(ok.copy(expires = "")).isEmpty())
        for (bad in listOf("2027-02-30", "31.01.2027", "2027-1-5", "soon", "2027-13-01")) {
            assertEquals(bad, DraftIssueField.EXPIRES, EntryValidator.validate(ok.copy(expires = bad)).single().field)
        }
    }

    @Test
    fun dirtyDetectionIgnoresTheKeyCounter() {
        val a = ok
        assertFalse(EntryValidator.isDirty(a, a.copy()))
        assertTrue(EntryValidator.isDirty(a, a.copy(title = "x")))
        assertTrue(EntryValidator.isDirty(a, a.withField("n")))
        assertFalse(EntryValidator.isDirty(a.withField("n"), a.withField("n")))
    }
}

class EntryDraftTest {
    @Test
    fun tagsSplitOnCommasSpacesAndDedupe() {
        assertEquals(listOf("a", "b", "c"), EntryDraft(tags = " a, b  c ,A,, ").tagList())
        assertEquals(listOf("工作", "重要"), EntryDraft(tags = "工作，重要").tagList())
        assertEquals(emptyList<String>(), EntryDraft().tagList())
    }

    @Test
    fun fieldHelpers() {
        var d = EntryDraft().withField("A", "1").withField("B", "2", protected = true)
        assertEquals(listOf(1L, 2L), d.fields.map { it.key })
        d = d.updateField(1) { it.copy(value = "x") }
        assertEquals("x", d.fields[0].value)
        d = d.removeField(1)
        assertEquals(listOf("B"), d.fields.map { it.name })
        assertEquals(3L, d.withField("C").fields.last().key)
    }

    @Test
    fun draftNeverPrintsSecrets() {
        val d = EntryDraft(password = "hunter2", notes = "secret").withField("k", "v2")
        assertFalse(d.toString().contains("hunter2"))
        assertFalse(d.toString().contains("secret"))
        assertFalse(d.fields[0].toString().contains("v2"))
    }

    @Test
    fun fromDetailFillsRevealedSecrets() {
        val detail = VaultEntryDetail(
            id = "e1", groupId = "g", kind = "apikey", title = "T", username = "", url = "https://u", notes = "n", tags = listOf("a", "b"),
            fields = listOf(VaultField("Key ID", "AK", false, true), VaultField("Secret", "", true, true)),
            hasPassword = true, hasTotp = false, expiresAt = ExpiryDates.parse("2027-01-31", ZoneId.of("UTC"))!!, createdAt = 1, modifiedAt = 2,
            attachments = listOf(VaultAttachment("a", 1)), historyCount = 0, inTrash = false, strength = PasswordStrength(10.0, "weak"),
        )
        val d = EntryDraft.fromDetail(detail, "pw", "", mapOf("Secret" to "s3"), ZoneId.of("UTC"))
        assertEquals("e1", d.id)
        assertEquals("pw", d.password)
        assertEquals("a, b", d.tags)
        assertEquals("2027-01-31", d.expires)
        assertEquals(listOf("AK", "s3"), d.fields.map { it.value })
        assertEquals(listOf(false, true), d.fields.map { it.protected })
        assertEquals("g", d.groupId)
    }
}

class ExpiryDatesTest {
    private val utc = ZoneId.of("UTC")

    @Test
    fun roundTrips() {
        val s = ExpiryDates.parse("2027-01-31", utc)!!
        assertEquals(1801439999L, s)
        assertEquals("2027-01-31", ExpiryDates.format(s, utc))
        assertEquals("", ExpiryDates.format(0, utc))
        assertEquals(0L, ExpiryDates.parse("", utc))
        assertEquals(0L, ExpiryDates.parse("  ", utc))
    }

    @Test
    fun zoneChangesTheInstantNotTheDate() {
        val tokyo = ZoneId.of("Asia/Tokyo")
        val s = ExpiryDates.parse("2027-01-31", tokyo)!!
        assertEquals("2027-01-31", ExpiryDates.format(s, tokyo))
    }

    @Test
    fun quickChips() {
        assertEquals("2026-11-25", ExpiryDates.inDays(30, LocalDate.of(2026, 10, 26)))
        assertEquals("2027-10-26", ExpiryDates.inDays(365, LocalDate.of(2026, 10, 26)))
    }
}

class KindTemplateTest {
    @Test
    fun apikeyTemplateMatchesTheSpec() {
        val t = KindTemplate.of("apikey")
        assertEquals("密钥", t.passwordLabel)
        assertEquals("接口地址", t.urlLabel)
        assertTrue(t.showUrl && t.showPassword)
        assertFalse(t.showUsername)
        assertEquals(listOf("Key ID", "Environment", "Region"), t.suggestedFields)
    }

    @Test
    fun loginSecretAndNote() {
        val login = KindTemplate.of("login")
        assertTrue(login.showUsername && login.showUrl && login.showPassword && login.showTotp)
        val secret = KindTemplate.of("secret")
        assertTrue(secret.showPassword)
        assertFalse(secret.showUsername || secret.showUrl)
        val note = KindTemplate.of("note")
        assertFalse(note.showPassword || note.showUsername || note.showUrl)
        assertTrue(note.showNotes)
        assertEquals("login", KindTemplate.of("weird").kind) // unknown kinds look like logins
    }

    @Test
    fun suggestionsSkipFieldsAlreadyThere() {
        val d = EntryDraft(kind = "apikey").withField("region", "eu")
        assertEquals(listOf("Key ID", "Environment"), KindTemplate.unusedSuggestions("apikey", d))
        assertEquals(emptyList<String>(), KindTemplate.unusedSuggestions("login", d))
    }
}

class MasterPasswordRulesTest {
    @Test
    fun emptyAndMismatchBlockWeakOnlyWarns() {
        assertEquals(MasterPasswordRules.Problem.EMPTY, MasterPasswordRules.problem("", ""))
        assertEquals(MasterPasswordRules.Problem.MISMATCH, MasterPasswordRules.problem("abcdefgh", "abcdefgi"))
        assertEquals(MasterPasswordRules.Problem.TOO_SHORT, MasterPasswordRules.problem("abc", "abc"))
        assertNull(MasterPasswordRules.problem("abcdefgh", "abcdefgh")) // weak-but-long-enough passwords are allowed
        assertEquals("请输入主密码", MasterPasswordRules.problemText(MasterPasswordRules.Problem.EMPTY))
        assertEquals("主密码至少需要 8 个字符", MasterPasswordRules.problemText(MasterPasswordRules.Problem.TOO_SHORT))
        assertNull(MasterPasswordRules.problemText(null))
    }

    @Test
    fun fileSizeLimits() {
        assertTrue(MasterPasswordRules.keyFileFits(1))
        assertTrue(MasterPasswordRules.keyFileFits(1L shl 20))
        assertFalse(MasterPasswordRules.keyFileFits((1L shl 20) + 1))
        assertFalse(MasterPasswordRules.keyFileFits(0))
        assertTrue(MasterPasswordRules.attachmentFits(5L shl 20))
        assertFalse(MasterPasswordRules.attachmentFits((5L shl 20) + 1))
    }
}

class KdfChoiceTest {
    @Test
    fun standardUnlessThePhoneWasSlow() {
        assertEquals(KdfLevel.STANDARD, KdfChoice.default(null))
        assertEquals(KdfLevel.STANDARD, KdfChoice.default(900))
        assertEquals(KdfLevel.STANDARD, KdfChoice.default(3000))
        assertEquals(KdfLevel.LOW, KdfChoice.default(3001))
        assertEquals("low", KdfLevel.LOW.apiName)
        assertEquals("standard", KdfLevel.STANDARD.apiName)
        assertEquals("high", KdfLevel.HIGH.apiName)
        assertNull(KdfChoice.slowDeviceNotice(1000))
        assertTrue(KdfChoice.slowDeviceNotice(4200)!!.contains("4.2"))
    }
}

class GeneratorOptionsTest {
    @Test
    fun passwordParams() {
        val p = GeneratorOptions(length = 24, symbols = false, excludeAmbiguous = true).toParams()
        assertEquals("password", p["type"])
        assertEquals(24, p["length"])
        assertEquals(false, p["symbols"])
        assertEquals(true, p["exclude_ambiguous"])
        assertFalse(p.containsKey("words"))
    }

    @Test
    fun passphraseParams() {
        val p = GeneratorOptions(passphrase = true, words = 5, separator = ".", capitalize = true, number = true).toParams()
        assertEquals("passphrase", p["type"])
        assertEquals(5, p["words"])
        assertEquals(".", p["separator"])
        assertEquals(true, p["capitalize"])
        assertEquals(true, p["number"])
        assertEquals("-", GeneratorOptions(passphrase = true, separator = "").toParams()["separator"])
    }

    @Test
    fun valuesAreClampedToTheCoreRanges() {
        assertEquals(4, GeneratorOptions(length = 1).normalized().length)
        assertEquals(256, GeneratorOptions(length = 999).normalized().length)
        assertEquals(3, GeneratorOptions(words = 0).normalized().words)
        assertEquals(20, GeneratorOptions(words = 99).normalized().words)
        assertEquals("abcd", GeneratorOptions(separator = "abcdef").normalized().separator)
    }

    @Test
    fun lastClassCannotBeSwitchedOff() {
        var o = GeneratorOptions()
        o = o.withClass('l', false).withClass('u', false).withClass('d', false)
        assertTrue(o.symbols)
        val still = o.withClass('s', false)
        assertTrue(still.symbols)
        assertTrue(o.withClass('l', true).lower)
    }
}

class AttachmentNamesTest {
    @Test
    fun sanitizesPathsAndControlCharacters() {
        assertEquals("passwd", AttachmentNames.sanitize("../../etc/passwd").substringAfterLast('_'))
        assertFalse(AttachmentNames.sanitize("a/b\\c:d").contains('/'))
        assertFalse(AttachmentNames.sanitize("a/b\\c:d").contains('\\'))
        assertEquals("证书.pdf", AttachmentNames.sanitize("证书.pdf"))
        assertEquals(AttachmentNames.FALLBACK, AttachmentNames.sanitize(null))
        assertEquals(AttachmentNames.FALLBACK, AttachmentNames.sanitize("   "))
        assertEquals("hidden", AttachmentNames.sanitize("..hidden"))
        assertTrue(AttachmentNames.sanitize("x".repeat(500)).toByteArray().size <= 200)
        assertFalse(AttachmentNames.sanitize("a\u0000b\nc").any { it.isISOControl() })
    }

    @Test
    fun exportNamesAndMimeTypes() {
        assertEquals("密码库备份-20261026.kdbx", AttachmentNames.exportName("密码库备份", "20261026", "kdbx"))
        assertEquals("application/pdf", AttachmentNames.mimeFor("a.PDF"))
        assertEquals("application/octet-stream", AttachmentNames.mimeFor("noext"))
    }
}

class MaskPolicyTest {
    @Test
    fun maskDoesNotLeakLengthAndNotesOfNoteEntriesAreSecret() {
        assertEquals(MaskPolicy.MASK, MaskPolicy.display(null))
        assertEquals("abc", MaskPolicy.display("abc"))
        assertEquals(15_000L, MaskPolicy.AUTO_MASK_MS)
        assertTrue(MaskPolicy.notesAreSecret("note"))
        assertFalse(MaskPolicy.notesAreSecret("login"))
    }
}

class VaultSettingOptionsTest {
    @Test
    fun everyOfferedChoiceIsAcceptedByTheCore() {
        assertTrue(VaultSettingOptions.AUTOLOCK_SECONDS.all(VaultSettingOptions::validAutolock))
        assertTrue(VaultSettingOptions.CLIP_CLEAR_SECONDS.all(VaultSettingOptions::validClipClear))
        assertFalse(VaultSettingOptions.validAutolock(29))
        assertFalse(VaultSettingOptions.validClipClear(4))
        assertFalse(VaultSettingOptions.validClipClear(301))
    }

    @Test
    fun labels() {
        assertEquals("30 秒", VaultSettingOptions.autolockLabel(30))
        assertEquals("5 分钟", VaultSettingOptions.autolockLabel(300))
        assertEquals("1 小时", VaultSettingOptions.autolockLabel(3600))
        assertEquals("不自动清除", VaultSettingOptions.clipClearLabel(0))
        assertEquals("20 秒", VaultSettingOptions.clipClearLabel(20))
    }
}
