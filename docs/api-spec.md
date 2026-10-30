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
| `LISTEN_FAILED` | 有 tailnet 地址，但端口绑不上（被占用，或被 Windows 保留端口范围占掉）；`status`/`diagnostics` 的 `listen_error` 给出原因 |
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
| `event_id` | string | 是 | UUIDv4；**每个对端、每个事件唯一**（同一内容发给多个对端时各用各的 id）。已被另一台设备的事件占用的 id 回 `ack error event_id_conflict`，不合并 |
| `kind` | string | 是 | `clipboard` / `file` / `control` |
| `source_device_id` | string | 是 | 与 TLS 指纹对应的设备 |
| `target_device_id` | string | 是 | 本机 |
| `sequence` | integer | 是 | ≥1，按链路（source→target）严格递增 |
| `created_at` / `expires_at` | string | 是 | `expires_at` 晚于 `created_at`，且相差不超过 7 天 |
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
| `file_done` | `transfer_id` | `pull` 类传输：接收方已收齐并通过 SHA-256 校验；发送方此时才记为“已送达”（写完最后一个字节不算） |
| `revoke` | — | 通知对端：我已撤销配对 |

## 3. 配对端点

仅这些端点接受未配对指纹。

### 3.1 `POST /v1/pair`

请求体（由加入方发出，TLS 客户端证书即其身份）：

```json
{
  "sid": "doLqTVRqFm2N",
  "device_id": "dev_…",
  "name": "Pixel",
  "platform": "android",
  "cert_fp": "base64url(sha256(cert))",
  "tailnet_addrs": ["100.101.146.200"],
  "port": 0,
  "nonce": "base64url(16 bytes)",
  "proof": "base64url(HMAC-SHA256(psk, …))"
}
```

字段与规则：

- `sid` = `base64url(HMAC-SHA256(psk, "clipbridge-pair-sid-v1")[:9])`：监听方据此找到会话，而不暴露 psk；不知道 psk 的人算不出它，所以乱发请求不会消耗别人会话的失败次数。
- `device_id` 必须等于 TLS 证书公钥派生的 id，`cert_fp` 必须等于该连接的证书指纹，否则 `PAIRING_FAILED`。
- `port`：加入方自己监听的端口（Android 为 0）；Win↔Win 时监听方据此日后回拨。
- `proof` = `base64url(HMAC-SHA256(psk, F("clipbridge-pair-v1") ‖ F(device_id) ‖ F(cert_fp) ‖ F(server_fp) ‖ F(nonce)))`，其中 `F(x)` = 2 字节大端长度 ‖ x（长度前缀，杜绝拼接歧义）。`server_fp` 是加入方在 TLS 握手里**实际看到**的监听方证书指纹：中间人用自己的证书终止 TLS，则加入方算出的 proof 里是中间人的指纹，真正的监听方验证必然失败——所以即使二维码没带 `fp`（手动输入，TOFU），中间人也过不了。
- 来源地址限流：每个来源地址突发 6 次，每 10 秒补 1 次，超出返回 `429 RATE_LIMITED`；另外同时最多 3 个打开的会话。

响应：

- `202`：`{"pair_id":"…","sas":"482913","state":"waiting_confirm"}`；监听方用户确认前保持。
- `GET /v1/pair/{pair_id}` 轮询（≤2 s 间隔，≤120 s；只有发起请求的那张证书能查询，否则 404）：`waiting_confirm` / `accepted`（含监听方的 `device_id`、`name`、`platform`、`tailnet_addrs`、`port`）/ `rejected` / `expired`。
- `PAIRING_FAILED`：proof 错误或会话不存在（两者故意不可区分）；同一会话错 3 次后作废。`PAIRING_EXPIRED`：会话 5 分钟、确认 120 秒后作废。psk 一次有效：确认（不论接受或拒绝）后即作废。

- **监听方也要证明自己**：`202` 与每次轮询的响应都带 `proof`（`ServerProof`：对 `psk`、`state`、双方 TLS 指纹、`nonce`、SAS、监听方 `device_id` 做长度前缀编码的 HMAC-SHA256）。加入方用 `hmac.Equal` 校验，不通过立即中止——没有配对码的冒充者即使在手动输入（TOFU）下也无法冒充监听方。
- **被移除的设备可以再次配对**：监听方看到 `pair.request` 带 `previously_removed:true`，用户确认后才清除该证书指纹的黑名单；加入方在写入新记录前同样清除。

SAS：两个证书指纹按字典序排序后与 `nonce` 一起做长度前缀编码，取 SHA-256 的前 20 bit，对 10⁶ 取模，补零成 6 位十进制。两端各自根据**自己看到的**指纹计算；加入方还要核对监听方返回的 `sas` 与自己算的一致，不一致立即中止。

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

- **补完（最后一步失败后重试）**：如果数据已全部写入但落盘/改名失败（返回 `500`），发送方重试时发送 `Content-Range: bytes */N`（`N` = 文件大小，无请求体），接收方只重新执行校验与改名。零字节文件同样用这种形式。
- 上传进行中，如果传输被取消、过期或对端被解除配对，接收方中止该请求并回 `410 CANCELLED`；中途取消的数据不会被发布。

### 5.3 状态

```http
HEAD /v1/files/{transfer_id}
```

返回 `X-Offset`、`X-State`、`X-Size`，用于断线后恢复（推送方据此从接收方实际收到的字节继续）。

### 5.4 协议细节（实现）

- **谁搬运数据**：`mode` 由拨号关系决定——发送方是监听方 → `pull`（接收方 `GET`，用 `Range` 续传）；发送方是拨号方 → `push`（接收方先发控制事件 `file_accept{transfer_id, offset}`，发送方再 `PUT`）。接收方收到的 `mode` 与自己的拨号关系不符时回 `file_reject{reason:"bad_mode"}`。
- **控制事件**（`kind: control`，走事件通道，有 sequence，可靠）：`file_accept{transfer_id, offset}`、`file_reject{transfer_id, reason}`、`file_cancel{transfer_id, reason}`。零字节文件没有传输阶段：接收方在 accept 时直接建好空文件，并发 `file_accept{offset:0}`（两种模式都发）告知发送方“已送达”。
- **访问控制**：`GET/HEAD/PUT` 只接受该传输的对端证书；其它已配对设备得到 `404`（不暴露传输是否存在），未配对证书由服务器统一回 `403 NOT_PAIRED`。
- **状态码**：`410` 过期/已取消；`409 OFFSET_MISMATCH` 带 `{"offset":N}`；`409 NOT_ACCEPTED/NOT_READY/SOURCE_CHANGED`；`422 HASH_MISMATCH`（并删除已收数据）；`507 NO_SPACE`。
- **完整性**：接收方把数据写进保存目录里的 `.<transfer_id>.part`，**全部收齐并核对 SHA-256 后**才原子改名为最终文件名（重名自动加 ` (1)`）；失败则删除 `.part`。核对失败时接收方发 `file_cancel{reason:"HASH_MISMATCH"}`——即使发送方已把传输记为“完成”（`pull` 下它写完最后一个字节就算送达），这条结论也会覆盖它。
- **报价上限**：每个对端最多同时有 32 个未回应的 offer，超出回 `file_reject{reason:"too_many"}`；offer 的有效期取发送方给的 `expires_at` 与“现在 + 24 h”的较小值。`file` 事件载荷 ≤ 8 KiB，`control` ≤ 2 KiB，超出回 `ack error TOO_LARGE` 且不入库；收件箱所有类型合计最多 2000 行。
- **文件名**：对端给的名字只取最后一段，替换控制字符/双向覆盖字符/Windows 禁用字符，避开 `CON`、`NUL`、`COM1`、`COM¹`、`CONIN$`…等保留名（设备名取第一个点之前的部分，`CON.tar.gz` 也算），去掉末尾的点和空格，限长 200 字节并保留扩展名。
- **磁盘空间**：accept 时检查目标卷剩余空间（文件大小 + 16 MiB），不足则拒绝并回 `NO_SPACE`，不删除其它文件。
- **重试**：下载/上传遇到网络错误按 1 s、2 s … 30 s 退避重试，直到完成、被取消或 offer 过期（24 h）；核心重启后自动恢复（Android 的 fd 来源无法恢复，标记 `SOURCE_LOST`）。

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
  "update_url": "https://…/release"
}
```

Android：`"listen": false`。`db_key_b64` 由壳从 DPAPI/Keystore 取出后传入，核心不落盘。`data_dir` 必须是绝对路径，`db_key_b64` 解码后必须正好 32 字节。

`app_version_code`（安装包的 versionCode，单调递增）与 `update_url`（构建期写入的 release-publish 服务基地址，含挂载路径，https；仅回环地址允许 http）用于自更新，见 [update.md](update.md)。`app_version_code` 为 0 或未设置（开发构建）、或没有 `update_url`，则自更新关闭。

### 7.3 方法

参数与结果均为 JSON 对象；失败返回 `{"ok":false,"error":{"code":…}}`（错误码见 §1.4）。

| 方法 | 参数 | 结果 / 说明 |
| --- | --- | --- |
| `version` | — | `{core, protocol}` |
| `status` | — | `{device_id, device_name, platform, listen, port, peers[], capture, capture_configured, listening_addrs[], listening_port, events_dropped}`；`capture_configured` 在用户第一次显式设置 `capture` 之前为 `false`（`capture` 默认 `on`，所以壳**不再**为此弹出询问；它只用于区分“从未设置”与“用户明确选择”）；`peers[]` 见 `peers.list` |
| `identity.get` | — | `{device_id, fingerprint}` |
| `diagnostics.get` | — | 版本、监听地址、各对端状态与游标、更新状态；**不含**密钥、psk、数据库密钥 |
| `settings.get` | `key?` | 有 `key`：`{key, value}`；否则 `{settings:{…}}`（见下表） |
| `settings.set` | `key`, `value` | 校验后保存；未知键或非法值 → `INVALID` |
| `pair.begin` | — | 监听方：`{session_id, link, host, port, expires_at, qr:{size, rows[]}}`。`link` 即二维码内容；`qr.rows` 是 `size`×`size` 的 `"0"/"1"` 字符串（含静区），壳自己画，不需要 QR 库。本机没有 tailnet 地址 → `NO_TAILNET`；有地址但绑不上端口 → `LISTEN_FAILED` |
| `pair.cancel` | `session_id` | 作废配对码 |
| `pair.pending` | — | `{pending:[{pair_id, name, platform, device_id, sas, expires_at}]}` |
| `pair.confirm` | `pair_id`, `accept` | 监听方：接受（保存对端并开始连接）或拒绝；已过期 → `PAIRING_EXPIRED` |
| `pair.join` | `link`，或 `host`+`port`+`psk`(+`fp`) | 加入方：立即返回 `{join_id}`；过程以事件 `pair.sas`、`pair.result` 报告。同时只能有一个进行中的加入 |
| `peers.list` | — | `{peers:[{device_id, name, platform, state, tailnet_addrs[], outbox_pending, recv_cursor, next_seq_out, last_seen_at, apply_mode, send_enabled}]}`；`state`：`connected` / `connecting` / `offline` / `unpaired` / `revoked` |
| `peers.reconnect` | `device_id` | 立即重试连接（`unpaired` 后用户重新确认、或想马上重连时）；未配对 → `NOT_PAIRED` |
| `peers.update` | `device_id`, `name?`, `apply_mode?`, `role?`, `send_enabled?` | `apply_mode`：`notify` / `manual` / `auto`；`role`：`auto` / `dialer` / `listener`；`send_enabled=false` 让自动广播（`clip.local_changed`、不带 `to` 的 `clip.send`）跳过该设备，显式指定 `to` 仍可发送 |
| `peers.remove` | `device_id`, `forget?` | 撤销配对：通知对端、拉黑指纹、断开；`forget` 同时删除记录（指纹仍被拉黑） |
| `clip.local_changed` | `text` | 壳报告本地复制。结果 `{sent:[{device_id,name,event_id}], suppressed?}`；`suppressed`：`capture_off` / `paused` / `empty` / `echo` / `duplicate` / `no_peers` / `too_large` / `sensitive_blocked`。`echo`：该文本是本机刚替某个对端写入剪贴板的内容（无时间上限的记忆，直到用户真正复制了别的内容）。自动广播对所有未被 `send_enabled=false` 关闭的已配对设备各发一个事件 |
| `clip.send` | `text`, `to[]?` | 用户显式发送，忽略捕获开关与回声检查；`{sent:[…]}` |
| `clip.begin_apply` | `event_id` | **写剪贴板之前**调用：先打开回声抑制窗口，避免写入触发的剪贴板通知在 `mark_applied` 之前被当成用户复制而回传 |
| `clip.mark_applied` | `event_id` | 壳已把文本写入剪贴板：确保回声状态已记录，并向发送方回 `applied`（与 `begin_apply` 幂等） |
| `clip.dismiss` | `event_id` | 用户忽略：向发送方回 `ignored/user_dismissed` |
| `inbox.list` | `limit?` | `{items:[{event_id, from, from_name, text, sensitive, state, received_at, expires_at, applied_at?}]}`，新的在前 |
| `inbox.get` | `event_id` | 一条同上 |
| `inbox.clear` | — | 清空历史 |
| `update.status` / `update.check` / `update.download` / `update.config` | 见 [update.md](update.md) | 自更新 |
| `file.offer` | `to`，以及 `path`（Windows）或 `fd`+`name`(+`size`)（Android，`detachFd()` 的整数，所有权交给核心） | `{transfer_id}`；立即返回，先在后台算 SHA-256（状态 `hashing`）再发出 offer。不可 seek 的 fd 会先落盘到 `data_dir/outgoing/` |
| `file.accept` | `transfer_id`, `save_dir?`（本地管道忽略它） | 接收方同意；默认保存到设置 `download_dir`（Windows：`下载\ClipBridge`；Android：`data_dir/received`，由壳搬进 MediaStore 后调用 `file.release`） |
| `file.reject` / `file.cancel` | `transfer_id` | 拒绝一个 offer / 取消进行中的传输（任一方） |
| `file.release` | `transfer_id` | 壳已把完成的文件移走：删除私有副本 |
| `transfers.list` | `limit?` | `{transfers:[{transfer_id, peer_id, peer_name, direction, name, size, sha256, state, offset, rate_bps, error?, path?, mime_type?, created_at, expires_at}]}` |

设置键（`settings.*`）：

| 键 | 取值 | 默认 | 含义 |
| --- | --- | --- | --- |
| `capture` | `on` / `off` | `on` | 是否把本机复制的内容自动同步出去；没有已配对设备时什么都不发 |
| `paused_until` | unix 秒 | `0` | 当前时间小于该值时：不自动发送（`suppressed: paused`），收到的内容不自动写入（`auto` 降级为 `manual`）；显式 `clip.send` 不受影响；到点自动恢复 |
| `clip_ttl_s` | 30–86400 | `900` | 事件在对端离线时最多等待的秒数，过期则丢弃并由 `skip` 越过 |
| `history_limit` | 10–5000 | `200` | 收件箱保留条数 |
| `send_sensitive` | `on` / `off` | `on` | 看起来像口令/令牌的内容是否允许发送 |
| `sensitive_detection` | `on` / `off` | `on` | 是否用 `LooksSensitive` 启发式判断敏感内容；关闭后本机不再隐藏、也不再因此阻止自动写入（密码库复制仍永不自动发送） |
| `default_apply_mode` | `notify` / `manual` / `auto` | `auto` | 新配对设备的初始策略（已有设备保持原策略） |
| `download_dir` | 绝对路径 | Windows `%USERPROFILE%\Downloads\ClipBridge`；Android `data_dir/received` | 接收文件的默认目录 |
| `max_file_bytes` | 1 MiB – 1 TiB | `4294967296` | 超过则自动拒绝 |

策略（`clip.received.decision`）：`notify`（只通知）、`manual`（通知，用户操作后才写剪贴板）、`auto`（壳做完自己的安全检查后写入）。敏感内容永不为 `auto`。`live` 为 `false` 表示事件在发送 20 秒之后才到达（对端离线补发、休眠唤醒、慢链路），核心已把 `auto` 降级为 `manual`：过期内容只提供，不自动覆盖剪贴板。`paused_until` 未到期时 `auto` 同样降级为 `manual`。

### 7.4 事件（`NextEvent`）

```json
{"type":"core.started","device_id":"dev_…"}
{"type":"core.listening","addrs":["100.101.146.117"],"port":47821}
{"type":"core.warning","code":"NO_TAILNET"}            // 另有 FINGERPRINT_MISMATCH
{"type":"core.warning","code":"LISTEN_FAILED","addr":"100.101.146.117","port":47821,"error":"…"}   // 有地址但绑不上；不会同时报 NO_TAILNET
{"type":"peer.state","device_id":"dev_…","state":"connected"}
{"type":"peer.revoked","device_id":"dev_…","name":"Pixel"}   // 对端撤销了与我们的配对
{"type":"clip.received","event_id":"…","from":"dev_…","from_name":"Tony-PC","text":"…","sensitive":false,"decision":"notify|manual|auto","live":true,"received_at":1790000000}
{"type":"clip.ack","event_id":"…","from":"dev_…","result":"received|applied|ignored|expired|error","reason":"user_dismissed"}
{"type":"outbox.expired","count":2}
{"type":"pair.request","pair_id":"…","name":"Pixel","platform":"android","device_id":"dev_…","sas":"482913","previously_removed":false,"expires_at":"…"}   // 监听方：等用户确认
{"type":"pair.sas","join_id":"…","sas":"482913"}                                                                        // 加入方：显示给用户核对
{"type":"pair.result","state":"accepted|rejected|expired|failed","role":"listener|joiner","pair_id":"…","join_id":"…","code":"PAIRING_FAILED","message":"…"}
{"type":"file.offered","transfer_id":"tr_…","from":"dev_…","from_name":"Pixel","name":"a.pdf","size":123456,"sha256":"…","mime_type":"application/pdf","expires_at":1790000000}
{"type":"file.state","transfer_id":"tr_…","state":"transferring"}
{"type":"file.progress","transfer_id":"tr_…","offset":1048576,"size":123456789,"rate_bps":52428800}
{"type":"file.done","transfer_id":"tr_…","direction":"in|out","path":"…","name":"a.pdf","size":123456,"sha256":"…"}
{"type":"file.failed","transfer_id":"tr_…","direction":"in|out","code":"HASH_MISMATCH","message":"…"}   // code 另有 REJECTED、CANCELLED、EXPIRED、NO_SPACE、TOO_LARGE、SOURCE_LOST…
{"type":"update.available","version_code":12,"version_name":"0.1.12","mandatory":false,"notes":"…"}
{"type":"update.progress","received":1048576,"total":15000000}
{"type":"update.ready","version_code":12,"version_name":"0.1.12","path":"…","mandatory":false}
{"type":"update.error","code":"HASH_MISMATCH","message":"…"}
```

敏感事件的 `clip.received` 对 UI 层的行为约束由壳实现：通知不显示 `text`。

## 8. 本地控制通道（命名管道）

```text
Windows：\\.\pipe\clipbridge.v1      仅创建它的用户可访问，拒绝远程客户端
开发/测试（非 Windows）：Unix 套接字，权限 0600
```

由 `Init` 配置项 `pipe` 开启（Windows 壳传 `\\.\pipe\clipbridge.v1`；为空则关闭）。地址已被占用（别的进程抢注了管道名）时核心照常工作，只发 `core.warning PIPE_UNAVAILABLE`。第一个管道实例使用 `FILE_FLAG_FIRST_PIPE_INSTANCE` 创建，避免被抢注后把客户端接到别人手里。

JSON Lines，每行一个请求，每个请求恰好一行回复，`id` 原样带回（任意 JSON 类型）：

```json
{"id":"1","method":"clip.send","params":{"text":"hello","to":["dev_…"]}}
{"id":"1","ok":true,"result":{"sent":[…]}}
```

- 回复的其余部分就是 `Core.Call` 的结果（`ok` + `result` 或 `error`）。
- 一行最长 1 MiB；格式错误的行得到一条 `INVALID` 回复，连接继续可用；空行忽略。
- **管道调用方不如界面可信**：`file.offer` 忽略 `fd`；`file.accept` 忽略 `save_dir`（文件只进用户设置的下载目录）；`inbox.list` / `inbox.get` 对 `sensitive` 条目返回 `text:""` 加 `redacted:true`，正文只在界面里显示。
- **只开放白名单方法**：`version`、`status`、`identity.get`、`diagnostics.get`、`peers.list`、`clip.send`、`inbox.list`、`inbox.get`、`transfers.list`、`file.offer`、`file.accept`、`file.reject`、`file.cancel`、`settings.get`、`pair.pending`。其余一律 `METHOD_NOT_FOUND`——尤其是 `pair.*`（配对必须由本人在界面上确认）、`peers.update/remove`、`update.*`、`settings.set` 和 `shutdown`：同一用户的其它进程不应能借此改变信任关系或更新来源。
- 事件（`NextEvent`）不走这个通道：它只有一个消费者，属于壳。

## 9. `cbridge`（命令行）

```text
cbridge [--json] <command>
├── version
├── status
├── peers [list]
├── clip send [--to <peer>] [--text …]        （没有 --text 时读 stdin；没有 --to 发给所有设备）
├── file send [--to <peer>] [--wait] <path>…
├── file accept <id> | file reject <id> | file cancel <id>
├── inbox list [--limit N] | inbox show <event-id>
├── transfers [list]
├── settings get [key]
├── doctor
└── call <method> [json]
```

- `<peer>` 是设备 ID，或设备名（唯一前缀即可）。
- 通过控制通道调用运行中的应用；应用未运行时退出码 3。地址默认是 §8 的管道，可用环境变量 `CBRIDGE_PIPE` 改。
- `--json` 时 stdout 只输出 JSON（错误也是 `{"ok":false,"error":{…}}`），人类可读的提示走 stderr。
- 不在 argv 放任何密钥；敏感条目的正文在 CLI 里一律不显示（`inbox show` 提示到界面查看）；`file accept` 不能指定保存目录。
- `file send --wait` 轮询到所有传输结束；任一失败/被拒时用对应退出码退出。
- `doctor` 检查 tailnet 监听地址、配对设备与连接状态，有问题退出码 6。

退出码：

| 码 | 含义 |
| ---: | --- |
| 0 | 成功（含幂等去重） |
| 1 | 其它失败 |
| 2 | 参数错误 |
| 3 | 应用/核心未运行 |
| 4 | 未配对 / 对端不存在 |
| 5 | 校验失败（`HASH_MISMATCH`） |
| 6 | 对端离线 / 无 tailnet / `doctor` 发现问题 |
| 7 | 过期 / 不存在 |
| 8 | 冲突 / 限流 |

## 10. Android 集成

- 分享：`ACTION_SEND` `text/plain`（发剪贴板）、`*/*`（发文件）。
- Intent（供其它 App / Tasker）：`com.clipbridge.action.SEND_TEXT`（extra `text`、可选 `peer`），需要用户在设置中开启"允许外部 Intent"。
- 快捷设置磁贴：启动一个透明 Activity，获得焦点后读取剪贴板并发送。
- 通知动作：复制 / 打开 / 忽略。
- 可选后台自动发送：Android 壳本地使用 `READ_LOGS` + 悬浮窗读取，读取后仍调用 `clip.local_changed`；协议/事件/数据库 schema 不变（见 [adr/006-android-background-read.md](adr/006-android-background-read.md)）。

## 11. 版本与兼容

- `hello` 不携带版本；协议版本由 WebSocket 子协议 `clipbridge.v1` 与信封 `version` 决定。
- 向后兼容：新增字段客户端必须忽略；不兼容变更使用 `clipbridge.v2` 子协议。
