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
    public const string SendText = "发送文字";
    public const string SendTextPrompt = "输入要发送给所有设备的文字";
    public const string ModeLabel = "收到内容时";
}
