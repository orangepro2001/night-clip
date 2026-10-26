package com.nightclip.app.logic

/**
 * Central user-facing text for code that has no Context (pure logic, unit-tested).
 * Compose screens and Context-based platform code use res/values/strings.xml; the two
 * sets are kept apart on purpose: this object must stay free of Android classes.
 */
object Strings {
    // Connection / notifications
    const val SENSITIVE_HIDDEN = "敏感内容（已隐藏）"
    const val SENSITIVE_SHORT = "敏感内容"
    const val UNKNOWN_DEVICE = "其他设备"
    const val NO_DEVICES_CONNECTED = "未连接设备"
    const val PUBLIC_CLIPBOARD_TITLE = "收到剪贴板内容"
    const val DEGRADED_SUFFIX = "（应用在后台，未自动复制）"

    fun clipTitle(fromName: String): String =
        "来自 ${fromName.ifBlank { UNKNOWN_DEVICE }} 的剪贴板"

    fun connectedDevices(count: Int): String =
        if (count <= 0) NO_DEVICES_CONNECTED else "已连接 $count 台设备"

    fun fileOfferTitle(fromName: String): String =
        "${fromName.ifBlank { UNKNOWN_DEVICE }} 想发送文件"

    fun fileOfferBody(name: String, size: Long): String =
        "$name（${Formatters.fileSize(size)}）"

    fun fileSaved(name: String): String = "已保存到“下载/ClipBridge”：$name"
    fun fileSavedPrivate(name: String, location: String): String =
        "已保存到应用的下载目录：$name（$location）。允许存储权限后可保存到“下载”。"
    fun fileSentDone(name: String): String = "已发送：$name"
    fun fileReceiving(name: String): String = "正在接收：$name"
    fun fileSending(name: String): String = "正在发送：$name"
    fun fileHashing(name: String): String = "正在准备：$name"
    const val FILE_FAILED_TITLE = "文件传输失败"
    const val FILE_HASH_MISMATCH = "文件校验失败，请让对方重发"

    // Relative time
    const val JUST_NOW = "刚刚"
    const val NEVER = "从未"
    fun minutesAgo(n: Long) = "$n 分钟前"
    fun hoursAgo(n: Long) = "$n 小时前"
    fun daysAgo(n: Long) = "$n 天前"

    // Peer states (shell-guide §1)
    const val STATE_CONNECTED = "已连接"
    const val STATE_CONNECTING = "正在连接"
    const val STATE_OFFLINE = "离线"
    const val STATE_UNPAIRED = "对方已不认识本机，需要重新配对"
    const val STATE_REVOKED = "已解除配对"
    fun stateUnknown(state: String) = "未知状态（$state）"
    fun queued(n: Int) = "排队 $n 条"

    // Apply modes
    const val MODE_NOTIFY = "只通知"
    const val MODE_MANUAL = "手动复制"
    const val MODE_AUTO = "自动复制"

    // Inbox states
    const val INBOX_RECEIVED = "未处理"
    const val INBOX_APPLIED = "已复制"
    const val INBOX_IGNORED = "已忽略"
    const val INBOX_EXPIRED = "已过期"
    const val INBOX_ERROR = "出错"

    // Transfer states
    const val TR_HASHING = "正在计算校验"
    const val TR_OFFERED = "等待对方确认"
    const val TR_OFFERED_IN = "等待你确认"
    const val TR_ACCEPTED = "已接受"
    const val TR_TRANSFERRING = "传输中"
    const val TR_DONE = "已完成"
    const val TR_REJECTED = "已拒绝"
    const val TR_CANCELLED = "已取消"
    const val TR_FAILED = "失败"
    const val TR_EXPIRED = "已过期"

    // Send results
    const val SEND_NOTHING = "没有可发送的已配对设备"
    fun sentTo(n: Int) = "已发送给 $n 台设备"
    const val SUPPRESSED_CAPTURE_OFF = "同步已关闭，未发送"
    const val SUPPRESSED_EMPTY = "内容为空，未发送"
    const val SUPPRESSED_ECHO = "与刚收到的内容相同，未发送"
    const val SUPPRESSED_DUPLICATE = "内容与上次相同，未重复发送"
    const val SUPPRESSED_NO_PEERS = "还没有配对设备"
    const val SUPPRESSED_TOO_LARGE = "太大（超过 64 KiB），请作为文件发送"
    const val SUPPRESSED_SENSITIVE = "看起来是敏感内容，设置中已禁止发送"
    const val CLIPBOARD_EMPTY = "剪贴板是空的"
    fun suppressedUnknown(code: String) = "未发送（$code）"

    // Pairing
    const val PAIR_NOT_A_LINK = "这不是 ClipBridge 的配对链接"
    const val PAIR_BAD_VERSION = "配对链接版本不受支持"
    const val PAIR_BAD_HOST = "地址必须是 IP 地址（例如 100.101.146.117）"
    const val PAIR_BAD_PORT = "端口必须在 1–65535 之间"
    const val PAIR_BAD_FP = "指纹格式不正确"
    const val PAIR_BAD_PSK = "配对密钥格式不正确（应为 22 个字符）"
    const val PAIR_BAD_EXP = "配对链接缺少有效期"
    const val PAIR_EXPIRED = "配对链接已过期，请在电脑上重新生成"
    const val PAIR_MISSING_HOST = "请填写电脑的地址"
    const val PAIR_MISSING_PORT = "请填写端口"
    const val PAIR_MISSING_PSK = "请填写配对密钥"
    const val PAIR_NOT_QR = "这不是 ClipBridge 的配对二维码"

    // Errors
    const val ERR_GENERIC = "操作失败"
    fun errUnknown(code: String) = "出现未知错误（$code）"
    const val ERR_NOT_READY = "核心尚未启动，请稍后再试"
    const val ERR_NO_FILE_ACCESS = "无法读取所选文件"
    const val ERR_NO_FILE_SIZE = "无法确定文件大小"
    const val ERR_SAVE_FAILED = "保存到“下载”失败"
    const val ERR_KEY_CORRUPT = "本机的数据库密钥文件已损坏或无法解开。为避免让现有数据库永久不可读，应用不会自动重建它。"
    const val ERR_KEY_STORE = "无法使用系统密钥库（Android Keystore）保护数据库密钥"
}
