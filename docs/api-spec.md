# ClipBridge 接口规范（`clipbridge/v1`，Tailnet 直连版）

> 范围：Windows 客户端、Android 客户端、Go 核心门面。  
> 前提：设备都在同一 tailnet；无 Gateway。  
> 状态：Phase 0 提案，评审后冻结 v1。协议名 `clipbridge/v1` 沿用（v2 Gateway 协议从未发布，见 [archive-v2/api-spec.md](archive-v2/api-spec.md)）。

## 1. 约定

### 1.1 传输

- 设备间：HTTPS（TLS 1.3，双向证书，指纹固定），端口默认 `47821`，仅在 tailnet 地址上监听。
- 实时通道：WebSocket，`Sec-WebSocket-Protocol: clipbridge.v1`，文本帧 JSON。
- 文件：同一 HTTPS 服务上的 `GET`/`PUT /v1/files/{id}`，`application/octet-stream`。
- 时间：RFC 3339 UTC。ID：事件为 UUIDv4；设备为 `dev_` + base32(sha256(SPKI))[:26]。
- 单个 WebSocket 消息 ≤ 512 KiB（`hello_ack.max_frame_bytes`）。剪贴板文本本身仍 ≤ 64 KiB，但 JSON 转义最坏会膨胀数倍，所以帧上限留有余量；文件不走 WebSocket。

### 1.2 安全默认值

- 证书校验 = 指纹等于配对保存的值；不使用系统根证书、不使用主机名校验。
- 未配对指纹只能访问 `POST /v1/pair`，其它路径返回 `403 NOT_PAIRED`。
- 来源地址必须在 `100.64.0.0/10` 或 `fd7a:115c:a1e0::/48`。
- psk、私钥、数据库主密钥不得出现在 URL（QR 中的 psk 只用于一次配对，不进入 HTTP 请求行）、日志、错误消息。
- 事件来源身份由 TLS 指纹决定，信封里的 `source_device_id` 必须与之一致，否则丢弃。

### 1.3 规范 JSON 与 hash

`content_hash = "sha256:" + hex(sha256(canonical_json(payload)))`。

规范 JSON：UTF-8；对象键按 Unicode 码点排序；无多余空白；数字最短十进制；字符串按 JSON 标准转义。核心用同一份实现生成与校验，两个壳不需要重复实现。

### 1.4 错误对象

```json
{"error":{"code":"TARGET_MISMATCH","message":"…","retryable":false,"details":{}}}
```

只依赖 `code`。主要错误码：

| code | 含义 |
| --- | --- |
| `NOT_PAIRED` | 指纹未配对 |
| `PAIRING_EXPIRED` / `PAIRING_FAILED` | 配对会话过期/校验失败 |
| `NO_TAILNET` | 本机没有 tailnet 地址 |
| `PEER_OFFLINE` | 对端未连接 |
| `TARGET_MISMATCH` / `SOURCE_MISMATCH` | 目标/来源不符 |
| `EXPIRED` | 事件或传输已过期 |
| `HASH_MISMATCH` | content_hash 或文件 SHA-256 不符 |
| `SEQUENCE_GAP` | 缺口，需要 resync |
| `TOO_LARGE` / `NO_SPACE` | 超限 / 磁盘不足 |
| `RATE_LIMITED` | 限流 |
| `INVALID` | 参数/配置无效（如 `data_dir` 非绝对路径、`db_key_b64` 缺失或不是 32 字节、平台与已存身份不符） |
| `DB_UNAVAILABLE` | 本地数据库无法打开/迁移，或库版本比核心新，或密文损坏 |
| `KEY_INVALID` | `db_key_b64` 与已有数据库不匹配（解不开设备私钥） |
| `NOT_INITIALIZED` | 核心未 `Init` 成功，或已 `Shutdown` |
| `METHOD_NOT_FOUND` | 未知方法 |
| `UPDATE_DISABLED` | 自更新未启用（开发构建或没有分发地址） |
| `UPDATE_FAILED` | 访问分发服务器失败（网络/HTTP），可重试 |
| `UPDATE_REJECTED` | 更新清单格式错误或签名不受信任 |
| `INTERNAL` | 核心内部错误（已 recover） |

## 2. 事件信封

每条事件对应**一条链路**（source→target），多目标时由发送方逐个对端展开。

```json
{
  "version": 1,
  "event_id": "5b8f0e5c-3f6e-4a8b-9f32-3d4c7f2a1e90",
  "kind": "clipboard",
  "source_device_id": "dev_…",
  "target_device_id": "dev_…",
  "sequence": 42,
  "created_at": "2026-10-26T08:00:00Z",
  "expires_at": "2026-10-26T08:15:00Z",
  "content_hash": "sha256:…",
  "sensitive": false,
  "payload": {}
}
```

| 字段 | 类型 | 必填 | 约束 |
| --- | --- | --- | --- |
| `version` | integer | 是 | `1` |
| `event_id` | string | 是 | UUIDv4；同一内容发给多个对端时可相同 |
| `kind` | string | 是 | `clipboard` / `file` / `control` |
| `source_device_id` | string | 是 | 与 TLS 指纹对应的设备 |
| `target_device_id` | string | 是 | 本机 |
| `sequence` | integer | 是 | ≥1，按链路（source→target）严格递增 |
| `created_at` / `expires_at` | string | 是 | `expires_at` 晚于 `created_at` |
| `content_hash` | string | 是 | 见 §1.3 |
| `sensitive` | boolean | 是 | 敏感内容不自动应用，通知不显示正文 |
| `payload` | object | 是 | 见下 |

事件**不转发**：接收方不得把收到的事件再发给第三方。

### 2.1 剪贴板 payload

```json
{"mime_type":"text/plain; charset=utf-8","content":"hello","size":5}
```

v1 只支持 UTF-8 纯文本，`size` ≤ 64 KiB。更大文本走文件。

### 2.2 文件 payload（offer）

```json
{
  "transfer_id": "tr_…",
  "name": "report.pdf",
  "size": 123456,
  "sha256": "hex…",
  "mime_type": "application/pdf",
  "mode": "pull"
}
```

`mode`：`pull`（发送方是监听方，接收方 GET 拉取）或 `push`（发送方是拨号方，接收方先 `file_accept`，发送方 PUT 推送）。`name` 只作显示，接收方必须清洗路径分隔符和保留名。

### 2.3 控制 payload

```json
{"action":"skip","through":41}
```

| action | 参数 | 含义 |
| --- | --- | --- |
| ~~`skip`~~ / ~~`resync`~~ | | **不是事件**：它们是链路帧，见 §4.3（它们不占用 sequence，也不进发件箱） |
| `file_accept` | `transfer_id`, `offset` | 接受 push 类传输，从 offset 开始 |
| `file_reject` | `transfer_id`, `reason` | 拒绝 |
| `file_cancel` | `transfer_id` | 取消 |
| `revoke` | — | 通知对端：我已撤销配对 |

## 3. 配对端点

仅这些端点接受未配对指纹。

### 3.1 `POST /v1/pair`

```json
{
  "device_id": "dev_…",
  "name": "Pixel",
  "platform": "android",
  "cert_fp": "base64url(sha256(cert))",
  "tailnet_addrs": ["100.101.146.200"],
  "proof": "base64url(HMAC-SHA256(psk, device_id || cert_fp || nonce))",
  "nonce": "base64url"
}
```

响应：

- `202`：`{"pair_id":"…","sas":"482913","state":"waiting_confirm"}`；Windows 用户确认前保持。
- `GET /v1/pair/{pair_id}` 轮询（≤2 s 间隔，≤120 s）：`waiting_confirm` / `accepted`（含对方 `device_id`、`name`、`tailnet_addrs`）/ `rejected`。
- `PAIRING_FAILED` 3 次后该会话作废；`PAIRING_EXPIRED` 5 分钟后作废。

SAS = `sha256(fpA || fpB || nonce)` 的前 20 bit，取 6 位十进制，两端分别显示。

### 3.2 QR / 深链

```text
clipbridge://pair?v=1&host=<ip>&port=47821&fp=<b64url>&psk=<b64url>&exp=<unix>
```

不包含长期凭证；`psk` 一次有效。

## 4. WebSocket 协议（`GET /v1/ws`）

### 4.1 hello / hello_ack

拨号方先发：

```json
{
  "type": "hello",
  "device_id": "dev_…",
  "name": "Pixel",
  "tailnet_addrs": ["100.101.146.200"],
  "recv_cursor": 41
}
```

`recv_cursor`：我已按序收到的、来自你的最大 sequence。监听方回：

```json
{
  "type": "hello_ack",
  "device_id": "dev_…",
  "server_time": "2026-10-26T08:00:00Z",
  "recv_cursor": 17,
  "max_frame_bytes": 65536
}
```

双方据此从发件箱补发 `sequence > 对方 recv_cursor` 且未过期的事件；若有缺口无法补发，先发 `skip`。

### 4.2 event / ack

```json
{"type":"event","envelope":{ … §2 … }}
```

```json
{"type":"ack","event_id":"…","result":"applied","reason":null,"acked_at":"…"}
```

`result`：`received` / `applied` / `ignored` / `expired` / `error`。`ignored` 的 `reason`：`duplicate`、`target_mismatch`、`policy_blocked`、`user_dismissed`。ack 幂等，不携带明文。`received` 仅表示已入库；"已应用"只来自 `applied`。

### 4.3 其它

链路层帧（不是事件，不占用 sequence，不入发件箱）：

```json
{"type":"skip","through":41}    // 发送方：≤41 的 sequence 不会再发（已过期/清除），接收方把游标推进到 41
{"type":"resync","after":17}    // 接收方：乱序缓存溢出，请从 17 之后重发
```

发送方在发送 sequence 不连续的下一个事件之前先发 `skip`（缺口由「已过期/已清除」的行产生），所以丢失的事件不会卡住后续事件。接收方对乱序事件最多缓存 64 条；`skip` 会连同缓存里 ≤through 的事件一起按序放行。

```json
{"type":"ping","ts":"…"}   {"type":"pong","ts":"…"}
{"type":"error","code":"RATE_LIMITED","message":"…","retryable":true}
{"type":"goaway","reason":"shutdown","reconnect_after_ms":1000}
```

- 双方每 25–30 s ping；60 s 无任何帧视为断线。
- 拨号方断线后指数退避重连（1 s、2 s、4 s…上限 60 s，带抖动）；认证失败（`NOT_PAIRED`）不重试，提示重新配对。
- 同一对端同时只保留一条 WebSocket；新连接取代旧连接。

## 5. 文件端点

要求已配对 mTLS。

### 5.1 拉取（`mode=pull`）

```http
GET /v1/files/{transfer_id}
Range: bytes=1048576-
```

返回 `206`/`200`，`Content-Length`、`ETag: "<sha256>"`；过期/撤销返回 `410`，未知返回 `404`，非本对端 offer 返回 `403`。

### 5.2 推送（`mode=push`）

接收方先通过 `file_accept` 给出 `offset`，发送方：

```http
PUT /v1/files/{transfer_id}
Content-Range: bytes 1048576-9999999/10000000
Content-Type: application/octet-stream
```

接收方追加写入临时文件；完整接收后校验 SHA-256，成功返回 `200 {"state":"complete"}`，失败返回 `422 HASH_MISMATCH` 并删除临时文件。`Content-Range` 起点与当前 offset 不符返回 `409`，响应体带正确 offset。

### 5.3 状态

```http
HEAD /v1/files/{transfer_id}
```

返回 `X-Offset`、`X-State`，用于断线后恢复。

## 6. 健康

`GET /healthz`（仅已配对指纹）：`{"ok":true,"version":"0.1.0","device_id":"dev_…"}`。

## 7. 核心门面（Windows DLL / Android AAR / 命名管道共用）

### 7.1 调用形式

```text
Call(method: string, paramsJSON: string) -> string   // {"ok":true,"result":…} | {"ok":false,"error":{…}}
NextEvent(timeoutMs: int) -> string                  // 事件 JSON；超时返回空
```

- Windows DLL：`cb_call` / `cb_next_event`（见 architecture §2.2）。
- Android：`Core.call(…)` / `Core.nextEvent(…)`（gomobile，字符串与整型）。
- 方法集在三处完全一致；壳不绕过门面访问核心内部。

### 7.2 初始化

```json
{
  "data_dir": "C:\\Users\\…\\AppData\\Local\\ClipBridge",
  "db_key_b64": "…",
  "device_name": "Tony-PC",
  "platform": "windows",
  "listen": true,
  "port": 47821,
  "app_version_code": 12,
  "app_version_name": "0.1.12",
  "update_url": "https://…/clipbridge"
}
```

Android：`"listen": false`。`db_key_b64` 由壳从 DPAPI/Keystore 取出后传入，核心不落盘。`data_dir` 必须是绝对路径，`db_key_b64` 解码后必须正好 32 字节。

`app_version_code`（安装包的 versionCode，单调递增）与 `update_url`（构建期写入的分发地址，https；仅回环地址允许 http）用于自更新，见 [update.md](update.md)。`app_version_code` 为 0 或未设置（开发构建）、或没有 `update_url`，则自更新关闭。

### 7.3 方法

| 方法 | 参数 | 说明 |
| --- | --- | --- |
| `status` | — | 本机 tailnet 地址、监听状态、对端连接状态、发件箱长度 |
| `settings.get` / `settings.set` | key/value | 捕获开关、TTL、默认应用策略、文件上限 |
| `pair.begin` | — | Windows：返回 QR 内容、过期时间 |
| `pair.join` | `qr` 或 `{host,port,psk?}` | Android：加入配对，返回 `pair_id` |
| `pair.confirm` | `pair_id`, `accept` | Windows：确认/拒绝 SAS |
| `peers.list` | — | 对端、状态、策略 |
| `peers.update` | `device_id`, `name`/`apply_mode`/`enabled` | |
| `peers.remove` | `device_id` | 撤销并拉黑指纹 |
| `clip.local_changed` | `text`, `seq?` | 壳报告本地复制；核心决定是否发送 |
| `clip.send` | `text`, `to[]?` | 用户手动发送 |
| `clip.mark_applied` | `event_id`, `seq_after?` | 壳已写入剪贴板 |
| `clip.dismiss` | `event_id` | 用户忽略 |
| `inbox.list` / `inbox.get` / `inbox.clear` | 分页/ID | 收件箱 |
| `file.offer` | `to`, `path` 或 `fd`+`name`+`size` | 创建 offer（`fd` 仅 Android） |
| `file.accept` / `file.reject` / `file.cancel` | `transfer_id`, `save_dir?` | |
| `transfers.list` | — | 传输状态与进度 |
| `update.status` | — | 自更新状态（见 [update.md](update.md)） |
| `update.check` | — | 立即检查；若开启自动下载则随后开始下载 |
| `update.download` | — | 开始下载已发现的新版本（幂等） |
| `update.config` | `auto?`, `url?` | 读取/修改自动下载开关与分发地址（`url:""` 恢复默认） |
| `diagnostics.get` | — | 版本、游标、最近错误（脱敏） |
| `shutdown` | — | 等同 `cb_shutdown` |

### 7.4 事件（`NextEvent`）

```json
{"type":"peer.state","device_id":"dev_…","state":"connected","path":"direct"}
{"type":"clip.received","event_id":"…","from":"dev_…","text":"…","sensitive":false,"decision":"notify|manual|auto"}
{"type":"clip.ack","event_id":"…","result":"applied"}
{"type":"file.offered","transfer_id":"tr_…","from":"dev_…","name":"a.pdf","size":123}
{"type":"file.progress","transfer_id":"tr_…","offset":1048576,"size":123456789,"rate_bps":52428800}
{"type":"file.done","transfer_id":"tr_…","path":"…","sha256":"…"}
{"type":"file.failed","transfer_id":"tr_…","code":"HASH_MISMATCH"}
{"type":"pair.request","pair_id":"…","name":"Pixel","platform":"android","sas":"482913"}
{"type":"pair.result","pair_id":"…","state":"accepted|rejected|expired"}
{"type":"core.warning","code":"NO_TAILNET"}
{"type":"update.available","version_code":12,"version_name":"0.1.12","mandatory":false,"notes":"…"}
{"type":"update.progress","received":1048576,"total":15000000}
{"type":"update.ready","version_code":12,"version_name":"0.1.12","path":"…","mandatory":false}
{"type":"update.error","code":"HASH_MISMATCH","message":"…"}
```

敏感事件的 `clip.received` 对 UI 层的行为约束由壳实现：通知不显示 `text`。

## 8. 本地命名管道（仅 Windows）

```text
\\.\pipe\clipbridge.v1      ACL：当前用户
```

JSON Lines，每行一个请求：

```json
{"id":"1","method":"clip.send","params":{"text":"hello","to":["dev_…"]}}
{"id":"1","ok":true,"result":{"event_id":"…"}}
```

方法集与 §7.3 相同（`shutdown` 和 `pair.*` 除外，仅限 GUI）。

## 9. `cbridge.exe`（仅 Windows，纯 Go）

```text
cbridge
├── status
├── peers list
├── clip send  [--to <peer>] [--text … | stdin]
├── file send  [--to <peer>] <path>…
├── inbox list [--limit N]
├── inbox apply <event-id>
├── transfers list
└── doctor
```

- 通过命名管道调用运行中的托盘应用；托盘应用未运行时返回退出码 3。
- `--json` 时 stdout 只输出 JSON，进度走 stderr。
- 不在 argv 放任何密钥。

退出码：

| 码 | 含义 |
| ---: | --- |
| 0 | 成功（含幂等去重） |
| 2 | 参数错误 |
| 3 | 托盘应用/核心未运行 |
| 4 | 未配对 / 对端不存在 |
| 5 | 校验失败 |
| 6 | 对端离线 / 无 tailnet |
| 7 | 过期 / 撤销 / 不存在 |
| 8 | 冲突 |

## 10. Android 集成

- 分享：`ACTION_SEND` `text/plain`（发剪贴板）、`*/*`（发文件）。
- Intent（供其它 App / Tasker）：`com.clipbridge.action.SEND_TEXT`（extra `text`、可选 `peer`），需要用户在设置中开启"允许外部 Intent"。
- 快捷设置磁贴：启动一个透明 Activity，获得焦点后读取剪贴板并发送。
- 通知动作：复制 / 打开 / 忽略。

## 11. 版本与兼容

- `hello` 不携带版本；协议版本由 WebSocket 子协议 `clipbridge.v1` 与信封 `version` 决定。
- 向后兼容：新增字段客户端必须忽略；不兼容变更使用 `clipbridge.v2` 子协议。
