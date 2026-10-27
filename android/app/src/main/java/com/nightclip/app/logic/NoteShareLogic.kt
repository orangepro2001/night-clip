package com.nightclip.app.logic

/** What a text or file shared to "save as note" turns into. Pure: no Intent class. */
sealed interface NoteSharePlan {
    /** [title] may be empty: the core then uses the first line of the text. */
    data class Create(val title: String, val body: String) : NoteSharePlan

    /** Shared files (`EXTRA_STREAM`): each becomes an attachment of a new note. [text] is a caption some apps add, may be empty. */
    data class Attach(val title: String, val text: String) : NoteSharePlan
    data object TooLarge : NoteSharePlan
    data object Nothing : NoteSharePlan
}

object NoteShareLogic {
    const val ACTION_SEND = ShareIntentLogic.ACTION_SEND

    /** The core refuses a body over 1 MiB (notes/api-spec.md §5). */
    const val MAX_BODY_BYTES = 1024 * 1024

    /** The core refuses a title over 1 KiB; titles from other apps are cut well below that. */
    const val MAX_TITLE_CHARS = 200

    /** At most this many files of one share are attached. */
    const val MAX_FILES = 20

    /**
     * [extraText] is the shared text (a browser sends the link), [extraSubject] the page title some apps add,
     * [hasStream] whether the share carries files. Text wins when there is any (as in the send-to-device target); files
     * come next; a share with only a subject saves the subject as the text. A subject becomes the note title.
     */
    fun plan(action: String?, mimeType: String?, extraText: CharSequence?, extraSubject: CharSequence?, hasStream: Boolean = false): NoteSharePlan {
        if (action != ACTION_SEND) return NoteSharePlan.Nothing
        val isText = mimeType == null || mimeType.startsWith("text/")
        val body = extraText?.toString().orEmpty()
        val subject = oneLine(extraSubject?.toString().orEmpty())
        val tooBig = body.toByteArray(Charsets.UTF_8).size > MAX_BODY_BYTES
        return when {
            isText && body.isNotBlank() ->
                if (tooBig) NoteSharePlan.TooLarge else NoteSharePlan.Create(cut(subject, MAX_TITLE_CHARS), body)
            hasStream -> if (tooBig) NoteSharePlan.TooLarge else NoteSharePlan.Attach(cut(subject, MAX_TITLE_CHARS), body.trim())
            !isText -> NoteSharePlan.Nothing
            subject.isNotBlank() -> NoteSharePlan.Create("", subject)
            else -> NoteSharePlan.Nothing
        }
    }

    /** The text of a note made from shared files: one paragraph per attachment, then the caption. */
    fun attachmentBody(markdowns: List<String>, caption: String): String =
        (markdowns + listOfNotNull(caption.trim().takeIf { it.isNotEmpty() })).joinToString("\n\n")

    private fun oneLine(s: String): String = s.replace(Regex("\\s+"), " ").trim()

    /** At most [max] chars, never ending in the first half of a surrogate pair. */
    private fun cut(s: String, max: Int): String {
        if (s.length <= max) return s
        val end = if (Character.isHighSurrogate(s[max - 1])) max - 1 else max
        return s.substring(0, end)
    }
}
