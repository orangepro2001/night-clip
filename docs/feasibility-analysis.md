# ClipBridge 可行性分析 v3（Go 核心 + Tailnet 直连）

> 日期：2026-10-26。前提：所有设备在同一 tailnet 并运行 Tailscale。  
> 本文只覆盖相对 v2 变化的部分；Windows/Android 剪贴板平台限制的结论不变，完整 v2 分析见 [archive-v2/feasibility-analysis.md](archive-v2/feasibility-analysis.md)。

## 1. 结论

| 能力 | 结论 | 说明 |
| --- | --- | --- |
| 设备间直连 | 可行 | tailnet 提供稳定 IP、NAT 穿透、WireGuard 加密路径 |
| 无服务端同步 | 可行但有语义代价 | 两端需同时在线；离线靠发件箱 + TTL |
| Go 核心 → Windows DLL → C# | 可行 | `c-shared` + P/Invoke 是成熟做法；用轮询事件泵避免回调问题 |
| Go 核心 → Android AAR → Kotlin | 可行 | gomobile 成熟；需规避网卡枚举与 DNS 限制，Phase 0 PoC 验证 |
| 剪贴板同步 | 可行 | Windows 自动捕获；Android 用户触发（平台限制不变） |
| 文件直传 | 可行 | HTTP Range + SHA-256；无存储配额；DERP 下较慢 |
| P2P | **可行，且比 v2 判断更容易** | tailnet 已解决穿透，不需要 WebRTC/libp2p |
| 全自动后台剪贴板 | 不可承诺 | Android 限制不变 |

## 2. 开源调研结论

| 项目 | 角色 | 结论 |
| --- | --- | --- |
| [ClipCascade](https://github.com/Sathvik-Rao/ClipCascade) | 同类产品，P2S + P2P 双模式 | 参考其验收用例和 Android 限制处理；不采用（栈不同，无 tailnet 前提） |
| [Clipbird Android](https://github.com/srilakshmikanthanp/clipbirdroid) | Kotlin，Windows ↔ Android，局域网 | 参考 Android 侧剪贴板与服务实现 |
| KDE Connect | 剪贴板 + 文件，TLS | 论坛报告 Android→Windows 剪贴板方向不稳定，印证"Android 用户触发"策略 |
| [LocalSend 协议](https://github.com/localsend/protocol) | REST + 自签 HTTPS | 借鉴"指纹固定的自签 HTTPS"思路；发现机制不需要 |
| pion/webrtc | Go WebRTC | 不采用：tailnet 下无收益，Android 网卡枚举风险 |
| go-libp2p | 通用 P2P | 不采用：依赖重，gomobile 打洞有[失败报告](https://discuss.libp2p.io/t/go-libp2p-with-gomobile-how-can-i-make-hole-punch-succeed/3691) |
| tsnet / libtailscale | 嵌入 Tailscale | 不采用：[Android 受限](https://pub.dev/packages/tsnet_flutter)，且用户本来运行 Tailscale |
| 本仓库 `croc` | 文件传输 | 不再依赖；其 `tailcattransport` 构建标签不含 Android，印证不嵌入的决定；仅参考限额/状态机设计 |

## 3. 新架构下的关键技术判断

### 3.1 Android 只出站拨号

- 不监听端口，避免：后台监听被杀、入站防火墙、`net.Interfaces()` 需要的 netlink 权限（targetSdk ≥ 30 受限）。
- 所有 Windows→Android 的数据通过 Android 主动建立的长连接回传；文件由"拨号方搬运"规则解决（pull/push）。
- 代价：Android 未连接时 Windows 不能唤醒它；只能排队。

### 3.2 Android 上的 Go 网络

- Go 解析器不可靠 → 一律用 IP 拨号，MagicDNS 名称仅显示。
- 不引入会枚举网卡的依赖（pion、libp2p、quic-go 默认行为）。
- Tailscale 作为 VPN 运行时，应用 socket 默认走 VPN；PoC P-A 需确认没有被分流排除。
- Android 只能有一个活动 VPN：与其它 VPN 冲突时功能不可用，属于前提限制。

### 3.3 Windows 监听

- 仅绑定 tailnet 地址，不绑 `0.0.0.0`；Tailscale 接口在登录后可能晚出现，需要重绑循环。
- 防火墙规则由安装器添加，远端地址限定 `100.64.0.0/10`。

### 3.4 DLL 与 gomobile 的边界

- 单一 JSON 门面：`Call`/`NextEvent`，两端同一份方法集。
- 不使用 Go→C# 回调；C# 事件泵线程阻塞调用 `cb_next_event`。
- Go panic 不能越过 C ABI：每个导出函数 recover。
- DLL 不可卸载：退出前 `cb_shutdown`。
- Android 文件发送用 `ParcelFileDescriptor.detachFd()` 传 fd，避免复制。

### 3.5 安全

- tailnet 成员 ≠ 授权：配对 + 指纹固定 + SAS 确认是必须的。
- 仅 TLS 1.3；证书验证为指纹比较；psk 一次性、5 分钟、3 次失败作废。
- 链路认证由 mTLS 提供，不再需要 v2 的 payload 加密和 Ed25519 事件签名（没有不可信中间方）。若将来引入中继，必须补端到端加密。
- 本地数据库字段加密，主密钥由 DPAPI / Keystore 保护。

### 3.6 离线语义（有意的取舍）

| 场景 | v2 Gateway | v3 直连 |
| --- | --- | --- |
| 两端同时在线 | 秒级 | 秒级（更低延迟） |
| 一端离线几分钟 | Gateway 存储，上线补齐 | 发送方发件箱，对端上线补齐（需发送方在线或事件仍在发件箱） |
| 发送方也离线/关机 | 仍可由 Gateway 投递 | **无法投递**，TTL 到期丢弃 |
| 运维 | 服务器、TLS、备份 | 无 |

对"两台自己的设备同步剪贴板和文件"，这个代价可以接受；若不能，使用 Phase 4+ 的中继。

## 4. 风险

| 风险 | 概率 | 影响 | 缓解 |
| --- | --- | --- | --- |
| Go 在 Android+VPN 下拨号失败 | 中 | 高 | Phase 0 PoC P-A；失败则停 |
| C# P/Invoke 事件泵稳定性 | 低 | 高 | PoC P-B，24 小时压测 |
| OEM 杀前台服务 | 高 | 中 | 游标补齐；引导设置 |
| 电脑休眠导致不可达 | 高 | 中 | UI 明示；TTL 排队 |
| 与其它 VPN 冲突 | 中 | 中 | 提示；文档；中继（P4+） |
| DERP 路径慢 | 中 | 低/中 | 显示路径与速率 |
| tailnet 内其它设备探测 | 低 | 中 | 指纹固定、仅配对设备可访问；ACL 示例 |
| 核心 panic | 低 | 高 | recover + 日志 + 重启提示 |
| 范围反弹 | 中 | 中 | ADR 守门 |

## 5. Go / No-Go

进入正式开发前必须满足（对应 [development-plan.md §3.3](development-plan.md)）：

- [ ] P-A：Android AAR 经 Tailscale 拨通 Windows DLL 监听端口。
- [ ] P-B：C# P/Invoke 长时间稳定。
- [ ] 指纹不符与非 tailnet 来源被拒绝。
- [ ] 断线重连补发、重复去重、缺口 `skip` 行为正确。
- [ ] 产品文案明确：需要 Tailscale；Android 后台不读剪贴板；电脑离线不可同步。

若 P-A 失败：评估 Android 端改用 libtailscale 或回退到可选中继方案；若 P-B 失败：Windows 改用 Go 子进程 + stdio/命名管道，C# 只做 UI。

## 6. 最终建议

1. 采用 ADR-005：Go 核心 + WPF/Kotlin 壳 + tailnet 直连。
2. 第一周只做三层栈打通和 PoC，不写功能。
3. 本机（Linux 开发机）不需要新增任何 `tailscale serve` 路径，也不部署服务端。
4. 把"离线中继"作为明确的后续选项，而不是 MVP 内容。
