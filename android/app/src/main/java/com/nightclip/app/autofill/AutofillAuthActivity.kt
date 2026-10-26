package com.nightclip.app.autofill

import android.app.Application
import android.app.assist.AssistStructure
import android.content.Intent
import android.os.Bundle
import android.os.Parcelable
import android.os.SystemClock
import android.view.View
import android.view.WindowManager
import android.view.autofill.AutofillManager
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.viewModels
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.res.stringResource
import androidx.core.content.IntentCompat
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.nightclip.app.MainActivity
import com.nightclip.app.R
import com.nightclip.app.app
import com.nightclip.app.core.ApiResult
import com.nightclip.app.logic.autofill.DatasetPlanner
import com.nightclip.app.logic.autofill.AutofillQuickUnlockPolicy
import com.nightclip.app.logic.autofill.QuickUnlockRegistry
import com.nightclip.app.logic.autofill.VaultErrors
import com.nightclip.app.platform.Actions
import com.nightclip.app.platform.AutofillPrefs
import com.nightclip.app.platform.VaultPrefs
import com.nightclip.app.ui.ClipBridgeTheme
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

sealed interface AuthUi {
    data object Working : AuthUi
    data class Password(val verify: Boolean, val busy: Boolean = false, val error: String? = null, val retryAfterS: Int = 0) : AuthUi
    data class Message(val text: String, val canOpenApp: Boolean) : AuthUi

    /** Hand [result] (a Dataset or a FillResponse) back to the system and close. */
    data class Finished(val result: Parcelable) : AuthUi
    data object Cancelled : AuthUi
}

/**
 * Transparent screen the system opens for an authentication:
 *  - MODE_UNLOCK: the vault is locked ("解锁以填充"): unlock, match again, return a FillResponse;
 *  - MODE_FILL: one dataset (TOTP refresh, or "verify the master password every time"): return that Dataset;
 *  - MODE_OPEN_VAULT: the "打开 ClipBridge 密码库" row: start the app.
 * FLAG_SECURE is set, nothing is logged, the password never leaves this screen except into `vault.unlock`.
 */
class AutofillAuthActivity : ComponentActivity() {
    private val vm: AuthViewModel by viewModels()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (app.prefs.vaultPreventCaptureNow) window.setFlags(WindowManager.LayoutParams.FLAG_SECURE, WindowManager.LayoutParams.FLAG_SECURE)
        window.decorView.importantForAutofill = View.IMPORTANT_FOR_AUTOFILL_NO_EXCLUDE_DESCENDANTS
        window.decorView.filterTouchesWhenObscured = true // no tapjacking of the password box

        if (intent.getStringExtra(ResponseFactory.EXTRA_MODE) == ResponseFactory.MODE_OPEN_VAULT) {
            startActivity(
                Intent(this, MainActivity::class.java)
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                    .putExtra(Actions.EXTRA_TAB, EXTRA_TAB_VAULT),
            )
            setResult(RESULT_CANCELED)
            finish()
            return
        }
        if (savedInstanceState == null) vm.start(intent)
        setContent { ClipBridgeTheme { Content() } }
    }

    @Composable
    private fun Content() {
        val ui by vm.ui.collectAsStateWithLifecycle()
        LaunchedEffect(ui) {
            when (val u = ui) {
                is AuthUi.Finished -> {
                    setResult(RESULT_OK, Intent().putExtra(AutofillManager.EXTRA_AUTHENTICATION_RESULT, u.result))
                    finish()
                }
                AuthUi.Cancelled -> {
                    setResult(RESULT_CANCELED)
                    finish()
                }
                is AuthUi.Password -> if (!u.busy && u.error == null) vm.tryQuickUnlock(this@AutofillAuthActivity)
                else -> {}
            }
        }
        when (val u = ui) {
            AuthUi.Working, is AuthUi.Finished, AuthUi.Cancelled -> if (!vm.silent) AutofillSheet(onDismiss = vm::cancel) { WorkingContent() }
            is AuthUi.Password -> AutofillSheet(onDismiss = vm::cancel) {
                PasswordContent(
                    title = stringResource(if (u.verify) R.string.autofill_verify_title else R.string.autofill_unlock_title),
                    text = if (u.verify) stringResource(R.string.autofill_verify_text) else null,
                    confirmLabel = stringResource(if (u.verify) R.string.autofill_confirm else R.string.autofill_unlock),
                    busy = u.busy,
                    error = u.error,
                    retryAfterS = u.retryAfterS,
                    onSubmit = vm::submit,
                    onCancel = vm::cancel,
                )
            }
            is AuthUi.Message -> AutofillSheet(onDismiss = vm::cancel) {
                MessageContent(
                    text = u.text,
                    openAppLabel = if (u.canOpenApp) stringResource(R.string.autofill_open_app) else null,
                    onOpenApp = {
                        startActivity(
                            Intent(this@AutofillAuthActivity, MainActivity::class.java)
                                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                                .putExtra(Actions.EXTRA_TAB, EXTRA_TAB_VAULT),
                        )
                        vm.cancel()
                    },
                    onClose = vm::cancel,
                )
            }
        }
    }

    companion object {
        /**
         * Value of Actions.EXTRA_TAB the vault-screens author maps to the vault tab (MainActivity passes it on to
         * LaunchRequests.tab). Until that tab exists the app just opens on its current tab.
         */
        const val EXTRA_TAB_VAULT = "vault"
    }
}

class AuthViewModel(app: Application) : AndroidViewModel(app) {
    private val _ui = MutableStateFlow<AuthUi>(AuthUi.Working)
    val ui: StateFlow<AuthUi> = _ui.asStateFlow()

    private var mode = ResponseFactory.MODE_UNLOCK
    private var entryId: String? = null
    private var verify = false
    private var analysis: Analysis? = null
    private var triedQuick = false

    /** The silent TOTP refresh shows no card; everything else shows progress. */
    val silent: Boolean get() = mode == ResponseFactory.MODE_FILL && !verify

    private val prefs get() = AutofillPrefs.get(getApplication())

    fun start(intent: Intent) {
        mode = intent.getStringExtra(ResponseFactory.EXTRA_MODE) ?: ResponseFactory.MODE_UNLOCK
        entryId = intent.getStringExtra(ResponseFactory.EXTRA_ENTRY_ID)
        verify = intent.getBooleanExtra(ResponseFactory.EXTRA_VERIFY, false)
        val structure = IntentCompat.getParcelableExtra(intent, AutofillManager.EXTRA_ASSIST_STRUCTURE, AssistStructure::class.java)
        viewModelScope.launch {
            analysis = structure?.let { FillPipeline.analyze(getApplication(), it, null, manual = true) }
            if (analysis == null) return@launch say(R.string.autofill_no_match, canOpenApp = false)
            val st = AutofillVault.status()
            val status = (st as? ApiResult.Ok)?.value ?: return@launch say(R.string.autofill_error_not_ready, canOpenApp = false)
            when {
                !status.exists -> say(R.string.autofill_no_vault, canOpenApp = true)
                !status.unlocked -> _ui.value = AuthUi.Password(verify = false, retryAfterS = status.retryAfterS)
                mode == ResponseFactory.MODE_FILL && verify -> _ui.value = AuthUi.Password(verify = true, retryAfterS = status.retryAfterS)
                else -> deliver(fresh = false)
            }
        }
    }

    /** Biometric quick unlock when a provider exists and the policy allows it; the default provider does nothing. */
    fun tryQuickUnlock(host: Any) {
        if (triedQuick) return
        triedQuick = true
        val provider = QuickUnlockRegistry.provider
        val decision = AutofillQuickUnlockPolicy.decide(
            provider, provider.state(), System.currentTimeMillis(), SystemClock.elapsedRealtime(),
            VaultPrefs(getApplication()).quickMaxAgeMs,
        )
        if (decision != AutofillQuickUnlockPolicy.Decision.USE_QUICK) return
        viewModelScope.launch {
            val pw = provider.obtainMasterPassword(host)
            if (pw == null) provider.onResult(false) else submit(pw, fromQuick = true)
        }
    }

    fun submit(password: String) = submit(password, fromQuick = false)

    private fun submit(password: String, fromQuick: Boolean) {
        val current = _ui.value as? AuthUi.Password ?: return
        _ui.value = current.copy(busy = true, error = null)
        viewModelScope.launch {
            val needsUnlock = !(AutofillVault.status().okOrNull?.unlocked ?: false)
            val r = if (needsUnlock) AutofillVault.unlock(password) else AutofillVault.verifyMaster(password)
            when (r) {
                is ApiResult.Ok -> {
                    if (fromQuick) QuickUnlockRegistry.provider.onResult(true)
                    deliver(fresh = true)
                }
                is ApiResult.Fail -> {
                    if (fromQuick) QuickUnlockRegistry.provider.onResult(false)
                    val retry = if (r.code == VaultErrors.RATE_LIMITED) AutofillVault.retryAfterS() else 0
                    _ui.value = current.copy(busy = false, error = VaultErrors.message(r.code, retry), retryAfterS = retry)
                }
            }
        }
    }

    private suspend fun deliver(fresh: Boolean) {
        _ui.value = AuthUi.Working
        val a = analysis ?: return say(R.string.autofill_no_match, canOpenApp = false)
        val matches = FillPipeline.matches(a.target) ?: return say(R.string.autofill_error_not_ready, canOpenApp = false)
        val ctx = getApplication<Application>()
        if (mode == ResponseFactory.MODE_FILL) {
            val plan = DatasetPlanner.plan(a.cluster, a.target, matches, prefs.requireAuthNow).datasets.firstOrNull { it.entry.id == entryId }
                ?: return say(R.string.autofill_no_match, canOpenApp = true)
            val values = FillValues.fetch(plan) ?: return say(R.string.autofill_error_not_ready, canOpenApp = false)
            _ui.value = AuthUi.Finished(ResponseFactory.filledDataset(ctx, a.parsed, plan, values))
            return
        }
        val built = FillPipeline.datasetResponse(ctx, a, matches, prefs.requireAuthNow, freshUnlock = fresh, offerOpenVault = false)
        val response = built.response
        if (response == null || built.datasetCount == 0) say(R.string.autofill_no_match, canOpenApp = true) else _ui.value = AuthUi.Finished(response)
    }

    private fun say(res: Int, canOpenApp: Boolean) {
        _ui.value = AuthUi.Message(getApplication<Application>().getString(res), canOpenApp)
    }

    fun cancel() {
        _ui.value = AuthUi.Cancelled
    }
}
