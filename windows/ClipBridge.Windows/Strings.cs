namespace ClipBridge.Windows;

/// <summary>
/// Every user-facing string of the Windows shell (Simplified Chinese). Nothing else in the
/// code base carries UI literals, so an English table can be added later by swapping this class.
/// Pure C#: it is linked into the unit-test project.
/// </summary>
public static partial class Strings
{
    public const string AppName = "ClipBridge";

    // ---- navigation / window --------------------------------------------------------
    public const string NavDevices = "设备";
    public const string NavInbox = "收件箱";
    public const string NavTransfers = "传输";
    public const string NavSettings = "设置";
    public const string NavDiagnostics = "诊断";
    public const string StatusStarting = "正在启动核心…";
    public const string StatusFailed = "核心启动失败";
    public const string StillRunningTitle = "ClipBridge 仍在运行";
    public const string StillRunningText = "窗口已隐藏到托盘，右键托盘图标可退出。";

    // ---- tray -----------------------------------------------------------------------
    public const string TrayOpen = "打开 ClipBridge";
    public const string TrayPair = "配对新设备…";
    public const string TraySync = "同步我复制的内容";
    public const string TraySendNow = "立即发送当前剪贴板";
    public const string TrayPause = "暂停同步";
    public const string TrayPause15 = "暂停 15 分钟";
    public const string TrayPause60 = "暂停 1 小时";
    public const string TrayPause240 = "暂停 4 小时";
    public static string TrayResume(string until) => "恢复同步（已暂停到 " + until + "）";
    public const string TrayUndo = "撤销上次自动写入";
    public const string DeviceAutoSend = "自动发送";
    public const string DeviceAutoSendTip = "复制时自动把内容发到这台设备；关闭后仍可手动发送";
    public const string AutoSyncOnTitle = "ClipBridge 自动同步已开启";
    public const string AutoSyncOnText = "在这台电脑上复制的文字会自动发到所有已配对的设备。右键托盘图标可以暂停或关闭。";
    public const string UndoDone = "已恢复到自动写入之前的内容";
    public const string TrayAutoStart = "开机自启";
    public const string TrayCheckUpdate = "检查更新";
    public const string TrayExit = "退出";
    public const string TrayTipNormal = "ClipBridge：运行正常";
    public const string TrayTipNoPeers = "ClipBridge：尚未配对设备";
    public const string TrayTipNoConnection = "ClipBridge：没有已连接的设备";
    public const string TrayTipWarning = "ClipBridge：需要注意";
    public static string TrayTipConnected(int connected, int total) => $"ClipBridge：已连接 {connected}/{total} 台设备";

    // ---- clipboard receive ----------------------------------------------------------
    public static string ReceivedTitle(string from) => $"来自 {from} 的剪贴板";
    public const string ReceivedSensitive = "敏感内容（已隐藏）";
    public const string ReceivedManualHint = "点击此通知可写入剪贴板";
    public const string ReceivedDowngraded = "你刚刚又复制了别的内容，未覆盖剪贴板。可在收件箱中复制。";
    public const string ReceivedNotifyHint = "已收到，可在收件箱中复制。";
    public const string AppliedToast = "已写入剪贴板";
    public const string ApplyFailed = "无法写入剪贴板（可能被其他程序占用），请稍后重试。";
    public const string SendNowDone = "已发送当前剪贴板内容";
    public const string SendNowNothing = "剪贴板里没有可发送的文本";
    public const string SendNowNoPeers = "没有已配对的设备";
    public const string TooLargeToSend = "剪贴板文本太大（超过 64 KiB），请作为文件发送。";
    public const string SensitiveBlocked = "内容看起来像口令或令牌，已按设置阻止发送。";
    public const string NotSentTitle = "未发送";

    // ---- pairing --------------------------------------------------------------------
    public const string PairListenTitle = "配对新设备";
    public const string PairListenIntro = "在另一台设备上打开 ClipBridge，扫描二维码或粘贴下面的链接。";
    public const string PairCopyLink = "复制链接";
    public const string PairLinkCopied = "链接已复制";
    public const string PairQrUnavailable = "无法生成二维码，请使用下面的链接。";
    public const string PairStarting = "正在生成配对码…";
    public static string PairExpiresIn(string mmss) => $"有效期剩余 {mmss}";
    public const string PairExpired = "配对码已过期，请关闭后重新生成";
    public const string PairWaitingForPeer = "等待对方加入…";
    public const string PairClose = "关闭";
    public const string PairJoinTitle = "加入配对";
    public const string PairJoinIntro = "粘贴对方显示的配对链接，或手动填写地址信息。";
    public const string PairTabLink = "粘贴链接";
    public const string PairTabManual = "手动输入";
    public const string PairLinkLabel = "配对链接";
    public const string PairHostLabel = "地址（Tailscale IP）";
    public const string PairPortLabel = "端口";
    public const string PairPskLabel = "一次性密钥";
    public const string PairFpLabel = "指纹（可选）";
    public const string PairStartJoin = "开始配对";
    public const string PairJoining = "正在连接对方…";
    public const string PairPasteFromClipboard = "从剪贴板粘贴";
    public const string PairSasPrompt = "请核对对方设备上显示的数字是否一致";
    public const string PairSasWaiting = "核对一致后，请在对方设备上点“允许”。";
    public const string PairSuccess = "配对成功";
    public static string PairSuccessWith(string name) => $"已与 {name} 配对";
    public const string PairRequestTitle = "配对请求";
    public static string PairRequestText(string name, string platform) => $"{name}（{platform}）请求与本机配对";
    public const string PairRequestSasPrompt = "请核对对方设备上显示的数字是否一致：";
    public const string PairAllow = "允许";
    public const string PairDeny = "拒绝";
    public const string PairRequestBalloonTitle = "配对请求";
    public static string PairRequestBalloon(string name) => $"{name} 请求配对，请核对数字后确认。";
    public const string PairRejected = "对方拒绝了配对";
    public const string PairFailed = "配对失败";
    public const string PairBusy = "已有一个配对正在进行";
    public const string PairLinkNotRecognised = "这不是有效的 ClipBridge 配对链接（应以 clipbridge://pair 开头）。";
    public const string PairLinkVersion = "配对链接的版本不受支持，请把两端都更新到最新版本。";
    public const string PairHostNotIp = "地址必须是 IP 地址（Tailscale 地址，形如 100.x.y.z）。";
    public const string PairHostNotTailnet = "地址不在 Tailscale 网段内（100.64.0.0/10 或 fd7a:115c:a1e0::/48）。";
    public const string PairBadPort = "端口必须是 1 到 65535 之间的整数。";
    public const string PairBadPsk = "一次性密钥格式不正确（应为 22 个字符的 URL 安全 Base64）。";
    public const string PairBadFp = "指纹格式不正确（应为 43 个字符的 URL 安全 Base64），不确定的话请留空。";

    // ---- peers / devices ------------------------------------------------------------
    public const string DevicesTitle = "已配对的设备";
    public const string DevicesEmpty = "还没有配对设备。点击“配对新设备…”，或在手机上扫描二维码。";
    public const string BtnPairNew = "配对新设备…";
    public const string BtnJoinPair = "加入配对…";
    public const string BtnSendFiles = "发送文件…";
    public const string BtnRename = "重命名";
    public const string BtnReconnect = "重新连接";
    public const string BtnUnpair = "解除配对";
    public const string BtnForget = "删除记录";
    public const string LabelPolicy = "策略";
    public const string ModeNotify = "只通知";
    public const string ModeManual = "手动复制";
    public const string ModeAuto = "自动写入";
    public const string PlatformWindows = "Windows";
    public const string PlatformAndroid = "Android";
    public const string StateConnected = "已连接";
    public const string StateConnecting = "正在连接";
    public const string StateOffline = "离线";
    public const string StateUnpaired = "对方已不认识本机，需要重新配对";
    public const string StateRevoked = "已解除配对";
    public static string PendingText(int n) => n > 0 ? $"排队 {n} 条" : "";
    public static string OfflineWithPending(int n) => n > 0 ? $"离线（排队 {n} 条）" : "离线";
    public const string LastSeenNever = "从未在线";
    public const string LastSeenNow = "刚刚";
    public static string LastSeenMinutes(int n) => $"{n} 分钟前";
    public static string LastSeenHours(int n) => $"{n} 小时前";
    public static string LastSeenDays(int n) => $"{n} 天前";
    public static string LastSeenLabel(string t) => $"最后在线：{t}";
    public const string RenameTitle = "重命名设备";
    public const string RenamePrompt = "新名称（1–64 个字符）：";
    public const string NameInvalid = "名称不能为空，且不能超过 64 个字符。";
    public static string ConfirmUnpair(string name) => $"确定要与“{name}”解除配对吗？\n对方也会收到通知，之后需要重新配对才能同步。";
    public static string ConfirmForget(string name) => $"确定要删除“{name}”的记录吗？";
    public const string ConfirmTitle = "请确认";
    public const string RepairHint = "对方已不认识本机。请先解除配对，再重新配对。";
    public const string RepairNow = "解除并重新配对";
    public const string ReconnectRefreshed = "已刷新状态；核心会自动重连。";
    public const string DropFilesHint = "把文件拖到设备上即可发送";
    public const string DropNoFiles = "只能发送文件，不能发送文件夹。";
    public const string PeerRevokedTitle = "设备已解除配对";
    public static string PeerRevokedText(string name) => $"{name} 已解除与本机的配对。";

    // ---- inbox ----------------------------------------------------------------------
    public const string InboxTitle = "收件箱";
    public const string InboxEmpty = "还没有收到过剪贴板内容。";
    public const string BtnCopy = "复制";
    public const string BtnIgnore = "忽略";
    public const string BtnClear = "清空";
    public const string BtnReveal = "显示";
    public const string BtnHide = "隐藏";
    public const string ConfirmClearInbox = "确定要清空收件箱吗？";
    public const string InboxStateReceived = "未处理";
    public const string InboxStateApplied = "已复制";
    public const string InboxStateIgnored = "已忽略";
    public const string InboxStateExpired = "已过期";
    public const string InboxStateError = "出错";
    public static string InboxFrom(string name) => $"来自 {name}";

    // ---- files / transfers ----------------------------------------------------------
    public const string TransfersTitle = "文件传输";
    public const string TransfersEmpty = "还没有文件传输。";
    public const string BtnAccept = "接收";
    public const string BtnReject = "拒绝";
    public const string BtnCancel = "取消";
    public const string BtnOpenFile = "打开文件";
    public const string BtnOpenFolder = "打开所在文件夹";
    public const string BtnChangeFolder = "更改…";
    public const string BtnBrowse = "浏览…";
    public const string BtnRefresh = "刷新";
    public const string DirOut = "发送";
    public const string DirIn = "接收";
    public const string TransferHashing = "正在计算校验值…";
    public const string TransferOffered = "等待对方确认";
    public const string TransferOfferedIn = "等待你接收";
    public const string TransferAccepted = "已同意，准备传输";
    public const string TransferTransferring = "传输中";
    public const string TransferDone = "已完成";
    public const string TransferRejected = "已被拒绝";
    public const string TransferCancelled = "已取消";
    public const string TransferExpired = "已过期";
    public const string TransferFailed = "失败";
    public static string TransferFailedWith(string reason) => $"失败：{reason}";
    public const string FileOfferTitle = "收到文件";
    public static string FileOfferFrom(string from) => $"{from} 想发给你一个文件";
    public const string FileOfferSaveTo = "保存到：";
    public const string FileOfferDanger = "这是可执行文件类型，请只接收来自可信设备的文件。";
    public const string FileOfferDecideLater = "稍后决定";
    public static string UpdateApplyFailed(string reason) => $"ClipBridge 无法安装更新，已保留当前版本。\n\n{reason}";
    public const string UpdateApplyFailedTitle = "ClipBridge 更新";
    public const string FileOfferBalloonTitle = "收到文件请求";
    public static string FileOfferBalloon(string from, string name, string size) => $"{from} 想发送 {name}（{size}）";
    public const string FileDoneTitle = "文件已接收";
    public static string FileDoneText(string name) => $"{name} 已保存。";
    public const string FileFailedTitle = "文件传输失败";
    public const string FileSendStarted = "已开始发送";
    public static string FileSendStartedN(int n) => $"已开始发送 {n} 个文件";
    public static string FileSendFailed(string name, string reason) => $"{name}：{reason}";
    public const string FilePickTitle = "选择要发送的文件";
    public const string FilePickPeerTitle = "发送到哪台设备";
    public const string FilePickPeerPrompt = "选择接收文件的设备：";
    public const string FileNoPeers = "没有可用的设备。请先配对，或等待对方上线。";
    public const string FileDangerTitle = "打开可执行文件";
    public static string FileDangerText(string name) => $"“{name}”是可执行文件，来自其他设备。确定要打开吗？";
    public const string FileMissing = "文件已不在原位置。";
    public const string BtnOk = "确定";
    public const string BtnSend = "发送";

    // ---- settings -------------------------------------------------------------------
    public const string SettingsTitle = "设置";
    public const string SettingsSync = "同步我复制的内容";
    public const string SettingsSyncHint = "开启后，你在这台电脑上复制的文本会发给已配对的设备。";
    public const string SettingsDefaultMode = "新设备的默认策略";
    public const string SettingsTtl = "离线时内容的有效期（秒，30–86400）";
    public const string SettingsHistory = "收件箱保留条数（10–5000）";
    public const string SettingsSendSensitive = "允许发送看起来像口令/令牌的内容";
    public const string SettingsAutoStart = "开机自启（最小化到托盘）";
    public const string SettingsDownloadDir = "接收文件的保存位置";
    public const string SettingsSave = "保存设置";
    public const string SettingsSaved = "设置已保存";
    public const string SettingsFirewallTitle = "防火墙";
    public const string SettingsFirewallText = "首次监听时 Windows 可能弹出防火墙授权窗口，请选择允许“专用网络”。设备之间只通过 Tailscale 地址直连。";
    public const string SettingsUpdateTitle = "更新";
    public const string SettingsAboutTitle = "关于";
    public static string AboutVersion(string v) => $"版本：{v}";
    public static string AboutDeviceId(string id) => $"设备 ID：{id}";
    public static string AboutFingerprint(string fp) => $"指纹前 8 位：{fp}";
    public const string UpdateMandatorySuffix = "（必须更新）";
    public const string UpdateAuto = "自动下载更新";
    public const string UpdateChecking = "正在检查更新…";
    public const string UpdateBannerTitle = "有新版本";
    public static string UpdateBannerAvailable(string v) => $"新版本 {v} 可用。";
    public static string UpdateBannerReady(string v) => $"新版本 {v} 已下载，可以重启安装。";
    public const string UpdatePrepareFailedTitle = "无法安装更新";
    public static string UpdatePrepareFailed(string reason) => $"无法准备更新：\n\n{reason}";
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
    public const string ValidationTtl = "有效期必须是 30 到 86400 之间的整数（秒）。";
    public const string ValidationHistory = "保留条数必须是 10 到 5000 之间的整数。";
    public const string ValidationDownloadDir = "保存位置必须是绝对路径，例如 C:\\Users\\你\\Downloads\\ClipBridge。";
    public const string ValidationMode = "策略取值无效。";
    public static string DurationSeconds(int s) => $"{s} 秒";
    public static string DurationMinutes(int m) => $"{m} 分钟";
    public static string DurationHours(int h) => $"{h} 小时";
    public static string DurationHoursMinutes(int h, int m) => $"{h} 小时 {m} 分钟";

    // ---- first run ------------------------------------------------------------------

    // ---- diagnostics ----------------------------------------------------------------
    public const string DiagTitle = "诊断";
    public const string DiagCopy = "复制诊断信息";
    public const string DiagCopied = "诊断信息已复制";
    public const string DiagOpenLogs = "打开日志文件夹";
    public const string DiagOpenData = "打开数据文件夹";
    public const string DiagUnavailable = "暂时无法读取诊断信息";
    public const string NoTailnetBanner = "未找到 Tailscale 地址：请确认 Tailscale 已登录并连接，然后重启 ClipBridge。";
    public const string ListenFailedBanner = "无法在 Tailscale 地址上监听：端口可能被占用，或被防火墙拦截。";
    public const string FingerprintMismatchBanner = "检测到与已配对设备的指纹不一致，已拒绝连接。";

    // ---- errors ---------------------------------------------------------------------
    public const string ErrorGeneric = "出错了";
    public static string ErrorUnknown(string code) => $"发生未知错误（{code}）";
    public static string ErrorWithDetail(string message, string detail) => string.IsNullOrWhiteSpace(detail) ? message : $"{message}\n{detail}";
    public const string ErrNoTailnet = "没有检测到 Tailscale 网络，请确认 Tailscale 已登录并连接";
    public const string ErrNotPaired = "这台设备没有与对方配对";
    public const string ErrPairingExpired = "配对码已过期，请重新生成";
    public const string ErrPairingFailed = "配对失败，请核对链接和数字后重试";
    public const string ErrPeerOffline = "对方当前不在线";
    public const string ErrTargetMismatch = "目标设备不匹配";
    public const string ErrSourceMismatch = "来源设备不匹配";
    public const string ErrExpired = "已过期";
    public const string ErrHashMismatch = "校验失败";
    public const string ErrFileHashMismatch = "文件校验失败，请让对方重发";
    public const string ErrSequenceGap = "同步出现缺口，正在自动恢复";
    public const string ErrTooLarge = "内容太大";
    public const string ErrFileTooLarge = "文件太大，超过了允许的上限";
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
    public const string ErrNotFound = "找不到对应的传输";
    public const string ErrLimitReached = "已达到上限";
    public const string SuppressedTooLarge = TooLargeToSend;

    // ---- startup failures -----------------------------------------------------------
    public const string StartupFailedTitle = "ClipBridge 无法启动";
    public const string KeyCorruptTitle = "数据库密钥文件已损坏";
    public static string KeyCorruptText(string detail) =>
        "为了不让已有的加密数据永久丢失，ClipBridge 不会自动重建密钥。\n请从备份恢复 devkey.dpapi，或在确认不需要旧数据后手动删除数据文件夹。\n\n" + detail;
    public const string CoreMissingText = "找不到 clipbridge_core.dll，请重新安装或重新解压完整的安装包。";
    public const string KeyMigrationWarning = "数据库密钥尚未迁移到 DPAPI，已暂时继续使用旧的密钥文件。";

    // ---- misc -----------------------------------------------------------------------
    public const string Copy = "复制";
    public const string Close = "关闭";
    public const string Unknown = "未知";
    public const string Dash = "—";
}
