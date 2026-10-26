package com.nightclip.app.platform

import android.content.Context
import android.content.SharedPreferences
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * Autofill settings, in their own preferences file (not ShellPrefs, which belongs to the rest of the app).
 * Holds flags only: never a credential, never a page or app name.
 */
class AutofillPrefs private constructor(context: Context) {
    private val prefs: SharedPreferences = context.getSharedPreferences("clipbridge_autofill", Context.MODE_PRIVATE)

    private val _requireAuth = MutableStateFlow(prefs.getBoolean(KEY_REQUIRE_AUTH, false))

    /** "每次填充前验证主密码"; default off. */
    val requireAuth: StateFlow<Boolean> = _requireAuth.asStateFlow()
    val requireAuthNow: Boolean get() = _requireAuth.value

    fun setRequireAuth(on: Boolean) {
        prefs.edit().putBoolean(KEY_REQUIRE_AUTH, on).apply()
        _requireAuth.value = on
    }

    companion object {
        /** Preference key, also the name used in the docs. */
        const val KEY_REQUIRE_AUTH = "autofill_require_auth"

        @Volatile private var instance: AutofillPrefs? = null

        fun get(context: Context): AutofillPrefs =
            instance ?: synchronized(this) { instance ?: AutofillPrefs(context.applicationContext).also { instance = it } }
    }
}
