# ADR-005：Go 核心库 + Tailnet 直连 P2P（取代 Gateway 作为主链路）

- 状态：Accepted
- 日期：2026-10-26
- 决策者：项目组
- 取代：[ADR-004](004-gateway-long-connection.md) 的"Gateway 是唯一控制面"部分（ADR-004 降级为"可选中继，Phase 4 之后再议"）
- 相关文档：[architecture.md](../architecture.md)、[api-spec.md](../api-spec.md)、[development-plan.md](../development-plan.md)
- 历史版本：v2 Gateway 方案归档于 [archive-v2/](../archive-v2/)

## 背景

v2 方案用自建 Gateway（WebSocket + SQLite 密文队列）做控制面，并明确排除 P2P。最终检讨发现三个问题：

1. 协议、加密、去重、序号校验、croc store 客户端要在 Go / .NET / Kotlin 各实现一遍，需要跨语言测试向量，漂移风险高。
2. 用户新增要求：核心用 Go 或 C，Windows 用 C/C++/C#，Android 用 Kotlin，并尽可能 P2P。
3. 用户确认**所有设备都在同一个 Tailscale tailnet 内**。tailnet 已经解决了 NAT 穿透（直连 WireGuard，必要时 DERP 中继）、设备间加密路径和稳定地址，所以自建 Gateway、STUN/TURN、WebRTC、局域网发现都不再必要。

## 决策

1. **单一 Go 核心库 `clipbridge-core`**，承载协议、身份、配对、对等连接、发件箱、游标、去重、文件传输。
2. **两个壳，不重复实现业务**：
   - Windows：C# WPF 托盘应用，通过 P/Invoke 调用 Go 编译的 `c-shared` DLL（`clipbridge_core.dll`）。WPF 只负责剪贴板监听/写入、托盘、通知、设置 UI、DPAPI。
   - Android：Kotlin + Compose，通过 `gomobile bind` 生成的 AAR 调用同一个核心。Kotlin 只负责 UI、前台服务、通知、分享/磁贴、剪贴板读写、Keystore、MediaStore。
3. **两个壳使用同一套 JSON 门面 API**：`Call(method, paramsJSON) → resultJSON` + `NextEvent(timeoutMs) → eventJSON`。避免在 C ABI 和 gomobile 中暴露复杂类型或跨语言回调。
4. **设备间直连**：端点之间走 tailnet 地址（`100.64.0.0/10`）上的 mTLS HTTPS + WebSocket，设备证书互相固定（pin）。
5. **拨号方向规则**：Windows 监听（只绑定 tailnet 地址），Android 只出站拨号；数据搬运总由拨号方发起（见 api-spec §7）。
6. **离线语义**：发送方持有发件箱和每链路序号；对端在线时按游标补发。两端不同时在线且超过 TTL 的事件会丢弃，并明确告知用户。
7. **不嵌入 tsnet / libtailscale**：核心只依赖系统已运行的 Tailscale 客户端（Windows Tailscale 服务、Android Tailscale VPN）。
8. croc store 退出 MVP 关键路径；croc 仅作为分块/断点/限额设计的参考，以及可选的"离线文件中转"后续项。

## 结果

### 正面

- 业务逻辑只有一份，Go 单测即可覆盖大部分正确性；删除跨语言测试向量和 Kotlin 版 croc 客户端的工作量。
- 没有服务端组件，没有 TLS 证书/域名/Docker 运维，也不需要在本机 `tailscale serve` 增加路径。
- 文件字节直接从设备到设备，经 tailnet 加密，无存储配额与 1 GiB 限制（改为可配置上限）。
- 延迟低，且 tailnet 能直连时不经任何第三方服务器。

### 负面

- **产品前提变强**：两端必须运行 Tailscale 且在同一 tailnet；Android 同一时刻只能有一个 VPN，使用其它 VPN 的用户无法使用。
- 无中继：Windows PC 休眠/关机时 Android 无法同步；Android 前台服务被杀时 Windows→Android 事件只能排队等待。
- Go 运行时进入 Windows 进程（DLL 不可卸载，Go panic 会拖垮宿主，需要在门面处统一 recover）和 APK（体积增加约 10–15 MB）。
- 需要自己实现配对与设备认证（Gateway 方案里本来也要做，但现在没有服务端兜底）。

### 中性

- tailnet 成员不等于被授权：应用层仍需配对和证书固定，不能把 "在 tailnet 里" 当作认证。
- 若 tailnet 暂时走 DERP 中继，吞吐会下降，但功能不受影响。

## 备选方案

### A. 保留 Gateway 并叠加 P2P（v2 + 加法）

拒绝作为首版：组件最多、协议路径最多。保留为 Phase 4 之后的可选"离线中继 peer"，需要新 ADR（含端到端加密，因为中继不再可信）。

### B. WebRTC（pion）+ 信令服务

拒绝：tailnet 已解决 NAT；pion/ICE 依赖在 Android 上会触发网卡枚举限制，复杂度无收益。

### C. libp2p

拒绝：依赖重，已有 [gomobile 下打洞失败的报告](https://discuss.libp2p.io/t/go-libp2p-with-gomobile-how-can-i-make-hole-punch-succeed/3691)，且 tailnet 下不需要。

### D. 嵌入 tsnet

拒绝：[Android 上网卡枚举受限](https://pub.dev/packages/tsnet_flutter)，且用户本来就要运行 Tailscale 客户端；重复节点会占用额外 tailnet 设备名额。

### E. 局域网 LocalSend 式发现

拒绝：目标是跨网络可用；tailnet 地址在局域网内同样直连，无需第二套发现机制。

### F. 核心用 C 而非 Go

拒绝：Go 提供 TLS、HTTP、SQLite（pure Go）、gomobile 和 `c-shared`，开发成本明显低于 C；仓库内 croc 的 Go 代码可作参考。

## 验证（Phase 0 Go/No-Go）

- [ ] Go 核心 AAR 在真机上经 Tailscale VPN 拨通 Windows DLL 监听的 mTLS 端口（IP 直拨，不依赖 DNS）。
- [ ] C# P/Invoke 调用 DLL：`NextEvent` 阻塞线程不死锁，关闭时干净退出。
- [ ] Android 端核心不调用 `net.Interfaces()`（targetSdk ≥ 30 受限）。
- [ ] 断线 5 分钟后重连，发件箱按游标补发，重复事件只应用一次。
- [ ] 证书指纹不匹配的连接被拒绝；非 tailnet 来源地址被拒绝。
