package com.nightclip.app.core

import com.nightclip.app.logic.EntryDraft
import com.nightclip.app.logic.ExpiryDates
import com.nightclip.app.logic.GeneratorOptions
import org.json.JSONArray
import org.json.JSONObject

/**
 * Type-safe wrapper over the `vault.*` facade methods (docs/vault-api.md §3). Every function suspends off the main
 * thread. Nothing here logs: parameters carry master passwords and secrets.
 */
object VaultApi {
    private suspend fun <T> run(method: String, params: JSONObject = JSONObject(), parse: (JSONObject) -> T): ApiResult<T> {
        val r = CoreHost.call(method, params)
        return if (r.ok) ApiResult.Ok(parse(r.result)) else ApiResult.Fail(r.errorCode, r.userMessage, r.retryAfterS)
    }

    private fun b64(bytes: ByteArray): String = java.util.Base64.getEncoder().encodeToString(bytes)

    /** `credParams`: password plus the optional key file, written into [into]. */
    internal fun putCreds(into: JSONObject, creds: VaultCreds, passwordKey: String = "password", keyFileKey: String = "key_file_b64"): JSONObject {
        into.put(passwordKey, creds.password)
        creds.keyFile?.let { into.put(keyFileKey, b64(it)) }
        return into
    }

    private fun creds(c: VaultCreds) = putCreds(JSONObject(), c)

    // ---- lifecycle ---------------------------------------------------------------------------

    suspend fun status() = run("vault.status", parse = VaultParsers::status)

    /** Result: create_ms. */
    suspend fun create(c: VaultCreds, kdf: String) =
        run("vault.create", creds(c).put("kdf", kdf)) { it.optLong("create_ms", 0) }

    /** Result: unlock_ms. */
    suspend fun unlock(c: VaultCreds) = run("vault.unlock", creds(c)) { it.optLong("unlock_ms", 0) }

    suspend fun lock() = run("vault.lock") { it.optBoolean("was_unlocked", false) }
    suspend fun verifyMaster(c: VaultCreds) = run("vault.verify_master", creds(c)) { }

    suspend fun changeMaster(current: VaultCreds, new: VaultCreds) =
        run("vault.change_master", creds(current).put("new", creds(new))) { }

    // ---- tools -------------------------------------------------------------------------------

    suspend fun strength(password: String) =
        run("vault.strength", JSONObject().put("password", password), VaultParsers::strength)

    suspend fun generate(options: GeneratorOptions): ApiResult<GeneratedSecret> {
        val p = JSONObject()
        for ((k, v) in options.toParams()) p.put(k, v)
        return run("vault.generate", p, VaultParsers::generated)
    }

    suspend fun health() = run("vault.health", parse = VaultParsers::health)

    // ---- entries -----------------------------------------------------------------------------

    suspend fun list(query: String, groupId: String?, kind: String?, trashOnly: Boolean): ApiResult<List<VaultEntrySummary>> {
        val p = JSONObject()
        if (query.isNotBlank()) p.put("query", query.trim())
        if (!groupId.isNullOrEmpty()) p.put("group_id", groupId)
        if (kind != null) p.put("kind", kind)
        if (trashOnly) p.put("trash", true)
        return run("vault.list", p, VaultParsers::list)
    }

    suspend fun get(id: String) = run("vault.get", JSONObject().put("id", id), VaultParsers::detail)

    /** On-screen display only: drop the value as soon as it is not shown any more. */
    suspend fun reveal(id: String, field: String) =
        run("vault.reveal", JSONObject().put("id", id).put("field", field)) { it.optString("value", "") }

    /** For the clipboard: the core also registers the text so it is never broadcast. */
    suspend fun copy(id: String, field: String) =
        run("vault.copy", JSONObject().put("id", id).put("field", field), VaultParsers::copy)

    suspend fun totp(id: String) = run("vault.totp", JSONObject().put("id", id), VaultParsers::totp)

    suspend fun add(d: EntryDraft) = run("vault.add", JSONObject().put("entry", entryJson(d))) { it.optString("id", "") }
    suspend fun update(id: String, d: EntryDraft) =
        run("vault.update", JSONObject().put("id", id).put("entry", entryJson(d))) { }

    /** Result: true when the entry was deleted for good (it was in the recycle bin already). */
    suspend fun delete(id: String) = run("vault.delete", JSONObject().put("id", id)) { it.optBoolean("permanent", false) }
    suspend fun restoreEntry(id: String) = run("vault.restore_entry", JSONObject().put("id", id)) { }
    suspend fun emptyTrash() = run("vault.empty_trash") { it.optInt("removed", 0) }
    suspend fun move(id: String, groupId: String) = run("vault.move", JSONObject().put("id", id).put("group_id", groupId)) { }
    suspend fun history(id: String) = run("vault.history", JSONObject().put("id", id), VaultParsers::history)
    suspend fun historyRestore(id: String, index: Int) =
        run("vault.history_restore", JSONObject().put("id", id).put("index", index)) { }

    // ---- groups and attachments --------------------------------------------------------------

    suspend fun groups() = run("vault.groups", parse = VaultParsers::groups)
    suspend fun groupAdd(parentId: String, name: String) =
        run("vault.group_add", JSONObject().put("parent_id", parentId).put("name", name)) { it.optString("id", "") }

    suspend fun attachAdd(id: String, name: String, path: String) =
        run("vault.attach_add", JSONObject().put("id", id).put("name", name).put("path", path)) { }

    /** The core writes the attachment to [path] (which must not exist yet). */
    suspend fun attachGet(id: String, name: String, path: String) =
        run("vault.attach_get", JSONObject().put("id", id).put("name", name).put("path", path)) { it.optLong("size", 0) }

    suspend fun attachRemove(id: String, name: String) =
        run("vault.attach_remove", JSONObject().put("id", id).put("name", name)) { }

    // ---- backups and restore -----------------------------------------------------------------

    suspend fun backupList() = run("vault.backup_list", parse = VaultParsers::backups)
    suspend fun backupNow() = run("vault.backup_now", parse = VaultParsers::backup)
    suspend fun backupVerify(name: String, c: VaultCreds) =
        run("vault.backup_verify", creds(c).put("name", name), VaultParsers::backupCheck)

    /** [c] null = the current credentials. */
    suspend fun restorePreview(name: String, c: VaultCreds?): ApiResult<RestorePreview> {
        val p = JSONObject().put("name", name)
        if (c != null) putCreds(p, c)
        return run("vault.restore_preview", p, VaultParsers::restorePreview)
    }

    suspend fun restore(name: String, mode: String, ids: List<String>, c: VaultCreds?): ApiResult<MergeStats> {
        val p = JSONObject().put("name", name).put("mode", mode)
        if (ids.isNotEmpty()) p.put("ids", JSONArray(ids))
        if (c != null) putCreds(p, c)
        return run("vault.restore", p, VaultParsers::stats)
    }

    suspend fun importFile(path: String, c: VaultCreds) =
        run("vault.import_file", creds(c).put("path", path), VaultParsers::stats)

    suspend fun importCsv(path: String) = run("vault.import_csv", JSONObject().put("path", path), VaultParsers::csvResult)

    /** [confirm] is the current master credential, [exportCreds] the new ones that encrypt the exported file. */
    suspend fun exportFile(path: String, confirm: VaultCreds, exportCreds: VaultCreds, kdf: String?): ApiResult<Unit> {
        val p = JSONObject().put("path", path)
        putCreds(p, confirm, "confirm_password", "confirm_key_file_b64")
        putCreds(p, exportCreds)
        if (kdf != null) p.put("kdf", kdf)
        return run("vault.export_file", p) { }
    }

    suspend fun exportCsv(path: String, confirm: VaultCreds): ApiResult<Unit> {
        val p = JSONObject().put("path", path)
        putCreds(p, confirm, "confirm_password", "confirm_key_file_b64")
        return run("vault.export_csv", p) { }
    }

    // ---- settings ----------------------------------------------------------------------------

    suspend fun setAutolock(seconds: Int) = CoreApi.setSetting("vault_autolock_s", seconds.toString())
    suspend fun setClipClear(seconds: Int) = CoreApi.setSetting("vault_clip_clear_s", seconds.toString())

    /** The JSON of `vault.add` / `vault.update` `entry` (docs/vault-api.md §3.2). Visible for tests. */
    internal fun entryJson(d: EntryDraft, zone: java.time.ZoneId = java.time.ZoneId.systemDefault()): JSONObject {
        val o = JSONObject()
            .put("kind", d.kind)
            .put("title", d.title.trim())
            .put("username", d.username)
            .put("password", d.password)
            .put("url", d.url.trim())
            .put("notes", d.notes)
            .put("tags", JSONArray(d.tagList()))
            .put("totp", d.totp.trim())
            .put("expires_at", ExpiryDates.parse(d.expires, zone) ?: 0L)
            .put("group_id", d.groupId)
        val fields = JSONArray()
        for (f in d.fields) {
            fields.put(JSONObject().put("name", f.name.trim()).put("value", f.value).put("protected", f.protected))
        }
        o.put("fields", fields)
        return o
    }
}
