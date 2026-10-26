package com.nightclip.app.logic

/** How secret fields are shown (docs/vault-api.md §4 rule 3): dots until "show", then masked again after 15 seconds. */
object MaskPolicy {
    const val AUTO_MASK_MS = 15_000L

    /** Always the same number of dots, so the mask does not leak the length of the secret. */
    const val MASK = "••••••••"

    fun display(value: String?): String = value ?: MASK

    /** Notes of "note" entries are the secret of that kind and are masked like a password; other kinds show notes openly. */
    fun notesAreSecret(kind: String): Boolean = kind == VaultTexts.KIND_NOTE
}
