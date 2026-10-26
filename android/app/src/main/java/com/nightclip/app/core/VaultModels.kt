package com.nightclip.app.core

import org.json.JSONArray
import org.json.JSONObject

/** Credentials of the vault: master password plus an optional key file. Never printed, never persisted by the shell. */
class VaultCreds(val password: String, val keyFile: ByteArray? = null) {
    override fun toString() = "VaultCreds(redacted)"
}

/** Argon2d cost the vault file uses (`vault.status.kdf`; the Go struct has no JSON tags, so the keys are capitalised). */
data class VaultKdfInfo(val memoryMiB: Long, val iterations: Long, val parallelism: Int)

/** `vault.status`. */
data class VaultStatus(
    val exists: Boolean,
    val unlocked: Boolean,
    val autolockS: Int,
    val clipClearS: Int,
    /** Seconds until the next unlock attempt is accepted (0 = not rate limited). */
    val retryAfterS: Int,
    val entries: Int?,
    val kdf: VaultKdfInfo?,
    val backups: Int,
    val lastManualBackupAt: Long,
)

/** Row of `vault.list`. Contains no secrets and no notes. */
data class VaultEntrySummary(
    val id: String,
    val groupId: String,
    val kind: String,
    val title: String,
    val username: String,
    val url: String,
    val tags: List<String>,
    val hasTotp: Boolean,
    val hasAttachments: Boolean,
    val modifiedAt: Long,
    val expiresAt: Long,
    val inTrash: Boolean,
)

data class VaultField(val name: String, val value: String, val protected: Boolean, val hasValue: Boolean)

data class VaultAttachment(val name: String, val size: Long)

data class PasswordStrength(val bits: Double, val label: String)

/** `vault.get`: everything except the secrets (password, TOTP secret and protected field values come from vault.reveal). */
data class VaultEntryDetail(
    val id: String,
    val groupId: String,
    val kind: String,
    val title: String,
    val username: String,
    val url: String,
    val notes: String,
    val tags: List<String>,
    val fields: List<VaultField>,
    val hasPassword: Boolean,
    val hasTotp: Boolean,
    val expiresAt: Long,
    val createdAt: Long,
    val modifiedAt: Long,
    val attachments: List<VaultAttachment>,
    val historyCount: Int,
    val inTrash: Boolean,
    val strength: PasswordStrength,
)

data class VaultHistoryVersion(val index: Int, val modifiedAt: Long, val title: String, val username: String, val url: String)

data class VaultGroup(val id: String, val parentId: String, val name: String, val path: String, val entries: Int, val isTrash: Boolean)

data class HealthIssue(val id: String, val title: String, val info: String)

data class VaultHealth(
    val weak: List<HealthIssue>,
    val reused: List<HealthIssue>,
    val old: List<HealthIssue>,
    val expired: List<HealthIssue>,
    val withoutTotp: Int,
    val total: Int,
) {
    val issueCount: Int get() = weak.size + reused.size + old.size + expired.size
}

data class VaultBackup(val name: String, val time: Long, val size: Long, val reason: String)

data class BackupCheck(val entries: Int, val groups: Int, val inTrash: Int, val modifiedAt: Long)

data class DiffItem(val id: String, val title: String, val modifiedAt: Long)

data class RestorePreview(
    val onlyInBackup: List<DiffItem>,
    val onlyInCurrent: List<DiffItem>,
    val changed: List<DiffItem>,
    val same: Int,
)

data class MergeStats(
    val entriesAdded: Int = 0,
    val entriesUpdated: Int = 0,
    val entriesRemoved: Int = 0,
    val entriesMoved: Int = 0,
    val groupsAdded: Int = 0,
    val groupsRemoved: Int = 0,
)

data class CsvImportResult(val added: Int, val skipped: Int)

data class TotpCode(val code: String, val secondsLeft: Int) {
    override fun toString() = "TotpCode(secondsLeft=$secondsLeft)"
}

/** Result of `vault.generate`; [value] is a fresh secret, so it is never printed. */
data class GeneratedSecret(val value: String, val strength: PasswordStrength) {
    override fun toString() = "GeneratedSecret(strength=$strength)"
}

/** `vault.copy`: the text to put on the clipboard and when to clear it again (0 = never). */
data class CopyResult(val value: String, val clearAfterS: Int) {
    override fun toString() = "CopyResult(clearAfterS=$clearAfterS)"
}

/** Parsers of the `vault.*` result shapes (docs/vault-api.md §3). Missing keys fall back to neutral values. */
object VaultParsers {
    private fun JSONArray?.objects(): List<JSONObject> =
        if (this == null) emptyList() else (0 until length()).mapNotNull { optJSONObject(it) }

    private fun JSONArray?.strings(): List<String> =
        if (this == null) emptyList() else (0 until length()).map { optString(it, "") }

    private fun JSONObject.kdfOrNull(key: String): VaultKdfInfo? {
        val k = optJSONObject(key) ?: return null
        return VaultKdfInfo(
            memoryMiB = if (k.has("MemoryMiB")) k.optLong("MemoryMiB") else k.optLong("memory_mib"),
            iterations = if (k.has("Iterations")) k.optLong("Iterations") else k.optLong("iterations"),
            parallelism = if (k.has("Parallelism")) k.optInt("Parallelism") else k.optInt("parallelism"),
        )
    }

    fun status(o: JSONObject) = VaultStatus(
        exists = o.optBoolean("exists", false),
        unlocked = o.optBoolean("unlocked", false),
        autolockS = o.optInt("autolock_s", 300),
        clipClearS = o.optInt("clip_clear_s", 20),
        retryAfterS = o.optInt("retry_after_s", 0),
        entries = if (o.has("entries")) o.optInt("entries") else null,
        kdf = o.kdfOrNull("kdf"),
        backups = o.optInt("backups", 0),
        lastManualBackupAt = o.optLong("last_manual_backup_at", 0),
    )

    fun summary(o: JSONObject) = VaultEntrySummary(
        id = o.optString("id", ""),
        groupId = o.optString("group_id", ""),
        kind = o.optString("kind", "login"),
        title = o.optString("title", ""),
        username = o.optString("username", ""),
        url = o.optString("url", ""),
        tags = o.optJSONArray("tags").strings(),
        hasTotp = o.optBoolean("has_totp", false),
        hasAttachments = o.optBoolean("has_attachments", false),
        modifiedAt = o.optLong("modified_at", 0),
        expiresAt = o.optLong("expires_at", 0),
        inTrash = o.optBoolean("in_trash", false),
    )

    fun list(o: JSONObject): List<VaultEntrySummary> = o.optJSONArray("entries").objects().map(::summary)

    fun strength(o: JSONObject?) = PasswordStrength(o?.optDouble("bits", 0.0) ?: 0.0, o?.optString("label", "very_weak") ?: "very_weak")

    fun detail(o: JSONObject) = VaultEntryDetail(
        id = o.optString("id", ""),
        groupId = o.optString("group_id", ""),
        kind = o.optString("kind", "login"),
        title = o.optString("title", ""),
        username = o.optString("username", ""),
        url = o.optString("url", ""),
        notes = o.optString("notes", ""),
        tags = o.optJSONArray("tags").strings(),
        fields = o.optJSONArray("fields").objects().map {
            VaultField(it.optString("name", ""), it.optString("value", ""), it.optBoolean("protected", false), it.optBoolean("has_value", false))
        },
        hasPassword = o.optBoolean("has_password", false),
        hasTotp = o.optBoolean("has_totp", false),
        expiresAt = o.optLong("expires_at", 0),
        createdAt = o.optLong("created_at", 0),
        modifiedAt = o.optLong("modified_at", 0),
        attachments = o.optJSONArray("attachments").objects().map { VaultAttachment(it.optString("name", ""), it.optLong("size", 0)) },
        historyCount = o.optInt("history", 0),
        inTrash = o.optBoolean("in_trash", false),
        strength = strength(o.optJSONObject("password_strength")),
    )

    fun history(o: JSONObject): List<VaultHistoryVersion> = o.optJSONArray("versions").objects().map {
        VaultHistoryVersion(it.optInt("index", 0), it.optLong("modified_at", 0), it.optString("title", ""), it.optString("username", ""), it.optString("url", ""))
    }

    fun groups(o: JSONObject): List<VaultGroup> = o.optJSONArray("groups").objects().map {
        VaultGroup(it.optString("id", ""), it.optString("parent_id", ""), it.optString("name", ""), it.optString("path", ""), it.optInt("entries", 0), it.optBoolean("is_trash", false))
    }

    private fun issues(a: JSONArray?): List<HealthIssue> =
        a.objects().map { HealthIssue(it.optString("id", ""), it.optString("title", ""), it.optString("info", "")) }

    fun health(o: JSONObject) = VaultHealth(
        weak = issues(o.optJSONArray("weak")),
        reused = issues(o.optJSONArray("reused")),
        old = issues(o.optJSONArray("old")),
        expired = issues(o.optJSONArray("expired")),
        withoutTotp = o.optInt("without_totp", 0),
        total = o.optInt("total", 0),
    )

    fun backup(o: JSONObject) = VaultBackup(o.optString("name", ""), o.optLong("time", 0), o.optLong("size", 0), o.optString("reason", ""))

    /** Parses the `result` of `vault.backup_list`: newest first, as the core returns it. */
    fun backups(o: JSONObject): List<VaultBackup> = o.optJSONArray("backups").objects().map(::backup)

    fun backupCheck(o: JSONObject) = BackupCheck(o.optInt("entries", 0), o.optInt("groups", 0), o.optInt("in_trash", 0), o.optLong("modified_at", 0))

    private fun diff(a: JSONArray?): List<DiffItem> =
        a.objects().map { DiffItem(it.optString("id", ""), it.optString("title", ""), it.optLong("modified_at", 0)) }

    fun restorePreview(o: JSONObject) = RestorePreview(
        onlyInBackup = diff(o.optJSONArray("only_in_backup")),
        onlyInCurrent = diff(o.optJSONArray("only_in_current")),
        changed = diff(o.optJSONArray("changed")),
        same = o.optInt("same", 0),
    )

    /** `{"stats":{…}}` of restore/import_file, or the stats object itself. */
    fun stats(o: JSONObject): MergeStats {
        val s = o.optJSONObject("stats") ?: o
        return MergeStats(
            entriesAdded = s.optInt("entries_added", 0),
            entriesUpdated = s.optInt("entries_updated", 0),
            entriesRemoved = s.optInt("entries_removed", 0),
            entriesMoved = s.optInt("entries_moved", 0),
            groupsAdded = s.optInt("groups_added", 0),
            groupsRemoved = s.optInt("groups_removed", 0),
        )
    }

    fun csvResult(o: JSONObject) = CsvImportResult(o.optInt("added", 0), o.optInt("skipped", 0))

    fun totp(o: JSONObject) = TotpCode(o.optString("code", ""), o.optInt("seconds_left", 0))

    fun generated(o: JSONObject) = GeneratedSecret(o.optString("value", ""), strength(o.optJSONObject("strength")))

    fun copy(o: JSONObject) = CopyResult(o.optString("value", ""), o.optInt("clear_after_s", 0))
}
