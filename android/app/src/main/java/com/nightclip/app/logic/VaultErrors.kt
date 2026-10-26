package com.nightclip.app.logic

import com.nightclip.app.core.ErrorMessages

/** Error code -> text for vault calls (docs/vault-api.md §2). Anything else falls back to [ErrorMessages]. */
object VaultErrors {
    enum class Context { GENERAL, UNLOCK, CREATE, EXPORT, IMPORT, BACKUP }

    fun message(code: String, retryAfterS: Int = 0, context: Context = Context.GENERAL): String = when (code) {
        "VAULT_LOCKED" -> "密码库已锁定，请先解锁"
        "VAULT_WRONG_CREDENTIALS" -> when (context) {
            Context.IMPORT -> "导入文件的密码或密钥文件不对，或文件已损坏。"
            Context.BACKUP -> "密码或密钥文件不对，或备份已损坏。如果这份备份是更换主密码之前的，请输入旧密码。"
            else -> "主密码或密钥文件不对，或文件已损坏。可以从备份恢复。"
        }
        "VAULT_NOT_FOUND" -> "找不到该条目、分组或备份"
        "VAULT_EXISTS" -> when (context) {
            Context.EXPORT -> "目标文件已存在，请换一个文件名"
            else -> "密码库已存在"
        }
        "VAULT_UNSUPPORTED" -> "这个库的加密参数不受支持或不安全。"
        "VAULT_BROKEN" -> "密码库状态不一致，请先锁定再重新解锁。"
        "RATE_LIMITED" -> if (retryAfterS > 0) "尝试次数过多，请 ${RateLimitCountdown.label(retryAfterS)}后再试" else "尝试次数过多，请稍后再试"
        "TOO_LARGE" -> "内容或文件太大"
        "INVALID" -> when (context) {
            Context.IMPORT -> "无法读取这个文件，请确认格式正确"
            else -> "输入无效，请检查后重试"
        }
        else -> ErrorMessages.forCode(code)
    }

    /** Codes that mean the vault is no longer open and the screens must go back to the locked state. */
    fun meansLocked(code: String): Boolean = code == "VAULT_LOCKED"

    /** Codes that count as a guessed-wrong credential for the unlock screen. */
    fun isWrongCredentials(code: String): Boolean = code == "VAULT_WRONG_CREDENTIALS"
}
