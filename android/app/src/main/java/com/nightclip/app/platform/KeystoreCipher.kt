package com.nightclip.app.platform

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import com.nightclip.app.logic.KeyCipher
import com.nightclip.app.logic.WrappedBlob
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * [KeyCipher] on a non-exportable AES-256 key in the AndroidKeyStore (AES/GCM/NoPadding).
 * The IV is generated randomly by the Keystore for every wrap and stored with the ciphertext
 * (see [WrappedBlob]). Unwrapping never creates a missing key: a vanished key is an error.
 */
class KeystoreCipher(private val alias: String = ALIAS) : KeyCipher {
    override fun wrap(plain: ByteArray): ByteArray {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, getOrCreateKey()) // Keystore picks a fresh random 12-byte IV
        val ct = cipher.doFinal(plain)
        return WrappedBlob.encode(cipher.iv, ct)
    }

    override fun unwrap(blob: ByteArray): ByteArray {
        val (iv, ct) = WrappedBlob.decode(blob)
        val key = existingKey() ?: throw IllegalStateException("Keystore key $alias is missing")
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(TAG_BITS, iv))
        return cipher.doFinal(ct)
    }

    private fun keyStore(): KeyStore = KeyStore.getInstance(PROVIDER).apply { load(null) }

    private fun existingKey(): SecretKey? = keyStore().getKey(alias, null) as? SecretKey

    private fun getOrCreateKey(): SecretKey {
        existingKey()?.let { return it }
        val gen = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, PROVIDER)
        gen.init(
            KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build(),
        )
        return gen.generateKey()
    }

    /** Removes the Keystore key; only used when the user resets all local data. */
    fun deleteKey() {
        val ks = keyStore()
        if (ks.containsAlias(alias)) ks.deleteEntry(alias)
    }

    companion object {
        const val ALIAS = "clipbridge_db_key_wrap_v1"
        private const val PROVIDER = "AndroidKeyStore"
        private const val TRANSFORMATION = "AES/GCM/NoPadding"
        private const val TAG_BITS = 128
    }
}
