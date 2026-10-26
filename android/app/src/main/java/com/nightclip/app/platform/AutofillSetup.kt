package com.nightclip.app.platform

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.Settings
import android.view.autofill.AutofillManager
import com.nightclip.app.logic.autofill.AutofillSetupLogic

/**
 * "Make ClipBridge the autofill service". The vault screens author calls [isDefaultService] / [open] from an
 * "启用自动填充" button, or simply shows `AutofillSettingsSection()` (autofill/AutofillSettingsSection.kt).
 */
object AutofillSetup {
    private fun manager(context: Context): AutofillManager? = context.getSystemService(AutofillManager::class.java)

    /** False on devices without an autofill framework (some Go / custom builds). */
    fun isSupported(context: Context): Boolean = manager(context)?.isAutofillSupported == true

    /** True when ClipBridge is the autofill service the user selected. */
    fun isDefaultService(context: Context): Boolean = manager(context)?.hasEnabledAutofillServices() == true

    fun state(context: Context): AutofillSetupLogic.State = AutofillSetupLogic.state(isSupported(context), isDefaultService(context))

    /** The system dialog "make ClipBridge your autofill service?" (needs no permission). */
    fun intentToEnable(context: Context): Intent =
        Intent(Settings.ACTION_REQUEST_SET_AUTOFILL_SERVICE, Uri.parse(AutofillSetupLogic.packageUri(context.packageName)))

    /** Opens the system dialog; falls back to the general settings when this device has no such dialog. */
    fun open(context: Context) {
        val intent = intentToEnable(context).apply { if (context !is android.app.Activity) addFlags(Intent.FLAG_ACTIVITY_NEW_TASK) }
        try {
            context.startActivity(intent)
        } catch (e: ActivityNotFoundException) {
            try {
                context.startActivity(Intent(Settings.ACTION_SETTINGS).apply { if (context !is android.app.Activity) addFlags(Intent.FLAG_ACTIVITY_NEW_TASK) })
            } catch (_: ActivityNotFoundException) {
                // nothing else to try
            }
        }
    }
}
