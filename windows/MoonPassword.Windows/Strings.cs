namespace MoonPassword.Windows;

/// <summary>
/// Every user-facing string of the desktop shells (Simplified Chinese). Nothing else in the
/// code base carries UI literals, so an English table can be added later by swapping this class.
/// Pure C#: it is linked into the unit-test project and the macOS shell.
/// </summary>
public static partial class Strings
{
    public const string AppName = "MoonPassword";

    // ---- navigation / window --------------------------------------------------------
    public const string NavSettings = "设置";
    public const string NavDiagnostics = "诊断";
    public const string StatusStarting = "正在启动核心…";
    public const string StatusFailed = "核心启动失败";
    public const string StillRunningTitle = "MoonPassword 仍在运行";
    public const string StillRunningText = "窗口已隐藏到托盘，右键托盘图标可退出。";
    public const string CoreNotRunningBanner = "核心没有运行：密码库暂时不可用。请重启 MoonPassword；仍然不行的话，诊断页有日志文件夹。";

    // ---- tray -----------------------------------------------------------------------
    public const string TrayOpen = "打开 MoonPassword";
    public const string TrayAutoStart = "开机自启";
    public const string TrayCheckUpdate = "检查更新";
    public const string TrayExit = "退出";
    public const string TrayTipLocked = "MoonPassword：密码库已锁定";
    public const string TrayTipUnlocked = "MoonPassword：密码库已解锁";
    public const string TrayTipWarning = "MoonPassword：需要注意";

    // ---- clipboard ------------------------------------------------------------------
    public const string ApplyFailed = "无法写入剪贴板（可能被其他程序占用），请稍后重试。";

    // ---- platforms ------------------------------------------------------------------
    public const string PlatformWindows = "Windows";
    public const string PlatformAndroid = "Android";
    public const string PlatformMacOS = "macOS";

    // ---- common buttons -------------------------------------------------------------
    public const string BtnCopy = "复制";
    public const string BtnClear = "清空";
    public const string BtnReveal = "显示";
    public const string BtnHide = "隐藏";
    public const string BtnCancel = "取消";
    public const string BtnOpenFolder = "打开所在文件夹";
    public const string BtnChangeFolder = "更改…";
    public const string BtnBrowse = "浏览…";
    public const string BtnRefresh = "刷新";
    public const string BtnRename = "重命名";
    public const string BtnOk = "确定";
    public const string ConfirmTitle = "请确认";
    public const string FileMissing = "文件已不在原位置。";

    // ---- settings -------------------------------------------------------------------
    public const string SettingsTitle = "设置";
    public const string SettingsAutoStart = "开机自启（最小化到托盘）";
    public const string SettingsNotifications = "显示通知";
    public const string SettingsNotificationsHint = "关闭后不再弹出更新和 SSH 密钥使用的提示。";
    public const string SettingsUpdateTitle = "更新";
    public const string SettingsAboutTitle = "关于";
    public static string AboutVersion(string v) => $"版本：{v}";
    public const string UpdateMandatorySuffix = "（必须更新）";
    public const string UpdateAuto = "自动下载更新";
    public const string UpdateChecking = "正在检查更新…";
    public const string UpdateBannerTitle = "有新版本";
    public static string UpdateBannerAvailable(string v) => $"新版本 {v} 可用。";
    public static string UpdateBannerReady(string v) => $"新版本 {v} 已下载，可以重启安装。";
    public const string UpdatePrepareFailedTitle = "无法安装更新";
    public static string UpdatePrepareFailed(string reason) => $"无法准备更新：\n\n{reason}";
    public static string UpdateApplyFailed(string reason) => $"MoonPassword 无法安装更新，已保留当前版本。\n\n{reason}";
    public const string UpdateApplyFailedTitle = "MoonPassword 更新";
    public const string UpdateButtonCheck = "检查更新";
    public const string UpdateButtonDownload = "下载";
    public const string UpdateButtonInstall = "重启并更新";
    public const string UpdateDevBuild = "开发版本：自动更新已关闭";
    public const string UpdateNotConfigured = "尚未配置更新地址";
    public static string UpdateUpToDate(string v) => $"已是最新版本（{v}）";
    public static string UpdateAvailable(string v, bool required) => $"发现新版本 {v}{(required ? "（必须更新）" : "")}";
    public static string UpdateDownloading(string v, string pct) => $"正在下载 {v}… {pct}";
    public static string UpdateReady(string v, bool required) => $"版本 {v} 已就绪，可以安装{(required ? "（必须更新）" : "")}";
    public static string UpdateError(string reason) => $"更新失败：{reason}";
    public static string UpdateCurrent(string v) => $"当前版本 {v}";
    public static string UpdateStatusUnavailable(string reason) => $"无法读取更新状态：{reason}";
    public static string DurationSeconds(int s) => $"{s} 秒";
    public static string DurationMinutes(int m) => $"{m} 分钟";
    public static string DurationHours(int h) => $"{h} 小时";
    public static string DurationHoursMinutes(int h, int m) => $"{h} 小时 {m} 分钟";
    public const string LastSeenNever = "从未";
    public const string LastSeenNow = "刚刚";
    public static string LastSeenMinutes(int n) => $"{n} 分钟前";
    public static string LastSeenHours(int n) => $"{n} 小时前";
    public static string LastSeenDays(int n) => $"{n} 天前";

    // ---- diagnostics ----------------------------------------------------------------
    public const string DiagTitle = "诊断";
    public const string DiagCopy = "复制诊断信息";
    public const string DiagCopied = "诊断信息已复制";
    public const string DiagOpenLogs = "打开日志文件夹";
    public const string DiagOpenData = "打开数据文件夹";
    public const string DiagUnavailable = "暂时无法读取诊断信息";

    // ---- errors ---------------------------------------------------------------------
    public const string ErrorGeneric = "出错了";
    public static string ErrorUnknown(string code) => $"发生未知错误（{code}）";
    public static string ErrorWithDetail(string message, string detail) => string.IsNullOrWhiteSpace(detail) ? message : $"{message}\n{detail}";
    public const string ErrExpired = "已过期";
    public const string ErrHashMismatch = "校验失败";
    public const string ErrTooLarge = "内容太大";
    public const string ErrNoSpace = "磁盘空间不足";
    public const string ErrRateLimited = "操作太频繁，请稍后再试";
    public const string ErrInvalid = "参数无效";
    public const string ErrDbUnavailable = "本地数据库无法打开，数据可能已损坏或来自更新的版本";
    public const string ErrKeyInvalid = "数据库密钥与现有数据不匹配，无法解锁本机数据";
    public const string ErrNotInitialized = "核心尚未启动";
    public const string ErrMethodNotFound = "核心不支持这个操作（版本不匹配？）";
    public const string ErrUpdateDisabled = "自动更新未启用";
    public const string ErrUpdateFailed = "无法访问更新服务器，请稍后重试";
    public const string ErrUpdateRejected = "更新包的签名或格式不受信任，已拒绝";
    public const string ErrInternal = "核心内部错误";
    public const string ErrInitFailed = "核心初始化失败";
    public const string ErrNotFound = "找不到对应的内容";
    public const string ErrLimitReached = "已达到上限";

    // ---- startup failures -----------------------------------------------------------
    public const string StartupFailedTitle = "MoonPassword 无法启动";
    public const string KeyCorruptTitle = "数据库密钥文件已损坏";
    public static string KeyCorruptText(string detail) =>
        "为了不让已有的加密数据永久丢失，MoonPassword 不会自动重建密钥。\n请从备份恢复 devkey.dpapi，或在确认不需要旧数据后手动删除数据文件夹。\n\n" + detail;
    public const string CoreMissingText = "找不到核心库，请重新安装或重新解压完整的安装包。";
    public const string KeyMigrationWarning = "数据库密钥尚未迁移到 DPAPI，已暂时继续使用旧的密钥文件。";

    // ---- misc -----------------------------------------------------------------------
    public const string Copy = "复制";
    public const string Close = "关闭";
    public const string Unknown = "未知";
    public const string Dash = "—";
}
