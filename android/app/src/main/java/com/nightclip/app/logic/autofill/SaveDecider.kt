package com.nightclip.app.logic.autofill

/** What the user typed on a form that was submitted. Held in memory only; see [PendingSaveStore]. */
data class SaveCandidate(
    val username: String,
    val password: String,
    val target: Target,
    /** Page url or `androidapp://package` suggested for the new entry. */
    val suggestedUrl: String,
    val suggestedTitle: String,
) {
    override fun toString(): String = "SaveCandidate(target=$target)" // never prints the password
}

sealed interface SaveDecision {
    /** Nothing to save: no password, or exactly this credential is already stored. */
    data object Ignore : SaveDecision

    /** Same account (username) already stored for this site/app with another password: offer to update it. */
    data class Update(val entry: MatchedEntry) : SaveDecision

    data object New : SaveDecision
}

object SaveDecider {
    /**
     * @param matches `vault.match` results already filtered by [SchemePolicy]
     * @param samePassword asks the vault whether the stored entry already has this password (a `vault.reveal` in the glue);
     *   only called for entries whose username equals the candidate's.
     */
    suspend fun decide(c: SaveCandidate, matches: List<MatchedEntry>, samePassword: suspend (MatchedEntry) -> Boolean): SaveDecision {
        if (c.password.isEmpty()) return SaveDecision.Ignore
        val sameAccount = matches.filter { it.kind == "login" && sameUser(it.username, c.username) }
        for (m in sameAccount) {
            if (samePassword(m)) return SaveDecision.Ignore
        }
        sameAccount.firstOrNull()?.let { return SaveDecision.Update(it) }
        return SaveDecision.New
    }

    /** E-mail style user names compare case-insensitively; empty only equals empty. */
    fun sameUser(a: String, b: String): Boolean = a.trim().equals(b.trim(), ignoreCase = true)

    fun suggestedUrl(t: Target): String = when (t) {
        is Target.Web -> "${t.scheme ?: "https"}://${t.host}"
        is Target.App -> "androidapp://${t.packageName}"
    }

    /** "www." is noise in a title. */
    fun suggestedTitle(t: Target, appLabel: String?): String = when (t) {
        is Target.Web -> t.host.removePrefix("www.")
        is Target.App -> appLabel?.takeIf { it.isNotBlank() } ?: t.packageName
    }
}

/**
 * Credentials waiting for the save screen. Only in memory (never prefs, never logs), single use, and gone after
 * [TTL_MS]. The clock is injectable for tests.
 */
class PendingSaveStore(private val now: () -> Long = { System.currentTimeMillis() }) {
    private class Item(val candidate: SaveCandidate, val expiresAt: Long)

    private val items = HashMap<String, Item>()
    private val rnd = java.security.SecureRandom()

    @Synchronized
    fun put(candidate: SaveCandidate): String {
        purge()
        val bytes = ByteArray(12).also { rnd.nextBytes(it) }
        val token = bytes.joinToString("") { "%02x".format(it) }
        items[token] = Item(candidate, now() + TTL_MS)
        return token
    }

    /** Removes and returns the candidate; null when unknown or expired. */
    @Synchronized
    fun take(token: String?): SaveCandidate? {
        purge()
        if (token == null) return null
        return items.remove(token)?.candidate
    }

    @Synchronized
    fun clear() = items.clear()

    @Synchronized
    fun size(): Int {
        purge()
        return items.size
    }

    private fun purge() {
        val t = now()
        items.values.removeAll { it.expiresAt <= t }
    }

    companion object {
        const val TTL_MS = 2 * 60 * 1000L

        /** The process-wide store the service and the save screen share. */
        val shared = PendingSaveStore()
    }
}
