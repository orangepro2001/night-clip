package com.nightclip.app.ui.vault

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.nightclip.app.R
import com.nightclip.app.core.HealthIssue
import com.nightclip.app.logic.PasswordStrengthUi
import com.nightclip.app.logic.VaultTexts
import com.nightclip.app.ui.SectionTitle

/** Weak / reused / old / expired passwords (vault.health: local only, nothing leaves the phone). */
@Composable
fun HealthScreen(vm: VaultViewModel, browse: VaultBrowseViewModel) {
    val health by browse.health.collectAsStateWithLifecycle()
    LaunchedEffect(Unit) { browse.loadHealth() }

    Column(Modifier.fillMaxSize()) {
        VaultTopBar(stringResource(R.string.vault_menu_health), onBack = { vm.pop() })
        val h = health
        if (h == null) {
            VaultLoading(Modifier.weight(1f))
        } else {
            Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(stringResource(R.string.vault_health_summary, h.total, h.issueCount), style = MaterialTheme.typography.titleMedium)
                HintText(stringResource(R.string.vault_health_local))
                if (h.issueCount == 0) Text(stringResource(R.string.vault_health_ok), color = MaterialTheme.colorScheme.primary)
                IssueSection(VaultTexts.HEALTH_WEAK, h.weak, { PasswordStrengthUi.text(it) }) { vm.push(VaultRoute.Detail(it)) }
                IssueSection(VaultTexts.HEALTH_REUSED, h.reused, { stringResource(R.string.vault_health_reused_info, it) }) { vm.push(VaultRoute.Detail(it)) }
                IssueSection(VaultTexts.HEALTH_OLD, h.old, { it }) { vm.push(VaultRoute.Detail(it)) }
                IssueSection(VaultTexts.HEALTH_EXPIRED, h.expired, { it }) { vm.push(VaultRoute.Detail(it)) }
                if (h.withoutTotp > 0) HintText(stringResource(R.string.vault_health_without_totp, h.withoutTotp))
            }
        }
    }
}

@Composable
private fun IssueSection(title: String, items: List<HealthIssue>, info: @Composable (String) -> String, onOpen: (String) -> Unit) {
    if (items.isEmpty()) return
    SectionTitle("$title（${items.size}）")
    items.forEach { i ->
        Column(Modifier.fillMaxWidth().clickable { onOpen(i.id) }.padding(vertical = 8.dp)) {
            Text(i.title.ifBlank { stringResource(R.string.vault_entry) }, style = MaterialTheme.typography.bodyLarge)
            val text = info(i.info)
            if (text.isNotBlank()) HintText(text)
        }
    }
}
