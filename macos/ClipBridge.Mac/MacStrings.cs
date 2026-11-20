namespace ClipBridge.Mac;

/// <summary>
/// Texts that differ on macOS. Everything else comes from the Windows shell's Strings (linked as-is),
/// so a wording change there reaches both desktops.
/// </summary>
internal static class MacStrings
{
    public const string WindowTitle = "MoonPassword";
    public const string AutoStartFailed = "无法设置登录时启动（请从“应用程序”文件夹里的 MoonPassword 启动后再试）。";
    public const string UpdateNeedsBundle = "开发版不能自动安装更新，请手动下载安装包。";
    public const string MenuShow = "打开 MoonPassword";
    public const string MenuCheckUpdate = "检查更新";
    public const string MenuQuit = "退出 MoonPassword";
    public const string SettingsAutoStart = "登录 macOS 时启动";
    public const string SettingsOpenLogs = "打开日志文件夹";
    public const string KeyStoreFailedTitle = "无法读取数据库密钥";

    // ---- password vault ---------------------------------------------------------------
    public const string VaultPreventCaptureNote =
        "使用 macOS 的窗口共享设置：截屏和录屏里这些窗口会是空白。较新的 macOS 上部分录屏或投屏工具可能不受此限制。";
    public const string AttachOpen = "打开";
    public const string FileTypeKdbx = "KDBX 文件";
    public const string FileTypeCsv = "CSV 文件";
    public const string FileTypeAll = "所有文件";
}
