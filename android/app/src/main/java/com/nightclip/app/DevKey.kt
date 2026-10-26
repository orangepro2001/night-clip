package com.nightclip.app

import java.io.File
import java.io.IOException
import java.nio.file.Files
import java.nio.file.StandardOpenOption
import java.security.SecureRandom
import java.util.Base64

/**
 * Temporary database-key provider: a random 32-byte key kept as base64 in
 * `devkey.b64` inside the app-private data directory. The key is NOT encrypted
 * at rest; only the Android app sandbox protects it.
 * TODO(WP10: Keystore): wrap the key with an Android Keystore secret instead.
 */
object DevKey {
    const val FILE_NAME = "devkey.b64"
    const val KEY_BYTES = 32

    /**
     * Returns the base64 key for [dataDir], creating it on first run. An unreadable
     * or wrong-sized key file throws instead of being regenerated: a new key would
     * make the existing encrypted database permanently unreadable.
     */
    fun loadOrCreate(dataDir: File): String {
        dataDir.mkdirs()
        val file = File(dataDir, FILE_NAME)
        if (!file.exists()) {
            val fresh = Base64.getEncoder().encodeToString(ByteArray(KEY_BYTES).also { SecureRandom().nextBytes(it) })
            try {
                // CREATE_NEW: if another thread won the race, fall through and read its key.
                Files.write(file.toPath(), fresh.toByteArray(Charsets.UTF_8), StandardOpenOption.CREATE_NEW)
            } catch (e: IOException) {
                if (!file.exists()) throw e
            }
        }
        val text = file.readText(Charsets.UTF_8).trim()
        val ok = try {
            Base64.getDecoder().decode(text).size == KEY_BYTES
        } catch (e: IllegalArgumentException) {
            false
        }
        check(ok) {
            "${file.path} is not a valid $KEY_BYTES-byte base64 key; refusing to replace it " +
                "because the existing database could no longer be opened."
        }
        return text
    }
}
