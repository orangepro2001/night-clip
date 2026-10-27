package com.nightclip.app.core

import com.nightclip.app.logic.NotesErrors
import org.json.JSONObject

/**
 * Type-safe wrapper over the `notes.*` facade methods (notes/api-spec.md). Every function suspends off the main thread.
 * Nothing here logs: parameters and results carry the text of notes.
 *
 * A failed call carries the core's error code and the general text for it; screens that know more about what they
 * were doing ask [NotesErrors.message] with a context.
 */
object NotesApi {
    /** Resolve actions of `notes.sync_resolve`. */
    const val KEEP_MAIN = "keep_main"
    const val KEEP_CONFLICT = "keep_conflict"
    const val MERGE = "merge"

    private suspend fun <T> run(method: String, params: JSONObject = JSONObject(), parse: (JSONObject) -> T): ApiResult<T> {
        val r = CoreHost.call(method, params)
        return if (r.ok) ApiResult.Ok(parse(r.result)) else ApiResult.Fail(r.errorCode, NotesErrors.message(r.errorCode))
    }

    // ---- notes -------------------------------------------------------------------------------

    suspend fun list(limit: Int, cursor: String?, deleted: Boolean) =
        run("notes.list", listParams(limit, cursor, deleted), NotesParsers::page)

    /** [revId] null = the newest revision. */
    suspend fun get(noteId: String, revId: String? = null) = run("notes.get", idParams(noteId, revId), NotesParsers::doc)

    suspend fun create(title: String, body: String) = run("notes.create", createParams(title, body), NotesParsers::created)

    /**
     * [baseRevId] is the revision the edit was made on; when it is not the newest any more the core merges the edit.
     * [title] / [body] null = unchanged (sent as absent, which keeps the base revision's value).
     */
    suspend fun update(noteId: String, baseRevId: String, title: String?, body: String?) =
        run("notes.update", updateParams(noteId, baseRevId, title, body), NotesParsers::updated)

    suspend fun delete(noteId: String) = run("notes.delete", JSONObject().put("note_id", noteId)) { it.optString("rev_id", "") }
    suspend fun restore(noteId: String) = run("notes.restore", JSONObject().put("note_id", noteId)) { it.optString("rev_id", "") }

    suspend fun history(noteId: String, limit: Int, cursor: String?) =
        run("notes.history", listParams(limit, cursor).put("note_id", noteId), NotesParsers::history)

    suspend fun render(noteId: String, revId: String? = null) = run("notes.render", idParams(noteId, revId), NotesParsers::rendered)

    /** The live preview: renders TEXT that is not saved (and so has no note id or revision). */
    suspend fun renderBody(body: String) = run("notes.render", JSONObject().put("body", body), NotesParsers::rendered)

    suspend fun normalize(body: String) = run("notes.normalize", JSONObject().put("body", body), NotesParsers::normalized)

    // ---- search, attachments, export and import ----------------------------------------------

    /** Full-text search of the live notes. Highlight offsets in the result are code points. */
    suspend fun search(query: String, limit: Int, cursor: String?) = run("notes.search", searchParams(query, limit, cursor), NotesParsers::search)

    /** Stores the file at the absolute [path] as an attachment; the result's `markdown` is what the note should contain. */
    suspend fun attachmentAdd(path: String) = run("notes.attachment_add", JSONObject().put("path", path), NotesParsers::attachment)

    suspend fun attachmentGet(blobId: String) = run("notes.attachment_get", JSONObject().put("blob_id", blobId), NotesParsers::attachment)

    /** Writes the attachment to the absolute [path] (the folder must exist; an existing file is replaced). */
    suspend fun attachmentExport(blobId: String, path: String) =
        run("notes.attachment_export", JSONObject().put("blob_id", blobId).put("path", path), NotesParsers::attachment)

    suspend fun attachmentCleanup() = run("notes.attachment_cleanup", parse = NotesParsers::cleanup)

    /** Writes a ZIP to [path], which must not exist. */
    suspend fun export(path: String, includeTrash: Boolean) = run("notes.export", exportParams(path, includeTrash), NotesParsers::exported)

    /** Imports the .zip / .md / .txt file at [path]; always makes new notes. */
    suspend fun import(path: String) = run("notes.import", JSONObject().put("path", path), NotesParsers::imported)

    // ---- sync --------------------------------------------------------------------------------

    suspend fun syncStatus() = run("notes.sync_status", parse = NotesParsers::syncStatus)

    /** Starts a round with [peerId] (null = every device) and returns at once; progress arrives as events. */
    suspend fun syncNow(peerId: String? = null) =
        run("notes.sync_now", JSONObject().apply { if (!peerId.isNullOrEmpty()) put("peer_id", peerId) }) { it.optBoolean("started", false) }

    suspend fun conflicts(limit: Int = 100, cursor: String? = null) =
        run("notes.sync_conflicts", listParams(limit, cursor), NotesParsers::conflicts)

    /** [action]: [KEEP_MAIN], [KEEP_CONFLICT] or [MERGE]. Result: the original note's newest revision. */
    suspend fun resolve(noteId: String, conflictNoteId: String, action: String) =
        run("notes.sync_resolve", resolveParams(noteId, conflictNoteId, action)) { it.optString("rev_id", "") }

    // ---- request bodies (visible for tests) --------------------------------------------------

    internal fun listParams(limit: Int, cursor: String?, deleted: Boolean = false): JSONObject {
        val p = JSONObject()
        if (limit > 0) p.put("limit", limit)
        if (!cursor.isNullOrEmpty()) p.put("cursor", cursor)
        if (deleted) p.put("deleted", true)
        return p
    }

    internal fun searchParams(query: String, limit: Int, cursor: String?): JSONObject {
        val p = JSONObject().put("query", query)
        if (limit > 0) p.put("limit", limit)
        if (!cursor.isNullOrEmpty()) p.put("cursor", cursor)
        return p
    }

    internal fun exportParams(path: String, includeTrash: Boolean): JSONObject {
        val p = JSONObject().put("path", path)
        if (includeTrash) p.put("include_trash", true)
        return p
    }

    internal fun idParams(noteId: String, revId: String?): JSONObject {
        val p = JSONObject().put("note_id", noteId)
        if (!revId.isNullOrEmpty()) p.put("rev_id", revId)
        return p
    }

    internal fun createParams(title: String, body: String): JSONObject = JSONObject().put("title", title).put("body", body)

    internal fun updateParams(noteId: String, baseRevId: String, title: String?, body: String?): JSONObject {
        val p = JSONObject().put("note_id", noteId)
        if (baseRevId.isNotEmpty()) p.put("base_rev_id", baseRevId)
        if (title != null) p.put("title", title)
        if (body != null) p.put("body", body)
        return p
    }

    internal fun resolveParams(noteId: String, conflictNoteId: String, action: String): JSONObject =
        JSONObject().put("note_id", noteId).put("conflict_note_id", conflictNoteId).put("action", action)
}
