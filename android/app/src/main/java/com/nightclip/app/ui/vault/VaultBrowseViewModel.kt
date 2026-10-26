package com.nightclip.app.ui.vault

import android.app.Application
import android.net.Uri
import androidx.lifecycle.viewModelScope
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.GeneratedSecret
import com.nightclip.app.core.TotpCode
import com.nightclip.app.core.VaultApi
import com.nightclip.app.core.VaultEntryDetail
import com.nightclip.app.core.VaultEntrySummary
import com.nightclip.app.core.VaultGroup
import com.nightclip.app.core.VaultHealth
import com.nightclip.app.core.VaultHistoryVersion
import com.nightclip.app.logic.AttachmentNames
import com.nightclip.app.logic.EntryDraft
import com.nightclip.app.logic.EntryValidator
import com.nightclip.app.logic.DraftIssue
import com.nightclip.app.logic.GeneratorOptions
import com.nightclip.app.logic.MasterPasswordRules
import com.nightclip.app.logic.VaultErrors
import com.nightclip.app.logic.VaultFilter
import com.nightclip.app.logic.VaultListLogic
import com.nightclip.app.logic.VaultTexts
import com.nightclip.app.platform.VaultClipboard
import com.nightclip.app.platform.VaultFiles
import com.nightclip.app.platform.VaultSession
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.FlowPreview
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.debounce
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** The entry list, the detail screen, the editor and the health report. Secrets only live in [draft] while the editor is open. */
@OptIn(FlowPreview::class, ExperimentalCoroutinesApi::class)
class VaultBrowseViewModel(application: Application) : VaultBaseViewModel(application) {
    private val _query = MutableStateFlow("")
    val query: StateFlow<String> = _query.asStateFlow()

    private val _filter = MutableStateFlow(VaultFilter.ALL)
    val filter: StateFlow<VaultFilter> = _filter.asStateFlow()

    /** null = every group. */
    private val _groupFilter = MutableStateFlow<String?>(null)
    val groupFilter: StateFlow<String?> = _groupFilter.asStateFlow()

    private val _groups = MutableStateFlow<List<VaultGroup>>(emptyList())
    val groups: StateFlow<List<VaultGroup>> = _groups.asStateFlow()

    private val _entries = MutableStateFlow<List<VaultEntrySummary>>(emptyList())
    val entries: StateFlow<List<VaultEntrySummary>> = _entries.asStateFlow()

    private val _loading = MutableStateFlow(false)
    val loading: StateFlow<Boolean> = _loading.asStateFlow()

    private val _detail = MutableStateFlow<VaultEntryDetail?>(null)
    val detail: StateFlow<VaultEntryDetail?> = _detail.asStateFlow()

    private val _history = MutableStateFlow<List<VaultHistoryVersion>>(emptyList())
    val history: StateFlow<List<VaultHistoryVersion>> = _history.asStateFlow()

    private val _draft = MutableStateFlow<EntryDraft?>(null)
    val draft: StateFlow<EntryDraft?> = _draft.asStateFlow()
    private var original: EntryDraft? = null
    private var originalGroup = ""

    private val _issues = MutableStateFlow<List<DraftIssue>>(emptyList())
    val issues: StateFlow<List<DraftIssue>> = _issues.asStateFlow()

    private val _saving = MutableStateFlow(false)
    val saving: StateFlow<Boolean> = _saving.asStateFlow()

    private val _health = MutableStateFlow<VaultHealth?>(null)
    val health: StateFlow<VaultHealth?> = _health.asStateFlow()

    private val reload = MutableStateFlow(0)

    init {
        viewModelScope.launch {
            combine(_query, _filter, _groupFilter, reload, VaultSession.dataVersion) { q, f, g, _, _ -> Triple(q, f, g) }
                .debounce(180)
                .collectLatest { (q, f, g) -> if (VaultSession.unlocked.value) load(q, f, g) }
        }
    }

    override fun wipe() {
        _query.value = ""
        _filter.value = VaultFilter.ALL
        _groupFilter.value = null
        _groups.value = emptyList()
        _entries.value = emptyList()
        _detail.value = null
        _history.value = emptyList()
        _draft.value = null
        original = null
        _issues.value = emptyList()
        _health.value = null
    }

    // ---- list ----------------------------------------------------------------------------------

    fun setQuery(q: String) {
        _query.value = q
    }

    fun setFilter(f: VaultFilter) {
        _filter.value = f
    }

    fun setGroupFilter(id: String?) {
        _groupFilter.value = id
    }

    /** Re-reads the list (screen shown, entry saved …). */
    fun reloadList() {
        reload.update { it + 1 }
    }

    private suspend fun load(q: String, f: VaultFilter, g: String?) {
        _loading.value = true
        try {
            when (val r = VaultApi.list(q, g, VaultListLogic.coreKind(f), VaultListLogic.coreTrashOnly(f))) {
                is ApiResult.Ok -> _entries.value = VaultListLogic.apply(r.value, f, System.currentTimeMillis() / 1000)
                is ApiResult.Fail -> fail(r)
            }
            (VaultApi.groups() as? ApiResult.Ok)?.let { _groups.value = it.value }
        } finally {
            _loading.value = false
        }
    }

    fun addGroup(name: String, parentId: String = "") {
        val n = name.trim()
        if (n.isEmpty()) return
        viewModelScope.launch {
            when (val r = VaultApi.groupAdd(parentId, n)) {
                is ApiResult.Ok -> reloadList()
                is ApiResult.Fail -> fail(r)
            }
        }
    }

    // ---- detail --------------------------------------------------------------------------------

    fun openDetail(id: String) {
        if (_detail.value?.id != id) _detail.value = null
        viewModelScope.launch {
            when (val r = VaultApi.get(id)) {
                is ApiResult.Ok -> _detail.value = r.value
                is ApiResult.Fail -> fail(r)
            }
        }
    }

    /** For on-screen display only; the screen drops the value after 15 seconds or when it is left. */
    suspend fun reveal(id: String, field: String): String? = when (val r = VaultApi.reveal(id, field)) {
        is ApiResult.Ok -> r.value
        is ApiResult.Fail -> {
            fail(r)
            null
        }
    }

    suspend fun totp(id: String): TotpCode? = when (val r = VaultApi.totp(id)) {
        is ApiResult.Ok -> r.value
        is ApiResult.Fail -> {
            fail(r)
            null
        }
    }

    /** vault.copy, then the marked write to the clipboard and the clear timer. */
    fun copy(id: String, field: String) {
        viewModelScope.launch {
            when (val r = VaultApi.copy(id, field)) {
                is ApiResult.Ok -> {
                    val ok = VaultClipboard.copy(app, r.value.value, r.value.clearAfterS)
                    say(if (ok) VaultTexts.copiedClears(r.value.clearAfterS) else VaultTexts.COPY_FAILED)
                }
                is ApiResult.Fail -> fail(r)
            }
        }
    }

    fun deleteEntry(id: String, onDone: () -> Unit) {
        viewModelScope.launch {
            when (val r = VaultApi.delete(id)) {
                is ApiResult.Ok -> {
                    say(if (r.value) VaultTexts.DELETED_FOREVER else VaultTexts.DELETED_TO_TRASH)
                    reloadList()
                    onDone()
                }
                is ApiResult.Fail -> fail(r)
            }
        }
    }

    fun restoreEntry(id: String, onDone: () -> Unit) {
        viewModelScope.launch {
            when (val r = VaultApi.restoreEntry(id)) {
                is ApiResult.Ok -> {
                    say(VaultTexts.RESTORED)
                    reloadList()
                    onDone()
                }
                is ApiResult.Fail -> fail(r)
            }
        }
    }

    fun emptyTrash() {
        viewModelScope.launch {
            when (val r = VaultApi.emptyTrash()) {
                is ApiResult.Ok -> {
                    say("已清空回收站（${r.value} 条）")
                    reloadList()
                }
                is ApiResult.Fail -> fail(r)
            }
        }
    }

    fun moveEntry(id: String, groupId: String) {
        viewModelScope.launch {
            when (val r = VaultApi.move(id, groupId)) {
                is ApiResult.Ok -> {
                    reloadList()
                    openDetail(id)
                }
                is ApiResult.Fail -> fail(r)
            }
        }
    }

    // ---- history -------------------------------------------------------------------------------

    fun loadHistory(id: String) {
        viewModelScope.launch {
            when (val r = VaultApi.history(id)) {
                is ApiResult.Ok -> _history.value = r.value
                is ApiResult.Fail -> fail(r)
            }
        }
    }

    fun restoreVersion(id: String, index: Int) {
        viewModelScope.launch {
            when (val r = VaultApi.historyRestore(id, index)) {
                is ApiResult.Ok -> {
                    say("已恢复到旧版本，当前版本进入历史")
                    reloadList()
                    openDetail(id)
                    loadHistory(id)
                }
                is ApiResult.Fail -> fail(r)
            }
        }
    }

    // ---- attachments ---------------------------------------------------------------------------

    fun addAttachment(id: String, uri: Uri) {
        viewModelScope.launch {
            val name = AttachmentNames.sanitize(VaultFiles.displayName(app, uri))
            when (val c = VaultFiles.copyToTemp(app, uri, MasterPasswordRules.MAX_ATTACHMENT_BYTES, "att")) {
                VaultFiles.Copied.TooLarge -> say(VaultTexts.ATTACHMENT_TOO_BIG)
                VaultFiles.Copied.Failed -> say(VaultTexts.FILE_READ_FAILED)
                is VaultFiles.Copied.Ok -> try {
                    when (val r = VaultApi.attachAdd(id, name, c.file.absolutePath)) {
                        is ApiResult.Ok -> {
                            say("已添加附件“$name”")
                            openDetail(id)
                        }
                        is ApiResult.Fail -> fail(r)
                    }
                } finally {
                    VaultFiles.wipeAndDelete(c.file)
                }
            }
        }
    }

    /** Writes the attachment to the SAF document [target] through a temp file that is deleted afterwards. */
    fun saveAttachment(id: String, name: String, target: Uri) {
        viewModelScope.launch {
            val temp = VaultFiles.newTempPath(app, "att")
            try {
                when (val r = VaultApi.attachGet(id, name, temp.absolutePath)) {
                    is ApiResult.Ok -> if (VaultFiles.copyToUri(app, temp, target)) {
                        say("已保存附件“$name”")
                    } else {
                        VaultFiles.deleteDocument(app, target)
                        say(VaultTexts.FILE_WRITE_FAILED)
                    }
                    is ApiResult.Fail -> {
                        VaultFiles.deleteDocument(app, target)
                        fail(r)
                    }
                }
            } finally {
                VaultFiles.wipeAndDelete(temp)
            }
        }
    }

    fun removeAttachment(id: String, name: String) {
        viewModelScope.launch {
            when (val r = VaultApi.attachRemove(id, name)) {
                is ApiResult.Ok -> openDetail(id)
                is ApiResult.Fail -> fail(r)
            }
        }
    }

    // ---- editor --------------------------------------------------------------------------------

    fun startNew(kind: String) {
        val d = EntryDraft.blank(kind, _groupFilter.value.orEmpty())
        _draft.value = d
        original = d
        _issues.value = emptyList()
    }

    /** Loads the entry and reveals its secrets into the draft (they are dropped by [discardDraft] / a lock). */
    fun startEdit(id: String) {
        viewModelScope.launch {
            val detail = (VaultApi.get(id) as? ApiResult.Ok)?.value ?: run {
                say(VaultErrors.message("VAULT_NOT_FOUND"))
                return@launch
            }
            val password = if (detail.hasPassword) VaultApi.reveal(id, "password").okOrNull.orEmpty() else ""
            val totp = if (detail.hasTotp) VaultApi.reveal(id, "totp_secret").okOrNull.orEmpty() else ""
            val protectedValues = HashMap<String, String>()
            for (f in detail.fields) if (f.protected && f.hasValue) protectedValues[f.name] = VaultApi.reveal(id, "field:${f.name}").okOrNull.orEmpty()
            val d = EntryDraft.fromDetail(detail, password, totp, protectedValues)
            originalGroup = detail.groupId
            _draft.value = d
            original = d
            _issues.value = emptyList()
        }
    }

    fun updateDraft(transform: (EntryDraft) -> EntryDraft) {
        _draft.update { it?.let(transform) }
    }

    fun isDraftDirty(): Boolean {
        val o = original ?: return false
        val d = _draft.value ?: return false
        return EntryValidator.isDirty(o, d)
    }

    fun discardDraft() {
        _draft.value = null
        original = null
        _issues.value = emptyList()
    }

    /** Validates, then adds or updates; [onSaved] gets the entry id. */
    fun save(onSaved: (String) -> Unit) {
        val d = _draft.value ?: return
        if (_saving.value) return
        val problems = EntryValidator.validate(d)
        _issues.value = problems
        if (problems.isNotEmpty()) return
        viewModelScope.launch {
            _saving.value = true
            try {
                val id = d.id
                if (id == null) {
                    when (val r = VaultApi.add(d)) {
                        is ApiResult.Ok -> finishSave(r.value, onSaved)
                        is ApiResult.Fail -> fail(r)
                    }
                } else {
                    when (val r = VaultApi.update(id, d)) {
                        is ApiResult.Ok -> {
                            if (originalGroup != d.groupId) VaultApi.move(id, d.groupId)
                            finishSave(id, onSaved)
                        }
                        is ApiResult.Fail -> fail(r)
                    }
                }
            } finally {
                _saving.value = false
            }
        }
    }

    private fun finishSave(id: String, onSaved: (String) -> Unit) {
        say(VaultTexts.SAVED)
        discardDraft()
        reloadList()
        openDetail(id)
        onSaved(id)
    }

    // ---- tools ---------------------------------------------------------------------------------

    suspend fun generate(options: GeneratorOptions): GeneratedSecret? = when (val r = VaultApi.generate(options)) {
        is ApiResult.Ok -> r.value
        is ApiResult.Fail -> {
            say(VaultErrors.message(r.code, r.retryAfterS))
            null
        }
    }

    suspend fun strengthLabel(password: String): String? = VaultApi.strength(password).okOrNull?.label

    fun loadHealth() {
        viewModelScope.launch {
            when (val r = VaultApi.health()) {
                is ApiResult.Ok -> _health.value = r.value
                is ApiResult.Fail -> fail(r)
            }
        }
    }
}
