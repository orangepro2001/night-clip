namespace ClipBridge.Windows;

/// <summary>User-facing text of the password vault page and its dialogs (Simplified Chinese). Pure C#, linked into the tests.</summary>
public static partial class Strings
{
    // ---- navigation / tray ------------------------------------------------------------
    public const string NavVault = "密码库";
    public const string TrayLockVault = "锁定密码库";

    // ---- common labels ----------------------------------------------------------------
    public const string Untitled = "（无标题）";
    public const string LabelTitle = "标题";
    public const string LabelUsername = "用户名";
    public const string LabelPassword = "密码";
    public const string LabelUrl = "网址";
    public const string LabelServiceUrl = "服务 / 接口地址";
    public const string LabelApiKeyValue = "密钥（API Key / Token）";
    public const string LabelSecretValue = "机密内容";
    public const string LabelNotes = "备注";
    public const string LabelTotp = "一次性验证码（TOTP）";
    public const string LabelTags = "标签（用逗号分隔）";
    public const string LabelTagsShort = "标签";
    public const string LabelExpiry = "到期日期";
    public const string LabelGroup = "分组";
    public const string LabelKind = "类型";
    public const string LabelCustomFields = "自定义字段";
    public const string LabelMasterPassword = "主密码";
    public const string LabelConfirmPassword = "再输入一次";
    public const string LabelKeyFile = "密钥文件（可选）";
    public const string LabelKdf = "加密强度";
    public const string KindLogin = "网站登录";
    public const string KindApiKey = "API 密钥";
    public const string KindSecret = "单个机密";
    public const string KindNote = "安全笔记";

    // ---- strength ---------------------------------------------------------------------
    public const string StrengthVeryWeak = "非常弱";
    public const string StrengthWeak = "弱";
    public const string StrengthFair = "一般";
    public const string StrengthGood = "较强";
    public const string StrengthStrong = "很强";
    public static string StrengthBits(int bits) => $"约 {bits} 位熵";

    // ---- key file ---------------------------------------------------------------------
    public const string BtnPickKeyFile = "选择密钥文件…";
    public const string BtnNoKeyFile = "不使用";
    public const string KeyFileNone = "未选择密钥文件";
    public const string KeyFileEmpty = "密钥文件是空的。";
    public const string KeyFileTooBig = "密钥文件太大（上限 1 MiB）。";
    public const string KeyFileMissing = "找不到这个密钥文件。";
    public const string KeyFileUnreadable = "无法读取这个密钥文件。";
    public const string KeyFileRequiredNote = "如果选择了密钥文件，以后每次解锁都需要同一个文件；丢失它同样会让密码库永远无法打开。";
    public static string KeyFileChosen(string name) => $"密钥文件：{name}";

    // ---- locked screen ----------------------------------------------------------------
    public const string LockedTitle = "密码库已锁定";
    public const string LockedHint = "输入主密码解锁。主密码只在这台电脑上用来解开加密文件，不会发送给任何设备。";
    public const string BtnUnlock = "解锁";
    public const string Unlocking = "正在解锁…（密钥派生可能需要几秒钟）";
    public static string UnlockWait(int seconds) => $"请等待 {seconds} 秒";
    public const string LockedIdle = "密码库因长时间没有操作已自动锁定。";
    public const string LockedSession = "因为锁屏、睡眠或切换用户，密码库已自动锁定。";
    public const string LockedOther = "密码库已锁定。";
    public const string UnlockSlowDevice = "这台电脑解锁较慢。可以在“设置”里改用较低的加密强度（更改主密码时选择）。";

    // ---- create screen ----------------------------------------------------------------
    public const string CreateTitle = "创建密码库";
    public const string CreateIntro = "密码库保存在这台电脑上，使用标准的 KDBX 4 格式加密，KeePassXC 等软件也能打开。";
    public const string CreateWarningTitle = "请务必记住主密码";
    public const string CreateWarningText =
        "主密码不会保存在任何地方，也没有“找回密码”功能。\n" +
        "如果忘记主密码（以及你选用的密钥文件），密码库里的所有内容将永远无法打开，开发者和本程序都帮不上忙。\n" +
        "建议创建后立刻导出一份加密备份，并把主密码记在安全的地方。";
    public const string CreateEmptyPassword = "请输入密码。";
    public const string CreateMismatch = "两次输入的密码不一致。";
    public const string CreateWeakWarning = "这个密码比较弱，仍然可以使用，但建议换一个更长、更随机的密码。";
    public const string CreateAdvanced = "高级选项";
    public const string KdfLow = "低（32 MiB，适合很旧的电脑）";
    public const string KdfStandard = "标准（64 MiB，推荐）";
    public const string KdfHigh = "高（128 MiB，更抗破解但更慢）";
    public const string BtnCreateVault = "创建密码库";
    public const string Creating = "正在创建…";
    public const string CreateDoneTitle = "密码库已创建";
    public const string CreateBackupPrompt =
        "现在导出一份加密备份吧：万一这台电脑出问题，凭备份文件和主密码仍能恢复全部内容。\n备份文件用你另外设定的密码加密。";
    public const string BtnBackupExportNow = "现在导出备份…";
    public const string BtnSkipForNow = "暂时跳过";

    // ---- main page --------------------------------------------------------------------
    public const string BtnNewEntry = "新建";
    public const string BtnEditEntry = "编辑";
    public const string BtnDeleteEntry = "删除";
    public const string BtnDeletePermanent = "永久删除";
    public const string BtnViewEntry = "查看";
    public const string BtnRestoreEntry = "还原";
    public const string BtnEmptyTrash = "清空回收站";
    public const string BtnLockNow = "立即锁定";
    public const string BtnHealth = "健康报告";
    public const string BtnBackups = "备份与恢复";
    public const string BtnMore = "更多 ▾";
    public const string BtnImport = "导入…";
    public const string BtnExport = "导出…";
    public const string BtnChangeMaster = "更改主密码…";
    public const string BtnVaultSettings = "密码库设置…";
    public const string FilterAll = "全部";
    public const string FilterLogins = "网站登录";
    public const string FilterApiKeys = "API 密钥";
    public const string FilterSecrets = "机密";
    public const string FilterNotes = "笔记";
    public const string FilterExpiring = "即将到期";
    public const string FilterTrash = "回收站";
    public const string GroupsTitle = "分组";
    public const string GroupRoot = "（顶层）";
    public const string BtnNewGroup = "新建分组";
    public const string BtnRenameGroup = "重命名";
    public const string BtnDeleteGroup = "删除分组";
    public const string GroupNamePrompt = "分组名称：";
    public const string GroupDeleteHint = "只能删除空分组。请先把里面的条目删除（进入回收站）。";
    public const string SearchHint = "搜索标题、用户名、网址、备注…";
    public static string VaultCount(int n) => $"{n} 项";
    public const string VaultEmpty = "这里还没有条目。点击“新建”添加第一个。";
    public const string VaultNoMatches = "没有找到匹配的条目。";
    public const string VaultTrashEmpty = "回收站是空的。";
    public const string BadgeTotp = "验证码";
    public const string BadgeExpired = "已过期";
    public const string BadgeExpiring = "即将到期";
    public const string VaultLoading = "正在读取密码库…";
    public const string VaultNotRunning = "核心还没有启动。";

    // ---- detail pane ------------------------------------------------------------------
    public const string DetailEmpty = "在左边选择一个条目查看详情";
    public const string BtnOpenUrl = "打开";
    public const string BtnCopyField = "复制";
    public const string TotpTitle = "一次性验证码";
    public const string TotpPaused = "已暂停刷新（一段时间没有操作）。动一下鼠标继续。";
    public static string TotpSeconds(int s) => $"{s} 秒";
    public const string LabelCreated = "创建时间";
    public const string LabelModified = "修改时间";
    public const string LabelExpiresAt = "到期时间";
    public const string LabelStrength = "密码强度";
    public const string HistoryTitle = "历史版本";
    public const string HistoryNone = "还没有历史版本。";
    public const string BtnRestoreVersion = "恢复到此版本";
    public const string HistoryRestoreConfirm = "把这个旧版本恢复为当前内容？当前内容会保存进历史。";
    public const string HistoryRestored = "已恢复到旧版本";
    public const string AttachmentsTitle = "附件";
    public const string AttachmentsNone = "没有附件";
    public const string BtnAddAttachment = "添加附件…";
    public const string BtnSaveAttachment = "另存为…";
    public const string BtnRemoveAttachment = "移除";
    public const string AttachTooBig = "附件太大（单个文件上限 5 MiB）。";
    public const string AttachRemoveConfirm = "从这个条目移除附件？";
    public const string AttachSaved = "附件已保存";
    public const string AttachExists = "那里已经有同名文件，为了安全不会覆盖。已换成新的文件名重试。";
    public const string UrlNotOpened = "只能打开 http 或 https 开头的网址。";
    public static string Copied(string label, int clearSeconds) =>
        clearSeconds > 0 ? $"已复制{label}，{clearSeconds} 秒后自动清除剪贴板" : $"已复制{label}（不会自动清除）";
    public const string ClipboardClearedByTimer = "剪贴板上的机密已清除";
    public const string ClipboardCopyFailed = "无法写入剪贴板（可能被其他程序占用），请稍后重试。";

    // ---- entry actions ----------------------------------------------------------------
    public const string DeleteToTrashConfirm = "把这个条目移入回收站？之后还可以从回收站还原。";
    public const string DeletePermanentConfirm = "永久删除这个条目？这无法撤销（只有备份里还留有旧文件）。";
    public static string EmptyTrashConfirm(int n) => $"永久删除回收站里的 {n} 个条目？这无法撤销。";
    public const string TrashAlreadyEmpty = "回收站已经是空的。";
    public static string TrashEmptied(int n) => $"已永久删除 {n} 个条目";
    public const string EntryMovedToTrash = "已移入回收站";
    public const string EntryDeleted = "已永久删除";
    public const string EntryRestored = "已还原";
    public const string EntrySaved = "已保存";
    public const string EntryChoose = "请先选择一个条目。";

    // ---- editor -----------------------------------------------------------------------
    public const string EditorNewTitle = "新建条目";
    public const string EditorEditTitle = "编辑条目";
    public const string BtnSave = "保存";
    public const string BtnGenerate = "生成…";
    public const string BtnShowPassword = "显示";
    public const string BtnHidePassword = "隐藏";
    public const string BtnAddField = "添加字段";
    public const string BtnRemove = "移除";
    public const string FieldName = "名称";
    public const string FieldValue = "值";
    public const string FieldProtected = "保护（遮盖）";
    public const string FieldSuggestionHint = "常用字段：";
    public const string EditorLoading = "正在读取条目…";
    public const string DiscardChangesTitle = "放弃修改？";
    public const string DiscardChangesText = "这个条目有尚未保存的修改，确定放弃吗？";
    public const string ValTitleRequired = "请填写标题。";
    public static string ValTooLarge(string label) => $"“{label}”太长（单项上限 256 KiB）。";
    public const string ValTooMany = "字段或标签太多（各最多 100 个）。";
    public static string ValFieldName(string name) => $"自定义字段名“{name}”无效：不能为空、不能超过 128 字节，也不能叫 Title / UserName / Password / URL / Notes / otp。";
    public static string ValFieldDuplicate(string name) => $"自定义字段“{name}”重复了。";
    public const string ValUrlNotWeb = "这个网址不是 http 或 https 开头，将无法用“打开”按钮打开（仍可保存）。";
    public const string ValTotpFormat = "验证码密钥看起来不像 otpauth:// 链接或 Base32 密钥（仍可保存）。";

    // ---- generator --------------------------------------------------------------------
    public const string GeneratorTitle = "生成密码";
    public const string GenModePassword = "随机密码";
    public const string GenModePassphrase = "口令短语";
    public static string GenLength(int n) => $"长度：{n}";
    public const string GenLower = "小写字母";
    public const string GenUpper = "大写字母";
    public const string GenDigits = "数字";
    public const string GenSymbols = "符号";
    public const string GenAmbiguous = "避免易混淆字符（0 O o 1 l I |）";
    public static string GenWords(int n) => $"词数：{n}";
    public const string GenSeparator = "分隔符";
    public const string GenCapitalize = "首字母大写";
    public const string GenAddNumber = "加一个数字";
    public const string BtnRegenerate = "重新生成";
    public const string BtnUsePassword = "使用";

    // ---- unlock/create/shared messages ------------------------------------------------
    public const string ErrVaultLocked = "密码库已锁定，请先解锁。";
    public const string ErrVaultWrongCredentials = "主密码或密钥文件不对，或文件已损坏。可以从备份恢复。";
    public const string ErrVaultNotFound = "找不到对应的条目、分组或备份。";
    public const string ErrVaultExists = "密码库或目标文件已经存在（为了安全不会覆盖，请换一个文件名）。";
    public const string ErrVaultUnsupported = "这个库的加密参数不受支持或不安全。";
    public const string ErrVaultBroken = "密码库状态异常。请先锁定，再重新解锁。";
    public static string ErrVaultRateLimited(int seconds) => $"错误次数过多，请 {seconds} 秒后再试。";
    public const string ErrVaultTooLarge = "内容太大，超过了允许的上限。";
    public const string ErrVaultInvalid = "输入无效，请检查后重试。";
    public const string ErrVaultUnexpected = "密码库操作失败。";

    // ---- health -----------------------------------------------------------------------
    public const string HealthTitle = "密码健康报告";
    public const string HealthNote = "所有检查都只在本机完成，不联网，也不会查询泄露库。回收站和笔记不计入。";
    public const string HealthWeak = "弱密码";
    public const string HealthWeakHint = "容易被猜到或穷举，建议换成生成器产生的密码。";
    public const string HealthReused = "重复使用的密码";
    public const string HealthReusedHint = "一个网站泄露后，其他用同一密码的账号都有风险。";
    public const string HealthOld = "很久没有修改";
    public const string HealthOldHint = "条目超过一年没有修改，考虑更新一下。";
    public const string HealthExpired = "已过期";
    public const string HealthExpiredHint = "设置了到期日期并且已经过了。";
    public static string HealthSharedBy(int n) => $"{n} 个条目共用";
    public static string HealthAllGood(int total) => $"检查了 {total} 个条目，没有发现问题。";
    public static string HealthSummary(int total, int weak, int reused, int old, int expired) =>
        $"检查了 {total} 个条目：弱密码 {weak}，重复 {reused}，久未修改 {old}，已过期 {expired}。双击一行跳到对应条目。";
    public static string HealthWithoutTotp(int n) => $"另有 {n} 个网站登录没有设置一次性验证码。";

    // ---- backups ----------------------------------------------------------------------
    public const string BackupsTitle = "备份与恢复";
    public const string BackupsIntro =
        "每次保存、删除、导入、恢复或改主密码之前，程序都会自动拍一张加密快照（保留最近 20 张，加上每天、每周各一张）。快照用拍摄当时的主密码加密。";
    public const string BackupsRestoreNote = "恢复之前程序会先给当前库拍一张快照，所以恢复本身也可以撤销（在列表里选“恢复前”的那一张再恢复一次）。";
    public const string BackupsNone = "还没有备份。";
    public const string BtnBackupNow = "立即备份";
    public const string BtnBackupVerify = "验证";
    public const string BtnBackupPreview = "预览差异";
    public const string ColTime = "时间";
    public const string ColReason = "原因";
    public const string ColSize = "大小";
    public const string BackupCreated = "已创建备份";
    public const string BackupPreviewPrompt = "预览会用当前的主密码打开这份备份。如果它是用别的主密码做的（比如改主密码之前），请在下面填那个密码；留空就是用当前的。";
    public const string BackupOtherPassword = "这份备份的主密码（留空 = 当前主密码）";
    public const string RestoreNothingSelected = "请至少勾选一个条目。";
    public const string BackupVerifyTitle = "验证备份";
    public const string BackupVerifyPrompt = "验证需要这份备份拍摄时用的主密码。没改过主密码的话就是现在的主密码；改过的话请填旧的。";
    public static string BackupVerified(int entries, int groups, int trash, string time) =>
        $"可以恢复：{entries} 个条目，{groups} 个分组，回收站 {trash} 个，最近修改 {time}。";
    public const string ReasonSave = "保存之前";
    public const string ReasonDelete = "删除之前";
    public const string ReasonImport = "导入之前";
    public const string ReasonRestore = "恢复之前";
    public const string ReasonRekey = "更改主密码之前";
    public const string ReasonMerge = "合并之前";
    public const string ReasonCreate = "创建库时";
    public const string ReasonUpgrade = "升级格式之前";
    public const string ReasonManual = "手动备份";
    public const string ReasonPrevious = "上一代文件";
    public const string ReasonExport = "导出时";
    public const string RestorePreviewTitle = "恢复预览";
    public const string RestoreChooseMode = "恢复方式";
    public const string RestoreModeEntries = "只恢复选中的条目";
    public const string RestoreModeMerge = "合并进当前库";
    public const string RestoreModeReplace = "整库替换";
    public const string RestoreEntriesHint = "把勾选的条目恢复成备份里的样子；当前版本进入历史，已经不存在的会重新建出来。";
    public const string RestoreMergeHint = "按合并规则把备份并入当前库：谁改得新就用谁，另一份进历史，不会删除现有条目。";
    public const string RestoreReplaceHint = "整个库换成备份的内容。只在当前库里的条目会消失（它们仍在恢复前的快照里）。";
    public const string RestoreReplaceWord = "替换";
    public static string RestoreReplaceConfirm => $"整库替换会让当前库变成备份的内容。\n请输入“{RestoreReplaceWord}”确认：";
    public const string BtnRestore = "恢复";
    public const string DiffOnlyInBackup = "只在备份里（可以找回）";
    public const string DiffOnlyInBackupHint = "当前库里没有这些条目。";
    public const string DiffChanged = "内容不同";
    public const string DiffChangedHint = "两边都有，但内容不一样；这里显示的是备份里的版本。";
    public const string DiffOnlyInCurrent = "只在当前库里";
    public const string DiffOnlyInCurrentHint = "整库替换时这些条目会消失；其他方式不会动它们。";
    public static string DiffSummary(int onlyBackup, int changed, int onlyCurrent, int same) =>
        $"只在备份里 {onlyBackup} 个，内容不同 {changed} 个，只在当前库 {onlyCurrent} 个，完全相同 {same} 个。";
    public const string DiffNothing = "这份备份和当前库没有差别。";
    public const string RestoreDone = "恢复完成";
    public static string StatsLine(int added, int updated, int removed, int moved, int groupsAdded, int groupsRemoved) =>
        $"新增 {added}，更新 {updated}，删除 {removed}，移动 {moved}；分组新增 {groupsAdded}，删除 {groupsRemoved}。";

    // ---- import / export --------------------------------------------------------------
    public const string ImportTitle = "导入";
    public const string ImportKdbx = "KDBX 文件（KeePassXC / KeePass）";
    public const string ImportCsv = "CSV 文件（浏览器、Bitwarden、1Password、LastPass…）";
    public const string ImportKdbxPassword = "这个文件的主密码";
    public const string ImportChooseFile = "选择文件…";
    public const string ImportNoFile = "还没有选择文件";
    public const string ImportCsvNote = "CSV 是明文文件。导入完成后建议把它安全删除。";
    public const string BtnImportNow = "导入";
    public static string ImportedCsv(int added, int skipped) => $"已导入 {added} 个条目，跳过 {skipped} 行。";
    public const string ImportedKdbx = "导入完成";
    public const string ExportTitle = "导出";
    public const string ExportKdbx = "加密的 KDBX 文件（推荐）";
    public const string ExportCsv = "CSV 文件（明文，不安全）";
    public const string ExportNeedMaster = "请输入当前的主密码。";
    public const string ExportMasterPrompt = "当前主密码（再次确认）";
    public const string ExportFilePassword = "导出文件的新密码";
    public const string ExportFilePasswordNote = "导出文件用这个密码加密，建议和主密码不同。需要时可再选一个密钥文件。";
    public const string ExportCsvWarningTitle = "警告：CSV 导出是明文";
    public const string ExportCsvWarning =
        "CSV 文件里的所有密码、API 密钥和笔记都是没有加密的明文。\n" +
        "任何能拿到这个文件的人（包括云盘、备份软件、恶意程序）都能直接看到全部内容。\n" +
        "只在必须迁移到别的软件时使用，导出后请立刻导入并安全删除这个文件。";
    public const string ExportCsvAreYouSure = "真的要导出没有加密的 CSV 文件吗？";
    public const string ExportCsvConfirmWord = "明文";
    public static string ExportCsvConfirmPrompt => $"我明白文件是明文的。请输入“{ExportCsvConfirmWord}”确认：";
    public const string ExportCsvNotConfirmed = "请输入确认字样。";
    public const string BtnExportNow = "导出…";
    public const string ExportDone = "导出完成";
    public static string ExportDoneAt(string path) => $"已导出到 {path}";
    public const string FilterKdbx = "KDBX 文件 (*.kdbx)|*.kdbx|所有文件 (*.*)|*.*";
    public const string FilterCsv = "CSV 文件 (*.csv)|*.csv|所有文件 (*.*)|*.*";
    public const string FilterAnyFile = "所有文件 (*.*)|*.*";

    // ---- change master ----------------------------------------------------------------
    public const string ChangeMasterTitle = "更改主密码";
    public const string ChangeCurrent = "当前主密码";
    public const string ChangeNew = "新的主密码";
    public const string ChangeNeedCurrent = "请输入当前的主密码。";
    public const string ChangeNote =
        "整个库会用新的主密码重新加密。注意：已有的旧备份仍然需要旧的主密码才能打开，请把旧密码保留到你确认不再需要旧备份为止。";
    public const string ChangeDone = "主密码已更改";
    public const string BtnChange = "更改";

    // ---- settings ---------------------------------------------------------------------
    public const string VaultSettingsTitle = "密码库设置";
    public const string VaultAutolockLabel = "无操作多少秒后自动锁定";
    public static string VaultAutolockInvalid(int min, int max) => $"自动锁定时间需要是 {min} 到 {max} 之间的整数（秒）。";
    public const string VaultClipClearLabel = "复制的机密多少秒后清除剪贴板（0 = 不清除）";
    public static string VaultClipClearInvalid(int min, int max) => $"清除时间需要是 0，或 {min} 到 {max} 之间的整数（秒）。";
    public const string VaultPreventCapture = "防止截屏和录屏（密码库页面显示期间）";
    public const string VaultPreventCaptureNote = "使用 Windows 的“排除捕获”功能；在较旧的系统上可能显示为黑块或不生效。";
    public const string VaultSettingsSaved = "已保存";
    public static string VaultKdfInfo(string kdf) => $"当前加密：{kdf}";

    // ---- generic dialog bits ----------------------------------------------------------
    public const string BtnConfirm = "确认";
    public const string BtnYes = "是";
    public const string BtnNo = "否";
    public const string AskPasswordTitle = "需要密码";
    public const string Working = "请稍候…";
    public const string VaultLockedToast = "密码库已锁定";
    public const string PreventCaptureUnavailable = "这台电脑不支持防截屏功能，密码库页面仍然可能被截屏。";
}
