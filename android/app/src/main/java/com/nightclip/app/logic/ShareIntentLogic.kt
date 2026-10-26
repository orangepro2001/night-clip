package com.nightclip.app.logic

/** What an incoming ACTION_SEND / ACTION_SEND_MULTIPLE intent asks for. Pure: no Intent class. */
sealed interface SharePlan {
    data class Text(val text: String) : SharePlan
    data class Files(val count: Int) : SharePlan
    data object TextTooLarge : SharePlan
    data object Nothing : SharePlan
}

object ShareIntentLogic {
    const val ACTION_SEND = "android.intent.action.SEND"
    const val ACTION_SEND_MULTIPLE = "android.intent.action.SEND_MULTIPLE"
    const val MAX_TEXT_BYTES = 64 * 1024

    /**
     * @param streamCount number of EXTRA_STREAM (or ClipData) URIs that came with the intent.
     * Text wins for text/plain with a body (browsers send a link); anything with streams is
     * files; subject is only a last resort for a text share without a body.
     */
    fun plan(action: String?, mimeType: String?, extraText: CharSequence?, extraSubject: CharSequence?, streamCount: Int): SharePlan {
        val body = extraText?.toString().orEmpty()
        val subject = extraSubject?.toString().orEmpty()
        return when (action) {
            ACTION_SEND_MULTIPLE -> if (streamCount > 0) SharePlan.Files(streamCount) else SharePlan.Nothing
            ACTION_SEND -> when {
                mimeType?.startsWith("text/plain") == true && body.isNotBlank() -> textPlan(body)
                streamCount > 0 -> SharePlan.Files(streamCount)
                body.isNotBlank() -> textPlan(body)
                subject.isNotBlank() -> textPlan(subject)
                else -> SharePlan.Nothing
            }
            else -> SharePlan.Nothing
        }
    }

    private fun textPlan(text: String): SharePlan =
        if (text.toByteArray(Charsets.UTF_8).size > MAX_TEXT_BYTES) SharePlan.TextTooLarge else SharePlan.Text(text)
}
