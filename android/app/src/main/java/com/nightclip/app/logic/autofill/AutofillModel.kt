package com.nightclip.app.logic.autofill

/**
 * Plain description of one view of an AssistStructure. The Android glue (autofill/AssistParser.kt)
 * fills it; everything that decides anything works on this class only, so it is unit-tested on the JVM.
 *
 * Nothing here is ever logged: [text] is a credential on a save request.
 */
data class NodeInfo(
    /** Index into the list of AutofillIds the glue keeps next to the nodes. */
    val ref: Int,
    /** Package of the window the node belongs to (taken from the platform, never from view content). */
    val packageName: String?,
    /** Domain the platform reports for web content (ViewNode.getWebDomain, inherited down the tree), host only. */
    val webDomain: String? = null,
    /** Scheme the platform reports for web content (ViewNode.getWebScheme, API 28+), null when unknown. */
    val webScheme: String? = null,
    val autofillHints: List<String> = emptyList(),
    /** `android:id` entry name (ViewNode.getIdEntry). */
    val idEntry: String? = null,
    val hint: String? = null,
    val contentDescription: String? = null,
    /** android.text.InputType bit field. */
    val inputType: Int = 0,
    /** HTML tag (ViewNode.getHtmlInfo().getTag()), lower case. */
    val htmlTag: String? = null,
    /** HTML attributes with lower-case names (type, autocomplete, name, id, placeholder, aria-label ...). */
    val htmlAttributes: Map<String, String> = emptyMap(),
    /** True when the node takes text (autofillType TEXT, or an html input). */
    val isTextField: Boolean = true,
    val isVisible: Boolean = true,
    val isFocused: Boolean = false,
    /** Current text; only filled while parsing a save request. */
    val text: String? = null,
) {
    /** Where the node lives: two nodes belong to one form only if this is equal. */
    val context: NodeContext get() = NodeContext(packageName.orEmpty(), webDomain.orEmpty().lowercase(), webScheme.orEmpty().lowercase())

    /** Debug aid that is safe to print: never the text. */
    override fun toString(): String = "NodeInfo(ref=$ref, id=$idEntry, hasText=${!text.isNullOrEmpty()})"
}

data class NodeContext(val packageName: String, val webDomain: String, val webScheme: String)

enum class FieldRole {
    USERNAME,
    PASSWORD,
    NEW_PASSWORD,
    ONE_TIME_CODE,
    CREDIT_CARD,
    IGNORED,
}

/** Where the decision for a role came from, strongest first. */
enum class Evidence { HINT, HTML, INPUT_TYPE, NAME, NONE }

/**
 * @param weak a username-like field that is only an e-mail/phone typed box: fine next to a password,
 *   never enough to offer credentials on its own.
 * @param ambiguousPassword a password field with nothing saying whether it is the current or a new one.
 * @param confirm a new-password field the name of which says "confirm / repeat".
 */
data class Classification(
    val role: FieldRole,
    val evidence: Evidence,
    val weak: Boolean = false,
    val ambiguousPassword: Boolean = false,
    /** A new-password field whose words say "confirm / repeat" (as opposed to "new"). */
    val confirm: Boolean = false,
)

data class ClassifiedNode(val node: NodeInfo, val role: FieldRole, val classification: Classification) {
    val ref: Int get() = node.ref
}

enum class FormKind { LOGIN, USERNAME_STEP, SIGNUP, CHANGE_PASSWORD, OTP, NONE }

/** One login-like form: a context-homogeneous run of classified fields. */
data class FormCluster(val fields: List<ClassifiedNode>) {
    val context: NodeContext get() = fields.first().node.context

    /** The username immediately before the first password of the cluster (or the last username when there is no password). */
    val username: ClassifiedNode?
        get() {
            val firstPw = fields.indexOfFirst { it.role == FieldRole.PASSWORD || it.role == FieldRole.NEW_PASSWORD }
            val upto = if (firstPw >= 0) fields.subList(0, firstPw) else fields
            return upto.lastOrNull { it.role == FieldRole.USERNAME }
        }
    val currentPasswords: List<ClassifiedNode> get() = fields.filter { it.role == FieldRole.PASSWORD }
    val newPasswords: List<ClassifiedNode> get() = fields.filter { it.role == FieldRole.NEW_PASSWORD }
    val oneTimeCodes: List<ClassifiedNode> get() = fields.filter { it.role == FieldRole.ONE_TIME_CODE }

    val kind: FormKind
        get() {
            val cur = currentPasswords.isNotEmpty()
            val new = newPasswords.isNotEmpty()
            return when {
                cur && new -> FormKind.CHANGE_PASSWORD
                new -> FormKind.SIGNUP
                cur -> FormKind.LOGIN
                username?.let { !it.classification.weak } == true -> FormKind.USERNAME_STEP
                username == null && oneTimeCodes.isNotEmpty() -> FormKind.OTP
                else -> FormKind.NONE
            }
        }

    /** Kinds for which stored credentials can be offered. SIGNUP is excluded: filling an old password there would be wrong. */
    val wantsCredentials: Boolean
        get() = kind == FormKind.LOGIN || kind == FormKind.USERNAME_STEP || kind == FormKind.CHANGE_PASSWORD || kind == FormKind.OTP

    /** Kinds for which "save to vault" is declared. */
    val offersSave: Boolean get() = kind == FormKind.LOGIN || kind == FormKind.SIGNUP || kind == FormKind.CHANGE_PASSWORD

    /** The password field that carries the secret to save: the new one on SIGNUP/CHANGE_PASSWORD, else the current one. */
    val passwordToSave: ClassifiedNode? get() = newPasswords.firstOrNull() ?: currentPasswords.firstOrNull()

    /** Every ref the system should trigger on / fill. */
    val refs: List<Int> get() = fields.map { it.ref }
}
