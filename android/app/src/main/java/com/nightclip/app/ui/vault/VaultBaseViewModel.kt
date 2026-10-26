package com.nightclip.app.ui.vault

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.nightclip.app.ClipBridgeApp
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.CoreHost
import com.nightclip.app.core.CoreState
import com.nightclip.app.logic.VaultErrors
import com.nightclip.app.platform.VaultSession
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch

/**
 * Base of the three vault view models: one-shot messages, error handling and the rule that every lock wipes
 * everything a view model holds (docs/vault-api.md §4 rule 4).
 */
abstract class VaultBaseViewModel(application: Application) : AndroidViewModel(application) {
    protected val app = application as ClipBridgeApp

    private val _messages = MutableSharedFlow<String>(extraBufferCapacity = 16)
    val messages: SharedFlow<String> = _messages.asSharedFlow()

    init {
        viewModelScope.launch { VaultSession.locks.collect { wipe() } }
    }

    /** Drops every secret, draft, list and detail this view model holds. Called on each lock. */
    protected abstract fun wipe()

    protected fun say(text: String) {
        _messages.tryEmit(text)
    }

    /**
     * Shows the message for a failed call. A VAULT_LOCKED reply means the core locked without the shell noticing
     * (the event was dropped): the shell state is brought in line with it.
     */
    protected fun fail(r: ApiResult.Fail, context: VaultErrors.Context = VaultErrors.Context.GENERAL) {
        if (VaultErrors.meansLocked(r.code)) {
            VaultSession.coreReportsLocked()
        }
        say(VaultErrors.message(r.code, r.retryAfterS, context))
    }

    protected suspend fun awaitCore() {
        CoreHost.state.first { it is CoreState.Ready }
    }
}
