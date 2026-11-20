package cloud.moonl.password.autofill

import android.app.assist.AssistStructure
import android.content.Context
import android.service.autofill.FillResponse
import android.view.autofill.AutofillId
import cloud.moonl.password.core.ApiResult
import cloud.moonl.password.logic.autofill.AuthPolicy
import cloud.moonl.password.logic.autofill.DatasetPlanner
import cloud.moonl.password.logic.autofill.FillMode
import cloud.moonl.password.logic.autofill.FormAnalyzer
import cloud.moonl.password.logic.autofill.FormCluster
import cloud.moonl.password.logic.autofill.MatchedEntry
import cloud.moonl.password.logic.autofill.SchemePolicy
import cloud.moonl.password.logic.autofill.Target
import cloud.moonl.password.logic.autofill.TargetResolver

/** What one request is about: the form the user is on and what it is for. */
class Analysis(val parsed: ParsedStructure, val cluster: FormCluster, val target: Target)

/** [response] is null when there is nothing to put in it; [datasetCount] counts the credential rows (not the save declaration). */
class Built(val response: FillResponse?, val datasetCount: Int)

/** The shared steps of "structure in, response out" used by the service and by the auth activity. */
object FillPipeline {
    /** Null: not a login-like form, our own package, a browser page without a platform-reported domain... nothing to offer. */
    fun analyze(context: Context, structure: AssistStructure, focusedId: AutofillId?, manual: Boolean): Analysis? {
        val parsed = AssistParser.parse(structure, focusedId, withValues = false)
        val cluster = FormAnalyzer.analyze(parsed.nodes, manual) ?: return null
        val target = TargetResolver.resolve(cluster, context.packageName) ?: return null
        return Analysis(parsed, cluster, target)
    }

    /** `vault.match` for the target, filtered again by the scheme rules. Null when the call failed (e.g. locked meanwhile). */
    suspend fun matches(target: Target): List<MatchedEntry>? {
        val r = AutofillVault.match(target) as? ApiResult.Ok ?: return null
        return SchemePolicy.allowed(target, r.value)
    }

    /**
     * Datasets for the matches. [freshUnlock]: the master password was typed a moment ago in our own screen, so
     * verify-first datasets can carry their values now.
     */
    suspend fun datasetResponse(
        context: Context,
        a: Analysis,
        matches: List<MatchedEntry>,
        requireAuth: Boolean,
        freshUnlock: Boolean,
        offerOpenVault: Boolean,
    ): Built {
        val plans = DatasetPlanner.plan(a.cluster, a.target, matches, requireAuth)
        val b = FillResponse.Builder()
        var count = 0
        for (plan in plans.datasets) {
            val mode = if (freshUnlock) AuthPolicy.afterFreshUnlock(plan.mode) else plan.mode
            if (mode == FillMode.INLINE) {
                val values = FillValues.fetch(plan) ?: continue
                b.addDataset(ResponseFactory.filledDataset(context, a.parsed, plan, values))
            } else {
                b.addDataset(ResponseFactory.deferredDataset(context, a.parsed, plan.copy(mode = mode)))
            }
            count++
        }
        if (count == 0 && plans.offerOpenVault && offerOpenVault) {
            b.addDataset(ResponseFactory.openVaultDataset(context, a.parsed, a.cluster.fields.first().ref))
            count++
        }
        ResponseFactory.saveInfo(a.parsed, a.cluster)?.let { b.setSaveInfo(it) }
        val hasSave = ResponseFactory.saveInfo(a.parsed, a.cluster) != null
        if (count == 0 && !hasSave) return Built(null, 0)
        return Built(b.build(), count)
    }
}
