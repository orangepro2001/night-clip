package com.nightclip.app.platform

import android.os.Build
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyPermanentlyInvalidatedException
import android.security.keystore.KeyProperties
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricPrompt
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import com.nightclip.app.core.VaultCreds
import com.nightclip.app.logic.WrappedBlob
import org.json.JSONObject
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Biometric quick unlock (docs/vault-api.md §4 rule 6). The master password (and key file bytes, if any) are wrapped
 * with an AES-256-GCM key in the Android Keystore that
 *  - needs the user's biometric for EVERY use (a CryptoObject, BIOMETRIC_STRONG only, no device-credential fallback),
 *  - is invalidated when biometrics are enrolled or removed,
 * and the resulting blob (iv + ciphertext, [WrappedBlob] format) lives in the app's private preferences.
 * Whether it may be used right now is decided by [com.nightclip.app.logic.QuickUnlockPolicy]; this class only does crypto and UI.
 */
class QuickUnlock(private val prefs: VaultPrefs) {
    sealed interface Result {
        class Secret(val creds: VaultCreds) : Result

        /** The user dismissed the prompt or chose the master password: not a failure. */
        data object Cancelled : Result

        /** The key was invalidated (biometrics changed) or the blob is unreadable: everything stored must go. */
        data object Invalidated : Result

        /** Too many wrong fingers, lockout, hardware error … counts as a failed quick unlock. */
        class Failed(val message: String) : Result
    }

    sealed interface EnrollResult {
        data object Done : EnrollResult
        data object Cancelled : EnrollResult
        class Failed(val message: String) : EnrollResult
    }

    /** Strong biometrics are enrolled and usable. */
    fun available(activity: FragmentActivity): Boolean =
        BiometricManager.from(activity).canAuthenticate(BiometricManager.Authenticators.BIOMETRIC_STRONG) == BiometricManager.BIOMETRIC_SUCCESS

    /** Deletes the Keystore key and the stored blob (switching off, wrong master password, master change, expiry). */
    fun purge() {
        try {
            val ks = keyStore()
            if (ks.containsAlias(ALIAS)) ks.deleteEntry(ALIAS)
        } catch (e: Exception) {
            // nothing to delete
        }
        prefs.quickBlob = null
    }

    /** Wraps [creds] after one biometric confirmation. A new key replaces any old one. */
    fun enroll(activity: FragmentActivity, creds: VaultCreds, onResult: (EnrollResult) -> Unit) {
        val cipher = try {
            purge()
            Cipher.getInstance(TRANSFORMATION).apply { init(Cipher.ENCRYPT_MODE, createKey()) }
        } catch (e: Exception) {
            purge()
            onResult(EnrollResult.Failed("无法创建受生物识别保护的密钥：${e.javaClass.simpleName}"))
            return
        }
        prompt(
            activity, cipher, "启用快速解锁", "确认指纹或面容，以便下次不用输入主密码",
            onSuccess = { c ->
                try {
                    val plain = encode(creds)
                    val ct = c.doFinal(plain)
                    plain.fill(0)
                    prefs.quickBlob = WrappedBlob.encode(c.iv, ct)
                    onResult(EnrollResult.Done)
                } catch (e: Exception) {
                    purge()
                    onResult(EnrollResult.Failed("无法保存快速解锁数据：${e.javaClass.simpleName}"))
                }
            },
            onError = { cancelled, msg ->
                purge()
                onResult(if (cancelled) EnrollResult.Cancelled else EnrollResult.Failed(msg))
            },
        )
    }

    /** Asks for the biometric and returns the wrapped credentials. */
    fun unlock(activity: FragmentActivity, onResult: (Result) -> Unit) {
        val blob = prefs.quickBlob
        if (blob == null) {
            onResult(Result.Invalidated)
            return
        }
        val cipher = try {
            val (iv, _) = WrappedBlob.decode(blob)
            val key = existingKey() ?: throw KeyPermanentlyInvalidatedException()
            Cipher.getInstance(TRANSFORMATION).apply { init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(TAG_BITS, iv)) }
        } catch (e: Exception) { // KeyPermanentlyInvalidatedException (biometrics changed) or an unreadable blob
            onResult(Result.Invalidated)
            return
        }
        prompt(
            activity, cipher, "解锁密码库", "用指纹或面容解锁",
            onSuccess = { c ->
                try {
                    val (_, ct) = WrappedBlob.decode(blob)
                    val plain = c.doFinal(ct)
                    val creds = decode(plain)
                    plain.fill(0)
                    onResult(Result.Secret(creds))
                } catch (e: Exception) {
                    onResult(Result.Invalidated)
                }
            },
            onError = { cancelled, msg -> onResult(if (cancelled) Result.Cancelled else Result.Failed(msg)) },
        )
    }

    private fun prompt(
        activity: FragmentActivity,
        cipher: Cipher,
        title: String,
        subtitle: String,
        onSuccess: (Cipher) -> Unit,
        onError: (cancelled: Boolean, message: String) -> Unit,
    ) {
        VaultSession.expectSystemUi()
        val executor = ContextCompat.getMainExecutor(activity)
        val bp = BiometricPrompt(
            activity, executor,
            object : BiometricPrompt.AuthenticationCallback() {
                override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) {
                    val c = result.cryptoObject?.cipher
                    if (c == null) onError(false, "没有取得密钥") else onSuccess(c)
                }

                override fun onAuthenticationError(errorCode: Int, errString: CharSequence) {
                    val cancelled = errorCode == BiometricPrompt.ERROR_USER_CANCELED ||
                        errorCode == BiometricPrompt.ERROR_NEGATIVE_BUTTON ||
                        errorCode == BiometricPrompt.ERROR_CANCELED
                    onError(cancelled, errString.toString())
                }
            },
        )
        val info = BiometricPrompt.PromptInfo.Builder()
            .setTitle(title)
            .setSubtitle(subtitle)
            .setNegativeButtonText("使用主密码")
            .setAllowedAuthenticators(BiometricManager.Authenticators.BIOMETRIC_STRONG)
            .setConfirmationRequired(false)
            .build()
        bp.authenticate(info, BiometricPrompt.CryptoObject(cipher))
    }

    private fun keyStore(): KeyStore = KeyStore.getInstance(PROVIDER).apply { load(null) }

    private fun existingKey(): SecretKey? = keyStore().getKey(ALIAS, null) as? SecretKey

    private fun createKey(): SecretKey {
        val spec = KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
            .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
            .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
            .setKeySize(256)
            .setUserAuthenticationRequired(true)
            .setInvalidatedByBiometricEnrollment(true)
        if (Build.VERSION.SDK_INT >= 30) {
            // timeout 0 = authentication for every single use of the key
            spec.setUserAuthenticationParameters(0, KeyProperties.AUTH_BIOMETRIC_STRONG)
        } else {
            @Suppress("DEPRECATION")
            spec.setUserAuthenticationValidityDurationSeconds(-1) // -1 = every use, biometric only
        }
        val gen = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, PROVIDER)
        gen.init(spec.build())
        return gen.generateKey()
    }

    private fun encode(c: VaultCreds): ByteArray {
        val o = JSONObject().put("p", c.password)
        c.keyFile?.let { o.put("k", java.util.Base64.getEncoder().encodeToString(it)) }
        return o.toString().toByteArray(Charsets.UTF_8)
    }

    private fun decode(plain: ByteArray): VaultCreds {
        val o = JSONObject(String(plain, Charsets.UTF_8))
        val k = o.optString("k", "")
        return VaultCreds(o.getString("p"), if (k.isEmpty()) null else java.util.Base64.getDecoder().decode(k))
    }

    private companion object {
        const val ALIAS = "clipbridge_vault_quick_unlock_v1"
        const val PROVIDER = "AndroidKeyStore"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val TAG_BITS = 128
    }
}
