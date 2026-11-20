# ClipBridge 架构设计 v3（Go 核心 + Tailnet 直连）

> 前提：所有设备在同一个 Tailscale tailnet 内，且运行 Tailscale 客户端。  
> 平台：Windows 客户端（C# WPF + Go DLL）、Android 客户端（Kotlin + Go AAR）。  
> 决策依据：[ADR-005](adr/005-go-core-and-tailnet-p2p.md)。v2 Gateway 方案已归档到 [archive-v2/](archive-v2/)。  
> 本文是实现目标，不代表已经完成。

## 1. 设计原则

1. **业务只写一次**：协议、身份、配对、连接、发件箱、去重、文件传输全部在 Go 核心；两端是壳。
2. **直连优先、无服务端**：设备经 tailnet 地址直接通信，不引入 Gateway、STUN/TURN、WebRTC。
3. **tailnet ≠ 授权**：能连上不代表可信；必须配对并固定对端证书。
4. **事件显式寻址**：`source_device_id` + `target_device_id` + `event_id` + 每链路 `sequence` + TTL。
5. **默认不覆盖剪贴板**：Windows 可配置自动应用；Android 默认通知 + 手动复制。
6. **拨号方发起数据搬运**：Android 不开监听端口，减少后台/权限/网卡枚举问题。
7. **诚实的离线语义**：对端离线超过 TTL 的事件丢弃并告知，不假装可靠送达。
8. **范围可验证**：每个模块都有明确"不做什么"。

## 2. 组件总览

```text
┌────────────────────────────────────────────┐
│ Windows 10/11 x64                          │
│  ClipBridge.exe (C# WPF 托盘)              │
│   ├─ ClipboardMonitor (WM_CLIPBOARDUPDATE) │
│   ├─ Tray / Toast / 设置 UI / DPAPI        │
│   └─ CoreInterop (P/Invoke)                │
│          │  cb_call / cb_next_event         │
│  clipbridge_core.dll (Go, c-shared)        │
│   ├─ 监听 <tailnet-ip>:47821  (mTLS HTTPS) │
│   ├─ 本地命名管道 \\.\pipe\clipbridge.v1   │
│   └─ SQLite（对端/事件/发件箱/传输）       │
│  cbridge.exe (Go CLI，经命名管道调用)      │
└───────────────▲────────────────────────────┘
                │  tailnet (WireGuard, 直连或 DERP)
                │  mTLS HTTPS + WebSocket，证书互相固定
┌───────────────┴────────────────────────────┐
│ Android 10–15                              │
│  App (Kotlin / Compose)                    │
│   ├─ SyncForegroundService（可选实时模式） │
│   ├─ Share / QS Tile / 通知动作            │
│   ├─ Keystore / MediaStore / SAF           │
│   └─ Core (gomobile AAR 门面)              │
│  clipbridge-core (Go) — 只出站拨号         │
│   └─ SQLite（对端/事件/发件箱/传输）       │
└────────────────────────────────────────────┘

运行前提：Windows 上 Tailscale 服务在线；Android 上 Tailscale VPN 已连接。
```

### 2.1 Go 核心库 `clipbridge-core`

模块（Go module，包名按职责划分）：

| 包 | 职责 |
| --- | --- |
| `proto` | 事件信封、规范 JSON、`content_hash`、错误码、版本 |
| `identity` | Ed25519 设备密钥、自签名 X.509 证书、`device_id` 派生、证书指纹 |
| `pairing` | 配对会话（QR/手动）、SAS 短码、一次性 PSK、限流 |
| `peer` | mTLS 服务端/客户端、WebSocket 链路、重连退避、拨号方向规则、tailnet 地址选择与来源校验 |
| `store` | SQLite（`modernc.org/sqlite`，纯 Go）：对端、事件、发件箱、游标、传输、设置；数据库密钥由壳保管 |
| `clip` | 回声抑制、去重、TTL、敏感策略、应用策略决策 |
| `xfer` | 文件 offer/accept、Range 上传下载、SHA-256、断点、磁盘空间检查 |
| `facade` | `Call(method, json)` / `NextEvent(timeoutMs)`，统一 panic recover，统一错误码 |
| `cmd/cbcore-dll` | `//export` C ABI，`-buildmode=c-shared` |
| `mobile` | gomobile 绑定包（只含字符串/整型/字节/fd，不暴露复杂类型） |
| `cmd/cbridge` | Windows CLI，经命名管道调用运行中的核心 |

构建：

```text
# Windows DLL（可在 Linux 交叉编译，需要 mingw-w64）
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
  go build -buildmode=c-shared -o clipbridge_core.dll ./cmd/cbcore-dll

# Android AAR
gomobile bind -target=android/arm64,android/amd64 -androidapi 26 -o clipbridge-core.aar ./mobile
```

Go 版本固定在仓库 `go.mod`（与 croc 同为 1.27.x 系列）。`cbridge.exe`（纯 Go，不需要 cgo）和 DLL 共用同一份包。

核心**不依赖** tsnet、libtailscale、pion、libp2p、quic-go（后几者会枚举网卡，Android 受限）。

### 2.2 Windows 壳（C# WPF，.NET 10 LTS）

职责：

- 托盘常驻、开机启动、单实例。
- `AddClipboardFormatListener` + `GetClipboardSequenceNumber`，带重试地读取 `CF_UNICODETEXT`。
- 把本地复制通过 `clip.local_changed` 交给核心；核心决定发不发。
- 收到 `clip.received` 事件：通知，或按策略写剪贴板（写入前由 C# 做剪贴板 sequence 检查，见 §6.1）。
- 配对界面：显示 QR / 配对码 / SAS 确认。
- 设置、对端列表、收件箱、诊断页。
- DPAPI 保护"核心数据库主密钥"（`CurrentUser` 范围），启动时通过 `cb_init` 传给核心。
- 文件发送/接收 UI（文件选择、进度、打开所在文件夹）。

不做：协议、重试、去重、加密、网络、序号——这些都在核心。

#### DLL C ABI

```c
// UTF-8，所有返回的 char* 必须用 cb_free 释放
int   cb_init(const char* config_json, char** err_out);
char* cb_call(const char* method, const char* params_json);   // {"ok":true,"result":...} | {"ok":false,"error":{...}}
char* cb_next_event(int timeout_ms);                           // 事件 JSON；超时返回 NULL
void  cb_shutdown(void);
void  cb_free(char* p);
```

线程模型：

- C# 起一个专用后台线程循环调用 `cb_next_event(1000)`，再 `Dispatcher.BeginInvoke` 回 UI 线程。**不使用从 Go 到 C# 的函数指针回调**，避免 cgo 回调线程、GC pin 和重入问题。
- `cb_call` 可从任意线程调用，核心内部串行化关键状态。
- 所有导出函数内部 `defer recover()`，panic 转成 `{"ok":false,"error":{"code":"INTERNAL"}}` 并写日志。
- DLL 无法卸载；退出时先 `cb_shutdown()`，再结束进程。
- 仅 x64。

#### 命名管道（外部集成）

核心（仅 Windows 构建）在 `\\.\pipe\clipbridge.v1` 提供 JSON Lines 接口，ACL 仅当前用户。`cbridge.exe` 和其他本地应用通过它调用 `cb_call` 同样的方法集。C# 壳不需要自己实现 IPC。

### 2.3 Android 壳（Kotlin + Compose）

- `minSdk 26`，`targetSdk` 取当前要求值；Android 10–15 为测试基线。
- 依赖 `clipbridge-core.aar`，通过 `Core.call(method, json)` / `Core.nextEvent(timeoutMs)` 使用。
- `SyncForegroundService`（类型用 `specialUse`，因为 Android 15 限制 `dataSync` 前台服务的每日时长）：用户开启"实时模式"后持有核心的运行状态和出站连接；常驻通知显示连接状态，提供断开按钮。
- 剪贴板读取默认只有四个入口：App 内按钮、分享面板、通知动作打开 Activity 后读取、快捷设置磁贴启动短暂 Activity 后读取。另有**可选进阶**：设置里开启“后台自动发送”后，`LogcatClipboardWatcher`（`READ_LOGS`）发现复制，`ClipboardOverlayReader` 用 1×1 `TYPE_APPLICATION_OVERLAY` 取焦读取；默认关闭，见 [ADR-006](adr/006-android-background-read.md)。
- 文件：接收时核心写入应用私有目录，完成后 Kotlin 通过 MediaStore 保存到 Downloads；发送时 Kotlin 用 `ContentResolver` 打开 `ParcelFileDescriptor`，`detachFd()` 后把 fd 交给核心（`file.offer {fd}`），避免复制一份到缓存。
- Android Keystore 保护"核心数据库主密钥"。
- **不做**：后台剪贴板轮询、AccessibilityService、FCM、监听端口、枚举网卡。可选的 `READ_LOGS` + 悬浮窗后台读取不是轮询，见 [ADR-006](adr/006-android-background-read.md)。

### 2.4 本项目不再包含的组件

- Gateway、croc store / croc-web、WebRTC/STUN/TURN、局域网发现、tsnet。
- iOS / macOS / Linux 客户端。

## 3. 身份、配对与信任

### 3.1 身份

- 每台设备首次启动生成 Ed25519 密钥对，并生成长期自签名证书（有效期 10 年，换证需重新配对）。
- `device_id = "dev_" + base32(sha256(SPKI DER))[:26]`，由公钥派生，无需注册中心。
- 私钥存于数据库，数据库由主密钥（DPAPI / Keystore 包装）加密。

### 3.2 配对（Windows 发起，Android 加入；Win↔Win 同理）

1. Windows 调用 `pair.begin`：核心生成 128-bit 一次性 `psk`（5 分钟，3 次失败作废），返回 QR 内容：

   ```text
   clipbridge://pair?v=1&host=100.101.146.117&port=47821&fp=<windows 证书指纹 base64url>&psk=<base64url>&exp=<unix>
   ```

2. Android 扫码（或手动输入 host/port/psk，此时没有 `fp`，只能走 TOFU + SAS 比对）。
3. Android 以 TLS 连接，若有 `fp` 则固定该指纹；调用 `POST /v1/pair`，携带自己的证书指纹、名称、平台、`HMAC(psk, 传输摘要)`。
4. Windows 校验 HMAC，弹出确认："与 **Pixel** 配对？校验码 **482 913**"。校验码 = `SAS(双方指纹)` 取 6 位。Android 同时显示相同校验码，用户核对后在 Windows 点"允许"。
5. 双方各自保存对方的证书指纹、`device_id`、名称、tailnet 地址，配对完成。`psk` 作废。
6. 之后所有连接都是 mTLS，且指纹必须在对端表中；不在表中的连接只允许访问 `/v1/pair`。

撤销：删除对端后立即断开，并把该指纹加入拒绝表。

### 3.3 连接准入（纵深防御）

- Windows 只在 tailnet 地址（`100.64.0.0/10` 或 `fd7a:115c:a1e0::/48`）上监听，绝不绑定 `0.0.0.0`；监听地址在 Tailscale 接口变化时重新绑定。找不到 tailnet 地址时不监听，并报告 `NO_TAILNET`。
- 对每个入站连接再检查来源地址在 tailnet 网段内。
- 安装器添加 Windows 防火墙入站规则：TCP 47821（及备用端口 42821，见 api-spec §1.1），远端地址限定 `100.64.0.0/10`；运行时不需要管理员权限。
- 建议同时在 tailnet ACL 中只放行这两台设备之间的 47821（文档中给出示例，不强制）。
- TLS 1.3 only；证书校验为"指纹等于配对时保存的值"，不使用系统根证书。

### 3.4 地址与 DNS

- 对端地址使用配对时记录的 tailnet IP，并在每次 `hello` 中更新；MagicDNS 名称仅用于显示和回退。
- Android 上 Go 解析器无法依赖 `/etc/resolv.conf`，所以**核心拨号一律使用 IP**。

## 4. 链路与事件生命周期

### 4.1 链路

- Android → Windows：Android 拨号 `wss://<win-ip>:47821/v1/ws`，一条长连接双向传事件。
- Windows → Android：复用同一条连接。Android 未连接时事件留在 Windows 发件箱。
- Win ↔ Win：`device_id` 字典序较小者拨号；同时拨通时保留拨号方较小者发起的那条。

### 4.2 发送

```text
壳读取内容
  → clip.local_changed
  → 核心：规范化文本、content_hash、回声检查、策略检查
  → 对每个目标对端：分配该链路的 sequence，写入发件箱
  → 链路在线：立刻通过 WebSocket 发送；否则等待连接
  → 对端 ack(received/applied/ignored/...) → 发件箱标记完成
```

### 4.3 接收

```text
WebSocket 收到 event
  → 来源 = 已配对对端（由 TLS 指纹决定，非字段自称）
  → target_device_id == 本机
  → event 未过期
  → event_id 未处理（去重）
  → content_hash 与 payload 匹配
  → sequence 处理（见 §4.4）
  → 写入本地收件箱，发 ack(received)
  → 发出 clip.received / file.offered 给壳
  → 壳写剪贴板前调用 clip.begin_apply，写入后调用 clip.mark_applied，核心发 ack(applied)
```

### 4.4 序号与缺口（修复 v2 遗留问题）

- `sequence` 是**每条链路（source→target）**独立递增的整数。
- 接收方游标 `recv_cursor` = 已按序收到的最大 sequence，在 `hello` 中告知对方。
- 发送方从发件箱补发 `sequence > recv_cursor` 且未过期的事件。
- 事件过期或被用户清除后，发送方对缺口发送 `skip {through: N}` 控制帧；接收方把游标推进到 N，**不会被丢失事件卡住**。
- 乱序到达：缓存最多 64 条等待缺口；超过则请求 `resync`。
- 重复到达：按 `event_id` 去重，仍回 ack（幂等）。
- 事件**不转发**：B 收到 A 的事件后不会再转发给 C，避免多设备回路。

### 4.5 TTL 与保留

| 项 | 默认 |
| --- | --- |
| 剪贴板事件 TTL | 15 分钟 |
| 文件 offer TTL | 24 小时（发送方文件需仍然存在且发送方在线） |
| 发件箱保留 | 事件过期后删除 |
| 本地收件箱 | 条数/天数可配，用户可清空 |

## 5. 数据模型（核心 SQLite）

```text
settings(key, value)
identity(device_id, name, platform, key_blob_enc, cert_der, created_at)
peers(device_id, name, platform, cert_fp, tailnet_addrs_json, port,
      role, state(paired/revoked), apply_mode, paired_at, last_seen_at)
links(peer_id, next_seq_out, recv_cursor, updated_at)
outbox(event_id, peer_id, sequence, kind, envelope_enc, created_at, expires_at,
       state(queued/sent/acked/expired), attempts, acked_at, ack_result)
inbox(event_id, peer_id, sequence, kind, payload_enc, received_at, expires_at,
      state(received/applied/ignored/expired/error), applied_at)
transfers(transfer_id, peer_id, direction, name, size, sha256, path,
          offset, state, error, created_at, expires_at)
clip_state(last_applied_event_id, last_applied_hash, last_local_sent_hash,
           suppress_until)
revoked_fps(cert_fp, revoked_at)
```

`*_enc` 字段用数据库主密钥做 AES-256-GCM，主密钥由 DPAPI / Keystore 保护。日志永不记录 payload、psk、私钥。

## 6. 剪贴板策略

### 6.1 Windows

```text
本地复制
  → WM_CLIPBOARDUPDATE → 读文本（失败重试 1–2 次，空内容不发）
  → clip.local_changed {text, win_seq}
  → 核心：hash == last_applied_hash 且在抑制窗口内 → 视为回声，不发送
         否则生成事件

远端到达
  → 核心发出 clip.received {event_id, text, sensitive, decision}
  → decision = notify | manual | auto
  → auto：C# 比较 GetClipboardSequenceNumber()
          与"到达时记录的 seq"；不同（用户又复制了）→ 不覆盖，降级为通知
          相同 → 写剪贴板 → clip.applied {event_id, win_seq_after}
  → 核心记录 last_applied_hash / suppress_until，并发 ack(applied)
```

默认：捕获开关首次启动由用户选择；自动应用默认关闭；敏感内容永不自动应用；保留有限本地历史并提供"恢复上一个剪贴板"。

### 6.2 Android

- 收到事件只发通知，用户点击"复制"才写入。
- 实时模式由用户显式开启，常驻通知。
- 发送入口见 §2.3；系统可能弹出剪贴板访问提示，需要在文案中说明。
- 后台自动发送（可选）同样只走 `clip.local_changed`，核心的 capture/暂停/回声/去重/大小/敏感规则不变；后台读取失败时回退到“打开应用时检查一次”。
- 敏感事件通知不显示正文预览。

### 6.3 策略表

| `apply_mode`（按对端设置） | Windows | Android |
| --- | --- | --- |
| `notify_only` | 通知，不写 | 通知，不写 |
| `manual` | 通知 + 托盘动作后写 | 通知动作后写 |
| `auto` | 通过 sequence 检查后写 | App 前台或实时模式下尝试，失败回退通知 |

## 7. 文件传输

文件字节**直接在设备间传输**，经同一 mTLS 通道（HTTP Range 实现断点）。数据搬运总由**拨号方**发起：

| 发送方 | 接收方 | 流程 |
| --- | --- | --- |
| Windows（监听方） | Android（拨号方） | Windows 发 `file` offer 事件 → Android 用户/策略接受 → Android `GET /v1/files/{id}` 带 `Range` 拉取 |
| Android（拨号方） | Windows（监听方） | Android 发 offer → Windows 接受后回 `file_accept{offset}` → Android `PUT /v1/files/{id}` 带 `Content-Range` 推送 |

- 发送前计算整个文件的 SHA-256 写入 offer；接收方落盘到临时文件，校验通过才重命名/保存，否则删除并报错。
- 断点：接收方记录 `offset`，重连后从 offset 续传；hash 不变才允许续传。
- 大文件流式处理，不整体读入内存；传输期间核心持有"保持连接"状态。
- 默认单文件上限可配置（默认 4 GiB）；磁盘空间不足提前拒绝。
- Android 接收：先写应用私有目录，完成后由 Kotlin 经 MediaStore 保存到 Downloads。
- 多文件：v1 逐个 offer，不打包。
- 进度、速率、路径类型（直连 / DERP 中继，若可从 Tailscale 获取）仅用于展示。

## 8. 故障与降级

| 故障 | 行为 |
| --- | --- |
| Tailscale 未运行 / VPN 未连接 | UI 提示"Tailscale 不可用"；不无限重连，指数退避上限 60 s |
| `NO_TAILNET`（Windows 找不到 tailnet 地址） | 不监听，托盘显示警告，Tailscale 接口出现后自动绑定 |
| Windows 休眠/关机 | Android 显示"电脑离线"，发件箱排队；唤醒后重连补发 |
| Android 服务被杀 | Windows 排队；打开 App 后重连并按游标补齐 |
| 事件超过 TTL | 发送 `skip`，UI 显示"已过期未送达" |
| 证书指纹不符 | 拒绝连接，记录安全事件；不回显任何内容 |
| 对端被撤销 | 立即断开，拒绝重连 |
| 走 DERP 中继 | 功能正常，吞吐下降；传输 UI 显示 |
| 磁盘不足 | 拒绝 offer，不自动删除其它文件 |
| 文件校验失败 | 删除临时文件，不保存，提示重发 |
| 核心 panic | 门面返回 `INTERNAL`，写日志；壳提示重启核心 |

## 9. 可观测性与日志

- 核心状态：每个对端的 `connected/connecting/offline`、当前路径、发件箱长度、`recv_cursor`。
- 事件统计：捕获/抑制/去重命中/自动应用失败原因/传输失败原因（无正文）。
- 日志脱敏：不记录 payload、psk、私钥、数据库主密钥；`device_id` 与 `event_id` 可记录。
- 诊断页：连接状态、tailnet 地址、证书指纹（前 8 位）、游标、最近错误。

## 10. 部署与分发

- Windows：安装器（MSI/Inno/WiX 均可）安装 `ClipBridge.exe`、`clipbridge_core.dll`、`cbridge.exe`，并添加限定远端地址的防火墙入站规则；自包含 .NET 或框架依赖二选一（Phase 3 决定）。
- Android：APK/AAB，签名；首次运行引导：安装并登录 Tailscale、通知权限、电池优化。
- 无服务端、无证书申请、无 Docker。
- 本机（Linux 开发机）的 `tailscale serve` 路径**不为本项目新增**，既有路径不动。

## 11. 明确放弃 / 暂缓

- 放弃：Gateway（MVP）、croc store（MVP）、WebRTC、libp2p、tsnet、局域网发现、FCM/WNS、iOS/macOS/Linux 客户端、AccessibilityService。
- 可选（默认关闭，需一次性 ADB 授权）：`READ_LOGS` + 悬浮窗后台读取；见 [ADR-006](adr/006-android-background-read.md)。
- 暂缓：离线中继 peer（需 ADR，必须端到端加密）、多文件打包、HTML/图片剪贴板、Ed25519 签名事件（mTLS 已覆盖链路认证，仅在引入中继时再需要）。

## 12. 参考源码

croc 不再是运行依赖，仅作设计参考：

| 文件 | 参考点 |
| --- | --- |
| `croc/src/store/service.go` | 限额、状态机、过期语义 |
| `croc/src/storecrypto/storecrypto.go` | 分块与完整性设计 |
| `croc/src/tailcattransport/` | 进程内 Tailscale 数据面的取舍（构建标签不含 Android，印证了不嵌入 tsnet 的决定） |
