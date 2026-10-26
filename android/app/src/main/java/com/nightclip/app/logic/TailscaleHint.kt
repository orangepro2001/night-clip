package com.nightclip.app.logic

/** The advisory Tailscale banner: only a hint, based on whether any VPN network exists. */
object TailscaleHint {
    const val MESSAGE = "未检测到 VPN 连接。ClipBridge 需要 Tailscale 保持已登录并连接，否则无法与其他设备通信。"

    /** Null = nothing to show. */
    fun message(vpnActive: Boolean): String? = if (vpnActive) null else MESSAGE
}
