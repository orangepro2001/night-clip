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
- Android 后台读取剪贴板（**默认**；可选的显式授权进阶方案见 [ADR-006](adr/006-android-background-read.md)）、AccessibilityService、FCM。
- 无 Tailscale 环境下的使用。
- 多租户、公网 SaaS、团队权限。
- 电脑关机/休眠时的离线投递（需要后续的中继方案，见 ADR-005）。

## 6. 平台能力判断（不变）

| 能力 | Windows | Android | 结论 |
| --- | --- | --- | --- |
| 读取剪贴板 | `WM_CLIPBOARDUPDATE` 监听 | 默认仅前台/默认输入法；可选 `READ_LOGS` + 悬浮窗进阶 | Windows 自动；Android 默认用户触发 |
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
| [release-runbook.md](release-runbook.md) | **发版操作说明**：预检、发布到 release-publish、桥接旧源、真机验证、常见问题 |
| [release-publish-integration.md](release-publish-integration.md) | 接入本机 release-publish 的可复用经验（决策、SDK 缺口、迁移、测试、清单） |
| [handoff-2026-10-28-release-publish.md](handoff-2026-10-28-release-publish.md) | 更新分发迁移的交接与上下文恢复 |
| [handoff-2026-10-28-macos.md](handoff-2026-10-28-macos.md) | macOS 客户端：架构（共用 Windows 逻辑层）、Linux 上构建/发布、部署到 Mac、验证清单、限制 |
| [handoff-2026-10-28-android-media-send.md](handoff-2026-10-28-android-media-send.md) | Android 发送图片/视频与拍照并发送：行为、改动、验证、待真机确认 |
| [ops-python-native-memory-bloat.md](ops-python-native-memory-bloat.md) | 运维记录：Python + 原生库常驻进程的 glibc arena 内存膨胀（7.8 GiB → 5.1 GiB），诊断方法与陷阱 |
| [../HANDOFF-20261027-0835.md](../HANDOFF-20261027-0835.md) | **最新交接快照**：写前 begin_apply 剪贴板回声修复、0.1.76 发布 |
| [../HANDOFF-20261027-0805.md](../HANDOFF-20261027-0805.md) | 上一轮快照：Android 底部导航 inset 修复、0.1.74 发布 |
| [../HANDOFF-20261027-0745.md](../HANDOFF-20261027-0745.md) | 更早快照：Android/Windows 紧凑专业工具风 UI、0.1.73 发布 |
| [../HANDOFF-20261027-0230.md](../HANDOFF-20261027-0230.md) | 更早快照：密码库列表点按复制/独立查看编辑、0.1.66 发布 |
| [../HANDOFF-20261027-0215.md](../HANDOFF-20261027-0215.md) | 更早快照：密码库空闲/后台/熄屏锁定与生物解锁有效期可定制、0.1.65 发布 |
| [../HANDOFF-20261027-0205.md](../HANDOFF-20261027-0205.md) | 更早快照：空闲自动锁定/生物解锁有效期、0.1.64 发布 |
| [../HANDOFF-20261027-0145.md](../HANDOFF-20261027-0145.md) | 更早快照：Android 文件选择器崩溃根因修复、0.1.63 发布 |
| [../HANDOFF-20261027-0130.md](../HANDOFF-20261027-0130.md) | 更早快照：敏感内容识别可关闭、0.1.62 发布 |
| [../HANDOFF-20261027-0115.md](../HANDOFF-20261027-0115.md) | 更早快照：密码库默认全量副本/无中心节点、Android 文件选择器加固、0.1.61 发布 |
| [../HANDOFF-20261027-0037.md](../HANDOFF-20261027-0037.md) | 更早快照：密码库防截屏改为可选、0.1.60 发布 |
| [../HANDOFF-20261027-0018.md](../HANDOFF-20261027-0018.md) | 更早快照：密码库 V1–V3 合并、后台自动发送、0.1.59 发布 |
| [../HANDOFF-20261026-220002.md](../HANDOFF-20261026-220002.md) | 更早快照：自动广播默认层、密码库 V0 结论、0.1.30 发布 |
| [../HANDOFF.md](../HANDOFF.md) | **交接文档**：当前实现状态、验证矩阵、决策记录、详细下一步 |
| [feasibility-analysis.md](feasibility-analysis.md) | v3 可行性、开源调研结论、PoC 与风险 |
| [architecture.md](architecture.md) | 组件、DLL/AAR 边界、配对与信任、事件与序号、文件传输、故障降级 |
| [api-spec.md](api-spec.md) | 信封、配对/WebSocket/文件端点、核心门面方法与事件、命名管道、CLI |
| [development-plan.md](development-plan.md) | 阶段、PoC Go/No-Go、验收、测试矩阵、风险 |
| [vault-v0-poc-report.md](vault-v0-poc-report.md) | 密码库 V0 验证：gokeepasslib（KDBX 4）实测结果、缺陷与补丁清单 |
| [ui-refresh-2026-10.md](ui-refresh-2026-10.md) | 紧凑专业工具风的 Android/Windows UI 改动说明 |
| [plan-vault.md](plan-vault.md) | **计划与实施状态**：密码/凭证管理（KDBX 4、备份/恢复、多设备同步、自动填充）；V1–V3 已合并，真机验证待做 |
| [vault-api.md](vault-api.md) | **密码库门面接口**：`vault.*` 方法、错误码、同步协议与壳层规则 |
| [adr/007-password-vault.md](adr/007-password-vault.md) | **密码库决策**：KDBX 4、本机加密、经 tailnet 在已配对设备间自动同步；每台完整副本 |
| [plan-auto-broadcast.md](plan-auto-broadcast.md) | **计划**：复制即自动广播到所有已配对设备（可行性、Android 限制、分阶段方案）与 Syncthing 评估 |
| [plan-macos.md](plan-macos.md) | **计划（路线 A 已实施，见 handoff-2026-10-28-macos.md）**：macOS 端——功能分级（完整/受限/难以做到）、UI 选型（SwiftUI 单独一份 vs Avalonia）、核心改动、签名与自更新、分阶段工期、待决策问题 |
| [plan-android-background-read.md](plan-android-background-read.md) | **计划与实施记录**：Android 后台读取剪贴板（悬浮窗取焦 + `READ_LOGS`，ClipCascade 做法） |
| [schemas/event.schema.json](schemas/event.schema.json) | 事件信封 JSON Schema |
| [adr/005-go-core-and-tailnet-p2p.md](adr/005-go-core-and-tailnet-p2p.md) | **当前核心决策** |
| [adr/006-android-background-read.md](adr/006-android-background-read.md) | **可选进阶**：Android 后台读取剪贴板的 `READ_LOGS` + 悬浮窗方案；默认关闭 |
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
- Android 后台自动发送是**可选进阶**：默认关闭，需要一次 ADB 授权 `READ_LOGS` + 悬浮窗；见 [ADR-006](adr/006-android-background-read.md)。

### 不支持

- iOS、macOS、Linux 客户端；任何服务端组件。
- **默认**不做 Android 后台读剪贴板；可选的显式授权后台读取见 [ADR-006](adr/006-android-background-read.md)。不做轮询、无障碍、输入法或 root 方案。
- 无 Tailscale、或 Android 被其它 VPN 占用的场景。

## 11. 下一步

1. 按 [development-plan.md §13](development-plan.md) 执行第一周：搭仓库与 CI，打通 DLL + P/Invoke 与 AAR + 真机 Tailscale 拨号。
2. 通过 P-A…P-E 后再做配对与事件链路；P-A 或 P-B 失败则停下重审。
3. 先文本，再文件；先手动应用，再评估自动应用。
4. 在 Windows 11 + Android 真机上验证剪贴板、后台、通知和断线补齐。
