package com.nightclip.app.logic.autofill

/**
 * Decides what a single text field is for. Order of trust (docs: the platform and the page author know best):
 * explicit autofill hints, then HTML attributes (autocomplete tokens, type), then the Android inputType flags,
 * then the words in ids, hints and placeholders (several languages). Pure Kotlin, no android.* types.
 */
object FieldClassifier {
    // android.text.InputType constants, spelled out so this file stays JVM-pure.
    private const val MASK_CLASS = 0xF
    private const val MASK_VARIATION = 0xFF0
    private const val CLASS_TEXT = 0x1
    private const val CLASS_NUMBER = 0x2
    private const val CLASS_PHONE = 0x3
    private const val TEXT_EMAIL = 0x20
    private const val TEXT_PASSWORD = 0x80
    private const val TEXT_VISIBLE_PASSWORD = 0x90
    private const val TEXT_WEB_EMAIL = 0xD0
    private const val TEXT_WEB_PASSWORD = 0xE0
    private const val NUMBER_PASSWORD = 0x10

    private val NOT_A_FIELD = Classification(FieldRole.IGNORED, Evidence.NONE)

    fun classify(n: NodeInfo): Classification {
        if (!n.isTextField || !n.isVisible) return NOT_A_FIELD
        val name = NameText.of(n)

        // 1. Explicit autofill hints (Android) and autocomplete tokens (HTML).
        explicit(n.autofillHints, Evidence.HINT, name)?.let { return it }
        val html = n.htmlAttributes
        explicit(tokens(html["autocomplete"]), Evidence.HTML, name)?.let { return it }

        // 2. HTML type / role.
        val type = html["type"]?.trim()?.lowercase()
        val role = html["role"]?.trim()?.lowercase()
        if (role == "search" || role == "searchbox") return Classification(FieldRole.IGNORED, Evidence.HTML)
        when (type) {
            "search", "hidden", "submit", "button", "reset", "image", "checkbox", "radio", "file", "date", "datetime-local",
            "month", "week", "time", "range", "color", "url", "number" -> {
                if (type == "number" && name.looksLikeOtp()) return Classification(FieldRole.ONE_TIME_CODE, Evidence.HTML)
                return Classification(FieldRole.IGNORED, Evidence.HTML)
            }
            "password" -> return passwordFrom(name, Evidence.HTML)
        }

        // 3. Android inputType flags.
        val cls = n.inputType and MASK_CLASS
        val variation = n.inputType and MASK_VARIATION
        if (cls == CLASS_TEXT && (variation == TEXT_PASSWORD || variation == TEXT_VISIBLE_PASSWORD || variation == TEXT_WEB_PASSWORD)) {
            return passwordFrom(name, Evidence.INPUT_TYPE)
        }
        if (cls == CLASS_NUMBER && (n.inputType and 0xFF0) == NUMBER_PASSWORD) {
            if (name.looksLikeOtp()) return Classification(FieldRole.ONE_TIME_CODE, Evidence.INPUT_TYPE)
            return passwordFrom(name, Evidence.INPUT_TYPE)
        }

        // Words that rule a field out even when its type says e-mail or text.
        if (name.isSearchLike()) return Classification(FieldRole.IGNORED, Evidence.NAME)
        if (name.isCardLike()) return Classification(FieldRole.CREDIT_CARD, Evidence.NAME)

        if (name.looksLikeOtp()) return Classification(FieldRole.ONE_TIME_CODE, if (type == null) Evidence.NAME else Evidence.HTML)
        if (type == "email" || type == "tel") return usernameFrom(name, Evidence.HTML)

        // 4. Words in ids, hints, placeholders, descriptions.
        if (name.hasPasswordWord()) return passwordFrom(name, Evidence.NAME)
        if (name.hasUsernameWord()) return usernameFrom(name, Evidence.NAME)

        // 5. Typed boxes with no helpful name.
        if (cls == CLASS_TEXT && (variation == TEXT_EMAIL || variation == TEXT_WEB_EMAIL)) {
            return Classification(FieldRole.USERNAME, Evidence.INPUT_TYPE, weak = true)
        }
        if (cls == CLASS_PHONE) return Classification(FieldRole.USERNAME, Evidence.INPUT_TYPE, weak = true)
        return NOT_A_FIELD
    }

    // ---- explicit tokens ------------------------------------------------------------------------

    private fun tokens(s: String?): List<String> = s?.trim()?.split(Regex("\\s+"))?.filter { it.isNotEmpty() }.orEmpty()

    private fun norm(s: String) = s.lowercase().filter { it != '-' && it != '_' && it != ' ' && it != '.' }

    private val STRONG_USERNAME = setOf("username", "newusername", "login", "userid")
    private val WEAK_USERNAME = setOf("emailaddress", "email", "phone", "phonenumber", "phonenational", "telephone", "mobile")
    private val CURRENT_PASSWORD = setOf("currentpassword")
    private val GENERIC_PASSWORD = setOf("password", "passwd")
    private val NEW_PASSWORD = setOf("newpassword")
    private val OTP = setOf("onetimecode", "smsotpcode", "otp", "totp", "onetimepassword")
    private val OTHER_KNOWN = setOf(
        "name", "givenname", "familyname", "additionalname", "honorificprefix", "honorificsuffix", "nickname", "organization",
        "organizationtitle", "streetaddress", "addressline1", "addressline2", "addressline3", "addresslevel1", "addresslevel2",
        "addresslevel3", "addresslevel4", "country", "countryname", "postalcode", "postaladdress", "postaladdressextended",
        "postaladdresscountry", "postaladdressregion", "postaladdresslocality", "bday", "birthday", "birthdate", "bdayday",
        "bdaymonth", "bdayyear", "sex", "gender", "language", "url", "photo", "impp", "transactionamount", "transactioncurrency",
        "search",
    )

    private fun explicit(tokens: List<String>, evidence: Evidence, name: NameText): Classification? {
        for (raw in tokens) {
            val t = norm(raw)
            when {
                t in NEW_PASSWORD -> return Classification(FieldRole.NEW_PASSWORD, evidence)
                t in CURRENT_PASSWORD -> return Classification(FieldRole.PASSWORD, evidence)
                t in GENERIC_PASSWORD -> return passwordFrom(name, evidence)
                t in OTP -> return Classification(FieldRole.ONE_TIME_CODE, evidence)
                t.startsWith("cc") && t.length > 2 -> return Classification(FieldRole.CREDIT_CARD, evidence)
                t.startsWith("creditcard") || t.startsWith("debitcard") -> return Classification(FieldRole.CREDIT_CARD, evidence)
                t in STRONG_USERNAME -> return Classification(FieldRole.USERNAME, evidence)
                t in WEAK_USERNAME || t.startsWith("tel") -> return Classification(FieldRole.USERNAME, evidence, weak = true)
                t in OTHER_KNOWN || t.startsWith("address") || t.startsWith("bday") || t.startsWith("birth") || t.startsWith("postal") ->
                    return Classification(FieldRole.IGNORED, evidence)
            }
        }
        return null
    }

    // ---- refinements ----------------------------------------------------------------------------

    /** A password-ish field: the words around it say whether it is new, confirmation or current. */
    private fun passwordFrom(name: NameText, evidence: Evidence): Classification = when {
        name.hasNewMarker() -> Classification(FieldRole.NEW_PASSWORD, evidence, confirm = name.hasConfirmMarker())
        name.hasCurrentMarker() -> Classification(FieldRole.PASSWORD, evidence)
        else -> Classification(FieldRole.PASSWORD, evidence, ambiguousPassword = true)
    }

    /** Strong only when the words say "user/login/account"; an e-mail or phone box on its own stays weak. */
    private fun usernameFrom(name: NameText, evidence: Evidence): Classification =
        Classification(FieldRole.USERNAME, evidence, weak = !name.hasStrongUsernameWord())
}

/**
 * The words a node carries in ids, hints, placeholders and descriptions, split so that "loginPassword",
 * "login_password" and "login-password" all give {login, password}. Matching is token based for short words
 * ("pass" must not fire on "passport") and substring based for long words and for CJK text.
 */
class NameText private constructor(private val tokens: List<String>, private val spaced: String, private val raw: String) {
    companion object {
        fun of(n: NodeInfo): NameText {
            val a = n.htmlAttributes
            val parts = listOfNotNull(
                n.idEntry, n.hint, n.contentDescription, a["name"], a["id"], a["placeholder"], a["aria-label"], a["title"], a["label"],
            )
            return from(parts.joinToString(" "))
        }

        fun from(text: String): NameText {
            // camelCase -> camel Case, then every non letter/digit becomes a space (CJK stays: it is a letter).
            val camel = text.replace(Regex("([a-z0-9])([A-Z])"), "$1 $2").replace(Regex("([a-z])([0-9])"), "$1 $2")
            val lower = camel.lowercase()
            val spaced = lower.map { if (it.isLetterOrDigit()) it else ' ' }.joinToString("").replace(Regex("\\s+"), " ").trim()
            return NameText(if (spaced.isEmpty()) emptyList() else spaced.split(' '), spaced, lower)
        }

        private val SEARCH = listOf("search", "query", "keyword", "captcha", "coupon", "promo", "voucher", "搜索", "搜尋", "查找", "检索", "查询", "検索", "검색", "buscar", "recherche", "suche", "поиск", "验证码图片", "图形验证码")
        private val SEARCH_TOKENS = setOf("q", "find", "filter", "zip", "zipcode", "postcode", "postal", "firstname", "lastname", "fullname", "birthday")
        private val CARD = listOf("cardnumber", "card number", "ccnum", "cc number", "cvv", "cvc", "cardholder", "creditcard", "credit card", "debitcard", "expiry", "expiration", "银行卡", "信用卡", "卡号", "持卡人", "有效期", "安全码", "tarjeta", "kreditkarte", "carte bancaire", "cartao", "iban")
        private val OTP = listOf(
            "one time", "onetime", "verification code", "verificationcode", "verify code", "auth code", "authcode", "authenticator", "authentication code",
            "two factor", "twofactor", "two step", "2 step", "2step", "2fa", "mfa", "totp", "otp", "sms code", "security token",
            "verifizierungscode", "bestatigungscode", "code de verification", "codigo de verificacion", "codice di verifica",
            "动态口令", "动态码", "动态密码", "两步验证", "二次验证", "双重验证", "二步验证", "身份验证器", "谷歌验证", "一次性密码", "二要素", "ワンタイム", "認証コード", "인증 코드", "일회용",
        )
        private val OTP_TOKENS = setOf("otp", "totp", "2fa", "mfa", "tfa")
        private val PASSWORD_LONG = listOf(
            "password", "passwd", "passphrase", "passcode", "密码", "密碼", "口令", "パスワード", "암호", "비밀번호", "contraseña", "contrasena",
            "mot de passe", "motdepasse", "passwort", "kennwort", "senha", "пароль", "wachtwoord", "hasło", "haslo", "şifre", "sifre", "parola",
            "lösenord", "losenord", "salasana", "adgangskode", "heslo", "jelszó", "jelszo", "kata sandi", "mật khẩu",
        )
        private val PASSWORD_TOKENS = setOf("pass", "pwd", "pw", "mdp", "pword", "passwd", "clave")
        private val USER_STRONG = listOf(
            "username", "userid", "user id", "user name", "login", "loginid", "signin", "sign in", "account", "uname", "用户名", "用戶名", "登录名", "登錄名", "登陆名",
            "账号", "帐号", "賬號", "帳號", "账户", "帳戶", "会员", "ユーザー名", "ユーザーid", "ユーザ", "ログインid", "아이디", "사용자", "usuario", "utilisateur", "identifiant",
            "benutzer", "nutzername", "anmeldename", "usuário", "логин", "пользователь", "gebruikersnaam", "użytkownik", "kullanıcı", "kullanici", "nome utente", "användar",
        )
        private val USER_STRONG_TOKENS = setOf("user", "uid", "usr", "acct", "member", "logon", "nick", "identifier")
        private val USER_WEAK = listOf(
            "email", "e mail", "emailaddress", "邮箱", "郵箱", "邮件", "電子郵件", "电子邮件", "メール", "이메일", "correo", "courriel", "почта",
            "phone", "mobile", "telephone", "手机", "手機", "电话", "電話", "携帯", "휴대폰", "teléfono", "telefono", "телефон",
        )
        private val USER_WEAK_TOKENS = setOf("mail", "tel", "cell", "gsm", "msisdn")
        private val CONFIRM_TOKENS = setOf("confirm", "confirmation", "confirmed", "repeat", "retype", "reenter", "again", "verify", "second", "bevestig")
        private val CONFIRM_PHRASES = listOf("re enter", "re type", "確認", "确认", "重复", "重複", "再次", "再输入", "重新输入", "비밀번호 확인", "подтвер", "повтор", "confirmar", "confirmer", "bestätigen", "bestatigen")
        private val NEW_MARK_TOKENS = setOf("new", "signup", "register", "registration", "create", "choose", "newpassword", "nueva", "nuevo", "nouveau", "neues", "novo", "nova") + CONFIRM_TOKENS
        private val NEW_MARK_PHRASES = listOf("新密码", "新密碼", "新しい", "새 비밀번호") + CONFIRM_PHRASES
        private val CURRENT_MARK_TOKENS = setOf("current", "old", "existing", "previous", "actual", "ancien", "alt", "altes")
        private val CURRENT_MARK_PHRASES = listOf("当前", "目前", "旧密码", "舊密碼", "原密码", "原密碼", "现有", "現在の", "기존", "текущ", "старый", "contraseña actual", "mot de passe actuel")
    }

    private fun containsPhrase(p: String): Boolean = spaced.contains(p) || raw.contains(p)

    private fun tokenHit(set: Set<String>): Boolean = tokens.any { it in set }

    /** A long word inside a token (so "txtPassword1" and "loginpassword" fire), or any CJK phrase inside the text. */
    private fun longHit(list: List<String>): Boolean = list.any { w ->
        if (w.any { it.code > 0x2E80 || it.code in 0x0400..0x04FF } ) containsPhrase(w) else if (w.contains(' ')) containsPhrase(w) else tokens.any { it.contains(w) }
    }

    fun isSearchLike(): Boolean = longHit(SEARCH) || (tokenHit(SEARCH_TOKENS) && !hasPasswordWord() && !hasUsernameWord())
    fun isCardLike(): Boolean = longHit(CARD) && !hasPasswordWord()
    fun looksLikeOtp(): Boolean = longHit(OTP) || tokenHit(OTP_TOKENS)
    fun hasPasswordWord(): Boolean = longHit(PASSWORD_LONG) || tokenHit(PASSWORD_TOKENS)
    fun hasStrongUsernameWord(): Boolean = longHit(USER_STRONG) || tokens.any { t -> t in USER_STRONG_TOKENS || (t.length > 4 && USER_STRONG_TOKENS.any { s -> t.startsWith(s) && s.length >= 4 && t != "usermanual" }) }
    fun hasUsernameWord(): Boolean = hasStrongUsernameWord() || longHit(USER_WEAK) || tokenHit(USER_WEAK_TOKENS)
    fun hasConfirmMarker(): Boolean = tokenHit(CONFIRM_TOKENS) || CONFIRM_PHRASES.any { containsPhrase(it) }
    fun hasNewMarker(): Boolean = tokenHit(NEW_MARK_TOKENS) || NEW_MARK_PHRASES.any { containsPhrase(it) }
    fun hasCurrentMarker(): Boolean = tokenHit(CURRENT_MARK_TOKENS) || CURRENT_MARK_PHRASES.any { containsPhrase(it) }
}
