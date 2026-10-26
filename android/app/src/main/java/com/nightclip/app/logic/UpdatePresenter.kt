package com.nightclip.app.logic

import com.nightclip.app.UpdateState

/** Chinese text for [UpdateState] (the English `text`/`buttonText` there are kept for logs and old tests). */
object UpdatePresenter {
    fun text(u: UpdateState): String {
        if (!u.enabled) return if (u.currentCode == 0L) "开发版本：自动更新已关闭" else "未配置更新服务"
        val must = if (u.mandatory) "（必须更新）" else ""
        return when (u.state) {
            "checking" -> "正在检查更新…"
            "up_to_date" -> "已是最新版本（${u.currentName}）"
            "available" -> "发现新版本 ${u.latestName}$must"
            "downloading" -> "正在下载 ${u.latestName}… ${(u.progress * 100).toInt()}%"
            "ready" -> "新版本 ${u.latestName} 已就绪，可以安装$must"
            "error" -> "更新失败：${u.errorMessage.ifEmpty { u.errorCode }}"
            else -> "当前版本 ${u.currentName}"
        }
    }

    fun buttonText(u: UpdateState): String = when (u.action) {
        UpdateState.Action.CHECK -> "检查更新"
        UpdateState.Action.DOWNLOAD -> "下载"
        UpdateState.Action.INSTALL -> "安装更新"
        UpdateState.Action.NONE -> ""
    }
}
