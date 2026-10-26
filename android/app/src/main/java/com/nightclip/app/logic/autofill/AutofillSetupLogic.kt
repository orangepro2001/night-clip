package com.nightclip.app.logic.autofill

/** The pure part of "make ClipBridge the autofill service" (the Android calls live in platform/AutofillSetup.kt). */
object AutofillSetupLogic {
    const val SERVICE_CLASS = "com.nightclip.app.autofill.VaultAutofillService"

    enum class State { UNSUPPORTED, NOT_ENABLED, ENABLED }

    fun state(supported: Boolean, enabledForUs: Boolean): State = when {
        !supported -> State.UNSUPPORTED
        enabledForUs -> State.ENABLED
        else -> State.NOT_ENABLED
    }

    /** The data of Settings.ACTION_REQUEST_SET_AUTOFILL_SERVICE: it names the package to be selected. */
    fun packageUri(packageName: String): String = "package:$packageName"

    /** `Settings.Secure.autofill_service` holds "package/class" (the class may be abbreviated with a leading dot). */
    fun isOurServiceId(settingValue: String?, packageName: String): Boolean {
        val v = settingValue?.trim().orEmpty()
        val slash = v.indexOf('/')
        if (slash <= 0) return false
        val pkg = v.substring(0, slash)
        var cls = v.substring(slash + 1)
        if (cls.startsWith(".")) cls = pkg + cls
        return pkg == packageName && cls == SERVICE_CLASS
    }
}
