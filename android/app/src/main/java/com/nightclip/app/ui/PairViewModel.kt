package com.nightclip.app.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.CoreApi
import com.nightclip.app.core.CoreHost
import com.nightclip.app.logic.LinkResult
import com.nightclip.app.logic.ManualPairInput
import com.nightclip.app.logic.ManualResult
import com.nightclip.app.logic.PairUi
import com.nightclip.app.logic.PairingFlow
import com.nightclip.app.logic.PairingLink
import com.nightclip.app.logic.Strings
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** Joiner-side pairing (phone joins a computer's QR code). The state machine itself is [PairingFlow]. */
class PairViewModel(application: Application) : AndroidViewModel(application) {
    private val _ui = MutableStateFlow<PairUi>(PairUi.Idle)
    val ui: StateFlow<PairUi> = _ui.asStateFlow()

    private val _error = MutableStateFlow<String?>(null)
    val inputError: StateFlow<String?> = _error.asStateFlow()

    /** A link handed over by a clipbridge:// VIEW intent, shown in the paste tab for the user to confirm. */
    private val _prefill = MutableStateFlow<String?>(null)
    val prefill: StateFlow<String?> = _prefill.asStateFlow()

    init {
        viewModelScope.launch { CoreHost.events.collect { ev -> _ui.update { PairingFlow.reduce(it, ev) } } }
    }

    fun setPrefill(link: String?) {
        _prefill.value = link
    }

    fun clearError() {
        _error.value = null
    }

    /** Text from a QR scan or the paste box. */
    fun joinWithText(raw: String, fromQr: Boolean = false) {
        val link = PairingLink.extract(raw)
        if (link == null) {
            _error.value = if (fromQr) Strings.PAIR_NOT_QR else Strings.PAIR_NOT_A_LINK
            return
        }
        when (val parsed = PairingLink.parse(link, System.currentTimeMillis() / 1000)) {
            is LinkResult.Bad -> _error.value = parsed.message
            is LinkResult.Ok -> begin { CoreApi.joinWithLink(link) }
        }
    }

    fun joinManual(host: String, port: String, psk: String, fp: String) {
        when (val v = ManualPairInput.validate(host, port, psk, fp)) {
            is ManualResult.Bad -> _error.value = listOfNotNull(v.hostError, v.portError, v.pskError, v.fpError).first()
            is ManualResult.Ok -> begin { CoreApi.joinManual(v.host, v.port, v.psk, v.fp) }
        }
    }

    private fun begin(call: suspend () -> ApiResult<String>) {
        if (_ui.value is PairUi.Joining || _ui.value is PairUi.ShowSas) return
        _error.value = null
        _ui.value = PairUi.Joining(null)
        viewModelScope.launch {
            when (val r = call()) {
                is ApiResult.Ok -> _ui.update { PairingFlow.withJoinId(it, r.value) }
                is ApiResult.Fail -> _ui.value = PairUi.Failed(r.code, r.message)
            }
        }
    }

    fun reset() {
        _ui.value = PairUi.Idle
        _error.value = null
    }
}
