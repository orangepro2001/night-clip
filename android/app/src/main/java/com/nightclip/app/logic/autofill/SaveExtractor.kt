package com.nightclip.app.logic.autofill

/** Turns the contexts of a SaveRequest into the credential the user just submitted, or null when there is nothing to save. */
object SaveExtractor {
    /**
     * @param contexts the parsed fill contexts in chronological order (the last one carries the final values)
     * @param appLabel resolves an app package to its visible name for the title suggestion
     */
    fun extract(contexts: List<List<NodeInfo>>, ownPackage: String, appLabel: (String) -> String? = { null }): SaveCandidate? {
        val last = contexts.lastOrNull() ?: return null
        val clusters = FormAnalyzer.clusters(last).filter { it.offersSave }
        for (c in clusters) {
            val pwNode = c.passwordToSave ?: continue
            val password = pwNode.node.text.orEmpty()
            if (password.isEmpty()) continue
            // "new password" + "confirm" typed differently: the form will be rejected, nothing to keep
            val newTexts = c.newPasswords.map { it.node.text.orEmpty() }.filter { it.isNotEmpty() }.distinct()
            if (newTexts.size > 1) continue
            val target = TargetResolver.resolve(c, ownPackage) ?: continue
            var username = c.username?.node?.text.orEmpty()
            if (username.isBlank()) username = earlierUsername(contexts.dropLast(1), target, ownPackage)
            return SaveCandidate(
                username = username.trim(),
                password = password,
                target = target,
                suggestedUrl = SaveDecider.suggestedUrl(target),
                suggestedTitle = SaveDecider.suggestedTitle(target, (target as? Target.App)?.let { appLabel(it.packageName) }),
            )
        }
        return null
    }

    /** Multi-step sign-in: the user name was typed on an earlier screen of the same site or app. */
    private fun earlierUsername(earlier: List<List<NodeInfo>>, target: Target, ownPackage: String): String {
        for (ctx in earlier.asReversed()) {
            for (c in FormAnalyzer.clusters(ctx).asReversed()) {
                val u = c.username ?: continue
                val text = u.node.text.orEmpty().trim()
                if (text.isNotEmpty() && TargetResolver.resolve(c, ownPackage) == target) return text
            }
        }
        return ""
    }
}
