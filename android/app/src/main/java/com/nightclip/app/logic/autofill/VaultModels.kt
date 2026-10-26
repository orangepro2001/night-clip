package com.nightclip.app.logic.autofill

import org.json.JSONArray
import org.json.JSONObject

data class VaultStatus(val exists: Boolean, val unlocked: Boolean, val retryAfterS: Int, val entries: Int)

/** One `vault.match` hit: the list summary plus the score. No secrets. */
data class MatchedEntry(
    val id: String,
    val title: String,
    val username: String,
    val url: String,
    val kind: String,
    val hasTotp: Boolean,
    val score: Int,
)

object VaultParsers {
    fun status(r: JSONObject) = VaultStatus(
        exists = r.optBoolean("exists", false),
        unlocked = r.optBoolean("unlocked", false),
        retryAfterS = r.optInt("retry_after_s", 0),
        entries = r.optInt("entries", 0),
    )

    fun matches(r: JSONObject): List<MatchedEntry> {
        val arr: JSONArray = r.optJSONArray("matches") ?: return emptyList()
        val out = ArrayList<MatchedEntry>(arr.length())
        for (i in 0 until arr.length()) {
            val o = arr.optJSONObject(i) ?: continue
            val id = o.optString("id")
            if (id.isEmpty()) continue
            out += MatchedEntry(
                id = id,
                title = o.optString("title"),
                username = o.optString("username"),
                url = o.optString("url"),
                kind = o.optString("kind", "login"),
                hasTotp = o.optBoolean("has_totp", false),
                score = o.optInt("score", 0),
            )
        }
        return out
    }
}

/** Messages for the `vault.*` error codes (docs/vault-api.md §2). Never contains a secret. */
object VaultErrors {
    const val LOCKED = "VAULT_LOCKED"
    const val WRONG = "VAULT_WRONG_CREDENTIALS"
    const val RATE_LIMITED = "RATE_LIMITED"

    fun message(code: String, retryAfterS: Int = 0): String = when (code) {
        LOCKED -> "密码库已锁定"
        WRONG -> "主密码不对，或文件已损坏。可以从备份恢复。"
        RATE_LIMITED -> if (retryAfterS > 0) "尝试次数过多，请 $retryAfterS 秒后再试" else "尝试次数过多，请稍后再试"
        "VAULT_NOT_FOUND" -> "条目不存在"
        "VAULT_EXISTS" -> "密码库已存在"
        "VAULT_UNSUPPORTED" -> "这个库的加密参数不受支持或不安全。"
        "VAULT_BROKEN" -> "密码库状态异常，请锁定后重新解锁"
        "TOO_LARGE" -> "内容太大"
        "INVALID" -> "输入无效，请检查后重试"
        "NOT_INITIALIZED", "INTERNAL" -> "ClipBridge 还没有准备好，请稍后再试"
        else -> "操作失败（$code）"
    }
}

/** Seconds a retry is still blocked, shown as a countdown. */
object RateLimit {
    fun remainingS(retryAfterS: Int, elapsedMs: Long): Int = maxOf(0, retryAfterS - (elapsedMs / 1000).toInt())
}

/** Builds the JSON `entry` of `vault.add` / `vault.update`. */
object EntryJson {
    fun newLogin(title: String, username: String, password: String, url: String): JSONObject = JSONObject()
        .put("kind", "login")
        .put("title", title.trim())
        .put("username", username)
        .put("password", password)
        .put("url", url.trim())

    /** Names of the protected custom fields whose value `vault.get` hides but which exist. */
    fun protectedFieldsToReveal(get: JSONObject): List<String> {
        val fields = get.optJSONArray("fields") ?: return emptyList()
        val out = mutableListOf<String>()
        for (i in 0 until fields.length()) {
            val f = fields.optJSONObject(i) ?: continue
            if (f.optBoolean("protected") && f.optBoolean("has_value")) out += f.optString("name")
        }
        return out
    }

    /**
     * `vault.update` replaces the whole entry, so everything else has to be sent back unchanged: the detail from
     * `vault.get`, the protected field values ([protectedValues], from `vault.reveal field:<name>`) and the
     * TOTP secret ([totpSecret], from `totp_secret`). Returns null when a protected value is missing, which
     * would otherwise silently wipe it.
     */
    fun withNewPassword(get: JSONObject, newPassword: String, protectedValues: Map<String, String>, totpSecret: String?): JSONObject? {
        val fields = JSONArray()
        val src = get.optJSONArray("fields") ?: JSONArray()
        for (i in 0 until src.length()) {
            val f = src.optJSONObject(i) ?: continue
            val name = f.optString("name")
            val prot = f.optBoolean("protected")
            val value = if (prot) {
                if (f.optBoolean("has_value")) protectedValues[name] ?: return null else ""
            } else {
                f.optString("value")
            }
            fields.put(JSONObject().put("name", name).put("value", value).put("protected", prot))
        }
        if (get.optBoolean("has_totp") && totpSecret.isNullOrEmpty()) return null
        return JSONObject()
            .put("kind", get.optString("kind", "login"))
            .put("title", get.optString("title"))
            .put("username", get.optString("username"))
            .put("password", newPassword)
            .put("url", get.optString("url"))
            .put("notes", get.optString("notes"))
            .put("tags", get.optJSONArray("tags") ?: JSONArray())
            .put("fields", fields)
            .put("totp", if (get.optBoolean("has_totp")) totpSecret else "")
            .put("expires_at", get.optLong("expires_at", 0))
            .put("group_id", get.optString("group_id"))
    }
}
