package com.nightclip.app.logic

/** Settings of the password generator sheet; [toParams] gives the `vault.generate` parameters. */
data class GeneratorOptions(
    val passphrase: Boolean = false,
    val length: Int = 20,
    val lower: Boolean = true,
    val upper: Boolean = true,
    val digits: Boolean = true,
    val symbols: Boolean = true,
    val excludeAmbiguous: Boolean = false,
    val words: Int = 6,
    val separator: String = "-",
    val capitalize: Boolean = false,
    val number: Boolean = false,
) {
    /** Values clamped to what the core accepts: length 4–256, words 3–20, separator at most 4 characters. */
    fun normalized(): GeneratorOptions = copy(
        length = length.coerceIn(MIN_LENGTH, MAX_LENGTH),
        words = words.coerceIn(MIN_WORDS, MAX_WORDS),
        separator = separator.take(4),
    )

    /** Parameters for vault.generate. No class selected means "all classes" for the core, which is also what the sheet shows. */
    fun toParams(): Map<String, Any> {
        val o = normalized()
        return if (o.passphrase) {
            mapOf("type" to "passphrase", "words" to o.words, "separator" to o.separator.ifEmpty { "-" }, "capitalize" to o.capitalize, "number" to o.number)
        } else {
            mapOf(
                "type" to "password", "length" to o.length,
                "lower" to o.lower, "upper" to o.upper, "digits" to o.digits, "symbols" to o.symbols,
                "exclude_ambiguous" to o.excludeAmbiguous,
            )
        }
    }

    /** Switching a class off is refused when it is the last one on (the sheet would otherwise silently mean "all"). */
    fun withClass(which: Char, on: Boolean): GeneratorOptions {
        val next = when (which) {
            'l' -> copy(lower = on)
            'u' -> copy(upper = on)
            'd' -> copy(digits = on)
            's' -> copy(symbols = on)
            else -> this
        }
        return if (next.lower || next.upper || next.digits || next.symbols) next else this
    }

    companion object {
        const val MIN_LENGTH = 4
        const val MAX_LENGTH = 256
        const val MIN_WORDS = 3
        const val MAX_WORDS = 20
    }
}
