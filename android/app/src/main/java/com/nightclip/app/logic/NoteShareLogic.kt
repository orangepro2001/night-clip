package com.nightclip.app.logic

/** What a text shared to "save as note" turns into. Pure: no Intent class. */
sealed interface NoteSharePlan {
    /** [title] may be empty: the core then uses the first line of the text. */
    data class Create(val title: String, val body: String) : NoteSharePlan
    data object TooLarge : NoteSharePlan
    data object Nothing : NoteSharePlan
}

object NoteShareLogic {
    const val ACTION_SEND = ShareIntentLogic.ACTION_SEND

    /** The core refuses a body over 1 MiB (notes/api-spec.md §5). */
    const val MAX_BODY_BYTES = 1024 * 1024

    /** The core refuses a title over 1 KiB; titles from other apps are cut well below that. */
    const val MAX_TITLE_CHARS = 200

    /**
     * [extraText] is the shared text (a browser sends the link), [extraSubject] the page title some apps add.
     * A subject becomes the note title; a share that has only a subject saves the subject as the text.
     */
    fun plan(action: String?, mimeType: String?, extraText: CharSequence?, extraSubject: CharSequence?): NoteSharePlan {
        if (action != ACTION_SEND) return NoteSharePlan.Nothing
        if (mimeType != null && !mimeType.startsWith("text/")) return NoteSharePlan.Nothing
        val body = extraText?.toString().orEmpty()
        val subject = oneLine(extraSubject?.toString().orEmpty())
        return when {
            body.isNotBlank() -> {
                if (body.toByteArray(Charsets.UTF_8).size > MAX_BODY_BYTES) NoteSharePlan.TooLarge
                else NoteSharePlan.Create(cut(subject, MAX_TITLE_CHARS), body)
            }
            subject.isNotBlank() -> NoteSharePlan.Create("", subject)
            else -> NoteSharePlan.Nothing
        }
    }

    private fun oneLine(s: String): String = s.replace(Regex("\\s+"), " ").trim()

    /** At most [max] chars, never ending in the first half of a surrogate pair. */
    private fun cut(s: String, max: Int): String {
        if (s.length <= max) return s
        val end = if (Character.isHighSurrogate(s[max - 1])) max - 1 else max
        return s.substring(0, end)
    }
}
