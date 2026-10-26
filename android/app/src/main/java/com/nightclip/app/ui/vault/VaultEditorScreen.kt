package com.nightclip.app.ui.vault

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.nightclip.app.R
import com.nightclip.app.logic.DraftIssueField
import com.nightclip.app.logic.EntryDraft
import com.nightclip.app.logic.ExpiryDates
import com.nightclip.app.logic.KindTemplate
import com.nightclip.app.logic.VaultTexts
import com.nightclip.app.ui.ConfirmDialog
import kotlinx.coroutines.delay

/** Add / edit form. The draft lives in [VaultBrowseViewModel] (survives rotation, wiped on lock or discard). */
@Composable
fun EntryEditorScreen(route: VaultRoute.Editor, vm: VaultViewModel, browse: VaultBrowseViewModel, onMessage: (String) -> Unit) {
    val draft by browse.draft.collectAsStateWithLifecycle()
    val issues by browse.issues.collectAsStateWithLifecycle()
    val saving by browse.saving.collectAsStateWithLifecycle()
    val groups by browse.groups.collectAsStateWithLifecycle()
    var discardPrompt by remember { mutableStateOf(false) }
    var generator by remember { mutableStateOf(false) }

    LaunchedEffect(route) {
        if (browse.draft.value == null) {
            if (route.id == null) browse.startNew(route.kind) else browse.startEdit(route.id)
        }
    }

    val leave = {
        if (browse.isDraftDirty()) {
            discardPrompt = true
        } else {
            browse.discardDraft()
            vm.pop()
        }
    }
    BackHandler { leave() }

    val d = draft
    Column(Modifier.fillMaxSize()) {
        VaultTopBar(stringResource(if (route.id == null) R.string.vault_new_entry else R.string.vault_edit_entry), onBack = leave) {
            TextButton(onClick = { browse.save { id -> vm.replaceTop(VaultRoute.Detail(id)) } }, enabled = d != null && !saving) {
                if (saving) CircularProgressIndicator(strokeWidth = 2.dp) else Text(stringResource(R.string.save))
            }
        }
        if (d == null) {
            VaultLoading(Modifier.weight(1f))
        } else {
            EditorForm(d, issues.associateBy({ it.fieldKey to it.field }, { it.message }), groups, browse, onGenerate = { generator = true })
        }
    }

    if (discardPrompt) {
        ConfirmDialog(
            title = stringResource(R.string.vault_discard_title),
            text = stringResource(R.string.vault_discard_text),
            confirmLabel = stringResource(R.string.vault_discard),
            onConfirm = {
                discardPrompt = false
                browse.discardDraft()
                vm.pop()
            },
            onDismiss = { discardPrompt = false },
        )
    }
    if (generator && d != null) {
        GeneratorSheet(
            browse = browse,
            onUse = { value ->
                browse.updateDraft { it.copy(password = value) }
                generator = false
            },
            onDismiss = { generator = false },
        )
    }
}

@Composable
private fun EditorForm(
    d: EntryDraft,
    errors: Map<Pair<Long?, DraftIssueField>, String>,
    groups: List<com.nightclip.app.core.VaultGroup>,
    browse: VaultBrowseViewModel,
    onGenerate: () -> Unit,
) {
    val template = KindTemplate.of(d.kind)
    var strength by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(d.password) {
        if (d.password.isEmpty()) {
            strength = null
        } else {
            delay(250)
            strength = browse.strengthLabel(d.password)
        }
    }
    fun err(field: DraftIssueField, key: Long? = null) = errors[key to field]

    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        if (d.id == null) {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                VaultTexts.KINDS.forEach { k ->
                    FilterChip(selected = d.kind == k, onClick = { browse.updateDraft { it.copy(kind = k) } }, label = { Text(VaultTexts.kindLabel(k)) })
                }
            }
        }
        OutlinedTextField(
            d.title, { v -> browse.updateDraft { it.copy(title = v) } },
            Modifier.fillMaxWidth(), singleLine = true, label = { Text(stringResource(R.string.vault_field_title)) },
            placeholder = { Text(template.titleHint) }, isError = err(DraftIssueField.TITLE) != null,
            supportingText = err(DraftIssueField.TITLE)?.let { { Text(it) } },
        )
        if (template.showUsername) {
            OutlinedTextField(
                d.username, { v -> browse.updateDraft { it.copy(username = v) } },
                Modifier.fillMaxWidth(), singleLine = true, label = { Text(stringResource(R.string.vault_field_username)) },
                keyboardOptions = KeyboardOptions(autoCorrectEnabled = false),
            )
        }
        if (template.showPassword) {
            SecretTextField(
                d.password, { v -> browse.updateDraft { it.copy(password = v) } }, template.passwordLabel,
                isError = err(DraftIssueField.PASSWORD) != null, supporting = err(DraftIssueField.PASSWORD),
            )
            StrengthBar(strength)
            if (template.canGenerate) OutlinedButton(onClick = onGenerate) { Text(stringResource(R.string.vault_generate)) }
        }
        if (template.showUrl) {
            OutlinedTextField(
                d.url, { v -> browse.updateDraft { it.copy(url = v) } },
                Modifier.fillMaxWidth(), singleLine = true, label = { Text(template.urlLabel) },
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, autoCorrectEnabled = false),
            )
        }
        if (template.showTotp) {
            SecretTextField(
                d.totp, { v -> browse.updateDraft { it.copy(totp = v) } }, stringResource(R.string.vault_field_totp_secret),
                isError = err(DraftIssueField.TOTP) != null, supporting = err(DraftIssueField.TOTP) ?: stringResource(R.string.vault_totp_hint),
            )
        }

        // custom fields
        Text(stringResource(R.string.vault_custom_fields), style = MaterialTheme.typography.titleSmall)
        d.fields.forEach { f ->
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                OutlinedTextField(
                    f.name, { v -> browse.updateDraft { it.updateField(f.key) { x -> x.copy(name = v) } } },
                    Modifier.fillMaxWidth(), singleLine = true, label = { Text(stringResource(R.string.vault_field_name)) },
                    isError = err(DraftIssueField.FIELD_NAME, f.key) != null, supportingText = err(DraftIssueField.FIELD_NAME, f.key)?.let { { Text(it) } },
                )
                if (f.protected) {
                    SecretTextField(
                        f.value, { v -> browse.updateDraft { it.updateField(f.key) { x -> x.copy(value = v) } } }, stringResource(R.string.vault_field_value),
                        isError = err(DraftIssueField.FIELD_VALUE, f.key) != null, supporting = err(DraftIssueField.FIELD_VALUE, f.key),
                    )
                } else {
                    OutlinedTextField(
                        f.value, { v -> browse.updateDraft { it.updateField(f.key) { x -> x.copy(value = v) } } },
                        Modifier.fillMaxWidth(), label = { Text(stringResource(R.string.vault_field_value)) },
                        isError = err(DraftIssueField.FIELD_VALUE, f.key) != null, supportingText = err(DraftIssueField.FIELD_VALUE, f.key)?.let { { Text(it) } },
                    )
                }
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Checkbox(checked = f.protected, onCheckedChange = { c -> browse.updateDraft { it.updateField(f.key) { x -> x.copy(protected = c) } } })
                    Text(stringResource(R.string.vault_field_protected), Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium)
                    TextButton(onClick = { browse.updateDraft { it.removeField(f.key) } }) { Text(stringResource(R.string.vault_field_remove)) }
                }
            }
        }
        err(DraftIssueField.TOO_MANY)?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
            OutlinedButton(onClick = { browse.updateDraft { it.withField("") } }) { Text(stringResource(R.string.vault_field_add)) }
        }
        val suggestions = KindTemplate.unusedSuggestions(d.kind, d)
        if (suggestions.isNotEmpty()) {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                suggestions.forEach { name ->
                    AssistChip(onClick = { browse.updateDraft { it.withField(name) } }, label = { Text(name) })
                }
            }
        }

        if (template.showNotes) {
            OutlinedTextField(
                d.notes, { v -> browse.updateDraft { it.copy(notes = v) } },
                Modifier.fillMaxWidth(), minLines = if (d.kind == VaultTexts.KIND_NOTE) 6 else 3, label = { Text(stringResource(R.string.vault_field_notes)) },
                isError = err(DraftIssueField.NOTES) != null, supportingText = err(DraftIssueField.NOTES)?.let { { Text(it) } },
            )
        }
        OutlinedTextField(
            d.tags, { v -> browse.updateDraft { it.copy(tags = v) } },
            Modifier.fillMaxWidth(), singleLine = true, label = { Text(stringResource(R.string.vault_field_tags)) },
            supportingText = { Text(stringResource(R.string.vault_tags_hint)) },
            keyboardOptions = KeyboardOptions(autoCorrectEnabled = false),
        )
        OutlinedTextField(
            d.expires, { v -> browse.updateDraft { it.copy(expires = v) } },
            Modifier.fillMaxWidth(), singleLine = true, label = { Text(stringResource(R.string.vault_field_expires)) },
            placeholder = { Text("2027-01-31") }, isError = err(DraftIssueField.EXPIRES) != null,
            supportingText = { Text(err(DraftIssueField.EXPIRES) ?: stringResource(R.string.vault_expires_hint)) },
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
        )
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            AssistChip(onClick = { browse.updateDraft { it.copy(expires = ExpiryDates.inDays(30)) } }, label = { Text(stringResource(R.string.vault_expires_30)) })
            AssistChip(onClick = { browse.updateDraft { it.copy(expires = ExpiryDates.inDays(90)) } }, label = { Text(stringResource(R.string.vault_expires_90)) })
            AssistChip(onClick = { browse.updateDraft { it.copy(expires = ExpiryDates.inDays(365)) } }, label = { Text(stringResource(R.string.vault_expires_365)) })
            if (d.expires.isNotEmpty()) AssistChip(onClick = { browse.updateDraft { it.copy(expires = "") } }, label = { Text(stringResource(R.string.vault_expires_none)) })
        }

        var groupMenu by remember { mutableStateOf(false) }
        val realGroups = groups.filter { !it.isTrash }
        if (realGroups.size > 1) {
            Column {
                Text(stringResource(R.string.vault_field_group), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                OutlinedButton(onClick = { groupMenu = true }) {
                    Text(realGroups.firstOrNull { it.id == d.groupId }?.path?.ifBlank { null } ?: stringResource(R.string.vault_group_root))
                }
                DropdownMenu(expanded = groupMenu, onDismissRequest = { groupMenu = false }) {
                    realGroups.forEach { g ->
                        DropdownMenuItem(text = { Text(g.path.ifBlank { stringResource(R.string.vault_group_root) }) }, onClick = {
                            groupMenu = false
                            browse.updateDraft { it.copy(groupId = g.id) }
                        })
                    }
                }
            }
        }
    }
}
