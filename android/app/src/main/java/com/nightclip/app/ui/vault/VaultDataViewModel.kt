package com.nightclip.app.ui.vault

import android.app.Application
import android.net.Uri
import androidx.lifecycle.viewModelScope
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.BackupCheck
import com.nightclip.app.core.MergeStats
import com.nightclip.app.core.RestorePreview
import com.nightclip.app.core.VaultApi
import com.nightclip.app.core.VaultBackup
import com.nightclip.app.core.VaultCreds
import com.nightclip.app.logic.BackupLogic
import com.nightclip.app.logic.VaultErrors
import com.nightclip.app.logic.VaultTexts
import com.nightclip.app.platform.VaultFiles
import com.nightclip.app.platform.VaultSession
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** What the user wants to export, kept between pressing the button and the system file picker answering. */
sealed interface PendingExport {
    class Kdbx(val confirm: VaultCreds, val exportCreds: VaultCreds, val kdf: String?) : PendingExport
    class Csv(val confirm: VaultCreds) : PendingExport
}

/** Result of a restore preview: the diff, plus the credentials of an older backup, held until the restore ran or the screen closed. */
class PreviewState(val backup: VaultBackup, val preview: RestorePreview, val creds: VaultCreds?) {
    override fun toString() = "PreviewState(${backup.name})"
}

/** Backups and restore, import and export. Passwords are held only while a call or a file picker is in flight. */
class VaultDataViewModel(application: Application) : VaultBaseViewModel(application) {
    private val _backups = MutableStateFlow<List<VaultBackup>>(emptyList())
    val backups: StateFlow<List<VaultBackup>> = _backups.asStateFlow()

    private val _busy = MutableStateFlow(false)
    val busy: StateFlow<Boolean> = _busy.asStateFlow()

    private val _verified = MutableStateFlow<Pair<String, BackupCheck>?>(null)
    val verified: StateFlow<Pair<String, BackupCheck>?> = _verified.asStateFlow()

    private val _preview = MutableStateFlow<PreviewState?>(null)
    val preview: StateFlow<PreviewState?> = _preview.asStateFlow()

    /** Text of the last import / restore result, shown in a dialog. */
    private val _result = MutableStateFlow<String?>(null)
    val result: StateFlow<String?> = _result.asStateFlow()

    private var pendingExport: PendingExport? = null
    private var pendingImport: Uri? = null

    override fun wipe() {
        pendingExport = null
        pendingImport = null
        _preview.value = null
        _verified.value = null
        _result.value = null
        _backups.value = emptyList()
    }

    // ---- backups -------------------------------------------------------------------------------

    fun loadBackups() {
        viewModelScope.launch {
            when (val r = VaultApi.backupList()) {
                is ApiResult.Ok -> _backups.value = r.value
                is ApiResult.Fail -> fail(r)
            }
        }
    }

    fun backupNow() {
        if (_busy.value) return
        viewModelScope.launch {
            _busy.value = true
            try {
                when (val r = VaultApi.backupNow()) {
                    is ApiResult.Ok -> {
                        say(VaultTexts.BACKUP_DONE)
                        loadBackups()
                    }
                    is ApiResult.Fail -> fail(r)
                }
            } finally {
                _busy.value = false
            }
        }
    }

    fun verify(backup: VaultBackup, creds: VaultCreds) {
        if (_busy.value) return
        viewModelScope.launch {
            _busy.value = true
            try {
                when (val r = VaultApi.backupVerify(backup.name, creds)) {
                    is ApiResult.Ok -> _verified.value = backup.name to r.value
                    is ApiResult.Fail -> fail(r, VaultErrors.Context.BACKUP)
                }
            } finally {
                _busy.value = false
            }
        }
    }

    fun clearVerified() {
        _verified.value = null
    }

    /** [creds] null = the vault's current credentials. */
    fun loadPreview(backup: VaultBackup, creds: VaultCreds?) {
        if (_busy.value) return
        viewModelScope.launch {
            _busy.value = true
            try {
                when (val r = VaultApi.restorePreview(backup.name, creds)) {
                    is ApiResult.Ok -> _preview.value = PreviewState(backup, r.value, creds)
                    is ApiResult.Fail -> fail(r, VaultErrors.Context.BACKUP)
                }
            } finally {
                _busy.value = false
            }
        }
    }

    fun closePreview() {
        _preview.value = null
    }

    fun restore(mode: String, selectedIds: Set<String>) {
        val p = _preview.value ?: return
        if (_busy.value) return
        val ids = BackupLogic.cleanSelection(p.preview, selectedIds)
        if (!BackupLogic.canRestore(mode, ids.size)) return
        viewModelScope.launch {
            _busy.value = true
            try {
                when (val r = VaultApi.restore(p.backup.name, mode, if (mode == VaultTexts.RESTORE_ENTRIES) ids else emptyList(), p.creds)) {
                    is ApiResult.Ok -> {
                        _preview.value = null
                        _result.value = "恢复完成：" + VaultTexts.mergeStatsText(r.value) + "。\n恢复前已自动备份，可以在备份列表里撤销。"
                        VaultSession.dataChanged()
                        loadBackups()
                    }
                    is ApiResult.Fail -> fail(r, VaultErrors.Context.BACKUP)
                }
            } finally {
                _busy.value = false
            }
        }
    }

    fun dismissResult() {
        _result.value = null
    }

    // ---- import --------------------------------------------------------------------------------

    /** The user picked a KDBX file; the password is asked next. */
    fun stageImport(uri: Uri) {
        pendingImport = uri
    }

    fun stagedImport(): Uri? = pendingImport

    fun cancelImport() {
        pendingImport = null
    }

    fun importKdbx(creds: VaultCreds) {
        val uri = pendingImport ?: return
        pendingImport = null
        runImport(uri, "kdbx") { path -> VaultApi.importFile(path, creds).let { r -> r.map { VaultTexts.mergeStatsText(it) } } }
    }

    fun importCsv(uri: Uri) {
        runImport(uri, "csv") { path ->
            VaultApi.importCsv(path).map { "新增 ${it.added} 条，跳过 ${it.skipped} 条" }
        }
    }

    private fun runImport(uri: Uri, extension: String, call: suspend (String) -> ApiResult<String>) {
        if (_busy.value) return
        viewModelScope.launch {
            _busy.value = true
            try {
                when (val c = VaultFiles.copyToTemp(app, uri, 64L shl 20, extension)) {
                    VaultFiles.Copied.TooLarge -> say("文件太大")
                    VaultFiles.Copied.Failed -> say(VaultTexts.FILE_READ_FAILED)
                    is VaultFiles.Copied.Ok -> try {
                        when (val r = call(c.file.absolutePath)) {
                            is ApiResult.Ok -> {
                                _result.value = "导入完成：${r.value}。\n导入前已自动备份。"
                                VaultSession.dataChanged()
                                loadBackups()
                            }
                            is ApiResult.Fail -> fail(r, VaultErrors.Context.IMPORT)
                        }
                    } finally {
                        VaultFiles.wipeAndDelete(c.file)
                    }
                }
            } finally {
                _busy.value = false
            }
        }
    }

    // ---- export --------------------------------------------------------------------------------

    /** Called when the user pressed "choose where to save"; the picker's answer comes to [finishExport]. */
    fun prepareExport(p: PendingExport) {
        pendingExport = p
    }

    /** [target] null = the picker was cancelled. */
    fun finishExport(target: Uri?) {
        val p = pendingExport
        pendingExport = null
        if (p == null || target == null) return
        if (_busy.value) return
        viewModelScope.launch {
            _busy.value = true
            val temp = VaultFiles.newTempPath(app, if (p is PendingExport.Csv) "csv" else "kdbx")
            try {
                val r = when (p) {
                    is PendingExport.Kdbx -> VaultApi.exportFile(temp.absolutePath, p.confirm, p.exportCreds, p.kdf)
                    is PendingExport.Csv -> VaultApi.exportCsv(temp.absolutePath, p.confirm)
                }
                when (r) {
                    is ApiResult.Ok -> if (VaultFiles.copyToUri(app, temp, target)) {
                        say("已导出")
                        _result.value = if (p is PendingExport.Csv) {
                            "已导出为明文 CSV。里面的密码没有加密，用完请立即删除这个文件。"
                        } else {
                            "已导出加密副本。请记住导出时设置的密码，并把文件放在安全的地方。"
                        }
                        loadBackups()
                    } else {
                        VaultFiles.deleteDocument(app, target)
                        say(VaultTexts.FILE_WRITE_FAILED)
                    }
                    is ApiResult.Fail -> {
                        VaultFiles.deleteDocument(app, target) // the picker already created an empty file
                        fail(r, VaultErrors.Context.EXPORT)
                    }
                }
            } finally {
                VaultFiles.wipeAndDelete(temp)
                _busy.value = false
            }
        }
    }

    fun cancelExport() {
        pendingExport = null
    }
}

private inline fun <T, R> ApiResult<T>.map(f: (T) -> R): ApiResult<R> = when (this) {
    is ApiResult.Ok -> ApiResult.Ok(f(value))
    is ApiResult.Fail -> this
}
