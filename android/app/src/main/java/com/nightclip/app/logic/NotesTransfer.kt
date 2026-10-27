package com.nightclip.app.logic

import com.nightclip.app.core.CleanupResult
import com.nightclip.app.core.ExportResult
import com.nightclip.app.core.ImportResult
import com.nightclip.app.core.ImportSkip
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/**
 * Export, import and attachment housekeeping of the notes: limits, file names (nothing a user, a file name or another
 * device chose ever reaches outside the app's cache folders) and the texts of the results. Pure.
 */
object NotesTransfer {
    /** The core reads the import file from disk; a bigger file is refused before it is copied. */
    const val MAX_IMPORT_BYTES = 256L * 1024 * 1024

    /** One attachment (notes/api-spec.md §2.2). */
    const val MAX_ATTACHMENT_BYTES = 32L * 1024 * 1024

    /** Cache folders (inside `cacheDir`) for the files that pass between the pickers and the core. */
    const val DIR_EXPORT = "notes-export"
    const val DIR_IMPORT = "notes-import"
    const val DIR_ADD = "notes-add"
    const val DIR_PICTURES = "notes"
    const val DIR_OPEN = "notes-open"

    /** MIME types offered by the import picker. */
    val IMPORT_MIME_TYPES = arrayOf(
        "application/zip", "application/x-zip-compressed", "text/markdown", "text/x-markdown", "text/plain", "application/octet-stream",
    )

    /** The name proposed in the "save as" dialog: `clipbridge-notes-20261027-1430.zip`. */
    fun exportFileName(nowMs: Long, zone: ZoneId = ZoneId.systemDefault()): String =
        "clipbridge-notes-" + DateTimeFormatter.ofPattern("yyyyMMdd-HHmm").withZone(zone).format(Instant.ofEpochMilli(nowMs)) + ".zip"

    /**
     * A name that does not exist yet, for a file the core must create ("notes.export" never overwrites): the caller
     * supplies a [nonce] (a random number); anything that is not a letter or digit is dropped from it.
     */
    fun uniqueName(prefix: String, nonce: String, extension: String): String {
        val n = nonce.filter { it.isLetterOrDigit() && it.code < 128 }.take(32).ifEmpty { "0" }
        val ext = extension.trimStart('.').filter { it.isLetterOrDigit() && it.code < 128 }.take(8)
        return if (ext.isEmpty()) "$prefix-$n" else "$prefix-$n.$ext"
    }

    /**
     * The extension (with the dot) an imported file is copied under, because the core decides by it: ".zip", ".md" or
     * ".txt". The name decides, then the MIME type. Null = not something the core can import.
     */
    fun importExtension(displayName: String?, mime: String?): String? {
        when (AttachmentFiles.extensionOf(displayName.orEmpty())) {
            "zip" -> return ".zip"
            "md", "markdown" -> return ".md"
            "txt" -> return ".txt"
        }
        return when (AttachmentFiles.baseMime(mime.orEmpty())) {
            "application/zip", "application/x-zip-compressed" -> ".zip"
            "text/markdown", "text/x-markdown" -> ".md"
            "text/plain" -> ".txt"
            else -> null
        }
    }

    /** Whether a picked file of [size] bytes (-1 = unknown) may be copied for import. */
    fun importSizeOk(size: Long): Boolean = size <= MAX_IMPORT_BYTES

    fun attachmentSizeOk(size: Long): Boolean = size <= MAX_ATTACHMENT_BYTES

    // ---- texts ---------------------------------------------------------------------------------

    const val EXPORT_TITLE = "导出笔记"
    const val EXPORT_INCLUDE_TRASH = "包含回收站里的笔记"
    const val EXPORT_HINT = "每篇笔记一个 Markdown 文件，打包成 ZIP。附件不在导出里，笔记里的附件链接会保留在文字中。"
    const val IMPORT_UNSUPPORTED = "不支持这种文件，请选择 .zip、.md 或 .txt 文件"
    const val IMPORT_TOO_LARGE = "文件太大（超过 256 MiB），没有导入"
    const val IMPORT_UNREADABLE = "无法读取所选文件"
    const val EXPORT_FAILED = "导出失败"
    const val BUSY_EXPORT = "正在导出…"
    const val BUSY_IMPORT = "正在导入…"
    const val BUSY_CLEANUP = "正在清理…"
    const val NOTHING_IMPORTED = "文件里没有可导入的笔记"
    const val SKIPPED_TITLE = "没有导入的文件"

    fun exportSummary(r: ExportResult): String = "已导出 ${r.count} 篇笔记（${Formatters.fileSize(r.bytes)}）"

    fun importSummary(r: ImportResult): String = when {
        r.imported == 0 && r.skipped.isEmpty() -> NOTHING_IMPORTED
        r.skipped.isEmpty() -> "导入了 ${r.imported} 篇"
        else -> "导入了 ${r.imported} 篇，跳过 ${r.skipped.size} 个"
    }

    /** The core's English reason as a sentence for the details dialog; unknown reasons are shown as they are. */
    fun skipReason(reason: String): String = when (reason) {
        "larger than the 1 MiB limit of a note" -> "超过单篇笔记 1 MiB 的上限"
        "cannot be read" -> "无法读取"
        "not a text file" -> "不是文本文件"
        "not a regular file" -> "不是普通文件"
        else -> reason
    }

    /** One line per skipped file for the details dialog. */
    fun skipLines(skipped: List<ImportSkip>): List<String> = skipped.map { "${it.name}：${skipReason(it.reason)}" }

    fun cleanupSummary(r: CleanupResult): String =
        if (r.removed <= 0) "没有可清理的附件" else "释放了 ${megabytes(r.freedBytes)}（删除 ${r.removed} 个附件）"

    /** "12.3 MB": one decimal, binary multiples like the rest of the app. */
    fun megabytes(bytes: Long): String = String.format(Locale.ROOT, "%.1f MB", bytes.coerceAtLeast(0) / (1024.0 * 1024.0))
}
