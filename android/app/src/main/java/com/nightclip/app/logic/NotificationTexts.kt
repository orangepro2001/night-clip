package com.nightclip.app.logic

/** Preview and masking rules for notifications and list rows (shell-guide §1 "敏感内容"). */
object NotificationTexts {
    const val PREVIEW_MAX = 80

    /**
     * One-line preview: whitespace runs collapse to a single space, the result is cut to
     * [max] code points and ends in an ellipsis when something was removed.
     */
    fun preview(text: String, max: Int = PREVIEW_MAX): String {
        val flat = text.trim().replace(Regex("\\s+"), " ")
        if (flat.codePointCount(0, flat.length) <= max) return flat
        val end = flat.offsetByCodePoints(0, max)
        return flat.substring(0, end).trimEnd() + "…"
    }

    /** Body shown in notifications and collapsed rows: never the text of a sensitive item. */
    fun clipBody(text: String, sensitive: Boolean): String =
        if (sensitive) Strings.SENSITIVE_HIDDEN else preview(text)

    /** Inbox row text; [revealed] is the user's explicit tap on a sensitive item. */
    fun inboxBody(text: String, sensitive: Boolean, revealed: Boolean): String =
        if (sensitive && !revealed) Strings.SENSITIVE_HIDDEN else if (sensitive) text else preview(text, 200)
}
