package com.nightclip.app.core

import org.json.JSONArray
import org.json.JSONObject

// Typed forms of the `notes.*` facade results (notes/api-spec.md). Every `*_at` value here is Unix MILLISECONDS
// (the rest of the app uses seconds). The ids are opaque strings. Anything may be missing: the core omits empty
// fields, so every parser below supplies a default instead of failing.

/** One line of `notes.list`; carries no body. */
data class NoteSummary(
    val noteId: String,
    /** The core fills an empty title with the first line of the text. */
    val title: String,
    val updatedAt: Long,
    val deleted: Boolean,
    val preview: String,
    /** The original note when this one is a conflict copy, "" otherwise. */
    val conflictOf: String = "",
) {
    val isConflictCopy: Boolean get() = conflictOf.isNotEmpty()
}

/** One page of `notes.list`; [nextCursor] null = the end. */
data class NotesPage(val items: List<NoteSummary>, val nextCursor: String?)

/** `notes.get`: one revision of a note with its text. */
data class NoteDoc(
    val noteId: String,
    val title: String,
    val body: String,
    val formatVersion: Int,
    val revId: String,
    val parents: List<String>,
    val createdAt: Long,
    /** When THIS revision was made. */
    val updatedAt: Long,
    val deleted: Boolean,
    /** A device id, or "merge" for a revision made by a merge. */
    val originDevice: String,
    val revCount: Int,
    /** False when an older revision was asked for. */
    val isHead: Boolean,
    val conflictOf: String = "",
) {
    // The text of a note never goes into logs, not even by accident.
    override fun toString() = "NoteDoc(noteId=$noteId, revId=$revId, deleted=$deleted)"
}

/** `notes.create`. */
data class NoteCreated(val noteId: String, val revId: String)

/**
 * `notes.update`: [revId] is the newest revision after the call; [conflictNoteId] set = the older side became a copy.
 * [changed] false = the edit changed nothing once normalised: no revision was made and [revId] is simply the note's
 * current one, which after a stale base may be another device's revision and not the result of this edit.
 */
data class UpdateOutcome(val revId: String, val merged: Boolean, val conflictNoteId: String? = null, val changed: Boolean = true)

/** One revision in `notes.history` (no body: ask `notes.get` with the [revId]). */
data class RevisionInfo(
    val revId: String,
    val title: String,
    val bodyHash: String,
    val originDevice: String,
    val createdAt: Long,
    val parents: List<String>,
    val deleted: Boolean,
)

data class HistoryPage(val items: List<RevisionInfo>, val nextCursor: String?)

/** One paired device in `notes.sync_status`. [detail] is the core's English diagnostic text, never shown as is. */
data class NotesPeerStatus(
    val peerId: String,
    val name: String,
    val connected: Boolean,
    /** idle, syncing, error or peer_old. */
    val state: String,
    val detail: String,
    /** 0 = never succeeded. */
    val lastSyncAt: Long,
    val behind: Int,
    val conflictCount: Int,
)

data class NotesSyncStatus(val peers: List<NotesPeerStatus>, val pendingPush: Int) {
    companion object {
        val EMPTY = NotesSyncStatus(emptyList(), 0)
    }
}

/** `notes.sync_conflicts` item: [conflictNoteId] is the copy, [noteId] the note it was set aside from. */
data class NoteConflict(val noteId: String, val conflictNoteId: String, val title: String, val createdAt: Long)

data class ConflictPage(val items: List<NoteConflict>, val nextCursor: String?)

/** A range of a [SearchHit] title or snippet. Offsets count Unicode CODE POINTS, not UTF-16 units (see NotesSearchLogic). */
data class Highlight(val start: Int, val end: Int)

/** One result of `notes.search`. */
data class SearchHit(
    val noteId: String,
    val title: String,
    val updatedAt: Long,
    val snippet: String,
    val highlights: List<Highlight>,
    val titleHighlights: List<Highlight>,
    val conflictOf: String = "",
) {
    val isConflictCopy: Boolean get() = conflictOf.isNotEmpty()
}

data class SearchPage(val items: List<SearchHit>, val nextCursor: String?)

/** `notes.attachment_add` / `_get` / `_export`. When [present] is false the file has not reached this device yet and the other fields are empty. */
data class AttachmentInfo(
    val blobId: String,
    val name: String,
    val mime: String,
    val size: Long,
    val present: Boolean,
    /** What to put into the note (only `notes.attachment_add` sets it). */
    val markdown: String = "",
)

/** `notes.attachment_cleanup`. */
data class CleanupResult(val removed: Int, val freedBytes: Long, val kept: Int)

/** `notes.export`. */
data class ExportResult(val path: String, val count: Int, val bytes: Long)

/** A file `notes.import` did not turn into a note; [reason] is the core's English text. */
data class ImportSkip(val name: String, val reason: String)

/** `notes.import`. */
data class ImportResult(val imported: Int, val skipped: List<ImportSkip>, val noteIds: List<String>)

data class TocEntry(val blockId: String, val level: Int, val text: String)

/** `notes.render`: the block tree the shell draws. It never parses Markdown itself. */
data class RenderedNote(val noteId: String, val revId: String, val blocks: List<NoteBlock>, val toc: List<TocEntry>)

/** `notes.normalize`. */
data class Normalized(val bodyNorm: String, val bodyHash: String)

enum class ColumnAlign { NONE, LEFT, CENTER, RIGHT }

/** Inline content of a heading, paragraph or table cell (api-spec §2.1). */
sealed interface NoteInline {
    data class Text(val text: String) : NoteInline
    data class Em(val children: List<NoteInline>) : NoteInline
    data class Strong(val children: List<NoteInline>) : NoteInline
    data class Del(val children: List<NoteInline>) : NoteInline
    data class Code(val text: String) : NoteInline
    data class Link(val href: String, val title: String, val children: List<NoteInline>) : NoteInline
    data class Image(val href: String, val alt: String, val title: String) : NoteInline
    data object HardBreak : NoteInline
    data object SoftBreak : NoteInline

    /** A kind this app version does not know: its text or children are shown as plain text. */
    data class Other(val kind: String, val text: String, val children: List<NoteInline>) : NoteInline
}

data class ListItem(val checked: Boolean?, val blocks: List<NoteBlock>)

/** Block of the render tree. [raw] is the block's canonical Markdown. */
sealed interface NoteBlock {
    val blockId: String
    val raw: String

    data class Heading(override val blockId: String, override val raw: String, val level: Int, val inline: List<NoteInline>) : NoteBlock
    data class Paragraph(override val blockId: String, override val raw: String, val inline: List<NoteInline>) : NoteBlock
    data class CodeBlock(override val blockId: String, override val raw: String, val lang: String, val info: String, val text: String) : NoteBlock
    data class Quote(override val blockId: String, override val raw: String, val children: List<NoteBlock>) : NoteBlock
    data class ListBlock(
        override val blockId: String,
        override val raw: String,
        val ordered: Boolean,
        val start: Int,
        val tight: Boolean,
        val items: List<ListItem>,
    ) : NoteBlock

    data class Table(
        override val blockId: String,
        override val raw: String,
        val align: List<ColumnAlign>,
        val header: List<List<NoteInline>>,
        val rows: List<List<List<NoteInline>>>,
    ) : NoteBlock

    data class Rule(override val blockId: String, override val raw: String) : NoteBlock

    /** Raw HTML kept as text. It is shown literally and never interpreted. */
    data class Html(override val blockId: String, override val raw: String, val text: String) : NoteBlock

    /** Syntax the core does not support (footnote, math …) or a block kind this app does not know: shown literally. */
    data class Unknown(override val blockId: String, override val raw: String, val kind: String, val text: String) : NoteBlock
}

/** Parsers for the JSON the core returns; all tolerate missing fields, JSON nulls and wrong types. */
object NotesParsers {
    /** Deepest block/inline nesting that is turned into objects; deeper content is kept as literal text. */
    const val MAX_DEPTH = 32

    // org.json's optString returns the text "null" for a JSON null on Android; go through isNull instead.
    private fun JSONObject.str(key: String): String = if (isNull(key)) "" else optString(key, "")
    private fun JSONObject.strOrNull(key: String): String? = str(key).takeIf { it.isNotEmpty() }
    private fun JSONObject.long(key: String): Long = if (isNull(key)) 0L else optLong(key, 0L)
    private fun JSONObject.int(key: String, fallback: Int = 0): Int = if (isNull(key)) fallback else optInt(key, fallback)
    private fun JSONObject.bool(key: String, fallback: Boolean = false): Boolean = if (isNull(key)) fallback else optBoolean(key, fallback)

    private fun JSONArray?.objects(): List<JSONObject> =
        if (this == null) emptyList() else (0 until length()).mapNotNull { optJSONObject(it) }

    private fun JSONArray?.strings(): List<String> =
        if (this == null) emptyList() else (0 until length()).mapNotNull { i -> if (isNull(i)) null else optString(i, "") }

    fun summary(o: JSONObject) = NoteSummary(
        noteId = o.str("note_id"),
        title = o.str("title"),
        updatedAt = o.long("updated_at"),
        deleted = o.bool("deleted"),
        preview = o.str("preview"),
        conflictOf = o.str("conflict_of"),
    )

    fun page(o: JSONObject) = NotesPage(o.optJSONArray("items").objects().map(::summary).filter { it.noteId.isNotEmpty() }, o.strOrNull("next_cursor"))

    fun doc(o: JSONObject) = NoteDoc(
        noteId = o.str("note_id"),
        title = o.str("title"),
        body = o.str("body"),
        formatVersion = o.int("format_version"),
        revId = o.str("rev_id"),
        parents = o.optJSONArray("parents").strings(),
        createdAt = o.long("created_at"),
        updatedAt = o.long("updated_at"),
        deleted = o.bool("deleted"),
        originDevice = o.str("origin_device"),
        revCount = o.int("rev_count"),
        isHead = o.bool("is_head", true),
        conflictOf = o.str("conflict_of"),
    )

    fun created(o: JSONObject) = NoteCreated(o.str("note_id"), o.str("rev_id"))

    // A core without the field made a revision whenever it said so by merged / a new id: absent counts as changed.
    fun updated(o: JSONObject) = UpdateOutcome(o.str("rev_id"), o.bool("merged"), o.strOrNull("conflict_note_id"), o.bool("changed", true))

    fun revision(o: JSONObject) = RevisionInfo(
        revId = o.str("rev_id"),
        title = o.str("title"),
        bodyHash = o.str("body_hash"),
        originDevice = o.str("origin_device"),
        createdAt = o.long("created_at"),
        parents = o.optJSONArray("parents").strings(),
        deleted = o.bool("deleted"),
    )

    fun history(o: JSONObject) =
        HistoryPage(o.optJSONArray("items").objects().map(::revision).filter { it.revId.isNotEmpty() }, o.strOrNull("next_cursor"))

    fun peerStatus(o: JSONObject) = NotesPeerStatus(
        peerId = o.str("peer_id"),
        name = o.str("name"),
        connected = o.bool("connected"),
        state = o.str("state").ifEmpty { "idle" },
        detail = o.str("detail"),
        lastSyncAt = o.long("last_sync_at"),
        behind = o.int("behind"),
        conflictCount = o.int("conflict_count"),
    )

    fun syncStatus(o: JSONObject) =
        NotesSyncStatus(o.optJSONArray("peers").objects().map(::peerStatus).filter { it.peerId.isNotEmpty() }, o.int("pending_push"))

    fun conflict(o: JSONObject) = NoteConflict(o.str("note_id"), o.str("conflict_note_id"), o.str("title"), o.long("created_at"))

    fun conflicts(o: JSONObject) =
        ConflictPage(o.optJSONArray("items").objects().map(::conflict).filter { it.noteId.isNotEmpty() && it.conflictNoteId.isNotEmpty() }, o.strOrNull("next_cursor"))

    private fun highlights(a: JSONArray?): List<Highlight> =
        a.objects().map { Highlight(it.int("start"), it.int("end")) }.filter { it.end > it.start && it.start >= 0 }

    fun hit(o: JSONObject) = SearchHit(
        noteId = o.str("note_id"),
        title = o.str("title"),
        updatedAt = o.long("updated_at"),
        snippet = o.str("snippet"),
        highlights = highlights(o.optJSONArray("highlights")),
        titleHighlights = highlights(o.optJSONArray("title_highlights")),
        conflictOf = o.str("conflict_of"),
    )

    fun search(o: JSONObject) =
        SearchPage(o.optJSONArray("items").objects().map(::hit).filter { it.noteId.isNotEmpty() }, o.strOrNull("next_cursor"))

    fun attachment(o: JSONObject) = AttachmentInfo(
        blobId = o.str("blob_id"),
        name = o.str("name"),
        mime = o.str("mime"),
        size = o.long("size"),
        present = o.bool("present"),
        markdown = o.str("markdown"),
    )

    fun cleanup(o: JSONObject) = CleanupResult(o.int("removed"), o.long("freed_bytes"), o.int("kept"))

    fun exported(o: JSONObject) = ExportResult(o.str("path"), o.int("count"), o.long("bytes"))

    fun imported(o: JSONObject) = ImportResult(
        imported = o.int("imported"),
        skipped = o.optJSONArray("skipped").objects().map { ImportSkip(it.str("name"), it.str("reason")) },
        noteIds = o.optJSONArray("note_ids").strings(),
    )

    fun normalized(o: JSONObject) = Normalized(o.str("body_norm"), o.str("body_hash"))

    fun rendered(o: JSONObject) = RenderedNote(
        noteId = o.str("note_id"),
        revId = o.str("rev_id"),
        blocks = blocks(o.optJSONArray("blocks"), 0),
        toc = o.optJSONArray("toc").objects().map { TocEntry(it.str("block_id"), it.int("level", 1), it.str("text")) },
    )

    // ---- render tree -------------------------------------------------------------------------

    fun blocks(a: JSONArray?, depth: Int): List<NoteBlock> = a.objects().map { block(it, depth) }

    fun block(o: JSONObject, depth: Int = 0): NoteBlock {
        val id = o.str("block_id")
        val raw = o.str("raw")
        val kind = o.str("kind")
        // Too deeply nested to build safely: keep the canonical text, shown literally.
        if (depth >= MAX_DEPTH) return NoteBlock.Unknown(id, raw, kind, o.str("text").ifEmpty { raw })
        return when (kind) {
            "heading" -> NoteBlock.Heading(id, raw, o.int("level", 1).coerceIn(1, 6), inlines(o.optJSONArray("inline"), depth + 1))
            "paragraph" -> NoteBlock.Paragraph(id, raw, inlines(o.optJSONArray("inline"), depth + 1))
            "code" -> NoteBlock.CodeBlock(id, raw, o.str("lang"), o.str("info"), o.str("text"))
            "quote" -> NoteBlock.Quote(id, raw, blocks(o.optJSONArray("children"), depth + 1))
            "list" -> NoteBlock.ListBlock(
                id, raw,
                ordered = o.bool("ordered"),
                start = o.int("start", 1),
                tight = o.bool("tight", true),
                items = o.optJSONArray("items").objects().map { item ->
                    ListItem(if (item.isNull("checked")) null else item.optBoolean("checked"), blocks(item.optJSONArray("blocks"), depth + 1))
                },
            )
            "table" -> NoteBlock.Table(
                id, raw,
                align = o.optJSONArray("align").strings().map(::alignOf),
                header = cells(o.optJSONArray("header"), depth + 1),
                rows = (o.optJSONArray("rows")?.let { r -> (0 until r.length()).map { cells(r.optJSONArray(it), depth + 1) } } ?: emptyList()),
            )
            "hr" -> NoteBlock.Rule(id, raw)
            "html" -> NoteBlock.Html(id, raw, o.str("text").ifEmpty { raw })
            else -> NoteBlock.Unknown(id, raw, kind, o.str("text").ifEmpty { raw })
        }
    }

    private fun alignOf(s: String) = when (s) {
        "left" -> ColumnAlign.LEFT
        "center" -> ColumnAlign.CENTER
        "right" -> ColumnAlign.RIGHT
        else -> ColumnAlign.NONE
    }

    private fun cells(row: JSONArray?, depth: Int): List<List<NoteInline>> =
        if (row == null) emptyList() else (0 until row.length()).map { inlines(row.optJSONArray(it), depth) }

    fun inlines(a: JSONArray?, depth: Int): List<NoteInline> = a.objects().mapNotNull { inline(it, depth) }

    private fun inline(o: JSONObject, depth: Int): NoteInline? {
        val kind = o.str("t")
        val text = o.str("text")
        if (depth >= MAX_DEPTH) return if (text.isNotEmpty()) NoteInline.Text(text) else null
        val children = { inlines(o.optJSONArray("children"), depth + 1) }
        return when (kind) {
            "text" -> NoteInline.Text(text)
            "em" -> NoteInline.Em(children())
            "strong" -> NoteInline.Strong(children())
            "del" -> NoteInline.Del(children())
            "code" -> NoteInline.Code(text)
            "link" -> NoteInline.Link(o.str("href"), o.str("title"), children())
            "image" -> NoteInline.Image(o.str("href"), o.str("alt"), o.str("title"))
            "br" -> NoteInline.HardBreak
            "softbr" -> NoteInline.SoftBreak
            else -> NoteInline.Other(kind, text, children())
        }
    }
}
