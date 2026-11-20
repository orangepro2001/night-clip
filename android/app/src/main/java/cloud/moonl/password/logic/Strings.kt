package cloud.moonl.password.logic

/**
 * Central user-facing text for code that has no Context (pure logic, unit-tested).
 * Compose screens and Context-based platform code use res/values/strings.xml; the two
 * sets are kept apart on purpose: this object must stay free of Android classes.
 */
object Strings {
    // Relative time
    const val JUST_NOW = "刚刚"
    const val NEVER = "从未"
    fun minutesAgo(n: Long) = "$n 分钟前"
    fun hoursAgo(n: Long) = "$n 小时前"
    fun daysAgo(n: Long) = "$n 天前"

    // Errors
    const val ERR_GENERIC = "操作失败"
    fun errUnknown(code: String) = "出现未知错误（$code）"
    const val ERR_NOT_READY = "核心尚未启动，请稍后再试"
    const val ERR_HASH_MISMATCH = "校验失败，文件可能已损坏"
    const val ERR_KEY_CORRUPT = "本机的数据库密钥文件已损坏或无法解开。为避免让现有数据库永久不可读，应用不会自动重建它。"
    const val ERR_KEY_STORE = "无法使用系统密钥库（Android Keystore）保护数据库密钥"
}
