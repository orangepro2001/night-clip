package com.nightclip.app.autofill

import android.os.Build
import android.os.CancellationSignal
import android.service.autofill.AutofillService
import android.service.autofill.FillCallback
import android.service.autofill.FillContext
import android.service.autofill.FillRequest
import android.service.autofill.FillResponse
import android.service.autofill.SaveCallback
import android.service.autofill.SaveRequest
import android.util.Log
import android.view.autofill.AutofillId
import com.nightclip.app.logic.autofill.PendingSaveStore
import com.nightclip.app.logic.autofill.SaveExtractor
import com.nightclip.app.platform.AutofillPrefs
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull

/**
 * ClipBridge as the system's autofill service (docs/plan-vault.md §7.1). The Go core lives in this process
 * ([com.nightclip.app.core.CoreHost] is started by the Application before any service callback runs), so a
 * request is a handful of in-process calls; the decisions are in logic/autofill.
 *
 * Rules enforced here and in the logic: nothing is offered for our own package, for forms that are not logins,
 * for browser pages without a platform-reported domain, or for entries that do not match strictly; secrets are
 * fetched only for matched entries, only for the fields asked for, at the moment the dataset is built; nothing is
 * logged beyond event names. No clipboard, no accessibility service.
 */
class VaultAutofillService : AutofillService() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    override fun onDestroy() {
        scope.cancel()
        super.onDestroy()
    }

    override fun onFillRequest(request: FillRequest, cancellationSignal: CancellationSignal, callback: FillCallback) {
        val job = scope.launch {
            val response: FillResponse? = try {
                withTimeoutOrNull(AutofillVault.FILL_TIMEOUT_MS) { buildResponse(request) }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                Log.w(TAG, "fill failed: ${e.javaClass.simpleName}") // never the message: it could echo a value
                null
            }
            callback.onSuccess(response)
        }
        cancellationSignal.setOnCancelListener { job.cancel() }
    }

    private suspend fun buildResponse(request: FillRequest): FillResponse? {
        val ctx = request.fillContexts.lastOrNull() ?: return null
        val manual = request.flags and FillRequest.FLAG_MANUAL_REQUEST != 0
        val a = FillPipeline.analyze(this, ctx.structure, focusedIdOf(ctx), manual) ?: return null

        val status = AutofillVault.status().okOrNull ?: return null
        if (!status.exists) return null // no vault: nothing to offer, nothing to unlock

        if (!a.cluster.wantsCredentials) {
            // sign-up page: never fill an old password, but still offer to save the new one
            val save = ResponseFactory.saveInfo(a.parsed, a.cluster) ?: return null
            return FillResponse.Builder().setSaveInfo(save).build()
        }
        if (!status.unlocked) return ResponseFactory.unlockResponse(this, a.parsed, a.cluster)

        val matches = FillPipeline.matches(a.target) ?: return null
        val requireAuth = AutofillPrefs.get(this).requireAuthNow
        return FillPipeline.datasetResponse(this, a, matches, requireAuth, freshUnlock = false, offerOpenVault = true).response
    }

    override fun onSaveRequest(request: SaveRequest, callback: SaveCallback) {
        scope.launch {
            try {
                val contexts = request.fillContexts.map { fc ->
                    AssistParser.parse(fc.structure, focusedIdOf(fc), withValues = true).nodes
                }
                val candidate = SaveExtractor.extract(contexts, packageName, ::appLabel)
                if (candidate == null) {
                    callback.onSuccess()
                    return@launch
                }
                val token = PendingSaveStore.shared.put(candidate)
                if (Build.VERSION.SDK_INT >= 28) {
                    // The system starts the screen when the save prompt is dismissed (background starts are restricted).
                    callback.onSuccess(ResponseFactory.saveScreenSender(this@VaultAutofillService, token))
                } else {
                    startActivity(ResponseFactory.saveScreenIntent(this@VaultAutofillService, token))
                    callback.onSuccess()
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                Log.w(TAG, "save failed: ${e.javaClass.simpleName}")
                callback.onFailure(null)
            }
        }
    }

    /** FillContext.getFocusedId exists from API 29; before that the parser relies on ViewNode.isFocused. */
    private fun focusedIdOf(fc: FillContext): AutofillId? = if (Build.VERSION.SDK_INT >= 29) fc.focusedId else null

    /** The visible name of an app, or null when the system will not tell us (package visibility). */
    private fun appLabel(pkg: String): String? = try {
        packageManager.getApplicationLabel(packageManager.getApplicationInfo(pkg, 0)).toString()
    } catch (e: Exception) {
        null
    }

    private companion object {
        const val TAG = "ClipBridge"
    }
}
