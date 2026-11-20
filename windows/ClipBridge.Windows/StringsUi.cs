namespace ClipBridge.Windows;

/// <summary>Strings of the Lumen-based Windows views (docs/ui-lumen-2026-11.md). Windows shell only.</summary>
public static partial class Strings
{
    // ---- shell ---------------------------------------------------------------------------------
    public const string NavGroupSystem = "系统";
    public const string ThemeToggleTip = "切换深色 / 浅色";
    public const string BtnLockVault = "锁定";
    public const string LockButtonTip = "立即锁定密码库（Ctrl+L）";
    public const string FooterLocked = "密码库已锁定";
    public const string FooterUnlocked = "密码库已解锁";
    public const string DevInstanceNoAutoStart = "开发实例（使用自定义数据目录）不会修改开机自启。";

    // ---- banners --------------------------------------------------------------------------------
    public const string UpdateAvailableTitle = "有新版本";
    public const string UpdateReadyTitle = "新版本已下载";
    public const string BannerDiagnose = "查看诊断";

    // ---- common buttons ---------------------------------------------------------------------------
    public const string BtnNext = "下一步";
    public const string BtnShowInFolder = "在文件夹中显示";
    public const string CopiedPlain = "已复制";

    // ---- diagnostics -----------------------------------------------------------------------------------
    public const string DiagIntro = "遇到问题时，把下面的报告复制给开发者。报告不含密码、密钥或条目内容。";
    public const string DiagCoreLabel = "核心";
    public const string DiagCoreRunning = "运行中";
    public const string DiagCoreStopped = "未启动";
    public const string DiagVaultLabel = "密码库";
    public const string DiagVaultLocked = "已锁定";
    public const string DiagVaultUnlocked = "已解锁";
    public const string DiagReportTitle = "诊断报告";
    public const string DiagReportHint = "版本、SSH 代理与更新状态。";

    // ---- settings -------------------------------------------------------------------------------------
    public const string SettingsAutoSaved = "更改会自动保存";
    public const string SettingsAppearanceTitle = "外观";
    public const string SettingsTheme = "主题";
    public const string SettingsThemeHint = "跟随系统时随 Windows 的深浅色切换。";
    public const string ThemeSystem = "跟随系统";
    public const string ThemeLight = "浅色";
    public const string ThemeDark = "深色";
    public const string SettingsAccent = "强调色";
    public const string SettingsGeneralTitle = "通知与启动";
    public const string SettingsAutoStartHint = "登录 Windows 后在托盘中启动。";
    public const string UpdateAutoHint = "有新版本时在后台下载，安装前会先询问。";
    public const string AboutVersionLabel = "版本";
    public const string AboutDeviceLabel = "设备名称";

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
    public const string BtnLeaveAnyway = "仍然离开";
    public const string BtnStay = "留在这里";
}
