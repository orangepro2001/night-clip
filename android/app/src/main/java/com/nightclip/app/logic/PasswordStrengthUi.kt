package com.nightclip.app.logic

/** Maps the core's strength label (very_weak … strong) to what the strength bar shows. */
object PasswordStrengthUi {
    private val ORDER = listOf("very_weak", "weak", "fair", "good", "strong")

    /** 0 (very weak) … 4 (strong); unknown labels count as the weakest. */
    fun level(label: String): Int = ORDER.indexOf(label).coerceAtLeast(0)

    /** Bar fill: 0.2 … 1.0. */
    fun fraction(label: String): Float = (level(label) + 1) / ORDER.size.toFloat()

    fun text(label: String): String = VaultTexts.strengthLabel(label)

    /** Weak and very weak passwords get a warning (create / change master password: warn but allow). */
    fun isWeak(label: String): Boolean = level(label) <= 1

    /** Warning text for a master password, or null if there is nothing to warn about. */
    fun masterWarning(label: String, length: Int): String? = when {
        length == 0 -> null
        isWeak(label) -> "这个主密码偏弱，建议用更长的口令短语。仍然可以使用。"
        length < 10 -> "主密码比较短，建议至少 12 个字符。仍然可以使用。"
        else -> null
    }
}
