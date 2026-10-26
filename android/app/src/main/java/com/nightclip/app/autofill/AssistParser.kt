package com.nightclip.app.autofill

import android.app.assist.AssistStructure
import android.os.Build
import android.view.View
import android.view.autofill.AutofillId
import com.nightclip.app.logic.autofill.NodeInfo

/** The text-like nodes of a structure plus the AutofillId behind each [NodeInfo.ref]. */
class ParsedStructure(val nodes: List<NodeInfo>, private val ids: List<AutofillId>) {
    fun id(ref: Int): AutofillId = ids[ref]
}

/**
 * Thin glue: AssistStructure -> [NodeInfo]. No decisions here. What it does on purpose for safety:
 *  - the package of every node is the system-reported activity package (never a window title or view text);
 *  - webDomain / webScheme are only what the platform says, inherited from the nearest ancestor that has one;
 *  - an invisible ancestor makes its whole subtree invisible;
 *  - the walk is bounded (node count, depth) so a hostile page cannot make us spin.
 * Nothing is logged.
 */
object AssistParser {
    private const val MAX_NODES = 5_000
    private const val MAX_DEPTH = 80

    private class Frame(val node: AssistStructure.ViewNode, val domain: String?, val scheme: String?, val visible: Boolean, val depth: Int)

    /**
     * @param focusedId the focused field the request reports (FillContext.focusedId); falls back to ViewNode.isFocused
     * @param withValues read the typed text too (save requests only)
     */
    fun parse(structure: AssistStructure, focusedId: AutofillId?, withValues: Boolean): ParsedStructure {
        val pkg = structure.activityComponent?.packageName
        val nodes = ArrayList<NodeInfo>()
        val ids = ArrayList<AutofillId>()
        var visited = 0
        for (w in 0 until structure.windowNodeCount) {
            val root = structure.getWindowNodeAt(w).rootViewNode ?: continue
            val stack = ArrayDeque<Frame>()
            stack.addLast(Frame(root, null, null, true, 0))
            while (stack.isNotEmpty() && visited < MAX_NODES) {
                val f = stack.removeLast()
                visited++
                val n = f.node

                val ownDomain = n.webDomain?.takeIf { it.isNotBlank() }
                val ownScheme = if (Build.VERSION.SDK_INT >= 28) n.webScheme?.takeIf { it.isNotBlank() } else null
                val domain = ownDomain ?: f.domain
                val scheme = when {
                    ownDomain == null -> f.scheme
                    ownScheme != null -> ownScheme
                    ownDomain == f.domain -> f.scheme
                    else -> null
                }
                val visible = f.visible && n.visibility == View.VISIBLE

                val id = n.autofillId
                val html = n.htmlInfo
                val tag = html?.tag?.lowercase()
                val isInput = tag == "input" || tag == "textarea"
                if (id != null && (n.autofillType == View.AUTOFILL_TYPE_TEXT || isInput)) {
                    val attrs = HashMap<String, String>()
                    html?.attributes?.forEach { p -> if (p.first != null && p.second != null) attrs[p.first.lowercase()] = p.second }
                    val text = if (withValues) n.autofillValue?.takeIf { it.isText }?.textValue?.toString() else null
                    nodes += NodeInfo(
                        ref = ids.size,
                        packageName = pkg,
                        webDomain = domain,
                        webScheme = scheme,
                        autofillHints = n.autofillHints?.toList().orEmpty(),
                        idEntry = n.idEntry,
                        hint = n.hint,
                        contentDescription = n.contentDescription?.toString(),
                        inputType = n.inputType,
                        htmlTag = tag,
                        htmlAttributes = attrs,
                        isTextField = true,
                        isVisible = visible,
                        isFocused = if (focusedId != null) id == focusedId else n.isFocused,
                        text = text,
                    )
                    ids += id
                }
                if (f.depth < MAX_DEPTH) {
                    for (i in n.childCount - 1 downTo 0) stack.addLast(Frame(n.getChildAt(i), domain, scheme, visible, f.depth + 1))
                }
            }
        }
        return ParsedStructure(nodes, ids)
    }
}
