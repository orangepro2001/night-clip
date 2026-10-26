package com.nightclip.app.logic.autofill

/** What the credentials are for: a web page (host the platform reported) or a native app (package the platform reported). */
sealed interface Target {
    /**
     * @param host exactly the webDomain the platform gave (lower-cased, nothing added or removed)
     * @param scheme "https", "http" or null when the platform did not say
     * @param embedded the page runs in a WebView of a non-browser app: such a domain can be claimed by the app
     *   (loadDataWithBaseURL), so filling there always asks for the master password
     */
    data class Web(val host: String, val scheme: String?, val embedded: Boolean) : Target

    data class App(val packageName: String) : Target
}

object TargetResolver {
    /** Browsers never get package-based matches: without a platform-reported domain they get nothing. */
    private val BROWSERS = setOf(
        "com.android.chrome", "com.chrome.beta", "com.chrome.dev", "com.chrome.canary", "org.chromium.chrome", "com.google.android.apps.chrome",
        "org.mozilla.firefox", "org.mozilla.firefox_beta", "org.mozilla.fenix", "org.mozilla.fenix.nightly", "org.mozilla.focus",
        "org.mozilla.klar", "org.mozilla.fennec_fdroid", "org.mozilla.rocket", "io.github.forkmaintainers.iceraven",
        "com.microsoft.emmx", "com.microsoft.emmx.beta", "com.microsoft.emmx.dev", "com.microsoft.emmx.canary",
        "com.brave.browser", "com.brave.browser_beta", "com.brave.browser_nightly",
        "com.sec.android.app.sbrowser", "com.sec.android.app.sbrowser.beta",
        "com.opera.browser", "com.opera.browser.beta", "com.opera.mini.native", "com.opera.mini.native.beta", "com.opera.gx",
        "com.vivaldi.browser", "com.vivaldi.browser.snapshot", "com.duckduckgo.mobile.android",
        "com.kiwibrowser.browser", "org.bromite.bromite", "org.torproject.torbrowser", "com.yandex.browser", "com.ecosia.android",
        "com.android.browser", "com.mi.globalbrowser", "com.huawei.browser", "com.UCMobile.intl", "com.uc.browser.en",
        "com.tencent.mtt", "com.baidu.browser.apps", "com.qihoo.browser", "com.quark.browser", "mark.via.gp", "com.cloudmosa.puffinFree",
    )

    private val BROWSERS_LOWER = BROWSERS.map { it.lowercase() }.toSet()

    /** A listed browser, or any package with "browser" in its name (a false positive only costs a package match). */
    fun isBrowser(packageName: String): Boolean {
        val p = packageName.lowercase()
        return p in BROWSERS_LOWER || p.contains("browser")
    }

    /** What a host name may look like; anything else the platform hands us is not trusted as a domain. */
    private val HOST = Regex("^(\\[[0-9a-f:.]+]|[a-z0-9]([a-z0-9._-]*[a-z0-9])?)$")

    fun cleanHost(raw: String?): String? {
        val h = raw?.trim()?.lowercase()?.trimEnd('.') ?: return null
        if (h.isEmpty() || h.length > 253 || !HOST.matches(h)) return null
        return h
    }

    /** Local development hosts and IP literals: the only places plain http is accepted for a domain match. */
    fun isLocalOrIpLiteral(host: String): Boolean {
        if (host == "localhost" || host.endsWith(".localhost")) return true
        if (host.startsWith("[")) return true
        val parts = host.split('.')
        return parts.size == 4 && parts.all { p -> p.isNotEmpty() && p.length <= 3 && p.all { it.isDigit() } && p.toInt() <= 255 }
    }

    fun resolve(cluster: FormCluster, ownPackage: String): Target? = resolve(cluster.fields.first().node, ownPackage)

    /**
     * Decide from the node the user is on (all fields of one cluster share their context by construction).
     * Our own package never autofills itself; a browser without a platform-reported domain gets nothing.
     */
    fun resolve(node: NodeInfo, ownPackage: String): Target? {
        val pkg = node.packageName?.trim().orEmpty()
        if (pkg.isEmpty() || pkg == ownPackage) return null
        val domainGiven = !node.webDomain.isNullOrBlank()
        if (domainGiven) {
            val host = cleanHost(node.webDomain) ?: return null // a domain we cannot read: no guesswork
            val scheme = node.webScheme?.trim()?.lowercase()?.takeIf { it == "https" || it == "http" }
            return Target.Web(host, scheme, embedded = !isBrowser(pkg))
        }
        if (isBrowser(pkg)) return null
        return Target.App(pkg)
    }

    /** The `url`/`package` parameters of `vault.match`. A missing scheme is not invented: the bare host is passed. */
    fun matchParams(t: Target): Pair<String?, String?> = when (t) {
        is Target.Web -> (if (t.scheme != null) "${t.scheme}://${t.host}" else t.host) to null
        is Target.App -> null to t.packageName
    }
}

/** Filters `vault.match` results again on the shell side with rules the core does not know: scheme and kind of match. */
object SchemePolicy {
    fun allowed(target: Target, matches: List<MatchedEntry>): List<MatchedEntry> = when (target) {
        is Target.App -> matches.filter { it.score >= 4 }
        is Target.Web -> matches.filter { it.score in 2..3 && schemeOk(target, it) }
    }

    /**
     * https page: any entry. http page: only on localhost / IP literals, or for an entry whose own URL is http
     * (the user saved it that way). A page whose scheme the platform did not tell counts as http.
     */
    private fun schemeOk(t: Target.Web, e: MatchedEntry): Boolean {
        if (t.scheme == "https") return true
        if (TargetResolver.isLocalOrIpLiteral(t.host)) return true
        return e.url.trim().lowercase().startsWith("http://")
    }
}
