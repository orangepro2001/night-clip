package com.nightclip.app.logic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class PairingLinkTest {
    private val psk = "AAECAwQFBgcICQoLDA0ODw"
    private val fp = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"
    private val now = 1_790_000_000L

    private fun link(host: String = "100.101.146.117", port: String = "47821", fp: String? = this.fp, psk: String = this.psk, exp: Long = now + 300, v: String = "1") =
        "clipbridge://pair?v=$v&host=$host&port=$port" + (if (fp != null) "&fp=$fp" else "") + "&psk=$psk&exp=$exp"

    private fun bad(raw: String, expected: String? = null): String {
        val r = PairingLink.parse(raw, now)
        assertTrue("expected a rejection for $raw but got $r", r is LinkResult.Bad)
        val m = (r as LinkResult.Bad).message
        if (expected != null) assertEquals(expected, m)
        return m
    }

    @Test
    fun acceptsTheDocumentedFormat() {
        val r = PairingLink.parse(link(), now) as LinkResult.Ok
        assertEquals("100.101.146.117", r.link.host)
        assertEquals(47821, r.link.port)
        assertEquals(psk, r.link.psk)
        assertEquals(fp, r.link.fp)
        assertEquals(now + 300, r.link.expiresAt)
        assertFalse(r.link.toString().contains(psk)) // the one-time key stays out of logs
    }

    @Test
    fun fingerprintIsOptional() {
        val r = PairingLink.parse(link(fp = null), now) as LinkResult.Ok
        assertNull(r.link.fp)
    }

    @Test
    fun ipv6HostsAreAccepted() {
        assertTrue(PairingLink.parse(link(host = "fd7a:115c:a1e0::1"), now) is LinkResult.Ok)
    }

    @Test
    fun rejectsWrongSchemeAndShapes() {
        bad("https://example.com", Strings.PAIR_NOT_A_LINK)
        bad("clipbridge://other?v=1", Strings.PAIR_NOT_A_LINK)
        bad("clipbridge://pair/extra?v=1&host=1.2.3.4", Strings.PAIR_NOT_A_LINK)
        bad("", Strings.PAIR_NOT_A_LINK)
    }

    @Test
    fun rejectsBadFields() {
        bad(link(v = "2"), Strings.PAIR_BAD_VERSION)
        bad(link(host = "tony-pc"), Strings.PAIR_BAD_HOST)
        bad(link(host = "999.1.1.1"), Strings.PAIR_BAD_HOST)
        bad(link(port = "0"), Strings.PAIR_BAD_PORT)
        bad(link(port = "70000"), Strings.PAIR_BAD_PORT)
        bad(link(port = "abc"), Strings.PAIR_BAD_PORT)
        bad(link(psk = "short"), Strings.PAIR_BAD_PSK)
        bad(link(fp = "short"), Strings.PAIR_BAD_FP)
        bad(link(psk = "AAECAwQFBgcICQoLDA0OD!"), Strings.PAIR_BAD_PSK)
    }

    @Test
    fun rejectsMissingParts() {
        bad("clipbridge://pair?v=1&host=1.2.3.4&port=1&exp=${now + 5}", Strings.PAIR_BAD_PSK)
        bad("clipbridge://pair?v=1&port=1&psk=$psk&exp=${now + 5}", Strings.PAIR_BAD_HOST)
        bad("clipbridge://pair?v=1&host=1.2.3.4&port=1&psk=$psk", Strings.PAIR_BAD_EXP)
    }

    @Test
    fun rejectsDuplicatedParameters() {
        bad(link() + "&host=100.1.1.1")
        bad(link() + "&psk=$psk")
    }

    @Test
    fun rejectsExpiredLinks() {
        bad(link(exp = now), Strings.PAIR_EXPIRED)
        bad(link(exp = now - 1), Strings.PAIR_EXPIRED)
        assertTrue(PairingLink.parse(link(exp = now + 1), now) is LinkResult.Ok)
    }

    @Test
    fun extractFindsTheLinkInsideOtherText() {
        val l = link()
        assertEquals(l, PairingLink.extract("  $l  "))
        assertEquals(l, PairingLink.extract("请打开 $l 然后确认"))
        assertEquals(l, PairingLink.extract("$l\nsecond line"))
        assertNull(PairingLink.extract("hello"))
        assertNull(PairingLink.extract(""))
    }

    @Test
    fun ipAddressValidation() {
        for (ok in listOf("100.64.0.1", "0.0.0.0", "255.255.255.255", "::1", "::", "fd7a:115c:a1e0::1", "1:2:3:4:5:6:7:8", "::ffff:1.2.3.4", "fe80::1")) {
            assertTrue(ok, PairingLink.isIpAddress(ok))
        }
        for (badIp in listOf("", "1.2.3", "1.2.3.4.5", "256.1.1.1", "a.b.c.d", "host", "1:2:3:4:5:6:7:8:9", "1::2::3", ":::", "12345::1", "1.2.3.04x", "g::1")) {
            assertFalse(badIp, PairingLink.isIpAddress(badIp))
        }
    }
}

class ManualPairInputTest {
    private val psk = "AAECAwQFBgcICQoLDA0ODw"
    private val fp = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"

    @Test
    fun acceptsAValidForm() {
        val r = ManualPairInput.validate(" 100.101.146.117 ", "47821", psk, "") as ManualResult.Ok
        assertEquals("100.101.146.117", r.host)
        assertEquals(47821, r.port)
        assertEquals(psk, r.psk)
        assertNull(r.fp)
        assertFalse(r.toString().contains(psk))
    }

    @Test
    fun keepsAFingerprintWhenGiven() {
        val r = ManualPairInput.validate("100.1.1.1", "1", psk, fp) as ManualResult.Ok
        assertEquals(fp, r.fp)
    }

    @Test
    fun paddingOnPastedKeysIsTolerated() {
        val r = ManualPairInput.validate("100.1.1.1", "1", "$psk==", "") as ManualResult.Ok
        assertEquals(psk, r.psk)
    }

    @Test
    fun reportsEveryFieldProblem() {
        val r = ManualPairInput.validate("", "", "", "bad") as ManualResult.Bad
        assertEquals(Strings.PAIR_MISSING_HOST, r.hostError)
        assertEquals(Strings.PAIR_MISSING_PORT, r.portError)
        assertEquals(Strings.PAIR_MISSING_PSK, r.pskError)
        assertEquals(Strings.PAIR_BAD_FP, r.fpError)
    }

    @Test
    fun rejectsNamesAndBadPorts() {
        val r = ManualPairInput.validate("tony-pc", "99999", "xx", "") as ManualResult.Bad
        assertEquals(Strings.PAIR_BAD_HOST, r.hostError)
        assertEquals(Strings.PAIR_BAD_PORT, r.portError)
        assertEquals(Strings.PAIR_BAD_PSK, r.pskError)
        assertNull(r.fpError)
    }
}
