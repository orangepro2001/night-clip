# ClipBridge 接口规范（`clipbridge/v1`）

> 范围：Windows 客户端、Android 客户端、ClipBridge Gateway、croc store。  
> 明确不支持：iOS、macOS、Linux 客户端；不使用 Bark/APNs。  
> 状态：Phase 0 提案，接口评审后冻结 v1。

## 1. 约定

### 1.1 传输

- Gateway REST：HTTPS，JSON UTF-8。
- 实时通道：WSS，`Sec-WebSocket-Protocol: clipbridge.v1`。
- 文件数据面：HTTPS，`application/octet-stream`。
- 时间：RFC 3339 UTC，例如 `2026-10-26T08:00:00Z`。
- ID：不透明字符串，建议 UUIDv4。
- 版本：`clipbridge/v1`；不兼容变更新增主版本。

### 1.2 安全默认值

- 所有公网通信必须 TLS。
- 长期 token、频道密钥、croc token 不得出现在 URL query、日志、错误消息。
- Gateway 只保存 `payload_ciphertext` 和路由元数据。
- 客户端默认不自动覆盖剪贴板。
- Android 不得后台读取剪贴板。

### 1.3 规范 JSON 与 hash

事件签名、`content_hash` 和 AAD 使用规范 JSON：

- UTF-8。
- 对象键按 Unicode 码点排序。
- 无多余空白。
- 数字最短十进制。
- 字符串按 JSON 标准转义。

`content_hash`：

```text
sha256(canonical_json(payload))
```

格式为 `sha256:` + 64 位小写 hex。

### 1.4 错误对象

```json
{
  "error": {
    "code": "TARGET_NOT_IN_CHANNEL",
    "message": "Target device is not a channel member.",
    "retryable": false,
    "details": {}
  }
}
```

`message` 可变，客户端只依赖 `code` 和 HTTP 状态。

## 2. 身份与频道

### 2.1 模型

```text
Vault
  ├── Windows device (device_id, token)
  └── Android device (device_id, token)
        └── Channel (channel_id, key_id)
              ├── Membership
              └── Events
```

- `device_id`：安装时生成；重装可重新配对。
- `platform`：`windows` 或 `android`。
- `channel_id`：业务隔离单位。
- `target_device_ids`：本次实际接收者。
- token 是设备访问凭证，不是用户账号。

### 2.2 频道策略（客户端本地）

```json
{
  "channel_id": "ch_...",
  "name": "work",
  "apply_mode": "manual",
  "sensitive_policy": "never_auto",
  "max_inline_bytes": 2048,
  "default_ttl_seconds": 900
}
```

`apply_mode`：

| 值 | Windows | Android |
| --- | --- | --- |
| `notify_only` | 通知，不写 | 通知，不写 |
| `manual` | 通知/托盘动作后写 | 通知动作后写 |
| `auto_foreground` | 前台/托盘在线且安全检查通过 | App 前台或明确实时模式下尝试 |
| `auto_any` | 可选，风险自担 | 不推荐，不承诺 |

## 3. 解密后事件信封

### 3.1 顶层字段

```json
{
  "version": 1,
  "event_id": "5b8f0e5c-3f6e-4a8b-9f32-3d4c7f2a1e90",
  "kind": "clipboard",
  "channel_id": "ch_...",
  "source_device_id": "dev_windows_...",
  "target_device_ids": ["dev_android_..."],
  "sequence": 42,
  "created_at": "2026-10-26T08:00:00Z",
  "expires_at": "2026-10-26T08:15:00Z",
  "content_hash": "sha256:...",
  "sensitive": false,
  "payload": {}
}
```

| 字段 | 类型 | 必填 | 约束 |
| --- | --- | --- | --- |
| `version` | integer | 是 | `1` |
| `event_id` | string | 是 | 8–128 字符；全局唯一 |
| `kind` | string | 是 | `clipboard` / `file` / `control` |
| `channel_id` | string | 是 | 非空 |
| `source_device_id` | string | 是 | 不得出现在 target 列表 |
| `target_device_ids` | array[string] | 是 | 1–100 项，去重 |
| `sequence` | integer | 是 | ≥1，按 `(channel_id, source_device_id)` 递增 |
| `created_at` | string | 是 | RFC 3339 |
| `expires_at` | string | 是 | 晚于 created_at |
| `content_hash` | string | 是 | `sha256:<64 hex>` |
| `sensitive` | boolean | 是 | 敏感内容默认不自动应用 |
| `payload` | object | 是 | 见下 |
| `auth` | object | 否 | 设备签名，Phase 2 |

### 3.2 剪贴板 payload

```json
{
  "mime_type": "text/plain; charset=utf-8",
  "content": "hello from Windows",
  "size": 18,
  "preview": "hello from Windows"
}
```

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `mime_type` | string | 是 | v1 只支持 `text/plain; charset=utf-8` |
| `content` | string | 是 | UTF-8 文本 |
| `size` | integer | 是 | UTF-8 字节数 |
| `preview` | string | 否 | 敏感事件应省略/脱敏 |

v1 不支持图片、HTML、富文本或文件列表作为剪贴板 payload；这些内容走文件事件或后续版本。

### 3.3 文件 payload

```json
{
  "transfer_id": "AbCd...",
  "mode": "store",
  "token": "croc-store-v1....",
  "url": "https://files.example.com/s/AbCd...#v1....",
  "manifest": {
    "file_count": 1,
    "total_bytes": 123456
  },
  "display_name": "report.pdf"
}
```

`token`/`url` 是 bearer secret，必须只存在于加密 payload 中。Gateway 不解析 payload。

### 3.4 控制 payload

```json
{
  "action": "resync",
  "requested_after": 41,
  "reason": "sequence_gap"
}
```

v1 动作：`resync`、`ping`、`pause`。

### 3.5 设备签名（Phase 2）

```json
{
  "auth": {
    "alg": "Ed25519",
    "key_id": "device-key-2026-01",
    "signature": "base64url..."
  }
}
```

签名覆盖除 `auth` 外的规范 JSON。首版可使用 TLS + device token；签名在 Phase 2 增强。

## 4. Gateway shipment（线格式）

Gateway 不接收明文 `payload`，而接收 shipment：

```json
{
  "version": 1,
  "event_id": "5b8f0e5c-3f6e-4a8b-9f32-3d4c7f2a1e90",
  "kind": "clipboard",
  "channel_id": "ch_...",
  "source_device_id": "dev_windows_...",
  "target_device_ids": ["dev_android_..."],
  "sequence": 42,
  "created_at": "2026-10-26T08:00:00Z",
  "expires_at": "2026-10-26T08:15:00Z",
  "content_hash": "sha256:...",
  "sensitive": false,
  "payload_ciphertext": "base64...",
  "nonce": "base64...",
  "key_id": "k1"
}
```

### 4.1 加密

- `payload_ciphertext`：AES-256-GCM 加密后的 payload，包含 16 字节 tag。
- `nonce`：12 字节随机 nonce 的 Base64。
- `key_id`：频道密钥版本。
- AAD：

```text
canonical_json({
  version, event_id, kind, channel_id, source_device_id,
  target_device_ids, sequence, created_at, expires_at,
  content_hash, sensitive, key_id
})
```

- Gateway 只验证结构、路由和 `content_hash` 格式，不解密。
- 接收端解密后重建 payload，并校验 `content_hash`。

### 4.2 路由校验

Gateway 必须：

- 校验 `source_device_id` 与认证设备一致。
- 校验 source 和所有 target 都是频道成员。
- 校验 source 不在 target 中。
- 校验 target 数量 ≤100。
- 校验 TTL 有限且不超过服务端上限。
- 对 `Idempotency-Key` 做幂等。

## 5. Gateway REST API

Base：`https://gateway.example.com/v1`  
认证：`Authorization: Bearer <device-token>`

### 5.1 配对会话

#### 创建会话

```http
POST /v1/pairing/sessions
Content-Type: application/json
```

```json
{
  "requested_by": {
    "name": "Windows Desktop",
    "platform": "windows"
  },
  "channel_name": "Personal"
}
```

响应 `201`：

```json
{
  "session_id": "pair_...",
  "code": "7K4P-9M2Q",
  "expires_at": "2026-10-26T08:05:00Z",
  "qr_payload": "clipbridge://pair?session=pair_...&code=7K4P-9M2Q"
}
```

要求：

- 配对码一次性、5 分钟有效。
- 创建接口限流。
- QR/URL 不包含长期 token。

#### 加入会话

```http
POST /v1/pairing/sessions/{session_id}/join
Content-Type: application/json
```

```json
{
  "code": "7K4P-9M2Q",
  "device": {
    "name": "Pixel",
    "platform": "android",
    "public_key": "base64url..."
  }
}
```

响应 `201`：

```json
{
  "vault_id": "vault_...",
  "device_id": "dev_android_...",
  "access_token": "clipbridge-device-token...",
  "channels": [
    {"channel_id": "ch_...", "name": "Personal", "key_id": "k1"}
  ]
}
```

`access_token` 只出现一次；客户端立即存入 DPAPI/Keystore。

### 5.2 设备

```http
GET /v1/devices
Authorization: Bearer <token>
```

```json
{
  "devices": [
    {
      "device_id": "dev_windows_...",
      "name": "Windows Desktop",
      "platform": "windows",
      "online": true,
      "last_seen_at": "2026-10-26T08:00:00Z"
    }
  ]
}
```

```http
PATCH /v1/devices/{device_id}
```

```json
{"name":"My PC"}
```

```http
DELETE /v1/devices/{device_id}
```

删除会撤销 token 和 WebSocket。

### 5.3 频道

```http
POST /v1/channels
```

```json
{"name":"work","default_ttl_seconds":900}
```

```http
GET /v1/channels
```

```json
{
  "channels": [
    {
      "channel_id": "ch_...",
      "name": "work",
      "enabled": true,
      "members": ["dev_windows_...", "dev_android_..."]
    }
  ]
}
```

```http
POST /v1/channels/{channel_id}/members
```

```json
{"device_id":"dev_android_..."}
```

```http
DELETE /v1/channels/{channel_id}/members/{device_id}
```

### 5.4 发送事件

```http
POST /v1/events
Authorization: Bearer <token>
Content-Type: application/json
Idempotency-Key: 5b8f0e5c-3f6e-4a8b-9f32-3d4c7f2a1e90
```

请求体为 shipment（见第 4 节）。

响应 `202`：

```json
{
  "event_id": "5b8f0e5c-3f6e-4a8b-9f32-3d4c7f2a1e90",
  "sequence": 42,
  "accepted_at": "2026-10-26T08:00:01Z",
  "delivery": [
    {"device_id": "dev_android_...", "state": "queued"}
  ]
}
```

- `accepted` 表示 Gateway 已落库，不表示目标已应用。
- 目标通过 WebSocket 或补拉收到。
- 同一 `Idempotency-Key` + 相同 body 返回同一结果；body 不同返回 409。

### 5.5 拉取事件

```http
GET /v1/events?channel_id=ch_...&after=41&limit=100
Authorization: Bearer <token>
```

```json
{
  "events": [
    {
      "event_id": "...",
      "channel_id": "ch_...",
      "source_device_id": "dev_windows_...",
      "sequence": 42,
      "kind": "clipboard",
      "created_at": "...",
      "expires_at": "...",
      "content_hash": "sha256:...",
      "sensitive": false,
      "payload_ciphertext": "base64...",
      "nonce": "base64...",
      "key_id": "k1",
      "target_device_ids": ["dev_android_..."]
    }
  ],
  "next_after": 42,
  "has_more": false
}
```

只返回该设备是目标的 shipment。

### 5.6 ack

```http
POST /v1/events/{event_id}/ack
```

```json
{
  "device_id": "dev_android_...",
  "result": "applied",
  "reason": null,
  "acked_at": "2026-10-26T08:00:03Z"
}
```

`result`：`received` / `applied` / `ignored` / `expired` / `error`。

- `ignored` 带 `reason`：`duplicate`、`target_mismatch`、`channel_disabled`、`policy_blocked`。
- ack 幂等；重复 ack 返回 204。
- ack 不携带明文。

### 5.7 同步状态

```http
GET /v1/sync/state?device_id=dev_android_...&channel_id=ch_...
```

```json
{
  "device_id": "dev_android_...",
  "channel_id": "ch_...",
  "last_received_sequence": 41,
  "last_applied_sequence": 39,
  "last_applied_event_id": "...",
  "pending_events": 1,
  "updated_at": "2026-10-26T08:00:03Z"
}
```

### 5.8 健康检查

```http
GET /healthz
```

```json
{"ok":true,"version":"0.1.0"}
```

## 6. WebSocket 协议

### 6.1 连接

```text
wss://gateway.example.com/v1/ws
Authorization: Bearer <device-token>
Sec-WebSocket-Protocol: clipbridge.v1
```

### 6.2 hello

```json
{
  "type": "hello",
  "request_id": "r1",
  "device_id": "dev_android_...",
  "cursors": [
    {"channel_id": "ch_...", "after": 41}
  ]
}
```

### 6.3 hello_ack

```json
{
  "type": "hello_ack",
  "request_id": "r1",
  "server_time": "2026-10-26T08:00:00Z",
  "channels": ["ch_..."],
  "max_inline_bytes": 2048
}
```

### 6.4 event

```json
{
  "type": "event",
  "shipment": {
    "version": 1,
    "event_id": "...",
    "kind": "clipboard",
    "channel_id": "ch_...",
    "source_device_id": "dev_windows_...",
    "target_device_ids": ["dev_android_..."],
    "sequence": 42,
    "created_at": "...",
    "expires_at": "...",
    "content_hash": "sha256:...",
    "sensitive": false,
    "payload_ciphertext": "base64...",
    "nonce": "base64...",
    "key_id": "k1"
  }
}
```

### 6.5 ack

```json
{
  "type": "ack",
  "event_id": "...",
  "device_id": "dev_android_...",
  "result": "applied",
  "reason": null,
  "acked_at": "..."
}
```

### 6.6 ping/pong

客户端每 25–30 秒发送：

```json
{"type":"ping","ts":"2026-10-26T08:00:00Z"}
```

服务端：

```json
{"type":"pong","ts":"2026-10-26T08:00:00Z"}
```

### 6.7 error / goaway

```json
{"type":"error","code":"RATE_LIMITED","message":"slow down","retryable":true}
```

```json
{"type":"goaway","reason":"server_restart","reconnect_after_ms":1000}
```

### 6.8 约束

- 单条消息建议 ≤64 KiB；文件不走 WebSocket。
- 服务端按设备限流。
- 客户端收到重复 `event_id` 必须幂等。
- 断线重连必须携带游标；Gateway 重投未 ack 事件。
- 任何一方不得把 WebSocket 消息当作“已应用”。

## 7. `cbridge.exe` CLI（仅 Windows）

> 只发布 Windows 客户端；Go 代码可交叉编译不代表支持 Linux。

### 7.1 命令树

```text
cbridge
├── auth
│   ├── pair
│   ├── status
│   └── logout
├── channel
│   ├── list
│   └── use
├── clip
│   └── send
├── file
│   ├── send
│   ├── inspect
│   ├── receive
│   └── revoke
├── inbox
│   ├── list
│   ├── show
│   └── apply
├── daemon
│   └── status
├── doctor
└── version
```

### 7.2 示例

```powershell
cbridge auth pair --gateway https://sync.example.com --json
cbridge clip send --channel work --to phone --text "hello" --json
Get-Clipboard | cbridge clip send --channel work --to phone --json
cbridge file send --channel work --to phone .\report.pdf --json
cbridge file receive "croc-store-v1...." --out .\received --json
cbridge inbox list --channel work --json
cbridge inbox apply <event-id> --json
cbridge doctor --json
```

### 7.3 JSON 输出

成功：

```json
{"ok":true,"command":"clip.send","data":{},"warnings":[]}
```

失败：

```json
{
  "ok": false,
  "command": "clip.send",
  "error": {
    "code": "TARGET_NOT_IN_CHANNEL",
    "message": "Target device is not a channel member.",
    "retryable": false,
    "details": {}
  },
  "warnings": []
}
```

- `--json` 时 stdout 只能是 JSON。
- 日志/进度走 stderr。
- token 不放在 argv；使用 stdin、环境变量或 Windows 凭据管理器。

### 7.4 退出码

| 退出码 | 含义 |
| ---: | --- |
| 0 | 成功（含幂等去重） |
| 2 | 参数/用法错误 |
| 3 | 配置缺失 |
| 4 | 认证/配对失败 |
| 5 | schema/hash/目标校验失败 |
| 6 | 网络/Gateway 失败 |
| 7 | 过期/撤销/不存在 |
| 8 | 冲突（claim、幂等冲突） |
| 9 | 加密/解密失败 |
| 10 | 内部错误 |
| 130 | 用户中断 |

### 7.5 配置

建议：

```text
%APPDATA%\ClipBridge\config.json
```

长期 token 存 Windows Credential Manager/DPAPI，不写入 JSON 明文。

环境变量：

| 变量 | 用途 |
| --- | --- |
| `CLIPBRIDGE_CONFIG` | 配置路径 |
| `CLIPBRIDGE_GATEWAY` | Gateway URL |
| `CLIPBRIDGE_DEVICE` | device ID |
| `CLIPBRIDGE_CHANNEL` | 默认频道 |
| `CLIPBRIDGE_TOKEN_FILE` | 从受限文件读取 token |
| `CLIPBRIDGE_STORE_URL` | croc store origin |
| `CLIPBRIDGE_LOG_LEVEL` | 日志级别 |

## 8. Windows 本地 IPC

托盘应用监听：

```text
\\.\pipe\clipbridge.v1
```

- ACL 限制为当前用户。
- JSON Lines。
- 单行请求，单行响应。

请求：

```json
{"id":"1","method":"clipboard.send","params":{"channel_id":"ch_...","text":"hello"}}
```

响应：

```json
{"id":"1","ok":true,"result":{"event_id":"..."}}
```

方法：

| 方法 | 说明 |
| --- | --- |
| `status` | 连接/频道/待同步状态 |
| `clipboard.send` | 发送文本 |
| `inbox.list` | 列出事件 |
| `inbox.apply` | 应用事件到剪贴板 |
| `file.send` | 触发文件上传 |
| `subscribe` | 订阅事件（可选） |

窗口关闭/托盘退出时应拒绝新请求并返回稳定错误。

## 9. Android 接口

### 9.1 标准分享

使用 `ACTION_SEND` + `text/plain`：

```text
ACTION_SEND
  MIME: text/plain
  EXTRA_TEXT: <text>
  EXTRA_CLIPBRIDGE_CHANNEL: <channel-id>
```

App 必须让用户确认频道/目标后发送。

### 9.2 自定义 Action

```text
clipbridge.intent.action.SEND_CLIPBOARD
  extra channel_id
```

由 App/快捷设置磁贴/通知动作触发，打开 Activity 后读取剪贴板。

```text
clipbridge.intent.action.OPEN_EVENT
  extra event_id
  extra channel_id
```

### 9.3 快捷设置磁贴

- 点击启动一个短生命周期 Activity。
- Activity 获得焦点后读取剪贴板。
- 读取失败/权限限制时提示用户手动分享。
- 不在 TileService 后台直接读剪贴板。

### 9.4 通知动作

| action | 行为 |
| --- | --- |
| `clipbridge.COPY` | 打开/校验后复制 |
| `clipbridge.OPEN` | 打开事件详情 |
| `clipbridge.IGNORE` | 标记忽略 |

### 9.5 文件

- 首版：`ACTION_VIEW` 打开 croc HTTPS 链接。
- 原生：下载到 Downloads/SAF，再由 App 分享/打开。
- App 不得把 croc fragment key 写入日志或分析事件。

## 10. croc store API

> 上游已存在，固定 `croc-store-v1` / croc `v11.5.5-10-g9c28c3bd`。  
> 上游 API 是官方客户端实现边界，不保证长期第三方兼容；需适配层和契约测试。

### 10.1 基础

```text
Base: https://<store-origin>/api/v1/store/transfers
Auth: Authorization: Bearer <capability>
```

通用响应头：

```http
Cache-Control: no-store
Referrer-Policy: no-referrer
X-Content-Type-Options: nosniff
```

### 10.2 创建 transfer

```http
POST /api/v1/store/transfers
Content-Type: application/json
```

```json
{
  "protocol": "croc-store-v1",
  "manifestBytes": 512,
  "chunkBytes": [1048592],
  "redeemVerifier": "base64url...",
  "files": 1,
  "plaintextBytes": 1048576,
  "downloads": 1,
  "expiresSeconds": 86400
}
```

响应 `201`：

```json
{
  "id": "AbCd...",
  "uploadToken": "base64url...",
  "uploadExpiresAt": "2026-10-26T09:00:00Z",
  "chunkSize": 4194304
}
```

### 10.3 上传

```http
PUT /api/v1/store/transfers/{id}/manifest
Authorization: Bearer <uploadToken>
Content-Type: application/octet-stream

<ciphertext bytes>
```

```http
PUT /api/v1/store/transfers/{id}/chunks/{index}
Authorization: Bearer <uploadToken>
Content-Type: application/octet-stream

<ciphertext bytes>
```

成功 `204`。

### 10.4 complete

```http
POST /api/v1/store/transfers/{id}/complete
Authorization: Bearer <uploadToken>
```

成功 `200`：

```json
{"expiresAt":"2026-10-27T08:00:00Z"}
```

### 10.5 manifest

```http
GET /api/v1/store/transfers/{id}/manifest
Authorization: Bearer <redeemCapability>
```

成功 `200`：加密 manifest 字节。

响应头：

```http
X-Croc-Expires-At: 2026-10-27T08:00:00Z
X-Croc-Downloads-Remaining: 1
```

### 10.6 claim

```http
POST /api/v1/store/transfers/{id}/claim
Authorization: Bearer <redeemCapability>
```

成功 `201`：

```json
{
  "claimToken": "base64url...",
  "claimExpiresAt": "2026-10-26T08:30:00Z",
  "expiresAt": "2026-10-27T08:00:00Z"
}
```

已有活动 claim：`423 Locked` + `Retry-After`。读取 chunk 会续期。

### 10.7 chunk

```http
GET /api/v1/store/transfers/{id}/chunks/{index}
Authorization: Bearer <claimToken>
```

成功 `200`：密文 chunk。

### 10.8 commit

```http
POST /api/v1/store/transfers/{id}/commit
Authorization: Bearer <claimToken>
```

成功 `204`；响应头 `X-Croc-Downloads-Remaining`。

### 10.9 release / revoke

```http
DELETE /api/v1/store/transfers/{id}/claim
Authorization: Bearer <claimToken>
```

```http
DELETE /api/v1/store/transfers/{id}
Authorization: Bearer <uploadToken>
```

成功 `204`。

### 10.10 浏览器链接

```text
https://files.example.com/s/<transfer-id>#v1.<master-key>
```

`#v1.<key>` 只在 fragment 中，不进入普通 HTTP 请求/Referer。完整链接仍是 bearer secret。

### 10.11 默认限制

| 项 | 默认 |
| --- | ---: |
| 单 transfer 明文 | 1 GiB（硬上限 2 GiB） |
| 总磁盘配额 | 5 GiB |
| 最少保留磁盘 | 512 MiB |
| 文件数 | 100 |
| 每 IP 每小时创建 | 5 |
| 每 IP 未完成上传 | 2 |
| 上传对象保留 | 1 小时 |
| claim | 30 分钟 |
| tombstone | 24 小时 |

## 11. 错误码

| HTTP | `code` | 含义 |
| --- | --- | --- |
| 400 | `INVALID_EVENT` | schema/字段错误 |
| 401 | `UNAUTHENTICATED` | token 缺失/无效 |
| 403 | `FORBIDDEN` | 无权限或来源不匹配 |
| 404 | `NOT_FOUND` | 资源不存在或不可见 |
| 409 | `IDEMPOTENCY_CONFLICT` | 同 key 不同 body |
| 410 | `EXPIRED` | 事件/transfer 过期 |
| 413 | `PAYLOAD_TOO_LARGE` | 超内联/文件限制 |
| 422 | `TARGET_NOT_IN_CHANNEL` | 目标不是频道成员 |
| 423 | `CLAIMED` | store claim 冲突 |
| 429 | `RATE_LIMITED` | 限流 |
| 500 | `INTERNAL` | 服务端错误 |
| 503 | `UNAVAILABLE` | 上游/数据库/存储不可用 |

## 12. 兼容与废弃

- `clipbridge/v1` 字段冻结后，新增字段必须可选。
- 不兼容变更使用 `clipbridge/v2`。
- croc store 适配固定版本；升级需重跑 manifest/chunk/claim/commit 契约测试。
- 不保留 Bark/APNs 字段；发现旧字段直接拒绝并记录诊断。
- Windows 客户端只发布 x64（ARM64 可选）；Android 只发布 APK/AAB。
- Linux/macOS/iOS 不在支持矩阵。

## 13. 安全清单

- [ ] Gateway 全量 TLS。
- [ ] Windows DPAPI / Android Keystore。
- [ ] 频道 payload AES-256-GCM。
- [ ] nonce 不复用。
- [ ] Gateway 只存密文和路由元数据。
- [ ] 剪贴板写前检查目标/频道/TTL/去重/sequence。
- [ ] Windows 剪贴板回声抑制。
- [ ] Android 不后台读剪贴板。
- [ ] croc token 不写日志。
- [ ] 限流、token 撤销、密钥轮换。

## 14. 可机读 schema

- [event.schema.json](schemas/event.schema.json)：解密后的事件信封。
- [shipment.schema.json](schemas/shipment.schema.json)：Gateway 线格式/AES-GCM 密文 shipment。
