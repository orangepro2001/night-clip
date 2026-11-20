namespace ClipBridge.Windows;

/// <summary>Strings of the Lumen-based Windows views (docs/ui-lumen-2026-11.md). Windows shell only.</summary>
public static partial class Strings
{
    // ---- shell ---------------------------------------------------------------------------------
    public const string NavGroupSync = "同步";
    public const string NavGroupTools = "工具";
    public const string NavGroupSystem = "系统";
    public const string BtnPairNewShort = "配对新设备";
    public const string PairButtonTip = "显示二维码，让另一台设备加入";
    public const string ThemeToggleTip = "切换深色 / 浅色";
    public const string SyncChipTip = "复制同步的状态；点击可暂停或关闭";
    public const string SyncChipOn = "同步已开启";
    public const string SyncChipOff = "同步已关闭";
    public static string SyncChipPaused(string time) => $"已暂停至 {time}";
    public const string SyncResumed = "已恢复同步";
    public const string SyncTurnOff = "关闭自动同步";
    public const string SyncTurnOn = "开启自动同步";
    public static string SyncPauseFor(string label) => $"暂停 {label}";
    public static string SyncPausedToast(string label) => $"同步已暂停 {label}";
    public const string FooterWarning = "有需要处理的问题";
    public const string FooterNoPeers = "还没有配对的设备";
    public static string FooterOnline(int online, int total) => $"{online} / {total} 台设备在线";
    public static string ShellSubtitle(string fingerprint) => $"指纹 {fingerprint}";
    public const string DevInstanceNoAutoStart = "开发实例（使用自定义数据目录）不会修改开机自启。";

    // ---- banners --------------------------------------------------------------------------------
    public const string UpdateAvailableTitle = "有新版本";
    public const string UpdateReadyTitle = "新版本已下载";
    public const string BannerDiagnose = "查看诊断";

    // ---- common buttons ---------------------------------------------------------------------------
    public const string BtnNext = "下一步";
    public const string BtnOpenAnyway = "仍然打开";
    public const string BtnShowInFolder = "在文件夹中显示";
    public const string BtnCopyDeviceId = "复制设备 ID";
    public const string CopiedPlain = "已复制";

    // ---- devices ----------------------------------------------------------------------------------
    public const string DevicesIntro = "复制的文字会自动发到在线设备；也可以在这里手动发送文字、剪贴板或文件。";
    public const string BtnSendFilesShort = "发送文件";
    public const string BtnJoinPairShort = "加入配对";
    public const string ComposerTitle = "快速发送";
    public const string ComposerPlaceholder = "输入要发送的文字…（Ctrl+Enter 发送）";
    public const string ComposerSend = "发送";
    public const string ComposerAllOnline = "所有在线设备";
    public static string ComposerHintAll(int online) => online == 0 ? "当前没有在线设备" : $"将发给 {online} 台在线设备";
    public const string ComposerHintOne = "离线时会排队，上线后送达";
    /// <summary>Every success text of <c>SendTextAsync</c> starts with this (see <see cref="SentTo"/>).</summary>
    public const string SentPrefix = "已发送";
    public const string SendTextEmpty = "请输入要发送的文字。";
    public const string DevicesEmptyTitle = "还没有配对的设备";
    public const string DevicesEmptyText = "在另一台设备上打开 ClipBridge，扫描本机的二维码；或者用“加入配对”输入对方的链接。";
    public static string DeviceCount(int total, int online) => $"共 {total} 台，{online} 台在线";
    public const string TipSendText = "发送文字…";
    public const string TipSendFiles = "发送文件…";
    public const string DeviceSettingsTip = "设备设置";
    public const string DeviceSettingsTitle = "设备设置";
    public const string RepairNowShort = "重新配对";
    public static string ReceivePolicyShort(string mode) => $"收到时：{mode}";
    public const string AutoSendOffShort = "不自动发送";
    public const string Reconnecting = "正在重新连接…";
    public const string ForgotDone = "已删除记录";
    public const string UnpairedDone = "已解除配对";
    public const string DeviceSaved = "设备设置已保存";
    public const string LabelName = "名称";
    public const string LabelReceivePolicy = "收到这台设备的内容时";
    public static string ModeHint(string mode) => mode switch
    {
        "auto" => "直接写入本机剪贴板（你刚复制过别的内容时改为通知）。",
        "manual" => "放进收件箱并通知，点“复制”才写入剪贴板。",
        _ => "只显示通知，内容留在收件箱里。",
    };

    // ---- inbox ------------------------------------------------------------------------------------
    public const string InboxFilterAll = "全部";
    public const string InboxFilterNew = "未处理";
    public const string InboxFilterCopied = "已复制";
    public const string InboxSearchPlaceholder = "搜索内容或来源";
    public static string InboxSummary(int total, int unhandled, int shown) =>
        shown == total ? $"共 {total} 条，{unhandled} 条未处理" : $"显示 {shown} / {total} 条，{unhandled} 条未处理";
    public const string InboxEmptyTitle = "收件箱是空的";
    public const string InboxNoMatch = "没有符合条件的内容";
    public const string InboxNoMatchHint = "换个关键词或筛选条件试试。";
    public const string InboxViewAll = "查看全文";
    public static string CharCount(int n) => $"{n} 个字符";
    public const string InboxClearTitle = "清空收件箱";
    public const string InboxCleared = "收件箱已清空";

    // ---- transfers -----------------------------------------------------------------------------------
    public const string TransfersFilterAll = "全部";
    public const string TransfersFilterActive = "进行中";
    public const string TransfersFilterDone = "已结束";
    public const string TransfersOpenDownloads = "打开接收文件夹";
    public const string TransfersEmptyTitle = "还没有文件传输";
    public const string TransfersNoMatch = "这里没有传输";
    public const string TransfersEmptyHint = "在“设备”页把文件拖到某台设备上，或点“发送文件”。收到的文件也会出现在这里。";

    // ---- diagnostics -----------------------------------------------------------------------------------
    public const string DiagIntro = "遇到连接问题时，把下面的报告复制给开发者。报告不含剪贴板内容和密钥。";
    public const string NoTailnetTitle = "没有检测到 Tailscale";
    public const string DiagCoreLabel = "核心";
    public const string DiagCoreRunning = "运行中";
    public const string DiagCoreStopped = "未启动";
    public const string DiagPeersLabel = "在线设备";
    public const string DiagPeersCaption = "在线 / 已配对";
    public const string DiagWarningsLabel = "警告";
    public const string DiagNoWarnings = "一切正常";
    public const string DiagReportTitle = "诊断报告";
    public const string DiagReportHint = "网络、监听端口、对端连接与最近的错误。";

    // ---- settings -------------------------------------------------------------------------------------
    public const string SettingsAutoSaved = "更改会自动保存";
    public const string SettingsAppearanceTitle = "外观";
    public const string SettingsTheme = "主题";
    public const string SettingsThemeHint = "跟随系统时随 Windows 的深浅色切换。";
    public const string ThemeSystem = "跟随系统";
    public const string ThemeLight = "浅色";
    public const string ThemeDark = "深色";
    public const string SettingsAccent = "强调色";
    public const string SettingsSyncTitle = "同步";
    public const string SettingsSendSensitiveHint = "关闭时，看起来像口令或令牌的内容只留在本机。";
    public const string SettingsTtlLabel = "离线内容有效期（秒）";
    public const string SettingsTtlHint = "30–86400";
    public const string SettingsHistoryLabel = "收件箱保留条数";
    public const string SettingsHistoryHint = "10–5000";
    public const string ValidationTtlShort = "请输入 30 到 86400 之间的整数。";
    public const string ValidationHistoryShort = "请输入 10 到 5000 之间的整数。";
    public const string SettingsGeneralTitle = "通知与启动";
    public const string SettingsAutoStartHint = "登录 Windows 后在托盘中启动。";
    public const string SettingsFilesTitle = "文件";
    public const string SettingsDownloadDirHint = "收到的文件默认保存在这里；图片会先预览，由你决定是否保存。";
    public const string UpdateAutoHint = "有新版本时在后台下载，安装前会先询问。";
    public const string AboutVersionLabel = "版本";
    public const string AboutDeviceIdLabel = "设备 ID";
    public const string AboutFingerprintLabel = "证书指纹";

    // ---- pairing ---------------------------------------------------------------------------------------
    public const string PairJoinInstead = "改为加入对方的配对";
    public const string PairClipboardNoText = "剪贴板里没有文字。";
    public const string PairSasWaitingShort = "请在对方设备上核对数字并点“允许”…";
    public const string PairConfirming = "正在确认…";

    // ---- files ------------------------------------------------------------------------------------------
    public const string FileOfferSaveToLabel = "保存到";
    public const string FileOfferImageHint = "图片会先打开预览，由你选择复制或保存。";

    // ---- vault ----------------------------------------------------------------------------------------
    public const string ErrorTitle = "出错了";
    public const string FileTypeKdbx = "KeePass 数据库 (*.kdbx)";
    public const string FileTypeCsv = "CSV 文件 (*.csv)";
    public const string FileTypeAll = "所有文件";
    public const string DetailEmptyTitle = "选择一个条目";
    public const string InTrashLabel = "在回收站中";
    public const string BtnDiscard = "放弃更改";
    public const string FieldNamePlaceholder = "字段名";
    public const string BtnDeletePermanentShort = "永久删除";
    public const string GroupNameInvalid = "分组名不能为空，且不能超过 128 字节。";
    public const string BackupChoose = "请先在列表中选择一个备份。";
    public const string ImportFileLabel = "文件";

    // ---- notes ----------------------------------------------------------------------------------------
    public const string NoteEditorEmptyTitle = "没有打开的笔记";
    public const string NoteLeaveTitle = "笔记还没有保存";
    public const string BtnLeaveAnyway = "仍然离开";
    public const string BtnStay = "留在这里";
    public const string NoteImportTypeName = "笔记文件 (*.zip, *.md, *.markdown, *.txt)";
    public const string BtnCleanup = "清理";
}
