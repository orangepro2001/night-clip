package com.nightclip.app.logic

import com.nightclip.app.core.VaultEntryDetail
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import java.time.format.DateTimeParseException

/** One custom field in the editor. [key] is only a stable identity for the list rows. */
data class DraftField(val key: Long, val name: String, val value: String, val protected: Boolean) {
    override fun toString() = "DraftField(key=$key)" // the value can be a secret
}

/** The entry being created or edited. Text fields hold what the user typed. */
data class EntryDraft(
    /** null = a new entry. */
    val id: String? = null,
    val kind: String = VaultTexts.KIND_LOGIN,
    val title: String = "",
    val username: String = "",
    val password: String = "",
    val url: String = "",
    val notes: String = "",
    /** Comma / space / newline separated. */
    val tags: String = "",
    val fields: List<DraftField> = emptyList(),
    val totp: String = "",
    /** "" (no expiry) or YYYY-MM-DD. */
    val expires: String = "",
    val groupId: String = "",
    val nextFieldKey: Long = 1,
) {
    override fun toString() = "EntryDraft(id=$id, kind=$kind)" // never print secrets

    fun withField(name: String, value: String = "", protected: Boolean = false): EntryDraft =
        copy(fields = fields + DraftField(nextFieldKey, name, value, protected), nextFieldKey = nextFieldKey + 1)

    fun updateField(key: Long, transform: (DraftField) -> DraftField): EntryDraft =
        copy(fields = fields.map { if (it.key == key) transform(it) else it })

    fun removeField(key: Long): EntryDraft = copy(fields = fields.filter { it.key != key })

    /** Tags as the core wants them: split on comma, space and newline, trimmed, de-duplicated, empty ones dropped. */
    fun tagList(): List<String> = tags.split(',', ' ', '\n', '，', '、').map { it.trim() }.filter { it.isNotEmpty() }.distinctBy { it.lowercase() }

    companion object {
        /** A blank entry of [kind] with the template's suggested custom fields NOT added (they are offered as chips). */
        fun blank(kind: String, groupId: String = ""): EntryDraft = EntryDraft(kind = kind, groupId = groupId)

        /**
         * Draft for editing an existing entry. The secrets are not in [detail]; the caller reveals them and passes
         * them in: [password], [totpSecret] and the values of protected fields by name.
         */
        fun fromDetail(
            detail: VaultEntryDetail,
            password: String,
            totpSecret: String,
            protectedValues: Map<String, String>,
            zone: ZoneId = ZoneId.systemDefault(),
        ): EntryDraft {
            var d = EntryDraft(
                id = detail.id,
                kind = detail.kind,
                title = detail.title,
                username = detail.username,
                password = password,
                url = detail.url,
                notes = detail.notes,
                tags = detail.tags.joinToString(", "),
                totp = totpSecret,
                expires = ExpiryDates.format(detail.expiresAt, zone),
                groupId = detail.groupId,
            )
            for (f in detail.fields) d = d.withField(f.name, if (f.protected) protectedValues[f.name].orEmpty() else f.value, f.protected)
            return d
        }
    }
}

/** YYYY-MM-DD <-> unix seconds. An expiry date means "valid through that day", so it is stored as 23:59:59 local time. */
object ExpiryDates {
    private val SHAPE = Regex("""^\d{4}-\d{2}-\d{2}$""")

    fun format(epochSeconds: Long, zone: ZoneId = ZoneId.systemDefault()): String =
        if (epochSeconds <= 0) "" else Instant.ofEpochSecond(epochSeconds).atZone(zone).toLocalDate().toString()

    /** null for text that is not a real calendar date; "" parses to 0 (no expiry). */
    fun parse(text: String, zone: ZoneId = ZoneId.systemDefault()): Long? {
        val t = text.trim()
        if (t.isEmpty()) return 0
        if (!SHAPE.matches(t)) return null
        return try {
            LocalDate.parse(t).atTime(23, 59, 59).atZone(zone).toEpochSecond()
        } catch (e: DateTimeParseException) {
            null
        }
    }

    /** Today plus [days], as the text field wants it. */
    fun inDays(days: Long, today: LocalDate = LocalDate.now()): String = today.plusDays(days).toString()
}

/** Entry kind templates: which inputs the editor shows and what it suggests. */
data class KindTemplate(
    val kind: String,
    val showUsername: Boolean,
    val showUrl: Boolean,
    val showPassword: Boolean,
    val showTotp: Boolean,
    val showNotes: Boolean,
    val passwordLabel: String,
    val urlLabel: String,
    val titleHint: String,
    /** Names offered as one-tap custom fields ("Key ID", "Environment", "Region" for API keys). */
    val suggestedFields: List<String>,
    /** Whether a generator makes sense for the secret. */
    val canGenerate: Boolean,
) {
    companion object {
        private val templates = mapOf(
            VaultTexts.KIND_LOGIN to KindTemplate(
                VaultTexts.KIND_LOGIN, showUsername = true, showUrl = true, showPassword = true, showTotp = true, showNotes = true,
                passwordLabel = "密码", urlLabel = "网址", titleHint = "例如：GitHub", suggestedFields = emptyList(), canGenerate = true,
            ),
            VaultTexts.KIND_APIKEY to KindTemplate(
                VaultTexts.KIND_APIKEY, showUsername = false, showUrl = true, showPassword = true, showTotp = false, showNotes = true,
                passwordLabel = "密钥", urlLabel = "接口地址", titleHint = "例如：OpenAI 生产环境",
                suggestedFields = listOf("Key ID", "Environment", "Region"), canGenerate = false,
            ),
            VaultTexts.KIND_SECRET to KindTemplate(
                VaultTexts.KIND_SECRET, showUsername = false, showUrl = false, showPassword = true, showTotp = false, showNotes = true,
                passwordLabel = "机密内容", urlLabel = "网址", titleHint = "例如：路由器 PIN、软件许可证", suggestedFields = emptyList(), canGenerate = true,
            ),
            VaultTexts.KIND_NOTE to KindTemplate(
                VaultTexts.KIND_NOTE, showUsername = false, showUrl = false, showPassword = false, showTotp = false, showNotes = true,
                passwordLabel = "", urlLabel = "", titleHint = "例如：恢复码、备忘", suggestedFields = emptyList(), canGenerate = false,
            ),
        )

        /** Unknown kinds are shown like logins (the core only keeps the kind as a hint). */
        fun of(kind: String): KindTemplate = templates[kind] ?: templates.getValue(VaultTexts.KIND_LOGIN)

        /** Suggested names that the draft does not use yet (case-insensitive). */
        fun unusedSuggestions(kind: String, draft: EntryDraft): List<String> {
            val used = draft.fields.map { it.name.trim().lowercase() }.toSet()
            return of(kind).suggestedFields.filter { it.lowercase() !in used }
        }
    }
}

/** What is wrong with a draft; [field] says where, [message] says what. */
data class DraftIssue(val field: DraftIssueField, val message: String, val fieldKey: Long? = null)

enum class DraftIssueField { TITLE, USERNAME, PASSWORD, URL, NOTES, TOTP, EXPIRES, FIELD_NAME, FIELD_VALUE, TOO_MANY }

/** Client-side validation that matches the core's limits (core/vault model.go `validate`), so most errors never reach the core. */
object EntryValidator {
    const val MAX_TEXT_BYTES = 256 * 1024
    const val MAX_FIELD_NAME_BYTES = 128
    const val MAX_FIELDS = 100
    const val MAX_TAGS = 100
    private val RESERVED = setOf("title", "username", "password", "url", "notes", "otp")

    private fun bytes(s: String) = s.toByteArray(Charsets.UTF_8).size

    /** Whether [name] is usable as a custom field name; null when it is, otherwise the reason. */
    fun fieldNameProblem(name: String, others: List<String> = emptyList()): String? {
        val n = name.trim()
        val lower = n.lowercase()
        return when {
            n.isEmpty() -> "字段名不能为空"
            bytes(n) > MAX_FIELD_NAME_BYTES -> "字段名太长（最多 128 字节）"
            lower in RESERVED || lower.startsWith("kph:") -> "“$n”是保留名称，请换一个"
            others.any { it.trim().lowercase() == lower } -> "字段名“$n”重复"
            else -> null
        }
    }

    fun validate(d: EntryDraft, zone: ZoneId = ZoneId.systemDefault()): List<DraftIssue> {
        val out = mutableListOf<DraftIssue>()
        if (d.title.isBlank()) out += DraftIssue(DraftIssueField.TITLE, "请填写标题")
        if (bytes(d.title) > MAX_TEXT_BYTES) out += DraftIssue(DraftIssueField.TITLE, "标题太长")
        if (bytes(d.username) > MAX_TEXT_BYTES) out += DraftIssue(DraftIssueField.USERNAME, "用户名太长")
        if (bytes(d.password) > MAX_TEXT_BYTES) out += DraftIssue(DraftIssueField.PASSWORD, "内容太长（最多 256 KiB）")
        if (bytes(d.url) > MAX_TEXT_BYTES) out += DraftIssue(DraftIssueField.URL, "网址太长")
        if (bytes(d.notes) > MAX_TEXT_BYTES) out += DraftIssue(DraftIssueField.NOTES, "备注太长（最多 256 KiB）")
        if (bytes(d.totp) > MAX_TEXT_BYTES) out += DraftIssue(DraftIssueField.TOTP, "验证码密钥太长")
        if (d.fields.size > MAX_FIELDS) out += DraftIssue(DraftIssueField.TOO_MANY, "自定义字段最多 100 个")
        if (d.tagList().size > MAX_TAGS) out += DraftIssue(DraftIssueField.TOO_MANY, "标签最多 100 个")
        val seen = mutableListOf<String>()
        for (f in d.fields) {
            fieldNameProblem(f.name, seen)?.let { out += DraftIssue(DraftIssueField.FIELD_NAME, it, f.key) }
            seen += f.name
            if (bytes(f.value) > MAX_TEXT_BYTES) out += DraftIssue(DraftIssueField.FIELD_VALUE, "字段“${f.name.trim()}”的内容太长", f.key)
        }
        if (ExpiryDates.parse(d.expires, zone) == null) out += DraftIssue(DraftIssueField.EXPIRES, "到期日请按 年-月-日 填写，例如 2027-01-31")
        return out
    }

    /** Whether the user changed anything since [original] (drives the "discard changes?" prompt). */
    fun isDirty(original: EntryDraft, current: EntryDraft): Boolean =
        original.copy(nextFieldKey = 0) != current.copy(nextFieldKey = 0)
}
