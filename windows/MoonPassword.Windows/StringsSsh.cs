using MoonPassword.Windows.Logic;

namespace MoonPassword.Windows;

/// <summary>User-facing text of the SSH key and SSH agent features (Simplified Chinese). Pure C#, linked into the tests and the macOS shell.</summary>
public static partial class Strings
{
    public const string BtnDone = "完成";
    public const string ValidationMasterRequired = "请输入主密码。";
    public const string SshAgentNotRunning = "SSH 代理还没开启，这把钥匙暂时不会提供给 ssh。";
    public const string SshBtnEnableAgent = "开启 SSH 代理";

    // ---- key problems and errors ----------------------------------------------------------
    public const string SshProblemPassphrase = "密钥本身还加了口令，代理用不了。点“去掉口令”输入一次即可。";
    public const string SshProblemExternal = "这个条目指向磁盘上的密钥文件（KeePassXC 的“外部文件”），MoonPassword 只用存在密码库里的密钥。";
    public const string SshProblemMissing = "条目里找不到密钥附件。";
    public const string SshProblemTooLarge = "密钥附件过大，不像是 SSH 私钥。";
    public const string SshProblemCorrupt = "密钥数据已损坏，无法读取。";

    public static string SshUnsupported(string reason) => reason switch
    {
        "dsa" => "DSA 密钥已被 OpenSSH 10 移除，无法使用。请改用 Ed25519 密钥。",
        "sk" => "这是硬件安全密钥（FIDO）的句柄，需要插着那把实体钥匙才能签名，MoonPassword 不支持。",
        "sshcom" => "这是 ssh.com 的私钥格式，请先用 ssh-keygen -i 转成 OpenSSH 格式。",
        "putty1" => "这是很旧的 PuTTY 密钥格式，请先用新版 PuTTYgen 另存一次。",
        "pbes1" => "这个 PKCS#8 文件用的是过时的加密方式，请用 openssl pkcs8 -topk8 -v2 aes-256-cbc 重新加密。",
        "public_only" => "这是公钥，不是私钥。登录需要的是同名不带 .pub 的那个文件。",
        "certificate" => "这是证书，不是私钥。",
        "rsa_small" => "RSA 密钥小于 2048 位，已不安全，请重新生成。",
        "curve" => "这条椭圆曲线 SSH 不支持（只支持 P-256、P-384、P-521）。",
        "multi" => "一个文件里有多把密钥，请分开保存后再导入。",
        "cipher" => "用了无法识别的加密算法。",
        _ => "这不是可识别的 SSH 私钥文件。",
    };

    public const string ErrSshNeedsPassphrase = "这把密钥有口令，请输入。";
    public const string ErrSshWrongPassphrase = "口令不对。";
    public const string ErrSshCorrupt = "密钥文件已损坏。";
    public const string ErrSshTooExpensive = "这把密钥的加密参数大得异常，为安全起见没有尝试解密。";
    public const string ErrSshDuplicate = "这把密钥已经在密码库里了。";
    public const string ErrSshAgentUnavailable = "SSH 代理不可用。";
    public const string ErrSshTooLarge = "文件太大，不像是 SSH 私钥。";

    // ---- notifications ----------------------------------------------------------------------
    public static string SshUsedTitle(SshUse u) => u.ProgramName.Length > 0 ? $"{u.ProgramName} 用了 SSH 密钥" : "SSH 密钥被使用";

    public static string SshUsedText(SshUse u)
    {
        var key = u.Title.Length > 0 ? $"「{u.Title}」" : SshText.ShortFingerprint(u.Fingerprint);
        var what = u.Kind switch
        {
            "userauth" when u.Host.Length > 0 => $"登录 {(u.User.Length > 0 ? u.User + "@" : "")}{u.Host}",
            "userauth" => u.User.Length > 0 ? $"以 {u.User} 登录服务器" : "登录服务器",
            "sshsig" when u.Namespace == "git" => "签名 Git 提交",
            "sshsig" => $"签名（{u.Namespace}）",
            _ => "签名",
        };
        return $"{key}：{what}" + (u.Forwarded ? "（经转发）" : "");
    }

    public const string SshRefusedTitle = "已拒绝 SSH 签名请求";

    public static string SshRefusedText(SshUse u) => u.Reason switch
    {
        "forwarding" => "有请求经 ssh -A 转发过来；转发默认关闭，可在设置里打开。",
        "no_keys" => "密码库已锁定，代理里没有可用的密钥。",
        "unknown_key" => "请求的密钥已不在代理里。",
        "host" => "这把钥匙限定了主机，请求来自别的服务器。",
        _ => "签名失败。",
    };

    public const string SshKeysNeededTitle = "SSH 需要解锁密码库";

    public static string SshKeysNeededText(string program) =>
        (program.Length > 0 ? $"{program} 想用 SSH 密钥，" : "有程序想用 SSH 密钥，") + "但密码库已锁定。点这里解锁后重试。";

    // ---- migration ----------------------------------------------------------------------------
    public const string SshBlockFingerprint = "密码库里的密钥和这个文件对不上";
    public const string SshBlockPub = "旁边没有对得上的 .pub 文件，ssh 配置会找不到这把钥匙";
    public const string SshBlockAgent = "SSH 代理还没有列出这把钥匙";

    public static string SshBlockReferenced(IReadOnlyList<string> files) =>
        $"{string.Join("、", files)} 直接读取这个文件（例如 SSHFS-Win、PuTTY），它们连不上代理";

    public static string SshUsedByHosts(IReadOnlyList<string> hosts) => "ssh 配置里用于：" + string.Join("、", hosts);
    public static string SshReferencedBy(string file, int line) => $"{file} 第 {line} 行直接用到这个文件";
    public const string SshPubMismatch = "旁边的 .pub 不是这把钥匙的公钥";

    // ---- environment checks ------------------------------------------------------------------
    public const string SshEnvNoOpenSshTitle = "没有找到 Windows 自带的 OpenSSH 客户端";
    public const string SshEnvNoOpenSshText = "在“设置 → 系统 → 可选功能”里添加“OpenSSH 客户端”。";
    public const string SshEnvServiceTitle = "Windows 的 OpenSSH Authentication Agent 服务占用了代理管道";
    public const string SshEnvServiceText = "停止并禁用这个服务（需要管理员权限），MoonPassword 才能接管 ssh 的默认代理地址。也可以改用别的管道名，再在 ssh 配置里写 IdentityAgent。";
    public const string SshEnvPipeTakenTitle = "代理管道已被其它程序占用";
    public static string SshEnvPipeTakenText(string pipe) => $"{pipe} 已有别的 SSH 代理（如 1Password、Bitwarden、KeePassXC）在用。关掉它的 SSH 代理，或给 MoonPassword 换一个管道名。";
    public const string SshEnvGitTitle = "Git 用的是自带的 ssh，连不上这个代理";
    public const string SshEnvGitText = "Git for Windows 自带的 ssh 不认 Windows 命名管道。让 Git 改用 Windows 自带的 OpenSSH 后，git pull / push 就能用密码库里的密钥。";
    public const string SshEnvGitOtherTitle = "Git 指定了别的 ssh 程序";
    public static string SshEnvGitOtherText(string cmd) => $"core.sshCommand 现在是 {cmd}。如果它不是 Windows 自带的 OpenSSH，就用不了这个代理；MoonPassword 不会改动你自己设置的值。";
    public const string SshEnvAuthSockTitle = "SSH_AUTH_SOCK 指向了别的代理";
    public static string SshEnvAuthSockText(string sock) => $"环境变量 SSH_AUTH_SOCK = {sock}，ssh 会去那里找钥匙而不是 MoonPassword。删掉这个变量，或把它设成 MoonPassword 的管道名。";

    // ---- vault page: list and detail ----------------------------------------------------------
    public const string SshMenuImport = "导入 SSH 密钥…";
    public const string SshMenuGenerate = "生成 SSH 密钥…";
    public const string SshMenuMigrate = "查看 SSH 主机和迁移密钥…";
    public const string SshSectionTitle = "SSH 密钥";
    public const string SshLabelType = "类型";
    public const string SshLabelFingerprint = "指纹";
    public const string SshLabelComment = "注释";
    public const string SshLabelPublicKey = "公钥";
    public const string SshLabelAgent = "提供给 SSH 代理";
    public const string SshAgentHint = "打开后，ssh、git 等程序能直接用这把钥匙登录，私钥不出密码库。";
    public const string SshBadgeLoaded = "已加载";
    public static string SshBadgeGrace(DateTimeOffset until) => $"保留至 {until.ToLocalTime():HH:mm}";
    public const string SshBadgeNotLoaded = "未加载";
    public const string SshBadgeAgentOff = "代理已关";
    public static string SshLastUsed(string when) => $"上次使用：{when}";
    public const string SshNeverUsed = "还没有被使用过";
    public const string SshBtnCopyPublic = "复制公钥";
    public const string SshBtnWritePub = "写入 .pub 文件…";
    public const string SshBtnExport = "导出私钥…";
    public const string SshBtnNormalize = "去掉口令…";
    public const string SshCopiedPublic = "公钥已复制";
    public static string SshPubWritten(string path) => $"已写入 {path}";
    public const string SshPubSame = "那个 .pub 文件已经是这把钥匙的公钥。";
    public const string SshPubExists = "那里已有一个不同的 .pub 文件，没有覆盖。";
    public const string SshNormalizeTitle = "去掉密钥的口令";
    public const string SshNormalizeText = "密码库本身已加密。去掉密钥自己的口令后，SSH 代理就能直接使用它；导出时可以重新加口令。";
    public const string SshNormalizeDone = "已去掉口令";
    public const string SshKindHint = "SSH 密钥由“导入”或“生成”创建，类型不能更改。";

    // ---- host binding ---------------------------------------------------------------------------
    public const string SshHostsTitle = "限定主机";
    public const string SshHostsAny = "任何主机";
    public const string SshHostsHint = "只在 ssh 连接这些服务器时提供这把钥匙；钥匙多时可避免服务器因尝试次数过多而拒绝登录。";
    public const string SshHostsEdit = "设置…";
    public const string SshHostsPromptTitle = "限定这把钥匙用于哪些主机";
    public const string SshHostsPromptLabel = "手动添加规则";
    public const string SshHostsManualHint = "每行一个主机名（可用 * 和 ?）、[主机]:端口或 SHA256 指纹。未选主机且规则为空时允许任何主机。";
    public const string SshHostsConfigHeading = "SSH 配置里的主机";
    public const string SshHostsConfigLoading = "正在读取 SSH 配置…";
    public const string SshHostsConfigEmpty = "没有找到命名的 Host 条目。";
    public const string SshHostsConfigChoose = "选择配置文件…";
    public const string SshHostsConfigFilter = "搜索别名或地址";
    public const string SshHostsConfigUnknown = "无法从配置确定服务器标识，可手动填写主机密钥指纹。";
    public const string SshHostsConfigHint = "代理会用 known_hosts 中对应的服务器主机密钥识别所选主机。";
    public static string SshHostsInvalid(string host) => $"“{host}”不是主机名、[主机]:端口或 SHA256 指纹。";
    public const string SshHostsInvalidAny = "有一行不是主机名、[主机]:端口或 SHA256 指纹，或者超过了 32 个。";
    public const string SshHostsSaved = "已更新限定主机";
    public static string SshHostsSuggest(IReadOnlyList<string> hosts) => "最近用于：" + string.Join("、", hosts.Select(SshHosts.Display));
    public const string SshHostsUseSuggested = "只用于这些主机";
    public const string SshHostsUnboundNote = "没有限定时，任何服务器都可能被依次尝试这把钥匙。"; 

    // ---- import window ------------------------------------------------------------------------
    public const string SshImportTitle = "导入 SSH 密钥";
    public const string SshImportIntro = "支持 OpenSSH、PEM（PKCS#1 / SEC1）、PKCS#8、DER 和 PuTTY .ppk（v2 / v3），加密的也可以。导入后私钥以 OpenSSH 格式保存在密码库里，可被 KeePassXC 直接使用。";
    public const string SshImportChooseFiles = "选择密钥文件…";
    public const string SshImportPasteLabel = "或者粘贴私钥文本";
    public const string SshImportPastePlaceholder = "-----BEGIN OPENSSH PRIVATE KEY-----";
    public const string SshImportPasteAdd = "检查粘贴的内容";
    public const string SshImportPasted = "粘贴的密钥";
    public const string SshImportEmpty = "还没有选择密钥。";
    public const string SshImportPassphrase = "口令";
    public const string SshImportUnlock = "解开";
    public const string SshImportReady = "可以导入";
    public const string SshImportNeedsPass = "需要口令";
    public const string SshImportWrongPass = "口令不对";
    public const string SshImportInVault = "已在密码库";
    public const string SshImportAgentToggle = "导入后提供给 SSH 代理";
    public static string SshImportButton(int n) => n > 0 ? $"导入 {n} 把" : "导入";
    public static string SshImportedToast(int n) => $"已导入 {n} 把 SSH 密钥";
    public const string SshFileTypeKeys = "SSH 私钥";
    public const string SshFileTypeAll = "所有文件";
    public static string SshFormatName(string format) => format switch
    {
        "openssh" or "openssh-blob" => "OpenSSH",
        "pem-pkcs1" => "PEM（PKCS#1）",
        "pem-sec1" => "PEM（SEC1）",
        "pkcs8" => "PKCS#8",
        "pkcs8-enc" => "PKCS#8（加密）",
        "der-pkcs8" => "DER（PKCS#8）",
        "der-pkcs1" => "DER（PKCS#1）",
        "der-sec1" => "DER（SEC1）",
        "ppk2" => "PuTTY PPK v2",
        "ppk3" => "PuTTY PPK v3",
        _ => format,
    };

    // ---- generate window ----------------------------------------------------------------------
    public const string SshGenerateTitle = "生成 SSH 密钥";
    public const string SshGenerateIntro = "新钥匙只存在密码库里。把公钥贴到 GitHub、服务器的 authorized_keys 等处即可使用。";
    public const string SshGenerateType = "类型";
    public const string SshGenerateTitleLabel = "名称";
    public const string SshGenerateTitlePlaceholder = "例如 GitHub、公司跳板机";
    public const string SshGenerateComment = "注释（写进公钥末尾）";
    public const string SshGenerateButton = "生成";
    public const string SshGenerateDone = "已生成。复制下面的公钥，贴到要登录的地方。";
    public const string SshTypeEd25519Hint = "推荐：短小、快、安全";
    public const string SshTypeRsaHint = "兼容很老的服务器";
    public const string SshTypeEcdsaHint = "少数设备只认 ECDSA";

    // ---- export ---------------------------------------------------------------------------------
    public const string SshExportTitle = "导出私钥";
    public const string SshExportWarning = "导出的文件就是私钥本身，拿到它的人能以你的身份登录。只在确实需要时导出，用完删除。";
    public const string SshExportFormat = "格式";
    public const string SshExportPassphrase = "给文件加口令（可留空）";
    public const string SshExportMaster = "主密码";
    public const string SshExportButton = "导出…";
    public static string SshExported(string path) => $"已导出到 {path}";
    public const string SshFormatOpenSsh = "OpenSSH（推荐）";
    public const string SshFormatPkcs8 = "PKCS#8";
    public const string SshFormatPem = "PEM（RSA / ECDSA）";

    // ---- migration wizard -------------------------------------------------------------------------
    public const string SshMigrateTitle = "把 ~/.ssh 的密钥移进密码库";
    public const string SshMigrateIntro = "导入后 ssh 通过 MoonPassword 的代理使用这些钥匙。私钥文件默认保留；只有确认一切正常、且没有别的程序直接读取它时，才可以选择移到回收站。";
    public const string SshMigrateScanning = "正在读取 SSH 配置并查找私钥…";
    public static string SshConfigTitle(int count) => $"SSH 主机配置（{count} 条）";
    public const string SshConfigEmpty = "没有找到明确命名的 Host 条目。";
    public const string SshConfigPartial = "部分 SSH 配置无法读取或超出扫描范围。";
    public const string SshConfigAgentHint = "下方显示配置文件中声明的值。把对应私钥导入密码库并启用代理后，可继续用 ssh 主机别名连接。OpenSSH 会按原配置选择密钥和代理。";
    public static string SshMigrateFound(int n, string dir) => n == 0 ? $"{dir} 里没有找到私钥。" : $"在 {dir} 找到 {n} 把私钥。";
    public const string SshMigrateNeedAgent = "SSH 代理还没开。迁移会顺便打开它。";
    public const string SshMigrateBackup = "先备份密码库";
    public const string SshMigrateImport = "导入选中的密钥";
    public const string SshMigrateRunning = "正在导入…";
    public const string SshMigrateResultTitle = "结果";
    public static string SshMigrateDone(int imported, int skipped) => $"已导入 {imported} 把" + (skipped > 0 ? $"，跳过 {skipped} 把" : "") + "。";
    public const string SshMigrateRecycle = "把可以安全删除的私钥文件移到回收站";
    public const string SshMigrateRecycleHint = "只列出通过全部检查的文件；回收站里还能还原。";
    public static string SshMigrateRecycled(int n) => $"已把 {n} 个私钥文件移到回收站";
    public const string SshMigrateKeep = "保留";
    public const string SshMigrateNothingToRecycle = "没有可以安全移走的文件，私钥文件都保留在原处。";
    public const string SshMigrateImportedState = "已导入";
    public const string SshMigrateSkippedState = "跳过";
    public const string SshMigrateFailedState = "失败";

    // ---- settings ---------------------------------------------------------------------------------
    public const string SshHostSetupTitle = "配置 SSH 主机";
    public const string SshSettingsTitle = "SSH 代理";
    public const string SshSettingsDesc = "让 ssh、git、VS Code 直接使用密码库里的 SSH 密钥，私钥不必放在 ~/.ssh。";
    public const string SshSettingEnable = "启用 SSH 代理";
    public const string SshSettingEnableHint = "监听 Windows ssh 的默认代理地址，不用改任何配置。";
    public const string SshSettingTtl = "密码库锁定后保留钥匙";
    public const string SshSettingTtlHint = "空闲自动锁定后，钥匙在代理里再留多久。手动锁定、锁屏、睡眠时立即清除。";
    public const string SshSettingForwarding = "允许经 ssh -A 转发使用";
    public const string SshSettingForwardingHint = "转发后，远端机器上的程序也能用你的钥匙签名。不需要就保持关闭。";
    public const string SshSettingNotify = "使用提醒";
    public const string SshNotifyFirst = "每个程序首次使用";
    public const string SshNotifyAll = "每次使用";
    public const string SshNotifyOff = "不提醒";
    public const string SshTtlFollow = "随密码库一起锁";
    public static string SshTtlHours(int h) => $"{h} 小时";
    public static string SshTtlMinutes(int m) => $"{m} 分钟";
    public const string SshStatusListening = "正在运行";
    public const string SshStatusStopped = "已关闭";
    public const string SshStatusConflict = "管道被占用";
    public const string SshStatusError = "启动失败";
    public const string SshStatusUnsupported = "此平台不支持";
    public static string SshStatusKeys(int n, bool grace, DateTimeOffset until) =>
        n == 0 ? "没有加载钥匙（解锁密码库后加载）" : grace ? $"已加载 {n} 把，保留至 {until.ToLocalTime():HH:mm}" : $"已加载 {n} 把";
    public const string SshBtnSelfTest = "自检";
    public static string SshSelfTestOk(int n) => $"代理工作正常，列出了 {n} 把钥匙。";
    public static string SshSelfTestFailed(string error) => $"自检失败：{error}";
    public const string SshBtnFixGit = "让 Git 使用 Windows OpenSSH";
    public const string SshFixGitTitle = "修改 Git 的全局配置？";
    public static string SshFixGitText(string command) => $"将执行：\n{command}\n\n这只改 Git 用哪个 ssh 程序，可以随时撤销。";
    public const string SshBtnUndoGit = "撤销 Git 设置";
    public const string SshGitFixed = "Git 现在使用 Windows OpenSSH";
    public const string SshGitUndone = "已撤销 Git 的 ssh 设置";
    public static string SshGitFailed(string error) => $"修改 Git 配置失败：{error}";
    public const string SshEnvAllGood = "环境检查没有发现问题。";
    public const string SshRecentTitle = "最近使用";
    public const string SshRecentEmpty = "还没有程序使用过 SSH 钥匙。";
    public const string SshBtnRefresh = "刷新";
    public const string SshServiceFixButton = "停止并禁用该服务…";
    public const string SshServiceFixTitle = "停止 Windows 的 SSH 代理服务？";
    public const string SshServiceFixText = "将以管理员身份运行：\nsc.exe stop ssh-agent\nsc.exe config ssh-agent start= disabled\n\nWindows 会弹出权限确认。";
    public const string SshServiceFixed = "服务已停止，正在重新启动 MoonPassword 的代理";
}
