package com.nightclip.app.logic

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.security.SecureRandom
import java.util.Base64

/** Stands in for the Keystore: XORs with a secret and prefixes a marker so tampering is detectable. */
private class FakeCipher(var failWrap: Boolean = false, var corruptUnwrap: Boolean = false, var failUnwrap: Boolean = false) : KeyCipher {
    private val secret = ByteArray(32).also { SecureRandom().nextBytes(it) }
    var wraps = 0

    override fun wrap(plain: ByteArray): ByteArray {
        if (failWrap) throw IllegalStateException("keystore down")
        wraps++
        val iv = ByteArray(12).also { SecureRandom().nextBytes(it) }
        val ct = ByteArray(plain.size + 1) { i -> if (i == 0) 0x5a else (plain[i - 1].toInt() xor secret[(i - 1) % 32].toInt()).toByte() }
        return WrappedBlob.encode(iv, ct)
    }

    override fun unwrap(blob: ByteArray): ByteArray {
        if (failUnwrap) throw IllegalStateException("key gone")
        val (_, ct) = WrappedBlob.decode(blob)
        if (ct.isEmpty() || ct[0] != 0x5a.toByte()) throw IllegalStateException("tag mismatch")
        val plain = ByteArray(ct.size - 1) { i -> (ct[i + 1].toInt() xor secret[i % 32].toInt()).toByte() }
        return if (corruptUnwrap) plain.also { if (it.isNotEmpty()) it[0] = (it[0].toInt() xor 1).toByte() } else plain
    }
}

class KeyWrappingTest {
    @get:Rule val tmp = TemporaryFolder()

    private val legacyKey = ByteArray(32) { (it + 1).toByte() }
    private val legacyB64 = Base64.getEncoder().encodeToString(legacyKey)

    private fun dir(): File = tmp.newFolder()
    private fun provider(d: File, c: KeyCipher) = DbKeyProvider(d, c)

    @Test
    fun freshInstallCreatesAWrappedKeyAndNoPlaintext() {
        val d = dir()
        val c = FakeCipher()
        val a = provider(d, c).loadOrCreate()
        assertEquals(32, Base64.getDecoder().decode(a.keyB64).size)
        assertTrue(File(d, "devkey.wrapped").isFile)
        assertFalse(File(d, "devkey.b64").exists())
        assertFalse(File(d, "devkey.wrapped.tmp").exists())
        // the file holds the wrapped form, not the key itself
        assertFalse(File(d, "devkey.wrapped").readBytes().toList().containsAll(Base64.getDecoder().decode(a.keyB64).toList()))
        val b = provider(d, c).loadOrCreate()
        assertEquals(a.keyB64, b.keyB64)
        assertEquals(1, c.wraps) // the second start only unwraps
    }

    @Test
    fun differentInstallsGetDifferentKeys() {
        val a = provider(dir(), FakeCipher()).loadOrCreate().keyB64
        val b = provider(dir(), FakeCipher()).loadOrCreate().keyB64
        assertNotEquals(a, b)
    }

    @Test
    fun migratesTheOldPlaintextKeyAndDeletesIt() {
        val d = dir()
        File(d, "devkey.b64").writeText(legacyB64 + "\n")
        val c = FakeCipher()
        val r = provider(d, c).loadOrCreate()
        assertEquals(legacyB64, r.keyB64)
        assertTrue(r.migrated)
        assertFalse(r.plaintextLeft)
        assertFalse(File(d, "devkey.b64").exists())
        assertTrue(File(d, "devkey.wrapped").isFile)
        // the next start reads the wrapped file and still returns the same key
        assertEquals(legacyB64, provider(d, c).loadOrCreate().keyB64)
    }

    @Test
    fun failedMigrationKeepsThePlaintextAndKeepsUsingIt() {
        val d = dir()
        File(d, "devkey.b64").writeText(legacyB64)
        val r = provider(d, FakeCipher(failWrap = true)).loadOrCreate()
        assertEquals(legacyB64, r.keyB64)
        assertFalse(r.migrated)
        assertTrue(r.plaintextLeft)
        assertTrue(File(d, "devkey.b64").exists())
        assertFalse(File(d, "devkey.wrapped").exists()) // no half-written wrapped file that would later look corrupt
        assertFalse(File(d, "devkey.wrapped.tmp").exists())
    }

    @Test
    fun migrationThatDoesNotRoundTripKeepsThePlaintext() {
        val d = dir()
        File(d, "devkey.b64").writeText(legacyB64)
        val r = provider(d, FakeCipher(corruptUnwrap = true)).loadOrCreate()
        assertEquals(legacyB64, r.keyB64)
        assertTrue(r.plaintextLeft)
        assertTrue(File(d, "devkey.b64").exists())
        assertFalse(File(d, "devkey.wrapped").exists())
    }

    @Test
    fun invalidPlaintextIsNeverReplaced() {
        val d = dir()
        File(d, "devkey.b64").writeText("not base64 !!")
        try {
            provider(d, FakeCipher()).loadOrCreate()
            fail("expected KeyCorruptedException")
        } catch (e: KeyCorruptedException) {
            // expected
        }
        assertEquals("not base64 !!", File(d, "devkey.b64").readText())
        assertFalse(File(d, "devkey.wrapped").exists())
        File(d, "devkey.b64").writeText(Base64.getEncoder().encodeToString(ByteArray(16)))
        try {
            provider(d, FakeCipher()).loadOrCreate()
            fail("wrong-size key must be rejected")
        } catch (e: KeyCorruptedException) {
            // expected
        }
    }

    @Test
    fun corruptWrappedFileIsAnErrorAndStaysUntouched() {
        val d = dir()
        val c = FakeCipher()
        provider(d, c).loadOrCreate()
        val f = File(d, "devkey.wrapped")
        f.writeBytes(byteArrayOf(9, 9, 9, 9, 9))
        try {
            provider(d, c).loadOrCreate()
            fail("expected KeyCorruptedException")
        } catch (e: KeyCorruptedException) {
            // expected
        }
        assertArrayEquals(byteArrayOf(9, 9, 9, 9, 9), f.readBytes()) // never regenerated
    }

    @Test
    fun missingKeystoreKeyIsAnErrorNotARegeneration() {
        val d = dir()
        provider(d, FakeCipher()).loadOrCreate()
        val before = File(d, "devkey.wrapped").readBytes()
        try {
            provider(d, FakeCipher(failUnwrap = true)).loadOrCreate() // the Keystore key is gone
            fail("expected KeyCorruptedException")
        } catch (e: KeyCorruptedException) {
            // expected
        }
        assertArrayEquals(before, File(d, "devkey.wrapped").readBytes())
    }

    @Test
    fun wrappedKeyOfTheWrongSizeIsRejected() {
        val d = dir()
        val c = FakeCipher()
        File(d, "devkey.wrapped").writeBytes(c.wrap(ByteArray(16)))
        try {
            provider(d, c).loadOrCreate()
            fail("expected KeyCorruptedException")
        } catch (e: KeyCorruptedException) {
            // expected
        }
    }

    @Test
    fun leftoverPlaintextIsRemovedOnlyWhenItMatches() {
        val d = dir()
        val c = FakeCipher()
        File(d, "devkey.b64").writeText(legacyB64)
        provider(d, c).loadOrCreate() // migrates
        File(d, "devkey.b64").writeText(legacyB64) // a stale copy reappears (delete had failed earlier)
        val r = provider(d, c).loadOrCreate()
        assertEquals(legacyB64, r.keyB64)
        assertFalse(File(d, "devkey.b64").exists())

        File(d, "devkey.b64").writeText(Base64.getEncoder().encodeToString(ByteArray(32) { 7 }))
        val r2 = provider(d, c).loadOrCreate()
        assertEquals(legacyB64, r2.keyB64) // the wrapped file stays authoritative
        assertTrue(r2.plaintextLeft)
        assertTrue(File(d, "devkey.b64").exists())
    }

    @Test
    fun interruptedTempFileIsIgnored() {
        val d = dir()
        File(d, "devkey.wrapped.tmp").writeBytes(byteArrayOf(1, 2, 3))
        val r = provider(d, FakeCipher()).loadOrCreate()
        assertEquals(32, Base64.getDecoder().decode(r.keyB64).size)
        assertFalse(File(d, "devkey.wrapped.tmp").exists())
    }

    @Test
    fun blobFormatRoundTripsAndRejectsGarbage() {
        val iv = ByteArray(12) { it.toByte() }
        val ct = ByteArray(48) { (it * 3).toByte() }
        val (iv2, ct2) = WrappedBlob.decode(WrappedBlob.encode(iv, ct))
        assertArrayEquals(iv, iv2)
        assertArrayEquals(ct, ct2)
        for (junk in listOf(byteArrayOf(), byteArrayOf(1), byteArrayOf(2, 12, 0), byteArrayOf(1, 12, 1, 2, 3), byteArrayOf(1, 0, 5, 5))) {
            try {
                WrappedBlob.decode(junk)
                fail("accepted ${junk.toList()}")
            } catch (e: IllegalArgumentException) {
                // expected
            }
        }
    }
}
