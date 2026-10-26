package com.nightclip.app

import org.json.JSONObject

/** The core's `update.status` result in a UI-friendly shape. */
data class UpdateState(
    val enabled: Boolean = false,
    val auto: Boolean = true,
    val state: String = "idle",
    val currentCode: Long = 0,
    val currentName: String = "",
    val latestCode: Long = 0,
    val latestName: String = "",
    val notes: String = "",
    val mandatory: Boolean = false,
    val received: Long = 0,
    val total: Long = 0,
    val readyPath: String = "",
    val errorCode: String = "",
    val errorMessage: String = "",
) {
    enum class Action { NONE, CHECK, DOWNLOAD, INSTALL }

    /** What the single action button does in this state. */
    val action: Action
        get() = if (!enabled) Action.NONE else when (state) {
            "available" -> Action.DOWNLOAD
            "ready" -> Action.INSTALL
            "checking", "downloading" -> Action.NONE
            else -> Action.CHECK
        }

    val buttonText: String
        get() = when (action) {
            Action.CHECK -> "Check for updates"
            Action.DOWNLOAD -> "Download"
            Action.INSTALL -> "Install update"
            Action.NONE -> ""
        }

    /** 0..1 while downloading. */
    val progress: Float
        get() = if (total > 0) (received.toDouble() / total).coerceIn(0.0, 1.0).toFloat() else 0f

    val text: String
        get() {
            if (!enabled) {
                return if (currentCode == 0L) "Development build: updates are off" else "Updates are not configured"
            }
            val must = if (mandatory) " (required)" else ""
            return when (state) {
                "checking" -> "Checking for updates…"
                "up_to_date" -> "Up to date ($currentName)"
                "available" -> "Version $latestName is available$must"
                "downloading" -> "Downloading $latestName… ${(progress * 100).toInt()}%"
                "ready" -> "Version $latestName is ready to install$must"
                "error" -> "Update failed: ${errorMessage.ifEmpty { errorCode }}"
                else -> "Version $currentName"
            }
        }

    companion object {
        /** Parses the `result` object of `update.status`; tolerant of missing fields. */
        fun from(result: JSONObject): UpdateState {
            val latest = result.optJSONObject("latest")
            val error = result.optJSONObject("error")
            return UpdateState(
                enabled = result.optBoolean("enabled", false),
                auto = result.optBoolean("auto", true),
                state = result.optString("state", "idle"),
                currentCode = result.optLong("current_code", 0),
                currentName = result.optString("current_name", ""),
                latestCode = latest?.optLong("version_code", 0) ?: 0,
                latestName = latest?.optString("version_name", "") ?: "",
                notes = latest?.optString("notes", "") ?: "",
                mandatory = result.optBoolean("mandatory", false),
                received = result.optLong("received", 0),
                total = result.optLong("total", 0),
                readyPath = result.optString("ready_path", ""),
                errorCode = error?.optString("code", "") ?: "",
                errorMessage = error?.optString("message", "") ?: "",
            )
        }
    }
}
