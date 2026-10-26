package com.nightclip.app

import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.lifecycleScope
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.CoreApi
import com.nightclip.app.core.PeerInfo
import com.nightclip.app.logic.PeerTexts
import com.nightclip.app.logic.ShareIntentLogic
import com.nightclip.app.logic.SharePlan
import com.nightclip.app.logic.Strings
import com.nightclip.app.platform.FileSender
import com.nightclip.app.ui.ClipBridgeTheme
import kotlinx.coroutines.launch

/**
 * Share target (ACTION_SEND / ACTION_SEND_MULTIPLE). Text goes out with `clip.send`; files go out as
 * `file.offer {fd}` after their descriptors are opened here, while Android's temporary read grant for
 * the shared URIs is still alive. With exactly one possible target nothing is asked.
 */
class ShareActivity : ComponentActivity() {
    private lateinit var plan: SharePlan
    private var uris: List<Uri> = emptyList()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        uris = streamUris(intent)
        plan = ShareIntentLogic.plan(intent.action, intent.type, intent.getCharSequenceExtra(Intent.EXTRA_TEXT), intent.getCharSequenceExtra(Intent.EXTRA_SUBJECT), uris.size)
        setContent {
            ClipBridgeTheme {
                ShareSheet(plan, onPick = ::send, onClose = ::finish, onOpenApp = {
                    startActivity(Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
                    finish()
                })
            }
        }
    }

    private fun send(to: List<String>?) {
        val app = this.app
        when (val p = plan) {
            is SharePlan.Text -> {
                app.appScope.launch {
                    val message = when (val r = CoreApi.sendClip(p.text, to)) {
                        is ApiResult.Ok -> PeerTexts.sendOutcome(r.value)
                        is ApiResult.Fail -> r.message
                    }
                    app.toastOnMain(message)
                }
                finish()
            }
            is SharePlan.Files -> {
                val target = to?.singleOrNull() ?: return
                lifecycleScope.launch {
                    // Open every descriptor before this activity (and its URI grants) goes away.
                    val opened = FileSender.open(this@ShareActivity, uris)
                    val files = opened.mapNotNull { it.first }
                    opened.firstNotNullOfOrNull { it.second }?.let { app.toastOnMain(it) }
                    if (files.isNotEmpty()) {
                        app.appScope.launch {
                            val results = FileSender.offer(target, files)
                            val failed = results.firstOrNull { it.error != null }
                            app.toastOnMain(failed?.error ?: app.getString(R.string.msg_files_offered, results.size))
                        }
                    }
                    finish()
                }
            }
            else -> finish()
        }
    }

    @Suppress("DEPRECATION")
    private fun streamUris(intent: Intent): List<Uri> {
        val found = LinkedHashSet<Uri>()
        if (intent.action == Intent.ACTION_SEND_MULTIPLE) {
            val list = if (Build.VERSION.SDK_INT >= 33) intent.getParcelableArrayListExtra(Intent.EXTRA_STREAM, Uri::class.java)
            else intent.getParcelableArrayListExtra<Uri>(Intent.EXTRA_STREAM)
            list?.let { found.addAll(it) }
        } else {
            val one = if (Build.VERSION.SDK_INT >= 33) intent.getParcelableExtra(Intent.EXTRA_STREAM, Uri::class.java)
            else intent.getParcelableExtra<Uri>(Intent.EXTRA_STREAM)
            one?.let { found.add(it) }
        }
        intent.clipData?.let { clip ->
            for (i in 0 until clip.itemCount) clip.getItemAt(i).uri?.let { found.add(it) }
        }
        return found.toList()
    }
}

@Composable
private fun ShareSheet(plan: SharePlan, onPick: (List<String>?) -> Unit, onClose: () -> Unit, onOpenApp: () -> Unit) {
    val peersState by produceState<ApiResult<List<PeerInfo>>?>(null) { value = CoreApi.peers() }
    val sendable = (peersState as? ApiResult.Ok)?.value.orEmpty().filter { it.canSend }
    var busy by remember { mutableStateOf(false) }

    val canShare = plan is SharePlan.Text || plan is SharePlan.Files
    // One possible target: no question asked.
    LaunchedEffect(sendable.size, canShare) {
        if (canShare && sendable.size == 1 && !busy) {
            busy = true
            onPick(listOf(sendable.first().deviceId))
        }
    }

    Box(Modifier.fillMaxSize().background(Color(0x66000000)).clickable(onClick = onClose), contentAlignment = Alignment.BottomCenter) {
        Card(Modifier.fillMaxWidth().clickable(enabled = false) {}) {
            Column(Modifier.padding(20.dp).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Text(stringResource(R.string.share_title), style = MaterialTheme.typography.titleLarge)
                when {
                    plan is SharePlan.TextTooLarge -> {
                        Text(Strings.SUPPRESSED_TOO_LARGE)
                        OutlinedButton(onClick = onClose) { Text(stringResource(R.string.close)) }
                    }
                    plan is SharePlan.Nothing -> {
                        Text(stringResource(R.string.share_nothing))
                        OutlinedButton(onClick = onClose) { Text(stringResource(R.string.close)) }
                    }
                    peersState == null || busy -> Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
                    peersState is ApiResult.Fail -> {
                        Text((peersState as ApiResult.Fail).message)
                        OutlinedButton(onClick = onClose) { Text(stringResource(R.string.close)) }
                    }
                    sendable.isEmpty() -> {
                        Text(stringResource(R.string.share_no_devices))
                        Button(onClick = onOpenApp) { Text(stringResource(R.string.share_open_app)) }
                    }
                    else -> {
                        sendable.forEach { p ->
                            FilledTonalButton(onClick = { busy = true; onPick(listOf(p.deviceId)) }, modifier = Modifier.fillMaxWidth()) {
                                Text("${p.name.ifBlank { Strings.UNKNOWN_DEVICE }} · ${PeerTexts.stateLabel(p)}", textAlign = TextAlign.Start)
                            }
                        }
                        if (plan is SharePlan.Text && sendable.size > 1) {
                            Button(onClick = { busy = true; onPick(null) }, modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.share_all)) }
                        }
                        OutlinedButton(onClick = onClose, modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.cancel)) }
                    }
                }
            }
        }
    }
}
