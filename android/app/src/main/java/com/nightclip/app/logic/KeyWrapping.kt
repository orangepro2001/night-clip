package com.nightclip.app.logic

import java.io.File
import java.io.IOException
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.security.SecureRandom
import java.util.Base64

/** Wraps/unwraps a small secret. The real implementation uses Android Keystore (AES/GCM). */
interface KeyCipher {
    /** Returns an opaque blob that [unwrap] turns back into [plain]. May throw. */
    fun wrap(plain: ByteArray): ByteArray

    /** Throws if the blob is damaged or the wrapping key is gone. */
    fun unwrap(blob: ByteArray): ByteArray
}

/** The wrapped file is unreadable: never regenerate the key, the database would be lost. */
class KeyCorruptedException(message: String, cause: Throwable? = null) : Exception(message, cause)

/** Layout of the wrapped blob: version, IV length, IV, ciphertext (including the GCM tag). */
object WrappedBlob {
    const val VERSION: Byte = 1

    fun encode(iv: ByteArray, ciphertext: ByteArray): ByteArray {
        require(iv.size in 1..255)
        return byteArrayOf(VERSION, iv.size.toByte()) + iv + ciphertext
    }

    /** Returns (iv, ciphertext) or throws IllegalArgumentException for a malformed blob. */
    fun decode(blob: ByteArray): Pair<ByteArray, ByteArray> {
        require(blob.size > 2 && blob[0] == VERSION) { "unsupported wrapped key format" }
        val ivLen = blob[1].toInt() and 0xff
        require(ivLen > 0 && blob.size > 2 + ivLen) { "truncated wrapped key" }
        return blob.copyOfRange(2, 2 + ivLen) to blob.copyOfRange(2 + ivLen, blob.size)
    }
}

/**
 * Database-key lifecycle in `<dataDir>`: `devkey.wrapped` (current), `devkey.b64` (old plaintext
 * from the first versions). See shell-guide §4 item 2:
 *  - wrapped present: it is authoritative; a bad file is an error, never recreated.
 *  - only plaintext: wrap it, verify it round-trips, then delete the plaintext. Any failure keeps
 *    the plaintext and keeps using it.
 *  - neither: generate a random key and store it wrapped.
 */
class DbKeyProvider(
    private val dir: File,
    private val cipher: KeyCipher,
    private val random: () -> ByteArray = { ByteArray(KEY_BYTES).also { SecureRandom().nextBytes(it) } },
) {
    data class Loaded(val keyB64: String, val migrated: Boolean, val plaintextLeft: Boolean) {
        override fun toString() = "Loaded(migrated=$migrated, plaintextLeft=$plaintextLeft)" // key stays out of logs
    }

    private val wrappedFile get() = File(dir, WRAPPED_NAME)
    private val tmpFile get() = File(dir, "$WRAPPED_NAME.tmp")
    private val legacyFile get() = File(dir, LEGACY_NAME)

    @Throws(KeyCorruptedException::class, IOException::class)
    fun loadOrCreate(): Loaded {
        dir.mkdirs()
        tmpFile.delete() // leftover of an interrupted write; never authoritative

        if (wrappedFile.exists()) {
            val key = unwrapFile()
            var left = false
            if (legacyFile.exists()) {
                // A previous run wrapped the key but could not delete the plaintext.
                val legacy = readLegacyOrNull()
                if (legacy != null && legacy.contentEquals(key)) {
                    left = !legacyFile.delete()
                } else {
                    left = true // differs: do not guess, keep both
                }
            }
            return Loaded(b64(key), migrated = false, plaintextLeft = left)
        }

        if (legacyFile.exists()) {
            val legacy = readLegacy() // invalid plaintext -> KeyCorruptedException
            val stored = try {
                storeWrapped(legacy)
                true
            } catch (e: Exception) {
                tmpFile.delete()
                false
            }
            if (!stored) return Loaded(b64(legacy), migrated = false, plaintextLeft = true)
            val deleted = legacyFile.delete()
            return Loaded(b64(legacy), migrated = true, plaintextLeft = !deleted)
        }

        val fresh = random()
        check(fresh.size == KEY_BYTES) { "random key must be $KEY_BYTES bytes" }
        storeWrapped(fresh)
        return Loaded(b64(fresh), migrated = false, plaintextLeft = false)
    }

    /** Wrap -> write temp -> read back and unwrap -> atomic rename. Throws if any step fails. */
    private fun storeWrapped(key: ByteArray) {
        val blob = cipher.wrap(key)
        Files.write(tmpFile.toPath(), blob)
        val back = Files.readAllBytes(tmpFile.toPath())
        val round = cipher.unwrap(back)
        if (!round.contentEquals(key)) throw IOException("wrapped key did not round-trip")
        Files.move(tmpFile.toPath(), wrappedFile.toPath(), StandardCopyOption.ATOMIC_MOVE)
    }

    private fun unwrapFile(): ByteArray {
        val key = try {
            cipher.unwrap(Files.readAllBytes(wrappedFile.toPath()))
        } catch (e: Exception) {
            throw KeyCorruptedException("${wrappedFile.name} cannot be unwrapped; refusing to replace it", e)
        }
        if (key.size != KEY_BYTES) throw KeyCorruptedException("${wrappedFile.name} holds a key of the wrong size")
        return key
    }

    private fun readLegacy(): ByteArray =
        readLegacyOrNull() ?: throw KeyCorruptedException("${legacyFile.name} is not a valid $KEY_BYTES-byte base64 key; refusing to replace it")

    private fun readLegacyOrNull(): ByteArray? = try {
        Base64.getDecoder().decode(legacyFile.readText(Charsets.UTF_8).trim()).takeIf { it.size == KEY_BYTES }
    } catch (e: IllegalArgumentException) {
        null
    } catch (e: IOException) {
        null
    }

    private fun b64(key: ByteArray) = Base64.getEncoder().encodeToString(key)

    companion object {
        const val WRAPPED_NAME = "devkey.wrapped"
        const val LEGACY_NAME = "devkey.b64"
        const val KEY_BYTES = 32
    }
}
