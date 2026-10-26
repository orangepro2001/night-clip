package com.nightclip.app.autofill

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.IntentSender
import android.os.Build
import android.service.autofill.Dataset
import android.service.autofill.FillResponse
import android.service.autofill.SaveInfo
import android.view.autofill.AutofillValue
import android.widget.RemoteViews
import com.nightclip.app.R
import com.nightclip.app.logic.autofill.DatasetPlan
import com.nightclip.app.logic.autofill.FillMode
import com.nightclip.app.logic.autofill.FormCluster
import java.util.concurrent.atomic.AtomicInteger

/** Builders for the android.service.autofill objects. No decisions: the plan comes from logic/autofill. */
object ResponseFactory {
    const val EXTRA_MODE = "com.nightclip.app.autofill.MODE"
    const val EXTRA_ENTRY_ID = "com.nightclip.app.autofill.ENTRY_ID"
    const val EXTRA_VERIFY = "com.nightclip.app.autofill.VERIFY"
    const val EXTRA_SAVE_TOKEN = "com.nightclip.app.autofill.SAVE_TOKEN"

    const val MODE_UNLOCK = "unlock"
    const val MODE_FILL = "fill"
    const val MODE_OPEN_VAULT = "open_vault"

    private val requestCodes = AtomicInteger(0x4100)

    /**
     * The system adds the AssistStructure to the intent when it launches an authentication, which only works for
     * a MUTABLE PendingIntent; the intent is explicit (our own activity), so no other app can redirect it.
     */
    private fun authSender(context: Context, intent: Intent): IntentSender {
        val flags = PendingIntent.FLAG_CANCEL_CURRENT or if (Build.VERSION.SDK_INT >= 31) PendingIntent.FLAG_MUTABLE else 0
        return PendingIntent.getActivity(context, requestCodes.incrementAndGet(), intent, flags).intentSender
    }

    fun presentation(context: Context, title: String, subtitle: String?): RemoteViews =
        RemoteViews(context.packageName, R.layout.autofill_item).apply {
            setTextViewText(R.id.autofill_title, title)
            if (subtitle.isNullOrBlank()) setViewVisibility(R.id.autofill_subtitle, android.view.View.GONE) else setTextViewText(R.id.autofill_subtitle, subtitle)
        }

    /** Row text for an entry: title, and the user name (or the kind of secret) below. Never a secret. */
    fun subtitleFor(context: Context, plan: DatasetPlan): String? {
        val e = plan.entry
        val base = when {
            e.username.isNotBlank() -> e.username
            e.kind == "apikey" -> context.getString(R.string.autofill_kind_apikey)
            e.kind == "secret" -> context.getString(R.string.autofill_kind_secret)
            else -> null
        }
        return if (plan.usesTotp && plan.fills.size == 1) context.getString(R.string.autofill_totp_sub, base ?: context.getString(R.string.autofill_kind_login)) else base
    }

    /** Declares "save to vault" for the form. Null when there is nothing to save from it. */
    fun saveInfo(parsed: ParsedStructure, cluster: FormCluster): SaveInfo? {
        if (!cluster.offersSave) return null
        val pw = cluster.passwordToSave ?: return null
        val user = cluster.username
        val type = if (user != null) SaveInfo.SAVE_DATA_TYPE_PASSWORD or SaveInfo.SAVE_DATA_TYPE_USERNAME else SaveInfo.SAVE_DATA_TYPE_PASSWORD
        val builder = SaveInfo.Builder(type, arrayOf(parsed.id(pw.ref)))
        if (user != null) builder.setOptionalIds(arrayOf(parsed.id(user.ref)))
        builder.setFlags(SaveInfo.FLAG_SAVE_ON_ALL_VIEWS_INVISIBLE)
        return builder.build()
    }

    /** Response for a locked vault: one row "解锁以填充" that opens [AutofillAuthActivity]. */
    fun unlockResponse(context: Context, parsed: ParsedStructure, cluster: FormCluster): FillResponse {
        val intent = Intent(context, AutofillAuthActivity::class.java).putExtra(EXTRA_MODE, MODE_UNLOCK)
        val ids = cluster.refs.map { parsed.id(it) }.toTypedArray()
        val b = FillResponse.Builder().setAuthentication(
            ids,
            authSender(context, intent),
            presentation(context, context.getString(R.string.autofill_unlock_item), context.getString(R.string.autofill_unlock_item_sub)),
        )
        saveInfo(parsed, cluster)?.let { b.setSaveInfo(it) }
        return b.build()
    }

    /** A dataset that carries real values (INLINE plans, and everything returned from the auth activity). */
    fun filledDataset(context: Context, parsed: ParsedStructure, plan: DatasetPlan, values: Map<Int, String>): Dataset {
        val b = Dataset.Builder(presentation(context, plan.entry.title.ifBlank { plan.entry.username }, subtitleFor(context, plan)))
        for ((ref, v) in values) b.setValue(parsed.id(ref), AutofillValue.forText(v))
        return b.build()
    }

    /** A dataset without values that runs [AutofillAuthActivity] when tapped (verify or TOTP refresh). */
    fun deferredDataset(context: Context, parsed: ParsedStructure, plan: DatasetPlan): Dataset {
        val intent = Intent(context, AutofillAuthActivity::class.java)
            .putExtra(EXTRA_MODE, MODE_FILL)
            .putExtra(EXTRA_ENTRY_ID, plan.entry.id)
            .putExtra(EXTRA_VERIFY, plan.mode == FillMode.DEFERRED_VERIFY)
        val b = Dataset.Builder(presentation(context, plan.entry.title.ifBlank { plan.entry.username }, subtitleFor(context, plan)))
            .setAuthentication(authSender(context, intent))
        for (f in plan.fills) b.setValue(parsed.id(f.ref), null) // null value: filled after the authentication
        return b.build()
    }

    /** The "打开 ClipBridge 密码库" row; [anchorRef] is any field of the form. */
    fun openVaultDataset(context: Context, parsed: ParsedStructure, anchorRef: Int): Dataset {
        val intent = Intent(context, AutofillAuthActivity::class.java).putExtra(EXTRA_MODE, MODE_OPEN_VAULT)
        return Dataset.Builder(
            presentation(context, context.getString(R.string.autofill_open_vault_item), context.getString(R.string.autofill_open_vault_item_sub)),
        ).setAuthentication(authSender(context, intent)).setValue(parsed.id(anchorRef), null).build()
    }

    /** Intent + sender that open the save screen. The token points into [com.nightclip.app.logic.autofill.PendingSaveStore]. */
    fun saveScreenIntent(context: Context, token: String): Intent =
        Intent(context, AutofillSaveActivity::class.java).putExtra(EXTRA_SAVE_TOKEN, token).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)

    fun saveScreenSender(context: Context, token: String): IntentSender {
        val flags = PendingIntent.FLAG_CANCEL_CURRENT or PendingIntent.FLAG_ONE_SHOT or PendingIntent.FLAG_IMMUTABLE
        return PendingIntent.getActivity(context, requestCodes.incrementAndGet(), saveScreenIntent(context, token), flags).intentSender
    }
}
