# ClipBridge 项目自述（工作名）

> 文档版本：v2（Windows + Android 收敛版）  
> 文档状态：可行性与方案设计阶段  
> 代码状态：尚未修改上游源码；当前只完成方案和接口文档  
> 工作名：**ClipBridge**，CLI 暂定 **`cbridge.exe`**，协议名称 **`clipbridge/v1`**

## 1. 结论先行

本轮把产品范围收敛为：

- **只做 Windows 客户端和 Android 客户端之间的通信与交互。**
- **放弃 iOS 客户端**，因此 Bark、APNs、通知扩展和 App Store 相关方案不再进入运行架构。
- **放弃 Linux 和 macOS 客户端。**
- 服务端 Gateway 和 croc store 仍可部署在 Linux/Docker 或 Windows Server；它们是运维载体，不属于客户端支持范围。
- 文件传输继续使用 croc 的加密存储模式（`croc-store-v1`），但不做 iOS 内嵌、也不做 Linux CLI。

## 1.1 与上一版相比的变化

| 项 | 上一版 | 当前版 |
| --- | --- | --- |
| 客户端平台 | iOS + 多平台 CLI | **仅 Windows + Android** |
| 控制面 | Bark + APNs | **ClipBridge Gateway + WebSocket** |
| iOS 扩展/Intent/App Store | 计划中 | **删除** |
| Linux/macOS 客户端 | 计划中/开放 | **删除** |
| Windows 客户端 | 未明确 | **WPF 托盘应用** |
| Android 客户端 | 未明确 | **Kotlin/Compose App** |
| CLI | `barkx` 概念 | **仅 Windows `cbridge.exe`** |
| 文件数据面 | croc store | **保持 croc store** |
| 预计 MVP | 6–10 周 | **8–11 周**（新增 Gateway，但删掉 iOS） |

最终形态：

```text
Windows 托盘应用（.NET/WPF）
        │  WebSocket + HTTPS
        ▼
ClipBridge Gateway（Go）
  - 设备配对 / 频道 / 事件队列
  - 密文存储 / 游标 / ack
        ▲
        │  WebSocket + HTTPS
        │
Android App（Kotlin / Compose）
  - 前台服务（可选实时模式）
  - 分享 / 快捷设置磁贴 / 通知动作
  - 文件接收与打开

文件数据面：
Windows / Android ── HTTPS ── croc store（croc-store-v1）
```

## 2. 为什么放弃 Bark

Bark 的核心链路是：

```text
Bark App（iOS） ← APNs ← Bark Server
```

它无法直接服务 Android 或 Windows：

- APNs 只覆盖 Apple 平台。
- Bark 的通知扩展、剪贴板自动复制和 App Intent 都是 iOS 专属。
- Bark key 只解决 Bark 设备注册，不是跨平台身份或消息队列。
- 继续保留 Bark 会迫使系统围绕 iOS 设计，和当前目标冲突。

因此 Bark 仓库仅作为前一版方案参考保留，**不进入当前产品的运行依赖**。当前控制面改为自建 **ClipBridge Gateway**。

## 3. 产品目标

1. Windows 与 Android 之间同步短文本剪贴板。
2. 事件必须定向到频道和设备，不能因为“收到了推送”就无差别覆盖剪贴板。
3. Windows 支持托盘常驻、开机启动、剪贴板变化捕获、手动/自动应用策略。
4. Android 支持 App 内操作、分享入口、快捷设置磁贴和通知动作；不依赖后台偷读剪贴板。
5. 文件通过 croc store 加密中转，支持 Windows 上传、Android 接收，以及反向路径。
6. 提供 Windows CLI 和本地 IPC，方便其他 Windows 应用集成。
7. 只支持 Windows + Android，减少 UI、构建、测试和发布矩阵。

## 4. 非目标

- 不支持 iOS、iPadOS、macOS、Linux 客户端。
- 不做 APNs、Bark 通知扩展、iOS App Intent 或 App Store 适配。
- 不承诺 Android 在后台自动读取剪贴板；Android 10+ 对此有严格限制。
- 不做 Android AccessibilityService 绕过系统剪贴板限制。
- 不在首版做 croc live P2P；只使用 croc store 的 HTTPS 分块 API。
- 不做 Windows WNS 或 Android FCM 作为强制依赖；首版以 Gateway 长连接为准。
- 不做局域网 mDNS 直连、多人协作、公网 SaaS 多租户。
- 不发布 Linux CLI；即使 Go 代码可交叉编译，也不提供 Linux 支持承诺。

## 5. 平台能力判断

| 能力 | Windows | Android | 结论 |
| --- | --- | --- | --- |
| 读取剪贴板 | 可监听 `WM_CLIPBOARDUPDATE` | 仅前台/默认输入法等受限场景可读 | Windows 可自动捕获；Android 必须用户触发 |
| 写入剪贴板 | 托盘应用可写 | 前台或用户动作下可靠；后台写入不作保证 | 默认通知动作/手动应用 |
| 后台常驻 | 托盘应用 + 开机启动 | 前台服务 + 常驻通知（可选） | 实时模式需用户明确开启 |
| 实时消息 | WebSocket | WebSocket（前台服务） | 由 Gateway 分发 |
| 离线补齐 | Gateway 游标 | Gateway 游标 + 打开 App 时补齐 | 不依赖系统推送 |
| 文件传输 | CLI/App + croc store | 浏览器优先，后续原生 store | 首版可用浏览器 |
| 通用集成 | CLI、命名管道、JSON | Intent、分享、通知动作 | 不提供 Linux/macOS SDK |

## 6. 核心设计

### 6.1 控制面

ClipBridge Gateway 是唯一控制面：

- 设备配对和访问 token。
- 频道与设备成员。
- 事件接收、存储、路由和去重。
- 每设备/频道游标。
- ack、离线补齐、WebSocket 推送。
- 只保存事件元数据和 `payload_ciphertext`，不保存明文剪贴板或文件内容。

### 6.2 数据面

文件使用 croc store：

- Windows 上传：`cbridge file send` 或托盘应用调用 CLI。
- Android 接收：首版打开 croc 浏览器链接；后续原生 store 客户端。
- 文件事件只携带 transfer ID、token、URL、大小和过期时间。
- 密钥只在加密 payload 内传递。

### 6.3 防污染

每条事件必须携带：

```text
event_id
channel_id
source_device_id
target_device_ids
sequence
created_at
expires_at
content_hash
sensitive
payload
```

接收端必须完成：

1. 频道和成员校验。
2. 本机在目标列表。
3. 来源不是本机。
4. 事件未过期。
5. `event_id` 未处理。
6. `content_hash` 与 payload 匹配。
7. sequence 连续。
8. 剪贴板未被用户更新（Windows 用 clipboard sequence/hash，Android 用应用内状态和用户动作）。
9. 策略允许自动应用。

## 7. 技术选型

| 层 | 选型 | 理由 |
| --- | --- | --- |
| Windows App | .NET 8 + WPF | 成熟、托盘/剪贴板/单文件发布简单，不依赖商店 |
| Android App | Kotlin + Jetpack Compose | 原生后台服务、通知、分享和目标 API |
| CLI | Go | 与 Gateway、croc storeclient 复用，单文件 `cbridge.exe` |
| Gateway | Go + SQLite（首版） | 单二进制、易部署；后续可换 PostgreSQL |
| 实时传输 | WebSocket over TLS | 双向、低延迟、可重连、无需 App Store/推送服务 |
| 文件传输 | croc store | 已有加密、分块、断点、claim/commit |
| 本地集成 | 命名管道 + JSON Lines | Windows 其他应用可调用 CLI 或 IPC |

## 8. 文档索引

| 文档 | 内容 |
| --- | --- |
| [feasibility-analysis.md](feasibility-analysis.md) | 新范围下的可行性、平台限制、PoC 和风险 |
| [architecture.md](architecture.md) | Windows/Android/Gateway/croc 的模块、数据流和安全模型 |
| [development-plan.md](development-plan.md) | 收敛后的阶段、里程碑、验收和测试矩阵 |
| [api-spec.md](api-spec.md) | 事件信封、Gateway REST/WebSocket、CLI、Windows IPC、Android Intent、croc store API |
| [schemas/event.schema.json](schemas/event.schema.json) | 解密后事件 payload 的 JSON Schema |
| [schemas/shipment.schema.json](schemas/shipment.schema.json) | Gateway 传输的加密 shipment Schema |
| [adr/003-windows-android-scope.md](adr/003-windows-android-scope.md) | 放弃 iOS/Linux/macOS 客户端的决策 |
| [adr/004-gateway-long-connection.md](adr/004-gateway-long-connection.md) | 用 Gateway 长连接替代 Bark/APNs 的决策 |
| [adr/001-control-plane-and-data-plane.md](adr/001-control-plane-and-data-plane.md) | 旧方案，已 Superseded，仅保留历史 |
| [adr/002-triggered-clipboard-sync.md](adr/002-triggered-clipboard-sync.md) | 旧方案，原则仍有效，平台部分已 Superseded |

## 9. 推荐的首个可运行闭环

```text
Windows 用户复制文本
  → 托盘应用计算 hash，生成事件
  → 通过 WebSocket 发给 Gateway
  → Gateway 落库并转发给目标 Android
  → Android 前台服务收到事件
  → 显示通知（默认不直接覆盖剪贴板）
  → 用户点击“复制”或打开 App 后写入
  → Android ack
  → Windows 记录状态
```

文件闭环：

```text
Windows 选择文件
  → cbridge file send 上传 croc store
  → file event 经 Gateway 发给 Android
  → Android 打开浏览器链接或原生客户端接收
  → 校验 SHA-256 / commit
```

## 10. 范围边界

### 支持

- Windows 10/11 x64（MVP 以 Windows 11 为主）。
- Android 10–15（以 Google/AOSP 和一台主流 OEM 为测试基线）。
- 自建 Gateway。
- 自建或公共服务 croc store。
- Windows CLI 与本地 IPC。
- Android 分享、快捷设置、通知动作。

### 不支持

- iOS、macOS、Linux 桌面客户端。
- APNs/Bark。
- Android 后台自动读取剪贴板。
- 无网关的纯 P2P 同步。
- Windows Store / Google Play 强制依赖。
- 企业多租户、审计合规套件。

## 11. 下一步

1. 确认 Gateway 是必须组件，不再尝试基于 Bark 的推送链路。
2. 按 Phase 0 完成 Gateway + Windows + Android 三端最小闭环。
3. 先做文本，再做文件；先手动应用，再评估自动应用。
4. 在 Windows 11 + Android 真机上验证剪贴板、后台、通知和断线补齐。
5. 通过 PoC 后进入 Phase 1；未通过则收缩为“手动同步工具”，不承诺实时后台。
