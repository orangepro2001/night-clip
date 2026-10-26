package com.nightclip.app

import android.content.Intent
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
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
    Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("ClipBridge", style = MaterialTheme.typography.headlineSmall)
        Text(status)
        Button(onClick = { status = readStatus() }) { Text("Refresh status") }
        Button(onClick = onStartService) { Text("Start connected mode") }
    }
}

private fun readStatus(): String {
    val reply: JSONObject = CoreHost.call("status")
    return if (reply.optBoolean("ok")) {
        reply.getJSONObject("result").toString(2)
    } else {
        reply.getJSONObject("error").optString("code")
    }
}
