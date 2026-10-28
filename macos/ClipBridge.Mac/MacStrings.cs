namespace ClipBridge.Mac;

/// <summary>
/// Texts that differ on macOS. Everything else comes from the Windows shell's Strings (linked as-is),
/// so a wording change there reaches both desktops.
/// </summary>
internal static class MacStrings
{
    public const string WindowTitle = "ClipBridge";
    public const string ReceivedManualHint = "打开 ClipBridge 的收件箱，点“复制”写入剪贴板。";
    public const string ReceivedNotifyHint = "在收件箱里可以复制。";
    public const string AutoStartFailed = "无法设置登录时启动（请从“应用程序”文件夹里的 ClipBridge 启动后再试）。";
    public const string UpdateNeedsBundle = "开发版不能自动安装更新，请手动下载安装包。";
    public const string MenuShow = "打开 ClipBridge";
    public const string MenuSendClipboard = "发送剪贴板";
    public const string MenuSync = "复制后自动发送";
    public const string MenuPair = "配对新设备…";
    public const string MenuJoin = "加入配对…";
    public const string MenuCheckUpdate = "检查更新";
    public const string MenuQuit = "退出 ClipBridge";
    public const string SettingsAutoStart = "登录 macOS 时启动";
    public const string SettingsOpenLogs = "打开日志文件夹";
    public const string SettingsChooseFolder = "选择…";
    public const string KeyStoreFailedTitle = "无法读取数据库密钥";
    public const string DropHere = "把文件拖到设备上即可发送";
    public const string ModeLabel = "收到内容时";

    // ---- password vault ---------------------------------------------------------------
    public const string VaultPreventCaptureNote =
        "使用 macOS 的窗口共享设置：截屏和录屏里这些窗口会是空白。较新的 macOS 上部分录屏或投屏工具可能不受此限制。";
    public const string AttachOpen = "打开";
    public const string FileTypeKdbx = "KDBX 文件";
    public const string FileTypeCsv = "CSV 文件";
    public const string FileTypeAll = "所有文件";
    public const string VaultSyncTitle = "与其他设备同步";
    public const string VaultSyncIntro = "密码库以加密文件在已配对的设备之间同步，每台设备都是完整副本，修改会自动合并。";
    public const string VaultSyncNone = "还没有已配对的设备。";
    public const string VaultSyncEnabled = "同步";
    public const string VaultSyncNow = "立即同步";
    public const string VaultSyncStarted = "已开始同步";
    public const string VaultSyncEnterPassword = "输入对方库的密码…";
    public const string VaultSyncPasswordPrompt = "对方设备上的密码库改过主密码。输入那个库现在的主密码，才能合并它的修改。";
    public const string VaultSyncApprove = "批准合并…";
    public const string VaultSyncApproveConfirm =
        "对方发来的库文件需要你批准：它可能不是同一个库，或者这次合并会删除或覆盖较多条目。合并前程序会先拍一张快照，可以在“备份与恢复”里撤销。确定合并吗？";
    public const string VaultSyncResolved = "已提交，正在合并";
    public static string VaultSyncLastOk(string time) => $"上次成功：{time}";

    public static string VaultSyncState(string state, bool connected) => state switch
    {
        "idle" => connected ? "正常" : "未连接",
        "pending_unlock" => "已收到对方的修改，解锁后合并",
        "needs_password" => "对方改了主密码，需要输入新密码才能合并",
        "review" => "需要你批准后才会合并",
        "peer_off" => "对方关闭了密码库同步",
        "error" => "同步出错",
        _ => state,
    };
}
