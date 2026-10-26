package com.nightclip.app.autofill

import android.app.Application
import android.content.Intent
import android.os.Bundle
import android.view.View
import android.view.WindowManager
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.viewModels
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.unit.dp
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.nightclip.app.MainActivity
import com.nightclip.app.R
import com.nightclip.app.core.ApiResult
import com.nightclip.app.logic.autofill.EntryJson
import com.nightclip.app.logic.autofill.MatchedEntry
import com.nightclip.app.logic.autofill.PendingSaveStore
import com.nightclip.app.logic.autofill.SaveCandidate
import com.nightclip.app.logic.autofill.SaveDecider
import com.nightclip.app.logic.autofill.SaveDecision
import com.nightclip.app.logic.autofill.VaultErrors
import com.nightclip.app.platform.Actions
import com.nightclip.app.ui.ClipBridgeTheme
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

sealed interface SaveUi {
    data object Working : SaveUi
    data class Password(val busy: Boolean = false, val error: String? = null, val retryAfterS: Int = 0) : SaveUi
    data class Update(val entry: MatchedEntry, val busy: Boolean = false, val error: String? = null) : SaveUi
    data class Form(val busy: Boolean = false, val error: String? = null) : SaveUi
    data class Message(val text: String, val canOpenApp: Boolean) : SaveUi
    data class Done(val text: String) : SaveUi
}

/**
 * "保存到密码库": opened by the system after the user accepted the save prompt of a submitted login form.
 * Prefilled from the platform-reported domain or package; the vault must be unlocked first. FLAG_SECURE, nothing logged.
 */
class AutofillSaveActivity : ComponentActivity() {
    private val vm: SaveViewModel by viewModels()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.setFlags(WindowManager.LayoutParams.FLAG_SECURE, WindowManager.LayoutParams.FLAG_SECURE)
        window.decorView.importantForAutofill = View.IMPORTANT_FOR_AUTOFILL_NO_EXCLUDE_DESCENDANTS
        window.decorView.filterTouchesWhenObscured = true
        if (savedInstanceState == null) vm.start(intent.getStringExtra(ResponseFactory.EXTRA_SAVE_TOKEN))
        setContent { ClipBridgeTheme { Content() } }
    }

    @Composable
    private fun Content() {
        val ui by vm.ui.collectAsStateWithLifecycle()
        LaunchedEffect(ui) {
            val u = ui
            if (u is SaveUi.Done) {
                Toast.makeText(this@AutofillSaveActivity, u.text, Toast.LENGTH_SHORT).show()
                finish()
            }
        }
        val dismiss = { finish() }
        AutofillSheet(onDismiss = dismiss) {
            when (val u = ui) {
                SaveUi.Working, is SaveUi.Done -> WorkingContent()
                is SaveUi.Password -> PasswordContent(
                    title = stringResource(R.string.autofill_unlock_title),
                    text = null,
                    confirmLabel = stringResource(R.string.autofill_unlock),
                    busy = u.busy,
                    error = u.error,
                    retryAfterS = u.retryAfterS,
                    onSubmit = vm::submit,
                    onCancel = dismiss,
                )
                is SaveUi.Message -> MessageContent(
                    text = u.text,
                    openAppLabel = if (u.canOpenApp) stringResource(R.string.autofill_open_app) else null,
                    onOpenApp = {
                        startActivity(
                            Intent(this@AutofillSaveActivity, MainActivity::class.java)
                                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                                .putExtra(Actions.EXTRA_TAB, AutofillAuthActivity.EXTRA_TAB_VAULT),
                        )
                        finish()
                    },
                    onClose = dismiss,
                )
                is SaveUi.Update -> UpdateContent(u, dismiss)
                is SaveUi.Form -> FormContent(u, dismiss)
            }
        }
    }

    @Composable
    private fun UpdateContent(u: SaveUi.Update, dismiss: () -> Unit) {
        Text(stringResource(R.string.autofill_save_update_title), style = MaterialTheme.typography.titleLarge)
        Text(
            stringResource(R.string.autofill_save_update_text, u.entry.title, u.entry.username.ifBlank { "—" }),
            style = MaterialTheme.typography.bodyMedium,
        )
        u.error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
        Button(onClick = vm::updateExisting, enabled = !u.busy, modifier = Modifier.fillMaxWidth()) {
            Text(stringResource(R.string.autofill_save_update_button))
        }
        OutlinedButton(onClick = vm::chooseNew, enabled = !u.busy, modifier = Modifier.fillMaxWidth()) {
            Text(stringResource(R.string.autofill_save_as_new))
        }
        TextButton(onClick = dismiss) { Text(stringResource(R.string.autofill_cancel)) }
    }

    @Composable
    private fun FormContent(u: SaveUi.Form, dismiss: () -> Unit) {
        var showPw by remember { mutableStateOf(false) }
        Text(stringResource(R.string.autofill_save_title), style = MaterialTheme.typography.titleLarge)
        FormField(stringResource(R.string.autofill_save_field_title), vm.title) { vm.title = it }
        FormField(stringResource(R.string.autofill_save_field_username), vm.username) { vm.username = it }
        FormField(stringResource(R.string.autofill_save_field_url), vm.url) { vm.url = it }
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            FormField(stringResource(R.string.autofill_save_field_password), vm.password, secret = !showPw, modifier = Modifier.weight(1f)) { vm.password = it }
            TextButton(onClick = { showPw = !showPw }) { Text(stringResource(if (showPw) R.string.hide else R.string.show)) }
        }
        Text(stringResource(R.string.autofill_save_new_hint), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        u.error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
            TextButton(onClick = dismiss) { Text(stringResource(R.string.autofill_cancel)) }
            Button(onClick = vm::saveNew, enabled = !u.busy && vm.password.isNotEmpty()) { Text(stringResource(R.string.autofill_save_button)) }
        }
    }
}

class SaveViewModel(app: Application) : AndroidViewModel(app) {
    private val _ui = MutableStateFlow<SaveUi>(SaveUi.Working)
    val ui: StateFlow<SaveUi> = _ui.asStateFlow()

    // Form state lives here so it survives rotation; it is memory only and dies with the screen.
    var title by mutableStateOf("")
    var username by mutableStateOf("")
    var url by mutableStateOf("")
    var password by mutableStateOf("")

    private var candidate: SaveCandidate? = null
    private var existing: MatchedEntry? = null

    private fun str(res: Int) = getApplication<Application>().getString(res)

    fun start(token: String?) {
        val c = PendingSaveStore.shared.take(token)
        if (c == null) {
            _ui.value = SaveUi.Message(str(R.string.autofill_save_expired), canOpenApp = false)
            return
        }
        candidate = c
        title = c.suggestedTitle
        username = c.username
        url = c.suggestedUrl
        password = c.password
        viewModelScope.launch {
            val st = AutofillVault.status().okOrNull ?: return@launch fail(R.string.autofill_error_not_ready)
            when {
                !st.exists -> _ui.value = SaveUi.Message(str(R.string.autofill_no_vault), canOpenApp = true)
                !st.unlocked -> _ui.value = SaveUi.Password(retryAfterS = st.retryAfterS)
                else -> decide()
            }
        }
    }

    private fun fail(res: Int) {
        _ui.value = SaveUi.Message(str(res), canOpenApp = false)
    }

    fun submit(masterPassword: String) {
        val current = _ui.value as? SaveUi.Password ?: return
        _ui.value = current.copy(busy = true, error = null)
        viewModelScope.launch {
            when (val r = AutofillVault.unlock(masterPassword)) {
                is ApiResult.Ok -> decide()
                is ApiResult.Fail -> {
                    val retry = if (r.code == VaultErrors.RATE_LIMITED) AutofillVault.retryAfterS() else 0
                    _ui.value = current.copy(busy = false, error = VaultErrors.message(r.code, retry), retryAfterS = retry)
                }
            }
        }
    }

    /** Compare with what the vault already knows for this site/app: ignore, offer an update, or save as new. */
    private suspend fun decide() {
        _ui.value = SaveUi.Working
        val c = candidate ?: return fail(R.string.autofill_save_expired)
        val matches = FillPipeline.matches(c.target) ?: return fail(R.string.autofill_error_not_ready)
        val decision = SaveDecider.decide(c, matches) { m ->
            (AutofillVault.reveal(m.id, "password") as? ApiResult.Ok)?.value == c.password
        }
        when (decision) {
            SaveDecision.Ignore -> _ui.value = SaveUi.Message(str(R.string.autofill_save_same), canOpenApp = false)
            is SaveDecision.Update -> {
                existing = decision.entry
                _ui.value = SaveUi.Update(decision.entry)
            }
            SaveDecision.New -> _ui.value = SaveUi.Form()
        }
    }

    fun chooseNew() {
        _ui.value = SaveUi.Form()
    }

    fun saveNew() {
        _ui.value = SaveUi.Form(busy = true)
        val entry = EntryJson.newLogin(title.ifBlank { candidate?.suggestedTitle.orEmpty() }, username.trim(), password, url)
        viewModelScope.launch {
            when (val r = AutofillVault.add(entry)) {
                is ApiResult.Ok -> _ui.value = SaveUi.Done(str(R.string.autofill_save_saved))
                is ApiResult.Fail -> _ui.value = SaveUi.Form(error = r.message)
            }
        }
    }

    /** `vault.update` replaces the whole entry: read it back (protected fields and TOTP secret too), change the password only. */
    fun updateExisting() {
        val entry = existing ?: return
        val newPassword = candidate?.password ?: return
        _ui.value = SaveUi.Update(entry, busy = true)
        viewModelScope.launch {
            val detail = (AutofillVault.get(entry.id) as? ApiResult.Ok)?.value ?: return@launch failUpdate(entry)
            val revealed = HashMap<String, String>()
            for (name in EntryJson.protectedFieldsToReveal(detail)) {
                revealed[name] = (AutofillVault.reveal(entry.id, "field:$name") as? ApiResult.Ok)?.value ?: return@launch failUpdate(entry)
            }
            val secret = if (detail.optBoolean("has_totp")) (AutofillVault.reveal(entry.id, "totp_secret") as? ApiResult.Ok)?.value else null
            val body = EntryJson.withNewPassword(detail, newPassword, revealed, secret) ?: return@launch failUpdate(entry)
            when (val r = AutofillVault.update(entry.id, body)) {
                is ApiResult.Ok -> _ui.value = SaveUi.Done(str(R.string.autofill_save_updated))
                is ApiResult.Fail -> _ui.value = SaveUi.Update(entry, error = r.message)
            }
        }
    }

    private fun failUpdate(entry: MatchedEntry) {
        _ui.value = SaveUi.Update(entry, error = str(R.string.autofill_error_not_ready))
    }
}
