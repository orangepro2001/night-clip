package com.nightclip.app.logic

import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/**
 * User-facing text of the notes feature for code without a Context (pure logic, unit-tested). Compose screens use
 * res/values/strings_notes.xml; this object holds the texts that logic classes produce.
 * Note times are Unix MILLISECONDS (the rest of the app uses seconds).
 */
object NotesTexts {
    const val UNTITLED = "（无标题）"
    const val EMPTY_BODY = "（空白笔记）"

    /** After this many days a note shows its date instead of "N 天前". */
    private const val RELATIVE_DAYS = 7L

    /** "刚刚" … "3 天前"; older than a week: "2026-10-27"; 0 = never. */
    fun updatedLabel(thenMs: Long, nowMs: Long, zone: ZoneId = ZoneId.systemDefault()): String {
        if (thenMs <= 0) return Strings.NEVER
        val ageS = (nowMs - thenMs) / 1000
        if (ageS >= RELATIVE_DAYS * 86400) return DateTimeFormatter.ofPattern("yyyy-MM-dd").withZone(zone).format(Instant.ofEpochMilli(thenMs))
        return Formatters.relativeTime(thenMs / 1000, nowMs / 1000)
    }

    /** "2026-10-27 14:30" for the history list. */
    fun dateTime(ms: Long, zone: ZoneId = ZoneId.systemDefault()): String =
        if (ms <= 0) Strings.NEVER else DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm").withZone(zone).format(Instant.ofEpochMilli(ms))

    // ---- editor status -----------------------------------------------------------------------

    const val SAVE_CLEAN = "已保存"
    const val SAVE_PENDING = "未保存"
    const val SAVE_SAVING = "正在保存…"
    const val SAVE_FAILED = "保存失败"
    const val SAVE_READ_ONLY = "只读"

    // ---- notices after a save or a reload ----------------------------------------------------

    const val NOTICE_MERGED = "这篇笔记在别处也被修改过，已自动合并，编辑区已更新为合并后的内容"
    const val NOTICE_CONFLICT_COPY = "同一处在别的设备上也被修改过，无法自动合并。两份内容都已保留：较旧的一份成了冲突副本，请到笔记列表里查看并处理"
    const val NOTICE_SAVED_AS_NEW = "原来的笔记已经不存在，你的内容已另存为新笔记"
    const val NOTICE_TRASHED = "这篇笔记已被移到回收站，恢复后才能继续编辑"
    const val NOTICE_RELOAD_KEPT_TEXT = "笔记在别处有更新，你的修改会在保存时与之合并"
    const val NOTICE_DISCARD_FAILED = "有修改还没能保存，离开会丢失这些修改"

    // ---- conflicts ---------------------------------------------------------------------------

    fun conflictBanner(count: Int) = "有 $count 处同步冲突需要你处理"
    const val CONFLICT_BADGE = "冲突副本"

    fun deletedElsewhere(title: String): String {
        val name = title.trim().ifEmpty { UNTITLED }
        return "「$name」在另一台设备上已被删除，但你在这里有较新的修改，所以保留了它。"
    }

    const val DELETED_ELSEWHERE_UNKNOWN = "有一篇笔记在另一台设备上已被删除，但你在这里有较新的修改，所以保留了它。"

    const val ACTION_KEEP_MAIN = "保留当前笔记"
    const val ACTION_KEEP_COPY = "改用副本的内容"
    const val ACTION_MERGE = "合并两者"
    const val ACTION_KEEP_MAIN_HINT = "丢掉冲突副本，当前笔记不变。"
    const val ACTION_KEEP_COPY_HINT = "用副本的标题和正文替换当前笔记，副本进回收站。"
    const val ACTION_MERGE_HINT = "把副本的正文接在当前笔记后面（用分隔线隔开），副本进回收站。"

    fun resolved(action: String) = when (action) {
        "keep_main" -> "已保留当前笔记，冲突副本已移到回收站"
        "keep_conflict" -> "已改用副本的内容，冲突副本已移到回收站"
        "merge" -> "已合并两份内容，冲突副本已移到回收站"
        else -> "冲突已处理"
    }

    // ---- lists and trash ---------------------------------------------------------------------

    const val TRASHED = "已移到回收站"
    const val RESTORED = "已恢复"
    const val CREATE_FAILED = "新建笔记失败"

    // ---- search ------------------------------------------------------------------------------

    const val SEARCH_NO_RESULTS = "没有找到相关的笔记"

    // ---- attachments -------------------------------------------------------------------------

    const val ATTACHMENT_SYNCING = "附件同步中…"
    const val ATTACHMENT_FAILED = "附件无法显示"
    const val ATTACHMENT_LOADING = "正在加载附件…"
    const val ATTACHMENT_ADDED = "已插入附件"
    const val ATTACHMENT_READ_ONLY = "回收站里的笔记不能插入附件，请先恢复"
    const val ATTACHMENT_NO_APP = "没有可以打开这种文件的应用，可以保存或分享"
    const val ATTACHMENT_SAVED = "已保存"
    const val ATTACHMENT_NOT_OPENED_TITLE = "不直接打开这种文件"
    const val ATTACHMENT_NOT_OPENED_TEXT = "安装包、脚本、网页这类文件不会从笔记里直接打开。可以先保存到手机，或分享给其他应用。"
    const val ATTACHMENT_SAVE = "保存"
    const val ATTACHMENT_SHARE = "分享"

    // ---- share target ------------------------------------------------------------------------

    const val SHARE_SAVING = "正在保存为笔记…"
    const val SHARE_SAVED = "已保存为笔记"
    const val SHARE_SAVED_PARTLY = "已保存为笔记，但有文件没能保存："
    const val SHARE_NOTHING = "没有可保存的文字"
    const val SHARE_TOO_LARGE = "太大（超过 1 MiB），无法保存为笔记"

    // ---- history -----------------------------------------------------------------------------

    const val HISTORY_NONE = "这篇笔记还没有保存过，所以没有历史版本"
    const val VERSION_RESTORED = "已恢复到所选版本（原来的内容仍在历史里）"

    /** Who made a revision: this device, another one, or a merge. [names] maps device ids to names. */
    fun origin(originDevice: String, selfDeviceId: String, names: Map<String, String>): String = when {
        originDevice == "merge" -> "合并产生"
        originDevice.isEmpty() -> "未知设备"
        originDevice == selfDeviceId -> "本机"
        else -> names[originDevice]?.takeIf { it.isNotBlank() } ?: Strings.UNKNOWN_DEVICE
    }
}
