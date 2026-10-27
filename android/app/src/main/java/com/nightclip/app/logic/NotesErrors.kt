package com.nightclip.app.logic

import com.nightclip.app.core.ErrorMessages

/** Error code -> text for `notes.*` calls (notes/api-spec.md §5). Anything else falls back to [ErrorMessages]. */
object NotesErrors {
    enum class Context { GENERAL, OPEN, SAVE, RESOLVE, SYNC, SHARE }

    fun message(code: String, context: Context = Context.GENERAL): String = when (code) {
        "NOT_FOUND" -> when (context) {
            Context.RESOLVE -> "这个冲突副本已经处理过了，或已不存在"
            Context.SAVE -> "原来的笔记已经不存在了"
            else -> "找不到这篇笔记，它可能已被彻底清理"
        }
        "CONFLICT" -> when (context) {
            Context.RESOLVE -> "原笔记在回收站里，请先恢复它再处理冲突"
            else -> "这篇笔记在回收站里，恢复后才能编辑"
        }
        "TOO_LARGE" -> "笔记太大：正文最多 1 MiB，标题最多 1 KiB"
        "UNSUPPORTED_FORMAT" -> "这篇笔记来自更新版本的 ClipBridge，请先更新本应用"
        "NOT_IMPLEMENTED" -> "这个功能暂未提供"
        "NOT_PAIRED" -> when (context) {
            Context.SYNC -> "没有可同步的已配对设备，或该设备已解除配对"
            else -> "该设备尚未配对，或已被对方解除配对"
        }
        "INVALID" -> "参数无效，请检查后重试"
        else -> ErrorMessages.forCode(code)
    }

    /** Codes after which retrying the same save cannot work; the editor waits for the next edit instead of looping. */
    fun isPermanent(code: String): Boolean = code in setOf("TOO_LARGE", "UNSUPPORTED_FORMAT", "NOT_IMPLEMENTED", "INVALID")
}
