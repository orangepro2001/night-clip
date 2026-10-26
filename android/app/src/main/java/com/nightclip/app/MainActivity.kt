package com.nightclip.app

import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject

/**
 * Scaffold screen proving the AAR boundary: starts the core in-process and
 * shows `status`. Pairing, inbox and settings arrive in Phase 0/1.
 */
class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        CoreHost.start(applicationContext, Build.MODEL)
        setContent {
            MaterialTheme {
                Surface(Modifier.fillMaxSize()) {
                    StatusScreen(
                        onStartService = {
                            startForegroundService(Intent(this, SyncForegroundService::class.java))
                        },
                    )
                }
            }
        }
    }
}

@Composable
private fun StatusScreen(onStartService: () -> Unit) {
    var status by remember { mutableStateOf(readStatus()) }
    Column(
        Modifier.verticalScroll(rememberScrollState()).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text("ClipBridge", style = MaterialTheme.typography.headlineSmall)
        Text(status)
        Button(onClick = { status = readStatus() }) { Text("Refresh status") }
        Button(onClick = onStartService) { Text("Start connected mode") }
        UpdateCard()
    }
}

/**
 * Self-update status and the one action that fits it. The core downloads and
 * verifies in the background; this screen polls `update.status` (it must not
 * drain the event queue, which belongs to the foreground service).
 */
@Composable
private fun UpdateCard() {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var st by remember { mutableStateOf(UpdateState()) }
    var note by remember { mutableStateOf("") }

    LaunchedEffect(Unit) {
        while (true) {
            st = withContext(Dispatchers.IO) { readUpdate() }
            delay(if (st.state == "downloading" || st.state == "checking") 500 else 3000)
        }
    }

    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("Updates", style = MaterialTheme.typography.titleMedium)
            Text(st.text)
            if (st.notes.isNotEmpty() && st.state in listOf("available", "ready")) Text(st.notes)
            if (st.state == "downloading") LinearProgressIndicator(progress = { st.progress }, Modifier.fillMaxWidth())
            if (note.isNotEmpty()) Text(note)
            if (st.action != UpdateState.Action.NONE) {
                Button(onClick = {
                    scope.launch {
                        note = ""
                        withContext(Dispatchers.IO) {
                            when (st.action) {
                                UpdateState.Action.CHECK -> callUpdate("update.check")
                                UpdateState.Action.DOWNLOAD -> callUpdate("update.download")
                                UpdateState.Action.INSTALL -> note = installMessage(context, st.readyPath)
                                UpdateState.Action.NONE -> Unit
                            }
                        }
                        st = withContext(Dispatchers.IO) { readUpdate() }
                    }
                }) { Text(st.buttonText) }
            }
            if (st.enabled) {
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    Switch(checked = st.auto, onCheckedChange = { on ->
                        scope.launch(Dispatchers.IO) {
                            callUpdate("update.config", JSONObject().put("auto", on))
                            st = readUpdate()
                        }
                    })
                    Text("Download updates automatically")
                }
            }
        }
    }
}

private fun readUpdate(): UpdateState = try {
    val reply = CoreHost.call("update.status")
    if (reply.optBoolean("ok")) UpdateState.from(reply.getJSONObject("result")) else UpdateState()
} catch (e: IllegalStateException) { // core not started yet
    UpdateState()
}

/** Runs an update method; a failed check surfaces through the next `update.status`. */
private fun callUpdate(method: String, params: JSONObject = JSONObject()): JSONObject = CoreHost.call(method, params)

private fun installMessage(context: Context, path: String): String = when (ApkInstaller.install(context, path)) {
    ApkInstaller.Result.STARTED -> ""
    ApkInstaller.Result.NEEDS_PERMISSION ->
        "Allow ClipBridge to install apps in the settings page that just opened, then tap Install update again."
    ApkInstaller.Result.MISSING_FILE -> "The downloaded update is missing. Check for updates again."
}

private fun readStatus(): String {
    val reply: JSONObject = CoreHost.call("status")
    return if (reply.optBoolean("ok")) {
        reply.getJSONObject("result").toString(2)
    } else {
        reply.getJSONObject("error").optString("code")
    }
}
