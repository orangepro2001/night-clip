package cloud.moonl.password.core

import org.json.JSONException
import org.json.JSONObject

/**
 * Typed form of the events returned by `Core.nextEvent` (api-spec §7.4, shell-guide §2).
 * Nothing here is ever logged except [type] and [logId]: texts and paths stay out of logs.
 */
sealed interface CoreEvent {
    val type: String

    /** Identifier that is safe to log, or empty. */
    val logId: String get() = ""

    data object CoreStarted : CoreEvent { override val type = "core.started" }

    data class UpdateAvailable(val versionCode: Long, val versionName: String, val mandatory: Boolean, val notes: String) : CoreEvent {
        override val type = "update.available"
    }

    data class UpdateProgress(val received: Long, val total: Long) : CoreEvent { override val type = "update.progress" }

    data class UpdateReady(val versionCode: Long, val versionName: String, val path: String, val mandatory: Boolean) : CoreEvent {
        override val type = "update.ready"
    }

    data class UpdateError(val code: String, val message: String) : CoreEvent { override val type = "update.error" }

    /** The password vault locked itself or was locked ([reason]: idle, user …). Carries no secrets. */
    data class VaultLocked(val reason: String) : CoreEvent { override val type = "vault.locked" }

    /** Something other than this screen changed the open vault: lists and details must be re-read. Carries no entry data. */
    data class VaultChanged(val reason: String) : CoreEvent { override val type = "vault.changed" }

    data class Unknown(override val type: String) : CoreEvent

    companion object {
        /** Parses one event line; returns null for text that is not a JSON object with a type. */
        fun parse(raw: String): CoreEvent? {
            val o = try {
                JSONObject(raw)
            } catch (e: JSONException) {
                return null
            }
            val type = o.optString("type", "")
            if (type.isEmpty()) return null
            return fromJson(type, o)
        }

        private fun fromJson(type: String, o: JSONObject): CoreEvent = when (type) {
            "core.started" -> CoreStarted
            "update.available" -> UpdateAvailable(o.optLong("version_code"), o.optString("version_name"), o.optBoolean("mandatory"), o.optString("notes"))
            "update.progress" -> UpdateProgress(o.optLong("received"), o.optLong("total"))
            "update.ready" -> UpdateReady(o.optLong("version_code"), o.optString("version_name"), o.optString("path"), o.optBoolean("mandatory"))
            "update.error" -> UpdateError(o.optString("code"), o.optString("message"))
            "vault.locked" -> VaultLocked(o.optString("reason"))
            "vault.changed" -> VaultChanged(o.optString("reason"))
            else -> Unknown(type)
        }
    }
}
