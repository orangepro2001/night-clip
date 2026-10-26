# ClipBridge 项目自述（工作名）

> 文档版本：v3（Go 核心 + Tailnet 直连）  
> 文档状态：方案已定稿，待 Phase 0 PoC 验证  
> 代码状态：尚未编写代码；`Bark/`、`croc/` 目录仅为参考，不是运行依赖  
> 工作名：**ClipBridge**，协议 **`clipbridge/v1`**，CLI **`cbridge.exe`**

## 1. 结论先行

- **范围**：只做 Windows 与 Android 之间的**剪贴板同步 + 文件传输**。
- **前提**：所有设备在同一个 **Tailscale tailnet** 中并运行 Tailscale 客户端。
- **核心**：一个 **Go** 库 `clipbridge-core` 承载全部业务（协议、配对、连接、发件箱、去重、文件传输）。
- **Windows**：C# **WPF** 托盘应用，通过 P/Invoke 调用 Go 编译的 `clipbridge_core.dll`。
- **Android**：**Kotlin** + Compose，通过 gomobile AAR 调用同一个 Go 核心。
- **P2P**：设备经 tailnet 地址直连（mTLS HTTPS + WebSocket，证书互相固定）。**没有 Gateway、没有 croc store、没有 WebRTC。**
- **离线**：发送方发件箱 + 每链路游标；对端上线后补发；超过 TTL 的事件丢弃并告知。

## 2. 与 v2 的变化

| 项 | v2（Gateway） | v3（当前） |
| --- | --- | --- |
| 控制面 | 自建 Gateway + WebSocket + SQLite | **无**，设备直连 |
| 业务逻辑 | Go / .NET / Kotlin 各实现一份 | **仅 Go 核心一份** |
| Windows | WPF 实现协议 | WPF 只做 UI/剪贴板，调用 Go DLL |
| Android | Kotlin 实现协议与加密 | Kotlin 只做 UI/服务，调用 Go AAR |
| 文件 | croc store 中转（Android 先走浏览器） | 设备间直传，HTTP Range 断点，SHA-256 |
| P2P | 明确排除 | **默认形态**（依赖 tailnet） |
| 服务运维 | TLS、域名、Docker、备份 | **无** |
| 预计周期 | 8–11 周 | 约 7.5–9 周（估计） |
| 新的硬前提 | 无 | **必须有 Tailscale**；Android 同时只能一个 VPN |

v2 全套文档归档在 [archive-v2/](archive-v2/)。

## 3. 最终形态

```text
Windows (WPF + clipbridge_core.dll)  ◄── tailnet / mTLS HTTPS + WebSocket ──►  Android (Kotlin + core AAR)
   监听 <tailnet-ip>:47821                                                       只出站拨号，不监听
```

数据搬运总由**拨号方**发起：Android 拉取 Windows 的文件，或在 Windows 同意后推送文件给 Windows。

## 4. 产品目标

1. Windows 与 Android 之间同步短文本剪贴板。
2. 事件定向到已配对设备，不无差别覆盖剪贴板。
3. Windows 托盘常驻、开机启动、剪贴板捕获、手动/自动应用策略。
4. Android 分享、快捷设置磁贴、通知动作；不依赖后台读剪贴板。
5. 双向文件传输，支持断点续传与完整性校验。
6. Windows CLI 与本地命名管道，便于集成。
7. 无服务端、无云依赖。

## 5. 非目标

- iOS、macOS、Linux 客户端。
- Bark、APNs、Gateway（MVP）、croc store（MVP）、WebRTC、libp2p、tsnet、局域网发现。
- Android 后台读取剪贴板、AccessibilityService、FCM。
- 无 Tailscale 环境下的使用。
- 多租户、公网 SaaS、团队权限。
- 电脑关机/休眠时的离线投递（需要后续的中继方案，见 ADR-005）。

## 6. 平台能力判断（不变）

| 能力 | Windows | Android | 结论 |
| --- | --- | --- | --- |
| 读取剪贴板 | `WM_CLIPBOARDUPDATE` 监听 | 仅前台/默认输入法等受限场景 | Windows 自动；Android 用户触发 |
| 写入剪贴板 | 托盘应用可写 | 前台或用户动作下可靠 | 默认通知 + 手动应用 |
| 后台连接 | 托盘应用 | 前台服务（可选实时模式） | 用户显式开启 |
| 离线补齐 | 发件箱 + 游标 | 同左 | 受 TTL 限制 |

## 7. 技术选型

| 层 | 选型 | 理由 |
| --- | --- | --- |
| 核心 | Go 1.27.x，SQLite（modernc，纯 Go） | 一份实现，TLS/HTTP/WS 成熟，交叉编译容易 |
| Windows 壳 | .NET 10 (LTS) + WPF | 托盘/剪贴板/DPAPI 成熟；P/Invoke 调用 DLL |
| Windows 绑定 | Go `c-shared` DLL，5 个 C 函数 | 稳定 ABI，无回调 |
| Android 壳 | Kotlin + Jetpack Compose | 前台服务、通知、分享、MediaStore |
| Android 绑定 | gomobile AAR | 字符串/整型/fd 门面 |
| 传输 | TLS 1.3 mTLS，HTTPS + WebSocket | 证书指纹固定；Range 断点 |
| 网络 | 系统 Tailscale | 直连/NAT 穿透/加密路径 |

## 8. 文档索引

| 文档 | 内容 |
| --- | --- |
| [user-guide.md](user-guide.md) | **使用说明书**：现在能做什么、安装、界面、更新、排错、计划中的用法 |
| [update.md](update.md) | 自动更新与本机分发：信任模型、发布流程 |
| [../HANDOFF-20261026-220002.md](../HANDOFF-20261026-220002.md) | **最新交接快照**：自动广播默认层、密码库 V0 结论、0.1.30 发布状态、待决事项与下一步 |
| [../HANDOFF.md](../HANDOFF.md) | **交接文档**：当前实现状态、验证矩阵、决策记录、详细下一步 |
| [feasibility-analysis.md](feasibility-analysis.md) | v3 可行性、开源调研结论、PoC 与风险 |
| [architecture.md](architecture.md) | 组件、DLL/AAR 边界、配对与信任、事件与序号、文件传输、故障降级 |
| [api-spec.md](api-spec.md) | 信封、配对/WebSocket/文件端点、核心门面方法与事件、命名管道、CLI |
| [development-plan.md](development-plan.md) | 阶段、PoC Go/No-Go、验收、测试矩阵、风险 |
| [vault-v0-poc-report.md](vault-v0-poc-report.md) | 密码库 V0 验证：gokeepasslib（KDBX 4）实测结果、缺陷与补丁清单 |
| [plan-vault.md](plan-vault.md) | **计划**：集成密码/凭证管理（KDBX 4、备份/恢复、多设备同步、自动填充）可行性评估与分阶段方案 |
| [plan-auto-broadcast.md](plan-auto-broadcast.md) | **计划**：复制即自动广播到所有已配对设备（可行性、Android 限制、分阶段方案）与 Syncthing 评估 |
| [schemas/event.schema.json](schemas/event.schema.json) | 事件信封 JSON Schema |
| [adr/005-go-core-and-tailnet-p2p.md](adr/005-go-core-and-tailnet-p2p.md) | **当前核心决策** |
| [adr/003-windows-android-scope.md](adr/003-windows-android-scope.md) | 客户端范围收敛为 Windows + Android |
| [adr/004-gateway-long-connection.md](adr/004-gateway-long-connection.md) | 已被 ADR-005 取代 |
| [adr/001-control-plane-and-data-plane.md](adr/001-control-plane-and-data-plane.md) | 旧方案，仅保留历史 |
| [adr/002-triggered-clipboard-sync.md](adr/002-triggered-clipboard-sync.md) | 旧方案，原则仍有效 |
| [archive-v2/](archive-v2/) | v2 Gateway 方案全套文档 |

## 9. 推荐的首个可运行闭环

```text
Windows 用户复制文本
  → WPF 读取 → cb_call(clip.local_changed)
  → Go 核心：hash / 回声检查 / 发件箱 / 序号
  → mTLS WebSocket → Android Go 核心 → 去重/校验 → 本地通知
  → 用户点"复制" → Android 写剪贴板 → clip.mark_applied → ack(applied)
  → Windows 记录状态
```

文件闭环：

```text
Windows 选择文件 → 核心计算 SHA-256 → file offer(pull)
  → Android 接受 → GET /v1/files/{id} (Range) → 临时文件 → 校验
  → Kotlin 经 MediaStore 保存到 Downloads
```

## 10. 范围边界

### 支持

- Windows 10/11 x64（MVP 以 Windows 11 为主）；Android 10–15。
- 所有设备在同一 tailnet 且 Tailscale 在线。

### 不支持

- iOS、macOS、Linux 客户端；任何服务端组件。
- Android 后台自动读剪贴板。
- 无 Tailscale、或 Android 被其它 VPN 占用的场景。

## 11. 下一步

1. 按 [development-plan.md §13](development-plan.md) 执行第一周：搭仓库与 CI，打通 DLL + P/Invoke 与 AAR + 真机 Tailscale 拨号。
2. 通过 P-A…P-E 后再做配对与事件链路；P-A 或 P-B 失败则停下重审。
3. 先文本，再文件；先手动应用，再评估自动应用。
4. 在 Windows 11 + Android 真机上验证剪贴板、后台、通知和断线补齐。
