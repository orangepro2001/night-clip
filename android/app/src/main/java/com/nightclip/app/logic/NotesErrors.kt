package com.nightclip.app.logic

import com.nightclip.app.core.ErrorMessages

/** Error code -> text for `notes.*` calls (notes/api-spec.md §5). Anything else falls back to [ErrorMessages]. */
object NotesErrors {
    enum class Context { GENERAL, OPEN, SAVE, RESOLVE, SYNC, SHARE, ATTACH, TRANSFER, SEARCH }

    fun message(code: String, context: Context = Context.GENERAL): String = when (code) {
        "NOT_FOUND" -> when (context) {
            Context.RESOLVE -> "这个冲突副本已经处理过了，或已不存在"
            Context.SAVE -> "原来的笔记已经不存在了"
            Context.ATTACH -> "找不到这个附件，它可能还没有同步到这台设备"
            else -> "找不到这篇笔记，它可能已被彻底清理"
        }
        "CONFLICT" -> when (context) {
            Context.RESOLVE -> "原笔记在回收站里，请先恢复它再处理冲突"
            else -> "这篇笔记在回收站里，恢复后才能编辑"
        }
        "TOO_LARGE" -> when (context) {
            Context.ATTACH -> "附件太大：每个附件最多 32 MiB"
            Context.TRANSFER -> "文件太大"
            else -> "笔记太大：正文最多 1 MiB，标题最多 1 KiB"
        }
        "UNSUPPORTED_FORMAT" -> "这篇笔记来自更新版本的 ClipBridge，请先更新本应用"
        "NOT_IMPLEMENTED" -> "这个功能暂未提供"
        "NOT_PAIRED" -> when (context) {
            Context.SYNC -> "没有可同步的已配对设备，或该设备已解除配对"
            else -> "该设备尚未配对，或已被对方解除配对"
        }
        "INVALID" -> when (context) {
            Context.ATTACH -> "无法读取这个文件"
            Context.TRANSFER -> "无法读取所选文件，或目标文件已存在"
            Context.SEARCH -> "请输入要搜索的文字"
            else -> "参数无效，请检查后重试"
        }
        else -> ErrorMessages.forCode(code)
    }

    /** Codes after which retrying the same save cannot work; the editor waits for the next edit instead of looping. */
    fun isPermanent(code: String): Boolean = code in setOf("TOO_LARGE", "UNSUPPORTED_FORMAT", "NOT_IMPLEMENTED", "INVALID")
}
