# ClipBridge 可行性分析（Windows + Android）

> 范围：Windows 客户端 + Android 客户端 + 自建 Gateway + croc store 文件数据面。  
> 明确不支持：iOS、iPadOS、macOS、Linux 客户端。  
> 日期：2026-10-26。

## 1. 结论

**新范围可行，而且比原来的 Bark/iOS 方案更收敛。**

关键判断：

| 能力 | 结论 | 说明 |
| --- | --- | --- |
| Windows 剪贴板监听与写入 | 可行 | 托盘应用可监听、读、写；需处理循环和覆盖 |
| Android 剪贴板读取 | 受限 | Android 10+ 通常只允许前台/默认输入法读取 |
| Android 剪贴板写入 | 部分可行 | 前台或用户动作下可靠；后台写入不作保证 |
| Windows ↔ Android 实时文本事件 | 可行 | Gateway + WebSocket + 前台服务/托盘应用 |
| Android 后台实时连接 | 可选可行 | 前台服务 + 常驻通知；受 OEM/省电策略影响 |
| 离线补齐 | 可行 | Gateway 保存加密事件 + 每设备游标 + ack |
| 文件传输 | 可行 | croc store；Windows CLI/App，Android 浏览器优先 |
| Android 原生 store 接收 | 可行但需开发 | 浏览器可作为首版降级 |
| Windows 安装与开机启动 | 可行 | WPF 单文件/安装器；无需商店 |
| 通用 Windows 集成 | 可行 | CLI + 命名管道 + JSON |
| 全自动后台剪贴板同步 | 不可承诺 | Android 平台限制；只能做可选实时模式和用户动作 |
| 放弃 Bark/APNs | 正确 | Bark 是 iOS/APNs 方案，不能服务 Android/Windows |

## 2. 为什么必须放弃 Bark 作为运行依赖

Bark 的链路是：

```text
Bark iOS App ← APNs ← Bark Server
```

它不能直接触达：

- Android App。
- Windows 托盘应用。
- 没有 Apple 设备的用户。

Bark 的以下能力也没有跨平台价值：

- iOS Notification Service Extension。
- `UIPasteboard` 自动复制。
- App Intent / iOS URL scheme。
- Bark key/APNs token。
- App Store 分发。

因此：

- Bark 不再作为控制面或客户端基础。
- APNs、通知扩展、iOS 剪贴板策略从计划中移除。
- 控制面改为自建 Gateway；这是本轮最大的架构变化。

## 3. Windows 端可行性

### 3.1 剪贴板

Windows 桌面应用可以：

- 使用 `AddClipboardFormatListener`/`WM_CLIPBOARDUPDATE` 监听变化。
- 使用 `GetClipboardSequenceNumber` 判断剪贴板版本。
- 读取/写入文本、HTML、位图、文件列表。
- 在用户会话内运行托盘应用并随登录启动。

风险：

- 监听 → 发送 → 远端写回 → 再次监听，可能形成回声循环。
- 用户可能在同一秒内复制新内容。
- 某些应用写剪贴板格式较慢，读取可能失败。
- 远程桌面/多会话、UAC 边界需要测试。

缓解：

- 记录 `content_hash`、剪贴板 sequence、最后应用的远程 event ID。
- 远端应用后设置短暂抑制窗口。
- 只处理明确支持的格式（首版 `text/plain`）。
- 读取失败时重试 1–2 次，之后放弃，不发送空内容。
- 默认按频道启停，不做全系统无差别广播。

### 3.2 后台运行

Windows 托盘应用可以常驻并维持 WebSocket。可行，但需处理：

- 开机启动。
- 休眠/唤醒后的重连。
- 网络切换。
- 单实例锁。
- 安全存储 token（DPAPI）。

Windows 不需要 WNS 来完成 MVP；托盘应用在线时即可实时收发。

## 4. Android 端可行性

### 4.1 剪贴板读取限制

Android 10+ 对剪贴板读取有明显限制：通常只有获得焦点的前台 App 或默认输入法可以读取。后台服务并不等于可以任意读取剪贴板。

因此：

- 不做后台剪贴板监听。
- 不使用 `AccessibilityService` 绕过限制。
- 读取入口限定为：
  - App 内“发送当前剪贴板”按钮。
  - 分享面板（用户主动分享文本）。
  - 通知动作打开 App 后读取。
  - 快捷设置磁贴启动一个短暂 Activity 后读取。
- 需要明确系统可能弹出剪贴板访问提示。

### 4.2 剪贴板写入限制

写剪贴板比读取宽松，但后台写入仍可能受系统版本/OEM 影响，且用户可能认为被“偷偷覆盖”。

因此：

- 默认收到事件只发本地通知。
- 用户点击“复制”后写入。
- 用户明确开启“实时模式”且 App/前台服务处于允许状态时，才可尝试自动写入。
- 自动写入必须有：
  - 事件 ID 去重。
  - 频道/目标校验。
  - TTL 校验。
  - 用户未在本地复制新内容的检查（App 内状态）。
  - 失败回退通知。

### 4.3 后台连接

Android 的实时模式可采用前台服务：

- 常驻通知（必须、透明）。
- 维持 WebSocket。
- 收到事件后发本地通知。
- 用户可随时关闭。
- 省电模式和 OEM 后台限制可能导致断线；断线后由 Gateway 游标补齐。

不把 FCM 作为首版硬依赖：

- 避免 Google Play Services 依赖和国内可用性问题。
- 避免引入 FCM 服务端密钥和额外隐私边界。
- 后续可作为可选加速通道，不作为唯一消息源。

### 4.4 文件接收

首版：

- Android 收到文件事件后打开浏览器链接。
- croc store 页面负责 manifest 解密和下载。
- 用户可以在浏览器保存文件，再回到 ClipBridge 查看事件状态。

后续：

- Kotlin 原生 `CrocStoreClient`：
  - HKDF-SHA-256。
  - AES-256-GCM。
  - manifest/chunk。
  - claim/commit。
  - SHA-256 校验。
- 原生接收的难点是大文件、后台下载、存储权限和断点状态，不是密码学本身。

## 5. 控制面可行性

### 5.1 为什么需要 Gateway

没有 Bark 后，需要一个跨平台控制面：

- 设备发现与配对。
- 频道和成员。
- 事件持久化。
- 在线推送/离线补齐。
- ack 和游标。
- 文件事件引用。
- 可观测性和审计。

Gateway 可以很小：

- Go + SQLite。
- 单二进制，Docker 或 Windows 服务部署。
- 只保存密文 payload 和元数据。
- WebSocket 负责实时；REST 负责配对、管理和补拉。

### 5.2 实时性

- Windows：托盘应用常驻，WebSocket 在线时可达秒级。
- Android：前台服务开启时可达秒级；未开启时在 App 打开后补齐。
- 不承诺“永远在线”或“后台绝对实时”。
- 网络切换、休眠、代理、TLS 中断都要有重连和游标。

### 5.3 可靠性

Gateway 的事件队列可以做到：

- 事件落库后返回 `accepted`。
- 目标设备通过 WebSocket 收到。
- 设备发送 ack。
- 未 ack 的事件在重连后重新投递。
- 重复投递由客户端 `event_id` 去重。

这比 APNs 更适合本次需求，因为：

- 有明确 ack。
- 可以查询历史。
- 不依赖 Apple/Google 推送服务。
- 可以自建和审计。

代价：

- 需要服务器和数据库。
- 需要维护 TLS、备份、限流和密钥。

## 6. croc store 文件可行性

croc store 已有：

- `croc-store-v1` 加密格式。
- 256-bit master key、HKDF、AES-256-GCM。
- 4 MiB 分块。
- manifest 加密。
- claim/commit 和下载次数。
- 浏览器和 CLI 接收。
- 断点续传。
- 服务端只存密文。

适用性：

- Windows 通过 Go `storeclient` 上传/下载很直接。
- Android 首版浏览器接收成本最低。
- Android 原生接收需要 Kotlin 实现，但可行。
- store 的 1 GiB 默认上限、5 GiB 总配额、100 文件上限需要 UI 明示。
- 文件链接是 bearer secret；只能在加密事件或安全渠道中传递。

## 7. 推荐方案

### 7.1 MVP

```text
Windows WPF 托盘应用
  - 监听剪贴板
  - 选择频道/目标
  - WebSocket 发事件
  - 收到事件后通知或写入

Android Kotlin App
  - 分享/按钮/通知动作发送
  - 前台服务接收（可选）
  - 通知 + 手动复制
  - 文件事件打开浏览器

Go Gateway
  - 配对/设备/频道
  - 密文事件队列
  - WebSocket 分发
  - ack/游标
  - SQLite

croc store
  - 文件密文与能力 token
```

### 7.2 后续增强

- Android 原生 store 客户端。
- Windows 文件发送 UI。
- Android 快捷设置磁贴完善。
- Windows toast 动作。
- 可选的 FCM/WNS 适配（仅作为“有新事件”的唤醒提示，仍以 Gateway 状态为准）。
- 局域网直连（仅在同一 LAN 且用户明确开启时）。

## 8. PoC 验证项

### P0：Gateway 与事件

- [ ] Windows/Android 能配对并拿到设备 token。
- [ ] 两端 WebSocket 连接稳定。
- [ ] 事件落库、转发、ack、重连补齐。
- [ ] 相同 `event_id` 重发不重复应用。

### P1：Windows → Android

- [ ] Windows 复制文本后生成事件。
- [ ] Android 前台服务收到并显示通知。
- [ ] 用户点击复制后写入 Android 剪贴板。
- [ ] 目标/频道不符不显示内容、不写入。
- [ ] Android 锁屏/后台/断网后重连补齐。

### P2：Android → Windows

- [ ] Android 分享/按钮发送文本。
- [ ] Windows 托盘应用收到并写入剪贴板。
- [ ] Windows 不把远端写入再次当成本地复制广播。
- [ ] 用户新复制内容不被旧事件覆盖。

### P3：文件

- [ ] Windows 上传 1 MiB/100 MiB 文件。
- [ ] Android 浏览器接收并校验。
- [ ] 文件事件过期/撤销提示正确。
- [ ] token 不出现在普通日志。
- [ ] 断网重试不重复消耗下载次数。

### P4：体验

- [ ] Windows 开机启动、托盘、单实例。
- [ ] Android 前台服务常驻通知、退出、省电设置。
- [ ] 通知权限被拒绝时的降级。
- [ ] 中文/英文文本、换行、Emoji、长文本。
- [ ] 1000 条事件收件箱性能。

## 9. 风险

| 风险 | 概率 | 影响 | 缓解 |
| --- | --- | --- | --- |
| Android 后台读取剪贴板不可行 | 高 | 高 | 只做用户触发；前台服务仅接收 |
| Android 后台写入不稳定 | 中 | 中 | 默认通知动作；自动写入可选 |
| OEM 杀前台服务 | 高 | 中 | 游标补齐；引导关闭省电限制 |
| Windows 剪贴板回声循环 | 中 | 高 | sequence/hash/suppress window |
| 用户剪贴板被覆盖 | 中 | 高 | 默认手动；可配置自动；撤销/恢复 |
| Gateway 单点故障 | 中 | 高 | 自建备份、健康检查、SQLite 备份、后续 PostgreSQL |
| TLS/token 泄露 | 低/中 | 高 | DPAPI/Keystore、TLS、日志脱敏、token 轮换 |
| croc store 配额/过期 | 中 | 中 | UI 明示、自建、重试、重新上传 |
| Android 原生 store 复杂 | 中 | 中 | 浏览器优先；分阶段实现 |
| 多 OEM 通知/权限差异 | 高 | 中 | 明确测试基线；降级提示 |
| 长连接耗电 | 中 | 中 | 用户可选实时模式；空闲退避；不使用轮询 |
| 依赖 Go/Node/Go 1.27 工具链 | 低 | 低 | 固定版本、CI 构建 |

## 10. 成本与周期

假设 1 名 Windows/.NET、1 名 Android、1 名 Go/后端，测试设备可用：

| 阶段 | 估计 |
| --- | --- |
| Phase 0：Gateway + 双端骨架 + 文本闭环 | 1–2 周 |
| Phase 1：Windows→Android 文本、去重、通知/复制 | 2 周 |
| Phase 2：Android→Windows 文本、剪贴板回写 | 1–2 周 |
| Phase 3：文件上传/浏览器接收 | 2 周 |
| Phase 4：Android 原生 store、打包、稳定性 | 2–3 周 |
| 合计 MVP（0–4） | 8–11 周 |

不含长期运维、应用商店审核（若上架）、品牌设计和多租户。

## 11. Go/No-Go

进入正式开发前必须满足：

- [ ] 两端能通过 Gateway 配对和收发事件。
- [ ] Android 前台服务断线后能补齐。
- [ ] Windows 复制不会因为远端回写形成循环。
- [ ] Android 目标/频道不符不写入。
- [ ] 用户新复制内容不会被旧事件覆盖。
- [ ] 1 MiB/100 MiB 文件可上传和接收。
- [ ] token 不出现在日志/URL query。
- [ ] 产品文案明确 Android 后台读取剪贴板不可用。

如果“Android 自动写入”不可靠，则保留通知 + 手动复制；这不影响核心可行性。

## 12. 最终建议

- 立即删除 Bark/APNs/iOS 相关开发和测试路径。
- 把 Gateway 作为正式组件，而不是可选增强。
- 首发只支持 Windows 11 + Android 10–15 的文本同步。
- 文件先走 croc store + 浏览器接收。
- 发布 Windows 安装器、Android APK/AAB 和 Gateway Docker 包。
- 把 Linux/macOS 客户端明确列为不支持，避免范围反弹。
