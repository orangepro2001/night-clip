package com.nightclip.app.logic.autofill

/** Android InputType values spelled out for the tests. */
object IT {
    const val TEXT = 0x1
    const val TEXT_EMAIL = 0x21
    const val TEXT_WEB_EMAIL = 0xD1
    const val TEXT_PASSWORD = 0x81
    const val TEXT_VISIBLE_PASSWORD = 0x91
    const val TEXT_WEB_PASSWORD = 0xE1
    const val NUMBER = 0x2
    const val NUMBER_PASSWORD = 0x12
    const val PHONE = 0x3
}

const val APP = "com.example.shop"
const val CHROME = "com.android.chrome"

fun node(
    ref: Int = 0,
    pkg: String? = APP,
    domain: String? = null,
    scheme: String? = null,
    hints: List<String> = emptyList(),
    id: String? = null,
    hint: String? = null,
    desc: String? = null,
    inputType: Int = IT.TEXT,
    tag: String? = null,
    html: Map<String, String> = emptyMap(),
    focused: Boolean = false,
    visible: Boolean = true,
    isText: Boolean = true,
    text: String? = null,
) = NodeInfo(
    ref = ref, packageName = pkg, webDomain = domain, webScheme = scheme, autofillHints = hints, idEntry = id, hint = hint,
    contentDescription = desc, inputType = inputType, htmlTag = tag, htmlAttributes = html, isTextField = isText,
    isVisible = visible, isFocused = focused, text = text,
)

fun web(ref: Int, type: String? = null, autocomplete: String? = null, name: String? = null, id: String? = null, placeholder: String? = null, domain: String = "example.com", scheme: String = "https", focused: Boolean = false, text: String? = null): NodeInfo {
    val a = HashMap<String, String>()
    type?.let { a["type"] = it }
    autocomplete?.let { a["autocomplete"] = it }
    name?.let { a["name"] = it }
    id?.let { a["id"] = it }
    placeholder?.let { a["placeholder"] = it }
    return node(ref, pkg = CHROME, domain = domain, scheme = scheme, tag = "input", html = a, focused = focused, text = text)
}

fun role(n: NodeInfo) = FieldClassifier.classify(n).role

fun entry(id: String, title: String = id, username: String = "me", url: String = "https://example.com", kind: String = "login", totp: Boolean = false, score: Int = 3) =
    MatchedEntry(id, title, username, url, kind, totp, score)
