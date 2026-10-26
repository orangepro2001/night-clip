package com.nightclip.app.core

import com.nightclip.app.logic.Strings
import org.json.JSONException
import org.json.JSONObject

/** The facade reply envelope: {"ok":true,"result":…} or {"ok":false,"error":{…}} (api-spec §7.1). */
data class Reply(
    val ok: Boolean,
    val result: JSONObject = JSONObject(),
    val errorCode: String = "",
    val errorMessage: String = "",
    /** `error.details.retry_after_s` of a RATE_LIMITED reply (vault unlock), 0 when absent. */
    val retryAfterS: Int = 0,
) {
    /** User-facing message for a failed reply. */
    val userMessage: String get() = ErrorMessages.forCode(errorCode, errorMessage)

    companion object {
        fun parse(raw: String): Reply = try {
            val o = JSONObject(raw)
            if (o.optBoolean("ok", false)) {
                Reply(true, o.optJSONObject("result") ?: JSONObject())
            } else {
                val e = o.optJSONObject("error")
                Reply(
                    false,
                    errorCode = e?.optString("code", "") ?: "",
                    errorMessage = e?.optString("message", "") ?: "",
                    retryAfterS = e?.optJSONObject("details")?.optInt("retry_after_s", 0) ?: 0,
                )
            }
        } catch (e: JSONException) {
            Reply(false, errorCode = "INTERNAL", errorMessage = "bad reply")
        }

        fun failure(code: String, message: String = ""): Reply = Reply(false, errorCode = code, errorMessage = message)

        /** Reply used when the core has not finished starting (or failed to). */
        fun notReady(): Reply = failure("NOT_INITIALIZED", Strings.ERR_NOT_READY)
    }
}
