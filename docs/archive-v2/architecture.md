# ClipBridge 架构设计（Windows + Android）

> 目标平台：Windows 客户端、Android 客户端、Go Gateway、croc store。  
> 明确不支持：iOS、macOS、Linux 客户端。  
> 本文是实现目标，不代表已经完成。

## 1. 设计原则

1. **只做两个客户端平台**：Windows 和 Android；不把 Linux/macOS/iOS 作为兼容目标。
2. **Gateway 是控制面**：事件、频道、设备、游标、ack 都在 Gateway 中定义；不再依赖 Bark/APNs。
3. **文件字节不进 Gateway**：文件走 croc store，Gateway 只传引用。
4. **事件显式寻址**：`channel_id` + `target_device_ids` + `event_id` + `sequence` + TTL。
5. **默认不覆盖剪贴板**：Windows 可配置自动应用；Android 默认通知 + 手动复制。
6. **端到端加密 payload**：Gateway 只保存 `payload_ciphertext` 和路由元数据。
7. **离线可恢复**：设备游标、未 ack 重投、客户端幂等。
8. **范围可验证**：每个模块都有明确“不做什么”。

## 2. 组件

```text
┌──────────────────────────────┐
│ Windows                      │
│  WPF 托盘应用                 │
│  - Clipboard watcher          │
│  - WebSocket client           │
│  - Tray/toast                 │
│  - Named pipe IPC             │
│  - cbridge.exe integration    │
└──────────────┬───────────────┘
               │ HTTPS/WSS
               ▼
┌──────────────────────────────┐       ┌────────────────────────┐
│ ClipBridge Gateway (Go)       │       │ croc store             │
│  - pairing/auth               │       │ - croc-store-v1        │
│  - devices/channels           │       │ - encrypted manifest   │
│  - event queue                │       │ - chunks + claim/commit│
│  - WebSocket fanout           │       └───────────┬────────────┘
│  - ack/cursors                │                   │
│  - SQLite                     │                   │
└──────────────┬───────────────┘                   │
               │ HTTPS/WSS                         │ HTTPS
               ▼                                   │
┌──────────────────────────────┐                   │
│ Android (Kotlin/Compose)      │───────────────────┘
│  - App UI                     │
│  - Foreground sync service    │
│  - Share / QS tile / actions  │
│  - Notification               │
│  - Browser/native store       │
└──────────────────────────────┘
```

### 2.1 Windows 客户端

建议技术栈：**.NET 8 + WPF**。

职责：

- 托盘常驻、开机启动、单实例。
- 监听 `WM_CLIPBOARDUPDATE`。
- 读取/写入文本剪贴板。
- 捕获后按频道策略发送事件。
- 接收事件后显示 toast/tray 通知，按策略应用。
- 管理设备、频道、目标、密钥和安全存储。
- 文件发送 UI（调用 `cbridge.exe` 或内置 store 客户端）。
- 提供本地命名管道 API 给其他 Windows 应用。
- 显示连接状态、待同步数量、错误和诊断。

首版格式：

- `text/plain` UTF-8。
- 后续可增加 HTML、图片、文件列表。

Windows 剪贴板循环防护：

```text
本地复制
  → WM_CLIPBOARDUPDATE
  → 读取文本 + GetClipboardSequenceNumber
  → hash
  → 若 hash == last_remote_applied_hash：视为远端回声，不发送
  → 否则发送事件，记录 last_local_sent_hash

远端应用
  → 设置 suppress_until / suppress_hash
  → 写入剪贴板
  → 更新 last_remote_applied_hash
  → 忽略下一次相同 hash 的变化
```

窗口/进程退出时释放剪贴板监听器；休眠唤醒后重新注册并重新同步状态。

### 2.2 Android 客户端

建议技术栈：**Kotlin + Jetpack Compose**，`minSdk` 26 或 28（Phase 0 决定）。

职责：

- App 内发送/接收文本。
- 通过分享面板接收其他 App 的文本。
- 通过快捷设置磁贴/通知动作触发“读取剪贴板”。
- 可选前台服务维持 WebSocket。
- 收到事件发本地通知；用户点击复制。
- 文件事件打开 croc 浏览器链接；后续原生 store。
- 设备/频道/策略管理。
- 使用 Android Keystore 保存 token/密钥。

明确不做：

- 不使用 AccessibilityService 读取剪贴板。
- 不在后台轮询剪贴板。
- 不承诺被系统杀死后仍实时收事件。
- 不在首版集成 FCM。
- 不在首版做原生 store 客户端。

### 2.3 Gateway

建议技术栈：**Go + SQLite**。

职责：

- 设备配对与访问 token。
- 设备、频道、成员关系。
- 事件 shipment 落库。
- WebSocket 实时分发。
- 每设备/频道游标。
- ack 和未 ack 重投。
- TTL 清理。
- 限流、审计（脱敏）。
- 静态配置和健康检查。

不负责：

- 保存明文 payload。
- 保存文件字节。
- 代替客户端做剪贴板策略。
- 代替客户端验证 `content_hash` 的明文一致性（Gateway 看不到明文）。
- 做跨租户 SaaS。

### 2.4 croc store

职责：

- 保存文件密文。
- 提供 manifest/chunk API。
- claim/commit、下载次数、过期、撤销。
- 浏览器接收页面。
- 服务端只保存能力验证器和 ciphertext。

Windows 和 Android 都只通过 HTTPS 访问；不把 store token 发给 Gateway。

## 3. 身份与频道

```text
Vault
  ├── Windows device
  │     └── Channel membership
  └── Android device
        └── Channel membership
```

- `vault_id`：一组互信设备。
- `device_id`：客户端安装时生成。
- `channel_id`：业务隔离单位。
- `target_device_ids`：每次事件的实际目标。
- token/密钥按设备分发。

首版配对流程：

1. Windows 显示一次性配对码/二维码。
2. Android 扫码或输入配对码。
3. 双方交换设备名称、平台、公钥（可选）和 token。
4. Gateway 创建频道或加入现有频道。
5. 客户端安全保存 token。

配对码一次性、5 分钟有效、限流。

## 4. 事件生命周期

### 4.1 发送

```text
客户端读取内容
  → 规范化文本
  → 计算 content_hash
  → 生成 event_id / sequence / created_at / expires_at
  → 选择 channel + target
  → 用频道密钥加密 payload
  → POST /v1/events 或 WebSocket event
  → Gateway 落库并返回 accepted
  → Gateway 推送给在线目标
  → 目标 ack
```

### 4.2 接收

```text
设备收到 shipment
  → 校验 channel 成员
  → 校验本机在 target_device_ids
  → 校验 source != 本机
  → 解密 payload_ciphertext
  → 重建事件并校验 schema/content_hash
  → 校验 event_id 未处理
  → 校验 expires_at
  → 校验 sequence
  → 按平台策略：
       Windows：通知 + 可选自动应用
       Android：通知 + 手动复制（或显式实时模式）
  → 记录本地状态
  → ack Gateway
```

### 4.3 离线补齐

Gateway 保存：

- `event_id`
- `channel_id`
- `source_device_id`
- `target_device_ids`
- `sequence`
- `created_at` / `expires_at`
- `content_hash`
- `payload_ciphertext`
- `key_id`
- 每个目标设备的 ack 状态

设备重连时发送：

```json
{
  "type": "hello",
  "device_id": "dev_...",
  "cursors": [
    {"channel_id": "ch_...", "after": 41}
  ]
}
```

Gateway 返回缺失事件，或者通过 REST 补拉。

## 5. 数据模型

### 5.1 Device

```text
device_id
name
platform              windows / android
public_key            可选
access_token_hash
created_at
last_seen_at
revoked_at
```

### 5.2 Channel

```text
channel_id
name
key_id
created_at
enabled
default_ttl_seconds
```

### 5.3 Membership

```text
channel_id
device_id
role                  owner / member
joined_at
```

### 5.4 Shipment（Gateway 存储）

```text
event_id
version
kind
channel_id
source_device_id
sequence
created_at
expires_at
content_hash
sensitive
payload_ciphertext
key_id
received_at
```

### 5.5 Delivery

```text
event_id
device_id
state                 queued / sent / acked / failed / expired
attempts
last_attempt_at
acked_at
ack_result
```

### 5.6 Local Event（客户端）

```text
event_id
channel_id
source_device_id
kind
sequence
payload
created_at
expires_at
content_hash
sensitive
state                 pending / applied / ignored / expired / error
applied_at
```

### 5.7 Clipboard State（Windows/Android）

```text
last_applied_event_id
last_applied_hash
last_local_sent_hash
last_sequence_by_channel_source
suppress_until
```

## 6. 剪贴板策略

### 6.1 Windows

默认建议：

- 捕获开关：默认关闭，首次启动由用户选择。
- 频道：用户明确选择。
- 自动应用：可开启，但必须有回声抑制和版本检查。
- 敏感内容：默认不自动应用。

Windows 可以可靠地自动写剪贴板，但仍需尊重用户当前操作：

- 如果当前剪贴板 sequence 在事件到达后发生变化，不自动覆盖。
- 如果应用正在前台且用户刚复制，不自动覆盖。
- 提供“撤销/恢复上一个剪贴板”功能（本地保存有限历史）。

### 6.2 Android

默认建议：

- 只通知，不自动写。
- 用户点击“复制”后写。
- 实时模式必须由用户开启，并显示常驻通知。
- 不在后台读取剪贴板。
- 分享面板是最稳定的发送入口。
- 快捷设置磁贴/通知动作应打开一个 Activity 再读取，避免服务无焦点时读取失败。

### 6.3 策略表

| 策略 | Windows | Android |
| --- | --- | --- |
| `notify_only` | 通知，不写 | 通知，不写 |
| `manual` | 通知 + 托盘动作 | 通知动作/打开 App 后复制 |
| `auto_foreground` | 前台/托盘在线且安全检查通过 | App 前台或明确实时模式下尝试 |
| `auto_any` | 可选，风险自担 | 不推荐，不承诺 |

## 7. 加密

### 7.1 事件 payload

- 每频道 256-bit 密钥。
- `payload_ciphertext` 使用 AES-256-GCM。
- 随机 96-bit nonce，随 ciphertext 一起传输。
- AAD 绑定 `event_id`、`channel_id`、`source_device_id`、`kind`、`sequence`。
- Gateway 只看到密文和路由元数据。

### 7.2 设备认证

首版：

- 配对后分发设备 access token。
- 所有 Gateway REST/WebSocket 使用 TLS + Bearer token。
- token 哈希存 Gateway，原 token 只在配对响应出现一次。

增强：

- 每设备 Ed25519 签名。
- shipment 签名覆盖元数据和 `payload_ciphertext` 的 hash。
- 频道成员固定公钥，支持轮换。

### 7.3 本地存储

- Windows：DPAPI 加密 token/频道密钥。
- Android：Keystore + EncryptedSharedPreferences/DataStore。
- CLI：Windows DPAPI 或凭据管理器；不得明文写入配置文件。
- 日志：不记录内容、token、key、croc URL fragment。

## 8. Gateway WebSocket 协议

### 8.1 连接

```text
wss://gateway.example.com/v1/ws
Authorization: Bearer <device-token>
Sec-WebSocket-Protocol: clipbridge.v1
```

### 8.2 客户端 hello

```json
{
  "type": "hello",
  "request_id": "r1",
  "device_id": "dev_...",
  "cursors": [
    {"channel_id": "ch_...", "after": 41}
  ]
}
```

### 8.3 服务端 hello_ack

```json
{
  "type": "hello_ack",
  "request_id": "r1",
  "server_time": "2026-10-26T08:00:00Z",
  "channels": ["ch_..."],
  "max_inline_bytes": 2048
}
```

### 8.4 事件

```json
{
  "type": "event",
  "shipment": {
    "version": 1,
    "event_id": "...",
    "kind": "clipboard",
    "channel_id": "ch_...",
    "source_device_id": "dev_...",
    "target_device_ids": ["dev_..."],
    "sequence": 42,
    "created_at": "...",
    "expires_at": "...",
    "content_hash": "sha256:...",
    "sensitive": false,
    "payload_ciphertext": "base64...",
    "key_id": "k1"
  }
}
```

### 8.5 ack

```json
{
  "type": "ack",
  "event_id": "...",
  "device_id": "dev_...",
  "result": "applied",
  "reason": null,
  "acked_at": "..."
}
```

### 8.6 心跳与重连

- 客户端每 25–30 秒发送 ping，服务端回 pong。
- 断线指数退避（1s、2s、4s……上限 60s）。
- 重连必须携带游标。
- 同一事件重复投递由 `event_id` 去重。
- 收到 `goaway` 时停止发送并重连。

## 9. Android 服务设计

### 9.1 前台服务

```text
SyncForegroundService
  - 常驻通知：连接状态、当前频道、断开按钮
  - WebSocket 生命周期
  - 事件接收 → 通知/数据库
  - 心跳
  - 重连
```

启动条件：

- 用户明确开启“实时同步”。
- 通知权限已授予。
- 用户忽略电池优化（推荐但非强制）。

停止条件：

- 用户关闭开关。
- token 失效。
- 连续认证失败。
- 系统强制停止。

### 9.2 非实时模式

- App 打开时连接并拉取未读事件。
- 可用 WorkManager 做低频补拉（不承诺实时）。
- 推送依赖后续可选 FCM，不在 MVP。

### 9.3 通知

- 事件通知默认不含完整敏感内容。
- 动作：`复制`、`打开`、`忽略`。
- 前台服务通知不可静默隐藏，明确告知连接状态。

## 10. Windows 组件设计

```text
ClipBridge.Windows.exe
  ├── App (WPF)
  ├── TrayIcon
  ├── ClipboardMonitor
  ├── SyncClient (WebSocket)
  ├── LocalPipeServer
  ├── CryptoStore (DPAPI)
  ├── EventStore (SQLite)
  └── FileBridge (cbridge.exe)
```

### 10.1 本地 IPC

命名管道：

```text
\\.\pipe\clipbridge.v1
```

JSON Lines 请求/响应：

```json
{"id":"1","method":"clipboard.send","params":{"channel_id":"ch_...","text":"hello"}}
{"id":"1","ok":true,"result":{"event_id":"..."}}
```

其他 Windows 应用可以：

- 调用 `cbridge.exe`。
- 或通过命名管道与托盘应用通信。

### 10.2 开机启动

- 使用当前用户启动项/计划任务。
- 默认不请求管理员权限。
- 托盘应用单实例，重复启动时激活已有窗口。

### 10.3 安装与更新

- MVP：MSI/EXE 安装器，或自包含单文件。
- 后续：可签名 MSIX。
- 自动更新可选；首版提供手动下载。
- 不依赖 Microsoft Store。

## 11. 文件传输

### 11.1 Windows 发送

```text
用户选择文件
  → cbridge file send --channel ch_... --to dev_... file.pdf
  → croc store 上传
  → cbridge 通过本地 IPC/Gateway 发送 file event
  → 显示 transfer id/expiry
```

### 11.2 Android 接收

首版：

- 点击文件事件 → 浏览器打开 croc URL。
- 浏览器负责解密/下载。
- 返回 App 后显示“已在浏览器打开”，不虚报“已保存”。

后续：

- 原生 Kotlin store 客户端：
  - 下载 manifest。
  - claim。
  - 下载 chunk。
  - 校验。
  - 保存到 Downloads。
  - commit。

### 11.3 Android 发送 / Windows 接收

- Android 分享文件 → App 上传 store（原生 store 客户端或后台任务）→ 发文件事件。
- Windows 收到后通过 `cbridge file receive` 下载。
- 首版如果 Android 上传未完成，可只支持 Windows→Android 文件，反向作为 Phase 2/3 增量。

## 12. 故障与降级

| 故障 | 行为 |
| --- | --- |
| Gateway 暂时不可达 | Windows 托盘重连；Android 前台服务重连；事件留队列 |
| Android 服务被杀 | 打开 App 后游标补齐；通知可能延迟 |
| Windows 休眠 | 唤醒后重连并补齐 |
| WebSocket 重复投递 | `event_id` 去重 |
| 目标不符 | 忽略并记录；不写剪贴板 |
| 过期 | 不应用；显示过期状态 |
| 剪贴板被用户更新 | 不覆盖；保留事件待处理 |
| croc store 423 | 被他人 claim；提示稍后重试 |
| croc store 410/404 | 过期/撤销；提示重新发送 |
| 磁盘不足 507 | 提示清理；不自动删除其他文件 |
| token 失效 | 提示重新配对；停止重连风暴 |

## 13. 可观测性

Gateway 指标：

- 连接数、在线设备数。
- 事件接收/投递/ack 数。
- 投递延迟（不含客户端应用耗时）。
- 未 ack 队列长度。
- 限流/认证失败。
- SQLite 大小、清理耗时。

客户端指标：

- 剪贴板捕获/抑制/回写次数。
- 去重命中。
- 自动应用失败原因。
- 文件上传/下载/校验失败。
- 前台服务断线/重连。

日志脱敏：

- 不记录 payload 明文、token、key、croc URL fragment。
- event ID/channel ID 可记录，但 channel ID 不可包含用户隐私。

## 14. 部署

### 14.1 最小部署

```text
Gateway: Docker / Windows 服务 / Linux VPS（服务端，不是客户端）
croc store: 自建 croc-web 或公共测试服务
TLS: 反向代理（Caddy/Nginx/Cloudflare Tunnel）
```

> Linux 仅作为服务端部署载体，不属于客户端支持范围。若完全不使用 Linux，可在 Windows Server 上运行 Go 服务和 croc-web。

### 14.2 数据保留

- Gateway 事件默认保留 7 天或事件 TTL 到期后删除。
- ack 后可缩短保留时间。
- croc store 按 `croc-store-v1` 自身有效期和下载次数。
- 本地收件箱按用户设置保留。

### 14.3 安全

- 强制 HTTPS/WSS。
- Gateway 限流：配对、登录、发送、补拉。
- 设备 token 可撤销。
- 频道成员变更需要 owner 权限。
- 定期轮换 TLS 证书。
- 备份 Gateway 数据库；croc store 密文目录按删除语义管理。

## 15. 明确放弃的组件

- Bark App、Bark Server 作为运行依赖。
- APNs、iOS Notification Service Extension。
- iOS App Intent、Share Extension、Keyboard Extension。
- macOS 客户端、Linux 客户端。
- Android AccessibilityService。
- 首版 FCM/WNS。
- 首版 croc live P2P。
- 首版多租户/团队权限。

## 16. 参考源码

当前范围仍使用 croc 的相关实现：

| 文件 | 用途 |
| --- | --- |
| `croc/src/store/service.go` | store HTTP API、限制、状态机 |
| `croc/src/storeclient/client.go` | 可复用上传/下载客户端 |
| `croc/src/storecrypto/storecrypto.go` | `croc-store-v1` 加密格式 |
| `croc/src/docs/STORED_TRANSFERS.md` | store 工作流和运维边界 |
| `croc/src/cli/cli.go`、`src/cli/store.go` | CLI 行为和参数参考 |

Bark 仓库不再作为运行依赖；旧版架构只保留在历史 ADR 中。
