package com.nightclip.app.logic

/** Choice of the Argon2 cost for a new vault (docs/vault-api.md §3.1). */
enum class KdfLevel(val apiName: String) {
    LOW("low"),
    STANDARD("standard"),
    HIGH("high");

    val label: String
        get() = when (this) {
            LOW -> "低（32 MiB，适合较慢的手机）"
            STANDARD -> "标准（64 MiB）"
            HIGH -> "高（128 MiB，更慢但更抗破解）"
        }
}

object KdfChoice {
    /** An unlock or create that took longer than this makes the next creation default to the low preset. */
    const val SLOW_MS = 3000L

    /** Phones default to "standard", or "low" if the previous create/unlock took more than [SLOW_MS]. */
    fun default(previousMs: Long?): KdfLevel = if (previousMs != null && previousMs > SLOW_MS) KdfLevel.LOW else KdfLevel.STANDARD

    fun slowDeviceNotice(previousMs: Long?): String? =
        if (previousMs != null && previousMs > SLOW_MS) "上次操作用了 ${previousMs / 1000.0} 秒，已自动选用较快的“低”档。" else null
}

/** Rules of the create-vault and change-master-password forms. A weak password only warns; an empty or mismatching one blocks. */
object MasterPasswordRules {
    enum class Problem { EMPTY, MISMATCH }

    fun problem(password: String, confirm: String): Problem? = when {
        password.isEmpty() -> Problem.EMPTY
        password != confirm -> Problem.MISMATCH
        else -> null
    }

    fun problemText(p: Problem?): String? = when (p) {
        Problem.EMPTY -> "请输入主密码"
        Problem.MISMATCH -> "两次输入的主密码不一致"
        null -> null
    }

    /** The core accepts key files up to 1 MiB (credParams.key_file_b64). */
    const val MAX_KEY_FILE_BYTES = 1 shl 20

    fun keyFileFits(size: Long): Boolean = size in 1..MAX_KEY_FILE_BYTES.toLong()

    /** Attachments up to 5 MiB (core MaxAttachmentBytes). */
    const val MAX_ATTACHMENT_BYTES = 5L shl 20

    fun attachmentFits(size: Long): Boolean = size in 0..MAX_ATTACHMENT_BYTES
}
