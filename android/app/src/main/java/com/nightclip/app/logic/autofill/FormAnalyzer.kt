package com.nightclip.app.logic.autofill

/**
 * Groups the classified fields of a screen into login-like forms and picks the one the user is on.
 * A form never spans two contexts (package, web domain, web scheme): fields from another frame or window
 * are never filled together with the focused one.
 */
object FormAnalyzer {
    /** Fields with a role we act on, in document order, with the ambiguous-password-run rule applied per context. */
    fun classify(nodes: List<NodeInfo>): List<ClassifiedNode> {
        val raw = nodes.mapNotNull {
            val c = FieldClassifier.classify(it)
            if (c.role == FieldRole.IGNORED || c.role == FieldRole.CREDIT_CARD) null else ClassifiedNode(it, c.role, c)
        }
        return resolveAmbiguousRuns(raw)
    }

    /**
     * Runs of consecutive password fields where the words do not say which is which. Counting the unlabelled ones
     * plus the "confirm" ones (as long as nothing is explicitly "new"): one is a login, two are "new + confirm",
     * three are "current + new + confirm".
     */
    private fun resolveAmbiguousRuns(list: List<ClassifiedNode>): List<ClassifiedNode> {
        val out = list.toMutableList()
        var i = 0
        while (i < out.size) {
            if (!out[i].isPasswordish()) {
                i++
                continue
            }
            var j = i
            while (j + 1 < out.size && out[j + 1].isPasswordish() && out[j + 1].node.context == out[i].node.context) j++
            val run = (i..j).toList()
            // an unlabelled password after a "new password" is its confirmation
            val firstNew = run.firstOrNull { out[it].role == FieldRole.NEW_PASSWORD }
            if (firstNew != null) for (k in run) if (k > firstNew && out[k].isAmbiguousPassword()) out[k] = out[k].copy(role = FieldRole.NEW_PASSWORD)
            val amb = run.filter { out[it].isAmbiguousPassword() }
            val explicitNew = run.any { out[it].role == FieldRole.NEW_PASSWORD && !out[it].classification.confirm }
            val confirms = run.count { out[it].role == FieldRole.NEW_PASSWORD && out[it].classification.confirm }
            if (amb.isNotEmpty() && !explicitNew) {
                val n = amb.size + confirms
                if (n >= 2) {
                    val keepCurrent = if (n >= 3) 1 else 0
                    for (k in amb.drop(keepCurrent)) out[k] = out[k].copy(role = FieldRole.NEW_PASSWORD)
                }
            }
            i = j + 1
        }
        return out
    }

    private fun ClassifiedNode.isPasswordish() = role == FieldRole.PASSWORD || role == FieldRole.NEW_PASSWORD
    private fun ClassifiedNode.isAmbiguousPassword() = role == FieldRole.PASSWORD && classification.ambiguousPassword

    fun clusters(nodes: List<NodeInfo>): List<FormCluster> {
        val classified = classify(nodes)
        val result = mutableListOf<FormCluster>()
        var cur = mutableListOf<ClassifiedNode>()
        fun close() {
            if (cur.isNotEmpty()) result += FormCluster(cur)
            cur = mutableListOf()
        }
        for (f in classified) {
            if (cur.isNotEmpty() && cur.first().node.context != f.node.context) close()
            val hasPassword = cur.any { it.role == FieldRole.PASSWORD || it.role == FieldRole.NEW_PASSWORD }
            val hasOtp = cur.any { it.role == FieldRole.ONE_TIME_CODE }
            when (f.role) {
                FieldRole.USERNAME -> {
                    if (hasPassword || hasOtp) close()
                }
                FieldRole.PASSWORD, FieldRole.NEW_PASSWORD -> {
                    if (hasOtp) close()
                }
                FieldRole.ONE_TIME_CODE -> {
                    // an inline code box after a password belongs to that login; otherwise it is a page of its own
                    if (!hasPassword || hasOtp) close()
                }
                else -> {}
            }
            cur += f
        }
        close()
        return result
    }

    /**
     * @param manual the user asked for autofill explicitly (FLAG_MANUAL_REQUEST): the focus need not be on a known field.
     * @return the form to act on, or null when this request is about something that is not a login form.
     */
    fun select(clusters: List<FormCluster>, nodes: List<NodeInfo>, manual: Boolean): FormCluster? {
        val focused = nodes.firstOrNull { it.isFocused }
        if (focused != null) {
            val own = clusters.firstOrNull { c -> c.fields.any { it.ref == focused.ref } }
            if (own != null) return own.takeIf { it.kind != FormKind.NONE }
            if (!manual) return null
        }
        val rank = listOf(FormKind.LOGIN, FormKind.CHANGE_PASSWORD, FormKind.USERNAME_STEP, FormKind.OTP, FormKind.SIGNUP)
        return rank.firstNotNullOfOrNull { k -> clusters.firstOrNull { it.kind == k } }
    }

    fun analyze(nodes: List<NodeInfo>, manual: Boolean = false): FormCluster? = select(clusters(nodes), nodes, manual)
}
